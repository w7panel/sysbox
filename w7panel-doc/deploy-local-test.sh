#!/usr/bin/env bash
#
# Build Sysbox from the local source tree, build the K3s deploy image, deploy it
# to the current local K3s cluster, and run a systemd-in-Sysbox smoke test.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PKGR_DIR="${ROOT_DIR}/sysbox-pkgr"
K8S_DIR="${PKGR_DIR}/k8s"

SYSBOX_VERSION="${SYSBOX_VERSION:-$(cat "${ROOT_DIR}/VERSION")}"
SYSBOX_VERSION_FULL="$(echo "${SYSBOX_VERSION}" | sed '/-[0-9]/!s/.*/&-0/')"
IMAGE_REPO="${IMAGE_REPO:-docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s}"
IMAGE_TAG="${IMAGE_TAG:-v${SYSBOX_VERSION_FULL}}"
IMAGE="${IMAGE:-${IMAGE_REPO}:${IMAGE_TAG}}"
PUSH_IMAGE="${PUSH_IMAGE:-false}"
NODE_NAME="${NODE_NAME:-}"
TEST_POD="${TEST_POD:-sysbox-release-test}"
TEST_IMAGE="${TEST_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker:latest}"
UBUNTU_MIRROR="${UBUNTU_MIRROR:-http://mirrors.aliyun.com/ubuntu}"
DOCKER_APT_MIRROR="${DOCKER_APT_MIRROR:-https://mirrors.aliyun.com/docker-ce/linux/ubuntu}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
KUBECTL="${KUBECTL:-kubectl}"
DOCKER="${DOCKER:-docker}"
K3S="${K3S:-k3s}"

info() { printf '[INFO] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*" >&2; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

make_local_source_link() {
    info "Point sysbox-pkgr sources/sysbox to local source tree"
    mkdir -p "${PKGR_DIR}/sources"
    ln -sfn ../.. "${PKGR_DIR}/sources/sysbox"
}

build_deb() {
    info "Build generic deb from local source"
    make -C "${PKGR_DIR}/deb" clean generic EDITION=ce \
        UBUNTU_MIRROR="${UBUNTU_MIRROR}" \
        DOCKER_APT_MIRROR="${DOCKER_APT_MIRROR}" \
        GOPROXY="${GOPROXY}"
}

find_deb() {
    find "${PKGR_DIR}/deb/build" -type f -name 'sysbox-ce_*.deb' | sort | tail -1
}

extract_bins() {
    local deb="$1"
    local tmpdir
    tmpdir="$(mktemp -d)"
    info "Extract Sysbox binaries from ${deb}"
    dpkg -x "${deb}" "${tmpdir}"
    mkdir -p "${K8S_DIR}/bin/sysbox-ce/generic"
    install -m 0755 \
        "${tmpdir}/usr/bin/sysbox-runc" \
        "${tmpdir}/usr/bin/sysbox-fs" \
        "${tmpdir}/usr/bin/sysbox-mgr" \
        "${K8S_DIR}/bin/sysbox-ce/generic/"
    rm -rf "${tmpdir}"
}

build_image() {
    info "Build deploy image ${IMAGE}"
    "${DOCKER}" build \
        -t "${IMAGE}" \
        --build-arg sys_arch=amd64 \
        --build-arg sysbox_version="${IMAGE_TAG}" \
        -f Dockerfile.sysbox-k3s \
        "${K8S_DIR}"
}

verify_image() {
    info "Verify image tools and Sysbox versions"
    "${DOCKER}" run --rm "${IMAGE}" kubectl version --client=true
    "${DOCKER}" run --rm "${IMAGE}" crictl --version
    "${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-runc --version
    "${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-fs --version
    "${DOCKER}" run --rm "${IMAGE}" /opt/sysbox/bin/generic/sysbox-mgr --version
}

push_image() {
    if [[ "${PUSH_IMAGE}" == "true" ]]; then
        info "Push deploy image ${IMAGE}"
        "${DOCKER}" push "${IMAGE}"
    else
        warn "Skip image push; set PUSH_IMAGE=true to push ${IMAGE}"
    fi
}

import_image_to_k3s() {
    local tar="/tmp/sysbox-deploy-k3s-${IMAGE_TAG}.tar"
    info "Import image into local K3s containerd"
    "${DOCKER}" save -o "${tar}" "${IMAGE}"
    "${K3S}" ctr images import "${tar}"
}

render_daemonset() {
    local out="$1"
    awk -v image="${IMAGE}" '
        /^[[:space:]]*image:/ && !done_image {
            sub(/image:.*/, "image: " image)
            done_image=1
        }
        /^[[:space:]]*imagePullPolicy:/ && !done_policy {
            sub(/imagePullPolicy:.*/, "imagePullPolicy: IfNotPresent")
            done_policy=1
        }
        {print}
    ' "${K8S_DIR}/manifests/daemonset/sysbox-deploy-k8s.yaml" > "${out}"
}

deploy_daemonset() {
    local ds
    ds="$(mktemp)"
    render_daemonset "${ds}"
    info "Apply Sysbox deploy RBAC, RuntimeClass, and DaemonSet"
    "${KUBECTL}" apply -f "${K8S_DIR}/manifests/rbac/sysbox-rbac.yaml"
    "${KUBECTL}" apply -f "${K8S_DIR}/manifests/runtime-class/sysbox-runtimeclass.yaml"
    "${KUBECTL}" label node "${NODE_NAME}" sysbox-install=yes --overwrite
    "${KUBECTL}" apply -f "${ds}"
    rm -f "${ds}"
}

resolve_node_name() {
    if [[ -z "${NODE_NAME}" ]]; then
        NODE_NAME="$("${KUBECTL}" get nodes -o jsonpath='{.items[0].metadata.name}')"
    fi
    [[ -n "${NODE_NAME}" ]] || die "failed to resolve Kubernetes node name"
}

wait_for_runtime() {
    info "Wait for sysbox-runtime=running on node ${NODE_NAME}"
    "${KUBECTL}" -n kube-system rollout status ds/sysbox-deploy-k8s --timeout=300s
    for _ in $(seq 1 120); do
        if "${KUBECTL}" get node "${NODE_NAME}" -o jsonpath='{.metadata.labels.sysbox-runtime}' | grep -qx running; then
            "${KUBECTL}" get node "${NODE_NAME}" --show-labels
            return
        fi
        sleep 2
    done
    die "node ${NODE_NAME} did not reach sysbox-runtime=running"
}

run_smoke_test() {
    info "Create smoke test pod ${TEST_POD}"
    "${KUBECTL}" delete pod "${TEST_POD}" --ignore-not-found
    cat <<EOF | "${KUBECTL}" apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${TEST_POD}
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  restartPolicy: Always
  containers:
  - name: ubuntu-systemd
    image: ${TEST_IMAGE}
    imagePullPolicy: IfNotPresent
    command: ["/sbin/init"]
EOF
    "${KUBECTL}" wait --for=condition=Ready "pod/${TEST_POD}" --timeout=180s
    "${KUBECTL}" get pod "${TEST_POD}" -o wide
    "${KUBECTL}" exec "${TEST_POD}" -- ps -p 1 -o pid,comm,args
    "${KUBECTL}" exec "${TEST_POD}" -- sh -lc 'mount | grep -E "sysboxfs|proc|cgroup" | head -20'
}

main() {
    need_cmd make
    need_cmd dpkg
    need_cmd "${DOCKER}"
    need_cmd "${KUBECTL}"
    need_cmd "${K3S}"
    resolve_node_name

    make_local_source_link
    build_deb

    deb="$(find_deb)"
    [[ -n "${deb}" ]] || die "deb package not found"
    info "Built deb: ${deb}"

    extract_bins "${deb}"
    build_image
    verify_image
    push_image
    import_image_to_k3s
    deploy_daemonset
    wait_for_runtime
    run_smoke_test

    info "Local deployment test passed: ${IMAGE}"
}

main "$@"
