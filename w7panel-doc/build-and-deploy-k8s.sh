#!/usr/bin/env bash
# Build local Sysbox binaries, push a small debug carrier image, and use a node
# debug Pod to install them atomically on one Kubernetes node.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
KUBECONFIG="${KUBECONFIG:-}"
TARGET_NODE="${TARGET_NODE:-}"
DEBUG_NAMESPACE="${DEBUG_NAMESPACE:-default}"
IMAGE_REPO="${IMAGE_REPO:-docker.cnb.cool/i0358/zpk/sysbox-debug-deploy}"
IMAGE_TAG="${IMAGE_TAG:-}"
RUN_SMOKE_TEST="${RUN_SMOKE_TEST:-false}"
TEST_POD="${TEST_POD:-sysbox-build-deploy-smoke}"
TEST_IMAGE="${TEST_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker:latest}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

BINARIES=(sysbox-runc sysbox-mgr sysbox-fs sysbox-snapshotter sysbox-admission)
KUBECTL=()
DEBUG_POD=""
IMAGE_CONTAINER=""
IMAGE=""

info() { printf '[INFO] %s\n' "$*"; }
ok() { printf '[ OK ] %s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }
progress() { printf '\n[%s/7] %s\n' "$1" "$2"; }
binary_progress() { printf '  [binary %s/%s] %s: %s\n' "$1" "${#BINARIES[@]}" "$2" "$3"; }

cleanup() {
    [[ -n "${IMAGE_CONTAINER}" ]] && docker rm -f "${IMAGE_CONTAINER}" >/dev/null 2>&1 || true
    [[ -n "${DEBUG_POD}" ]] && "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" delete pod "${DEBUG_POD}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

sha256() {
    sha256sum "$1" | awk '{print $1}'
}

source_path() {
    case "$1" in
        sysbox-runc) printf '%s\n' "${ROOT_DIR}/sysbox-runc/build/amd64/sysbox-runc" ;;
        sysbox-mgr) printf '%s\n' "${ROOT_DIR}/sysbox-mgr/build/sysbox-mgr" ;;
        sysbox-fs) printf '%s\n' "${ROOT_DIR}/sysbox-fs/build/amd64/sysbox-fs" ;;
        sysbox-snapshotter) printf '%s\n' "${ROOT_DIR}/sysbox-snapshotter/build/amd64/sysbox-snapshotter" ;;
        sysbox-admission) printf '%s\n' "${ROOT_DIR}/sysbox-admission/build/amd64/sysbox-admission" ;;
        *) die "unknown binary: $1" ;;
    esac
}

source_revision_tag() {
    local revision dirty_input dirty_hash
    revision="$(git -C "${ROOT_DIR}" rev-parse --short=12 HEAD)"
    dirty_input="$({
        git -C "${ROOT_DIR}" diff --binary
        git -C "${ROOT_DIR}" diff --cached --binary
        git -C "${ROOT_DIR}" ls-files --others --exclude-standard -z | xargs -0r sha256sum
        git -C "${ROOT_DIR}" submodule status --recursive | grep -E '^[+-]' || true
        git -C "${ROOT_DIR}" submodule foreach --quiet --recursive \
            'git diff --binary; git diff --cached --binary; git ls-files --others --exclude-standard -z | xargs -0r sha256sum' 2>/dev/null
    } || true)"
    if [[ -n "${dirty_input}" ]]; then
        dirty_hash="$(printf '%s' "${dirty_input}" | sha256sum | cut -c1-12)"
        printf '%s-dirty-%s\n' "${revision}" "${dirty_hash}"
    else
        printf '%s\n' "${revision}"
    fi
}

check_inputs() {
    [[ -n "${KUBECONFIG}" && -f "${KUBECONFIG}" ]] || die "KUBECONFIG is required and must exist"
    [[ -n "${TARGET_NODE}" ]] || die "TARGET_NODE is required"
    [[ "${IMAGE_REPO}" == docker.cnb.cool/i0358/zpk/* ]] || die "IMAGE_REPO must be under docker.cnb.cool/i0358/zpk/"
    [[ "${RUN_SMOKE_TEST}" == true || "${RUN_SMOKE_TEST}" == false ]] || die "RUN_SMOKE_TEST must be true or false"
    need_cmd git
    need_cmd make
    need_cmd docker
    need_cmd kubectl
    need_cmd sha256sum
    KUBECTL=(kubectl --kubeconfig "${KUBECONFIG}")
    "${KUBECTL[@]}" get node "${TARGET_NODE}" >/dev/null || die "target node not found: ${TARGET_NODE}"
    docker info >/dev/null || die "docker daemon is unavailable"
}

build_local_binaries() {
    local binary index=0 path
    export GOPROXY
    make -C "${ROOT_DIR}/sysbox-ipc"
    make -C "${ROOT_DIR}/sysbox-runc"
    make -C "${ROOT_DIR}/sysbox-snapshotter"
    make -C "${ROOT_DIR}/sysbox-admission"
    make -C "${ROOT_DIR}/sysbox-fs"
    make -C "${ROOT_DIR}/sysbox-mgr"
    for binary in "${BINARIES[@]}"; do
        index=$((index + 1))
        binary_progress "${index}" "local build" "${binary}"
        path="$(source_path "${binary}")"
        [[ -x "${path}" ]] || die "local build did not produce executable: ${path}"
        printf '  %-22s %s\n' "${binary}" "$(sha256 "${path}")"
    done
}

build_and_push_image() {
    docker build -f "${SCRIPT_DIR}/Dockerfile.sysbox-debug-deploy" -t "${IMAGE}" "${ROOT_DIR}"
    docker image inspect "${IMAGE}" --format 'image={{.Id}} size={{.Size}}'
    docker push "${IMAGE}"
}

verify_image_binaries() {
    local binary index=0 local_sha image_sha image_file
    IMAGE_CONTAINER="$(docker create "${IMAGE}")"
    for binary in "${BINARIES[@]}"; do
        index=$((index + 1))
        binary_progress "${index}" "verify carrier image" "${binary}"
        image_file="$(mktemp "${TMPDIR:-/tmp}/sysbox-image-${binary}.XXXXXX")"
        docker cp "${IMAGE_CONTAINER}:/sysbox-bin/${binary}" "${image_file}"
        local_sha="$(sha256 "$(source_path "${binary}")")"
        image_sha="$(sha256 "${image_file}")"
        rm -f -- "${image_file}"
        [[ "${local_sha}" == "${image_sha}" ]] || die "image binary mismatch: ${binary}"
        ok "${binary}: local=image=${local_sha}"
    done
    docker rm -f "${IMAGE_CONTAINER}" >/dev/null
    IMAGE_CONTAINER=""
}

create_debug_pod() {
    local output prefix
    prefix="node-debugger-${TARGET_NODE//./-}-"
    output="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" debug "node/${TARGET_NODE}" \
        --image="${IMAGE}" --image-pull-policy=Always --profile=sysadmin --attach=false -- sleep infinity 2>&1)"
    printf '%s\n' "${output}"
    DEBUG_POD="$(grep -Eo 'node-debugger-[a-z0-9-]+' <<<"${output}" | tail -1 || true)"
    if [[ -z "${DEBUG_POD}" ]]; then
        DEBUG_POD="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" get pods --field-selector="spec.nodeName=${TARGET_NODE}" \
            --sort-by=.metadata.creationTimestamp -o name | sed 's@^pod/@@' | grep "^${prefix}" | tail -1 || true)"
    fi
    [[ -n "${DEBUG_POD}" ]] || die "failed to find node debug Pod"
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" wait --for=condition=Ready "pod/${DEBUG_POD}" --timeout=180s
}

install_host_binaries() {
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- sh -c '
        set -eu
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

verify_host_binaries() {
    local binary index=0 local_sha host_sha
    for binary in "${BINARIES[@]}"; do
        index=$((index + 1))
        binary_progress "${index}" "verify target node" "${binary}"
        local_sha="$(sha256 "$(source_path "${binary}")")"
        host_sha="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
            sha256sum "/host/usr/bin/${binary}" | awk '{print $1}')"
        [[ "${local_sha}" == "${host_sha}" ]] || die "host binary mismatch: ${binary}"
        ok "${binary}: local=image=host=${local_sha}"
    done
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
        chroot /host systemctl is-active sysbox-mgr sysbox-fs sysbox-snapshotter
}

run_smoke_test() {
    [[ "${RUN_SMOKE_TEST}" == true ]] || return 0
    "${KUBECTL[@]}" delete pod "${TEST_POD}" --ignore-not-found
    cat <<EOF | "${KUBECTL[@]}" apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${TEST_POD}
spec:
  nodeName: ${TARGET_NODE}
  runtimeClassName: sysbox-runc
  hostUsers: false
  restartPolicy: Always
  containers:
  - name: ubuntu-systemd
    image: ${TEST_IMAGE}
    command: ["/sbin/init"]
EOF
    "${KUBECTL[@]}" wait --for=condition=Ready "pod/${TEST_POD}" --timeout=240s
    "${KUBECTL[@]}" exec "${TEST_POD}" -- ps -p 1 -o pid,comm,args
}

main() {
    progress 1 "检查构建机、Kubernetes API 与目标节点"
    check_inputs
    IMAGE_TAG="${IMAGE_TAG:-$(source_revision_tag)}"
    IMAGE="${IMAGE_REPO}:${IMAGE_TAG}"
    info "debug carrier image: ${IMAGE}"
    progress 2 "在当前主机编译 5 个 Sysbox 二进制"
    build_local_binaries
    progress 3 "本地打包并推送 ZPK debug 镜像"
    build_and_push_image
    progress 4 "校验本地二进制与 debug 镜像"
    verify_image_binaries
    progress 5 "创建目标节点 debug Pod 并拉取本次镜像"
    create_debug_pod
    progress 6 "原子替换目标节点 Sysbox 二进制并重启服务"
    install_host_binaries
    progress 7 "校验本地、镜像与目标节点二进制 SHA256"
    verify_host_binaries
    run_smoke_test
    ok "debug deployment complete: ${IMAGE} on ${TARGET_NODE}"
}

main "$@"
