#!/usr/bin/env bash
#
# Deploy a published sysbox-deploy-k3s image to a K3s/Kubernetes cluster and
# optionally run a systemd-in-Sysbox smoke test.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
K8S_DIR="${ROOT_DIR}/sysbox-pkgr/k8s"

IMAGE="${IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s:v0.7.0-1}"
NODE_NAME="${NODE_NAME:-}"
NODE_SELECTOR_VALUE="${NODE_SELECTOR_VALUE:-yes}"
RUN_TEST="${RUN_TEST:-true}"
TEST_POD="${TEST_POD:-sysbox-release-test}"
TEST_IMAGE="${TEST_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker:latest}"
IMAGE_PULL_POLICY="${IMAGE_PULL_POLICY:-IfNotPresent}"
KUBECTL="${KUBECTL:-kubectl}"

info() { printf '[INFO] %s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
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

deploy_sysbox() {
    local ds
    ds="$(mktemp)"
    render_daemonset "${ds}"

    info "Apply RBAC and RuntimeClass"
    "${KUBECTL}" apply -f "${K8S_DIR}/manifests/rbac/sysbox-rbac.yaml"
    "${KUBECTL}" apply -f "${K8S_DIR}/manifests/runtime-class/sysbox-runtimeclass.yaml"

    info "Label node ${NODE_NAME}: sysbox-install=${NODE_SELECTOR_VALUE}"
    "${KUBECTL}" label node "${NODE_NAME}" "sysbox-install=${NODE_SELECTOR_VALUE}" --overwrite

    info "Apply sysbox-deploy-k8s DaemonSet with image ${IMAGE}"
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
    info "Wait for DaemonSet rollout"
    "${KUBECTL}" -n kube-system rollout status ds/sysbox-deploy-k8s --timeout=300s

    info "Wait for node label sysbox-runtime=running"
    for _ in $(seq 1 120); do
        if "${KUBECTL}" get node "${NODE_NAME}" -o jsonpath='{.metadata.labels.sysbox-runtime}' | grep -qx running; then
            "${KUBECTL}" get node "${NODE_NAME}" --show-labels
            "${KUBECTL}" -n kube-system get ds sysbox-deploy-k8s -o wide
            return
        fi
        sleep 2
    done

    "${KUBECTL}" -n kube-system logs ds/sysbox-deploy-k8s --tail=200 || true
    die "node ${NODE_NAME} did not reach sysbox-runtime=running"
}

run_smoke_test() {
    [[ "${RUN_TEST}" == "true" ]] || return 0

    info "Run smoke test pod ${TEST_POD}"
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
    need_cmd "${KUBECTL}"
    resolve_node_name
    deploy_sysbox
    wait_for_runtime
    run_smoke_test
    info "Deployment passed: ${IMAGE}"
}

main "$@"
