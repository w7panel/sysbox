#!/usr/bin/env bash
#
# Verify that Sysbox special directories use the rootfs PVC and survive two Pod
# recreations. By default it waits only until the target container can exec;
# use TEST_WAIT_MODE=ready when the whole Pod must become Ready between checks.
#
# Usage:
#   KUBECONFIG=/root/.kube/218.config \
#   NAMESPACE=k3k-console-164315 \
#   DEPLOYMENT=k3k-ckm-new-a3-server \
#   CONTAINER=k3k-ckm-new-a3-server \
#   ./w7panel-doc/persistent-special-mount-test.sh

set -Eeuo pipefail

KUBECONFIG="${KUBECONFIG:-/home/.kubeconfig}"
NAMESPACE="${NAMESPACE:-default}"
DEPLOYMENT="${DEPLOYMENT:?DEPLOYMENT is required}"
CONTAINER="${CONTAINER:-}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-300}"
TEST_WAIT_MODE="${TEST_WAIT_MODE:-exec}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"

K=("${KUBECTL_BIN}" --kubeconfig "${KUBECONFIG}" --request-timeout=30s)
TOKEN="sysbox-pvc-$(date +%Y%m%d%H%M%S)-$$"
MARKER=".${TOKEN}"

SPECIAL_MOUNTS=(
    "docker|/var/lib/docker"
    "kubelet|/var/lib/kubelet"
    "k0s|/var/lib/k0s"
    "k3s-agent|/var/lib/rancher/k3s/agent"
    "rke2|/var/lib/rancher/rke2"
    "buildkit|/var/lib/buildkit"
    "containerd-overlay|/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs"
)

info() { printf '[INFO] %s\n' "$*"; }
pass() { printf '[PASS] %s\n' "$*"; }
die() { printf '[FAIL] %s\n' "$*" >&2; exit 1; }

pod_exec() {
    local pod="$1"
    shift
    "${K[@]}" exec -n "${NAMESPACE}" "${pod}" -c "${CONTAINER}" -- "$@"
}

deployment_selector() {
    local selector
    selector="$("${K[@]}" get deployment -n "${NAMESPACE}" "${DEPLOYMENT}" \
        -o go-template='{{range $key, $value := .spec.selector.matchLabels}}{{printf "%s=%s," $key $value}}{{end}}')"
    selector="${selector%,}"
    [[ -n "${selector}" ]] || die "Deployment selector is empty"
    printf '%s\n' "${selector}"
}

usable_pod() {
    local selector="$1"
    local excluded_uid="${2:-}"
    local deadline=$((SECONDS + TIMEOUT_SECONDS))
    local pod state uid deleting running ready

    while ((SECONDS < deadline)); do
        while read -r pod; do
            [[ -n "${pod}" ]] || continue
            pod="${pod#pod/}"
            state="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
                -o jsonpath="{.metadata.uid}|{.metadata.deletionTimestamp}|{.status.conditions[?(@.type=='Ready')].status}|{.status.containerStatuses[?(@.name=='${CONTAINER}')].state.running.startedAt}" \
                2>/dev/null || true)"
            IFS='|' read -r uid deleting ready running <<<"${state}"
            [[ -n "${uid}" && "${uid}" != "${excluded_uid}" && -z "${deleting}" ]] || continue
            if [[ "${TEST_WAIT_MODE}" == "ready" && "${ready}" != True ]]; then
                continue
            fi
            if [[ -n "${running}" ]] && pod_exec "${pod}" true >/dev/null 2>&1; then
                printf '%s\n' "${pod}"
                return 0
            fi
        done < <("${K[@]}" get pod -n "${NAMESPACE}" -l "${selector}" \
            --sort-by=.metadata.creationTimestamp -o name 2>/dev/null || true)
        sleep 1
    done
    return 1
}

check_no_hidden_mount() {
    local pod="$1"
    local mounts
    mounts="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
        -o jsonpath="{range .spec.containers[*].volumeMounts[*]}{.mountPath}{'\n'}{end}")"
    if grep -q '^/var/lib/sysbox/rootfs-special-volume/' <<<"${mounts}"; then
        die "${pod}/${CONTAINER} still has an admission-injected hidden PVC mount"
    fi
    pass "Pod YAML has no rootfs-special-volume mount"
}

check_and_update_markers() {
    local pod="$1"
    local expected_phase="$2"
    local write_phase="$3"
    local args=() pair

    for pair in "${SPECIAL_MOUNTS[@]}"; do
        args+=("${pair%%|*}" "${pair#*|}")
    done
    pod_exec "${pod}" sh -c '
        set -eu
        token="$1"
        marker="$2"
        expected_phase="$3"
        write_phase="$4"
        shift 4
        expected_source=""
        while [ "$#" -gt 0 ]; do
            name="$1"
            path="$2"
            shift 2
            line="$(grep -F " /rootfs/upper${path} ${path} " /proc/self/mountinfo | tail -1)"
            [ -n "${line}" ] || { echo "missing PVC raw upper mount: ${path}" >&2; exit 1; }
            case "${line}" in *idmapped*) ;; *) echo "mount is not idmapped: ${path}" >&2; exit 1;; esac
            case "${line}" in *" - ext4 "*) ;; *) echo "mount is not ext4: ${path}" >&2; exit 1;; esac
            source="$(printf "%s\n" "${line}" | awk "{for (i=1; i<=NF; i++) if (\$i == \"-\") {print \$(i+2); exit}}")"
            [ -n "${source}" ] || { echo "mount source is empty: ${path}" >&2; exit 1; }
            if [ -z "${expected_source}" ]; then
                expected_source="${source}"
            elif [ "${source}" != "${expected_source}" ]; then
                echo "mount source mismatch: ${path}: ${source}, want ${expected_source}" >&2
                exit 1
            fi
            owner="$(stat -c "%u:%g" "${path}")"
            [ "${owner}" = "0:0" ] || { echo "owner mismatch: ${path}: ${owner}" >&2; exit 1; }
            if [ -n "${expected_phase}" ]; then
                actual="$(cat "${path}/${marker}")"
                expected="${token}:${expected_phase}:${path}"
                [ "${actual}" = "${expected}" ] || { echo "marker mismatch: ${path}: ${actual}" >&2; exit 1; }
            fi
            if [ -n "${write_phase}" ]; then
                printf "%s\n" "${token}:${write_phase}:${path}" > "${path}/${marker}"
            else
                rm -f "${path}/${marker}"
            fi
            printf "  %s\n" "${line}"
        done
        printf "SOURCE=%s\n" "${expected_source}"
    ' sh "${TOKEN}" "${MARKER}" "${expected_phase}" "${write_phase}" "${args[@]}" || \
        die "batched special mount or marker validation failed for ${pod}"
    pass "all seven special directories are PVC-backed ext4 idmapped mounts with root ownership"
    if [[ -n "${expected_phase}" ]]; then
        pass "all seven ${expected_phase} markers survived Pod recreation"
    fi
    if [[ -n "${write_phase}" ]]; then
        pass "${write_phase} persistence markers written"
    fi
}

main() {
    local selector pod pod_uid second_pod second_uid third_pod

    "${K[@]}" get deployment -n "${NAMESPACE}" "${DEPLOYMENT}" >/dev/null
    if [[ -z "${CONTAINER}" ]]; then
        CONTAINER="$("${K[@]}" get deployment -n "${NAMESPACE}" "${DEPLOYMENT}" \
            -o jsonpath='{.spec.template.spec.containers[0].name}')"
    fi
    selector="$(deployment_selector)"
    [[ "${TEST_WAIT_MODE}" == "exec" || "${TEST_WAIT_MODE}" == "ready" ]] || \
        die "TEST_WAIT_MODE must be exec or ready"
    pod="$(usable_pod "${selector}")" || die "no usable Pod found for ${DEPLOYMENT}"
    pod_uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" -o jsonpath='{.metadata.uid}')"

    info "initial Pod: ${NAMESPACE}/${pod}"
    check_no_hidden_mount "${pod}"
    check_and_update_markers "${pod}" "" first

    info "delete ${pod} and wait for a new usable Pod (${TEST_WAIT_MODE})"
    "${K[@]}" delete pod -n "${NAMESPACE}" "${pod}" --wait=false >/dev/null
    second_pod="$(usable_pod "${selector}" "${pod_uid}")" || die "first replacement Pod did not become usable"
    second_uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${second_pod}" -o jsonpath='{.metadata.uid}')"

    info "first replacement Pod: ${NAMESPACE}/${second_pod}"
    check_no_hidden_mount "${second_pod}"
    check_and_update_markers "${second_pod}" first second

    info "delete ${second_pod} and wait for a second usable Pod (${TEST_WAIT_MODE})"
    "${K[@]}" delete pod -n "${NAMESPACE}" "${second_pod}" --wait=false >/dev/null
    third_pod="$(usable_pod "${selector}" "${second_uid}")" || die "second replacement Pod did not become usable"

    info "second replacement Pod: ${NAMESPACE}/${third_pod}"
    check_no_hidden_mount "${third_pod}"
    check_and_update_markers "${third_pod}" second ""
    pass "persistent special mount end-to-end test passed (wait mode: ${TEST_WAIT_MODE})"
}

main "$@"
