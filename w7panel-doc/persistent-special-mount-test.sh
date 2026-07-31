#!/usr/bin/env bash
#
# Verify that Sysbox special directories use the rootfs PVC and survive a Pod
# recreation. This test restarts one Pod managed by the target Deployment.
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

ready_pod() {
    local selector="$1"
    local excluded_uid="${2:-}"
    local deadline=$((SECONDS + TIMEOUT_SECONDS))
    local pod uid ready deleting

    while ((SECONDS < deadline)); do
        while read -r pod; do
            [[ -n "${pod}" ]] || continue
            pod="${pod#pod/}"
            uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
                -o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
            ready="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
                -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
            deleting="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
                -o jsonpath='{.metadata.deletionTimestamp}' 2>/dev/null || true)"
            if [[ -n "${uid}" && "${uid}" != "${excluded_uid}" && "${ready}" == True && -z "${deleting}" ]]; then
                printf '%s\n' "${pod}"
                return 0
            fi
        done < <("${K[@]}" get pod -n "${NAMESPACE}" -l "${selector}" \
            --sort-by=.metadata.creationTimestamp -o name 2>/dev/null || true)
        sleep 2
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

check_mounts() {
    local pod="$1"
    local pair name path line

    for pair in "${SPECIAL_MOUNTS[@]}"; do
        name="${pair%%|*}"
        path="${pair#*|}"
        line="$(pod_exec "${pod}" sh -c '
            line="$(grep -F " /rootfs/special/$2 $1 " /proc/self/mountinfo | tail -1)"
            [ -n "$line" ] || exit 1
            printf "%s\n" "$line"
        ' sh "${path}" "${name}")" || die "${path} is not mounted from PVC rootfs/special/${name}"
        printf '  %s\n' "${line}"
    done
    pass "all seven special directories are PVC-backed"
}

check_ownership() {
    local pod="$1"
    local pair path owner

    for pair in "${SPECIAL_MOUNTS[@]}"; do
        path="${pair#*|}"
        owner="$(pod_exec "${pod}" stat -c '%u:%g' "${path}")"
        [[ "${owner}" == "0:0" ]] || die "${path} owner is ${owner}, want 0:0"
    done
    pass "all seven special directories have container root ownership"
}

write_markers() {
    local pod="$1"
    local phase="$2"
    local pair path

    for pair in "${SPECIAL_MOUNTS[@]}"; do
        path="${pair#*|}"
        pod_exec "${pod}" sh -c 'printf "%s\n" "$1:$2:$3" > "$3/$4"' \
            sh "${TOKEN}" "${phase}" "${path}" "${MARKER}" || die "${path} is not writable"
    done
    pass "${phase} persistence markers written"
}

check_markers() {
    local pod="$1"
    local phase="$2"
    local pair path actual

    for pair in "${SPECIAL_MOUNTS[@]}"; do
        path="${pair#*|}"
        actual="$(pod_exec "${pod}" sh -c 'cat "$1/$2"' sh "${path}" "${MARKER}")"
        [[ "${actual}" == "${TOKEN}:${phase}:${path}" ]] || die "marker mismatch under ${path}: ${actual}"
    done
    pass "all seven ${phase} markers survived Pod recreation"
}

remove_markers() {
    local pod="$1"
    local pair path

    for pair in "${SPECIAL_MOUNTS[@]}"; do
        path="${pair#*|}"
        pod_exec "${pod}" rm -f "${path}/${MARKER}" >/dev/null
    done
}

main() {
    local selector pod pod_uid second_pod second_uid third_pod

    "${K[@]}" get deployment -n "${NAMESPACE}" "${DEPLOYMENT}" >/dev/null
    if [[ -z "${CONTAINER}" ]]; then
        CONTAINER="$("${K[@]}" get deployment -n "${NAMESPACE}" "${DEPLOYMENT}" \
            -o jsonpath='{.spec.template.spec.containers[0].name}')"
    fi
    selector="$(deployment_selector)"
    pod="$(ready_pod "${selector}")" || die "no ready Pod found for ${DEPLOYMENT}"
    pod_uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" -o jsonpath='{.metadata.uid}')"

    info "initial Pod: ${NAMESPACE}/${pod}"
    check_no_hidden_mount "${pod}"
    check_mounts "${pod}"
    check_ownership "${pod}"
    write_markers "${pod}" first

    info "delete ${pod} and wait for a new ready Pod"
    "${K[@]}" delete pod -n "${NAMESPACE}" "${pod}" --wait=false >/dev/null
    second_pod="$(ready_pod "${selector}" "${pod_uid}")" || die "first replacement Pod did not become ready"
    second_uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${second_pod}" -o jsonpath='{.metadata.uid}')"

    info "first replacement Pod: ${NAMESPACE}/${second_pod}"
    check_no_hidden_mount "${second_pod}"
    check_mounts "${second_pod}"
    check_ownership "${second_pod}"
    check_markers "${second_pod}" first
    write_markers "${second_pod}" second

    info "delete ${second_pod} and wait for a second replacement Pod"
    "${K[@]}" delete pod -n "${NAMESPACE}" "${second_pod}" --wait=false >/dev/null
    third_pod="$(ready_pod "${selector}" "${second_uid}")" || die "second replacement Pod did not become ready"

    info "second replacement Pod: ${NAMESPACE}/${third_pod}"
    check_no_hidden_mount "${third_pod}"
    check_mounts "${third_pod}"
    check_ownership "${third_pod}"
    check_markers "${third_pod}" second
    remove_markers "${third_pod}"
    pass "persistent special mount end-to-end test passed"
}

main "$@"
