#!/usr/bin/env bash
# Deploy Sysbox with a debug carrier image, manifests, or a local release build.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
K8S_DIR="${ROOT_DIR}/sysbox-pkgr/k8s"
DEBUG_ENV_FILE="${DEBUG_ENV_FILE:-${ROOT_DIR}/dist/debug-image.env}"
KUBECONFIG="${KUBECONFIG:-/home/.kubeconfig}"
LOCAL_KUBECONFIG="${LOCAL_KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
DEBUG_NAMESPACE="${DEBUG_NAMESPACE:-default}"
NODE_NAME="${NODE_NAME:-}"
TARGET_NODE="${TARGET_NODE:-}"
NODE_SELECTOR_VALUE="${NODE_SELECTOR_VALUE:-yes}"
RUN_TEST="${RUN_TEST:-true}"
RUN_SMOKE_TEST="${RUN_SMOKE_TEST:-false}"
TEST_POD="${TEST_POD:-sysbox-release-test}"
TEST_IMAGE="${TEST_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker:latest}"
IMAGE_PULL_POLICY="${IMAGE_PULL_POLICY:-IfNotPresent}"
K3S="${K3S:-k3s}"
IMAGE="${IMAGE:-}"

BINARIES=(sysbox-runc sysbox-mgr sysbox-fs sysbox-snapshotter sysbox-admission)
KUBECTL=(kubectl --kubeconfig "${KUBECONFIG}")
DEBUG_POD=""
SELECTED_MODE=""

info() { printf '[INFO] %s\n' "$*"; }
ok() { printf '[ OK ] %s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage:
  deploy.sh debug       Deploy IMAGE binaries to TARGET_NODE
  deploy.sh manifests   Deploy IMAGE with the legacy manifests
  deploy.sh local-test  Build, import, deploy, and test a local release

Common environment:
  KUBECONFIG  Kubernetes config (default: /home/.kubeconfig)

debug:
  IMAGE, TARGET_NODE, DEBUG_NAMESPACE

manifests:
  IMAGE, NODE_NAME, NODE_SELECTOR_VALUE, IMAGE_PULL_POLICY, RUN_TEST

local-test:
  LOCAL_KUBECONFIG, LOCAL_NODE_NAME (only when hostname detection fails)
EOF
}

choose_mode() {
    local choice

    [[ -t 0 ]] || { usage >&2; exit 2; }
    PS3="Select deploy mode: "
    select choice in debug manifests local-test quit; do
        case "${choice}" in
            debug|manifests|local-test) SELECTED_MODE="${choice}"; return ;;
            quit) exit 0 ;;
            *) printf 'Invalid selection\n' >&2 ;;
        esac
    done
}

require_value() {
    local name="$1" label="$2" value

    [[ -n "${!name:-}" ]] && return
    [[ -t 0 ]] || die "${name} is required"
    while [[ -z "${value:-}" ]]; do
        read -r -p "${label}: " value
    done
    printf -v "${name}" '%s' "${value}"
}

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

check_kubernetes() {
    [[ -f "${KUBECONFIG}" ]] || die "kubeconfig not found: ${KUBECONFIG}"
    need_cmd kubectl
    "${KUBECTL[@]}" version --client >/dev/null
}

cleanup_debug_pod() {
    [[ -n "${DEBUG_POD}" ]] || return 0
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" delete pod "${DEBUG_POD}" \
        --ignore-not-found --wait=false >/dev/null 2>&1 || true
    DEBUG_POD=""
}

create_debug_pod() {
    local output prefix

    prefix="node-debugger-${TARGET_NODE//./-}-"
    output="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" debug "node/${TARGET_NODE}" \
        --image="${IMAGE}" --image-pull-policy=Always --profile=sysadmin \
        --attach=false -- sleep infinity 2>&1)"
    printf '%s\n' "${output}"
    DEBUG_POD="$(grep -Eo 'node-debugger-[a-z0-9-]+' <<<"${output}" | tail -1 || true)"
    if [[ -z "${DEBUG_POD}" ]]; then
        DEBUG_POD="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" get pods \
            --field-selector="spec.nodeName=${TARGET_NODE}" \
            --sort-by=.metadata.creationTimestamp -o name \
            | sed 's@^pod/@@' | grep "^${prefix}" | tail -1 || true)"
    fi
    [[ -n "${DEBUG_POD}" ]] || die "failed to find node debug Pod"
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" wait \
        --for=condition=Ready "pod/${DEBUG_POD}" --timeout=180s
}

install_debug_binaries() {
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- sh -c '
        set -eu
        mkdir -p /host/etc/systemd/system/sysbox-mgr.service.d
        cat > /host/etc/systemd/system/sysbox-mgr.service.d/timeout.conf <<EOF
[Service]
TimeoutStartSec=10min
TimeoutStopSec=10min
EOF
        chroot /host systemctl stop sysbox sysbox-fs sysbox-mgr sysbox-snapshotter 2>/dev/null || true
        for name in sysbox-runc sysbox-mgr sysbox-fs sysbox-snapshotter sysbox-admission; do
            test -x "/sysbox-bin/${name}"
            install -m 0755 "/sysbox-bin/${name}" "/host/usr/bin/.${name}.new"
            mv -f "/host/usr/bin/.${name}.new" "/host/usr/bin/${name}"
        done
        chroot /host systemctl daemon-reload
        chroot /host systemctl restart sysbox-mgr sysbox-fs sysbox-snapshotter
    '
}

verify_debug_binaries() {
    local binary image_sha host_sha

    for binary in "${BINARIES[@]}"; do
        image_sha="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
            sha256sum "/sysbox-bin/${binary}" | awk '{print $1}')"
        host_sha="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
            sha256sum "/host/usr/bin/${binary}" | awk '{print $1}')"
        [[ "${image_sha}" == "${host_sha}" ]] || die "host binary mismatch: ${binary}"
        ok "${binary}: image=host=${image_sha}"
    done

    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
        chroot /host systemctl is-active sysbox-mgr sysbox-fs sysbox-snapshotter
}

deploy_debug() {
    if [[ -z "${IMAGE}" && -f "${DEBUG_ENV_FILE}" ]]; then
        # shellcheck disable=SC1090
        source "${DEBUG_ENV_FILE}"
        info "Loaded IMAGE from ${DEBUG_ENV_FILE}"
    fi
    require_value IMAGE "Debug image"
    require_value TARGET_NODE "Target node"
    check_kubernetes
    "${KUBECTL[@]}" get node "${TARGET_NODE}" >/dev/null || die "target node not found: ${TARGET_NODE}"

    trap cleanup_debug_pod EXIT
    info "Create debug Pod on ${TARGET_NODE} with ${IMAGE}"
    create_debug_pod
    info "Atomically replace Sysbox binaries"
    install_debug_binaries
    verify_debug_binaries
    cleanup_debug_pod
    trap - EXIT

    if [[ "${RUN_SMOKE_TEST}" == "true" ]]; then
        NODE_NAME="${TARGET_NODE}" POD_NAME="${TEST_POD}" IMAGE="${TEST_IMAGE}" \
            KUBECONFIG="${KUBECONFIG}" "${SCRIPT_DIR}/tests/test-pod.sh" all
    fi
    ok "debug deployment complete: ${IMAGE} on ${TARGET_NODE}"
}

resolve_node_name() {
    if [[ -z "${NODE_NAME}" ]]; then
        NODE_NAME="$("${KUBECTL[@]}" get nodes -o jsonpath='{.items[0].metadata.name}')"
    fi
    [[ -n "${NODE_NAME}" ]] || die "failed to resolve Kubernetes node name"
}

render_daemonset() {
    local out="$1"
    awk -v image="${IMAGE}" -v policy="${IMAGE_PULL_POLICY}" '
        /^[[:space:]]*image:/ && !done_image {
            sub(/image:.*/, "image: " image)
            done_image=1
        }
        /^[[:space:]]*imagePullPolicy:/ && !done_policy {
            sub(/imagePullPolicy:.*/, "imagePullPolicy: " policy)
            done_policy=1
        }
        {print}
    ' "${K8S_DIR}/manifests/daemonset/sysbox-deploy-k8s.yaml" > "${out}"
}

apply_manifests() {
    local daemonset

    daemonset="$(mktemp)"
    render_daemonset "${daemonset}"
    "${KUBECTL[@]}" apply -f "${K8S_DIR}/manifests/rbac/sysbox-rbac.yaml"
    "${KUBECTL[@]}" apply -f "${K8S_DIR}/manifests/runtime-class/sysbox-runtimeclass.yaml"
    "${KUBECTL[@]}" label node "${NODE_NAME}" \
        "sysbox-install=${NODE_SELECTOR_VALUE}" --overwrite
    "${KUBECTL[@]}" apply -f "${daemonset}"
    rm -f "${daemonset}"
}

wait_for_runtime() {
    local attempt

    "${KUBECTL[@]}" -n kube-system rollout status ds/sysbox-deploy-k8s --timeout=300s
    for ((attempt = 0; attempt < 120; attempt++)); do
        if [[ "$("${KUBECTL[@]}" get node "${NODE_NAME}" \
            -o jsonpath='{.metadata.labels.sysbox-runtime}')" == "running" ]]; then
            "${KUBECTL[@]}" -n kube-system get ds sysbox-deploy-k8s -o wide
            return
        fi
        sleep 2
    done
    "${KUBECTL[@]}" -n kube-system logs ds/sysbox-deploy-k8s --tail=200 || true
    die "node ${NODE_NAME} did not reach sysbox-runtime=running"
}

deploy_manifests() {
    require_value IMAGE "Deploy image"
    check_kubernetes
    resolve_node_name
    info "Deploy ${IMAGE} to ${NODE_NAME} with manifests"
    apply_manifests
    wait_for_runtime

    if [[ "${RUN_TEST}" == "true" ]]; then
        NODE_NAME="${NODE_NAME}" POD_NAME="${TEST_POD}" IMAGE="${TEST_IMAGE}" \
            KUBECONFIG="${KUBECONFIG}" \
            "${SCRIPT_DIR}/tests/test-pod.sh" all
    fi
    ok "manifests deployment complete: ${IMAGE}"
}

deploy_local_test() {
    local sysbox_version version_full image_tar target_cluster_uid local_cluster_uid
    local local_node_name="${LOCAL_NODE_NAME:-}" node_ref node_hostname
    local local_nodes=()

    need_cmd docker
    need_cmd "${K3S}"
    check_kubernetes
    [[ -f "${LOCAL_KUBECONFIG}" ]] || die "local K3s kubeconfig not found: ${LOCAL_KUBECONFIG}"
    target_cluster_uid="$("${KUBECTL[@]}" get namespace kube-system -o jsonpath='{.metadata.uid}')"
    local_cluster_uid="$(kubectl --kubeconfig "${LOCAL_KUBECONFIG}" \
        get namespace kube-system -o jsonpath='{.metadata.uid}')"
    [[ "${target_cluster_uid}" == "${local_cluster_uid}" ]] || \
        die "KUBECONFIG does not point to the local K3s cluster"
    mapfile -t local_nodes < <(kubectl --kubeconfig "${LOCAL_KUBECONFIG}" get nodes -o name)
    if [[ -z "${local_node_name}" ]]; then
        for node_ref in "${local_nodes[@]}"; do
            node_hostname="$(kubectl --kubeconfig "${LOCAL_KUBECONFIG}" get "${node_ref}" \
                -o jsonpath='{.status.addresses[?(@.type=="Hostname")].address}')"
            if [[ "${node_hostname}" == "$(hostname)" || "${node_hostname}" == "$(hostname -s)" ]]; then
                local_node_name="${node_ref#node/}"
                break
            fi
        done
    fi
    [[ -n "${local_node_name}" ]] || \
        die "failed to identify the local K3s node; set LOCAL_NODE_NAME"
    [[ -z "${NODE_NAME}" || "${NODE_NAME}" == "${local_node_name}" ]] || \
        die "NODE_NAME must be the local image-import node: ${local_node_name}"
    NODE_NAME="${local_node_name}"
    "${KUBECTL[@]}" get node "${NODE_NAME}" >/dev/null

    sysbox_version="${SYSBOX_VERSION:-$(<"${ROOT_DIR}/VERSION")}"
    version_full="$(sed '/-[0-9]/!s/.*/&-0/' <<<"${sysbox_version}")"
    IMAGE="${IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s:v${version_full}-local}"

    info "Build local release image ${IMAGE}"
    IMAGE="${IMAGE}" PUSH_IMAGE=false SAVE_IMAGE_TAR=false PACKAGE_CHART=false \
        GITHUB_TOKEN='' "${SCRIPT_DIR}/build.sh" release

    image_tar="$(mktemp "${TMPDIR:-/tmp}/sysbox-deploy-k3s.XXXXXX.tar")"
    trap 'rm -f "${image_tar}"' EXIT
    docker save -o "${image_tar}" "${IMAGE}"
    "${K3S}" ctr images import "${image_tar}"
    rm -f "${image_tar}"
    trap - EXIT

    IMAGE_PULL_POLICY=IfNotPresent deploy_manifests
}

main() {
    local mode

    [[ $# -le 1 ]] || { usage >&2; exit 2; }
    mode="${1:-}"
    if [[ -z "${mode}" ]]; then
        choose_mode
        mode="${SELECTED_MODE}"
    fi
    case "${mode}" in
        debug) deploy_debug ;;
        manifests) deploy_manifests ;;
        local-test) deploy_local_test ;;
        -h|--help) usage ;;
        *) usage >&2; exit 2 ;;
    esac
}

main "$@"
