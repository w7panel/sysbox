#!/usr/bin/env bash
#
# Sysbox rootfs rw-layer 端到端测试：
#   1. 检查宿主 Sysbox 服务与 containerd proxy snapshotter。
#   2. 创建临时 PVC 和带 rootfs-rw-layer annotation 的 Deployment。
#   3. 验证业务容器 / 使用 PVC-backed fuse-overlayfs。
#   4. 写入、修改、删除 rootfs 文件，重建 Pod 后验证状态仍然存在。
#   5. 检查相关服务日志并清理临时资源。
#
# 环境变量：
#   KUBECONFIG          默认 /home/.kubeconfig
#   NAMESPACE           默认 default
#   STORAGE_CLASS       默认 longhorn
#   TEST_IMAGE          默认 ubuntu:24.04
#   TIMEOUT             默认 300s
#   HOST_DEBUG_NAMESPACE 默认 default
#   HOST_DEBUG_POD      默认 sysbox-debug-tools

set -Eeuo pipefail

KUBECONFIG="${KUBECONFIG:-/home/.kubeconfig}"
NAMESPACE="${NAMESPACE:-default}"
STORAGE_CLASS="${STORAGE_CLASS:-longhorn}"
TEST_IMAGE="${TEST_IMAGE:-ubuntu:24.04}"
TIMEOUT="${TIMEOUT:-300s}"
HOST_DEBUG_NAMESPACE="${HOST_DEBUG_NAMESPACE:-default}"
HOST_DEBUG_POD="${HOST_DEBUG_POD:-sysbox-debug-tools}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"

RUN_ID="$(date +%H%M%S)-$$"
NAME="snapshot-test-${RUN_ID}"
CONTAINER="system"
LABEL="app=${NAME}"
START_EPOCH="$(date +%s)"
TOKEN="sysbox-rootfs-${RUN_ID}"
POD=""
NEW_POD=""
CREATED=false

K=("${KUBECTL_BIN}" --kubeconfig "${KUBECONFIG}" --request-timeout=30s)

info() { printf '[INFO] %s\n' "$*"; }
pass() { printf '[PASS] %s\n' "$*"; }
die() { printf '[FAIL] %s\n' "$*" >&2; exit 1; }

host_exec() {
    "${K[@]}" exec -n "${HOST_DEBUG_NAMESPACE}" "${HOST_DEBUG_POD}" -- \
        nsenter -t 1 -m -u -i -n -p -- "$@"
}

collect_diagnostics() {
    info "收集失败诊断"
    "${K[@]}" get pod,pvc -n "${NAMESPACE}" -l "${LABEL}" -o wide >&2 || true
    "${K[@]}" describe deployment -n "${NAMESPACE}" "${NAME}" >&2 || true
    "${K[@]}" get events -n "${NAMESPACE}" --sort-by=.lastTimestamp >&2 || true
    host_exec journalctl -u sysbox-snapshotter -u sysbox-fs -u sysbox-mgr \
        --since "@${START_EPOCH}" --no-pager >&2 || true
}

cleanup() {
    [[ "${CREATED}" == true ]] || return 0

    info "清理临时资源 ${NAMESPACE}/${NAME}"
    "${K[@]}" delete deployment -n "${NAMESPACE}" "${NAME}" \
        --ignore-not-found --wait=true --timeout="${TIMEOUT}" >/dev/null 2>&1 || true
    "${K[@]}" delete pvc -n "${NAMESPACE}" "${NAME}" \
        --ignore-not-found --wait=true --timeout="${TIMEOUT}" >/dev/null 2>&1 || true
}

on_exit() {
    local status=$?
    trap - EXIT
    set +e
    if ((status != 0)); then
        collect_diagnostics
    fi
    cleanup
    if ((status == 0)); then
        printf '\n[PASS] Sysbox rootfs 持久化端到端测试通过\n'
    else
        printf '\n[FAIL] Sysbox rootfs 持久化端到端测试失败\n' >&2
    fi
    exit "${status}"
}

trap on_exit EXIT

wait_for_pvc_bound() {
    local deadline=$((SECONDS + ${TIMEOUT%s}))
    local phase

    while ((SECONDS < deadline)); do
        phase="$("${K[@]}" get pvc -n "${NAMESPACE}" "${NAME}" \
            -o jsonpath='{.status.phase}' 2>/dev/null || true)"
        [[ "${phase}" == Bound ]] && return 0
        sleep 2
    done
    return 1
}

latest_ready_pod() {
    local excluded_uid="${1:-}"
    local deadline=$((SECONDS + ${TIMEOUT%s}))
    local pod uid ready

    while ((SECONDS < deadline)); do
        while read -r pod; do
            [[ -n "${pod}" ]] || continue
            pod="${pod#pod/}"
            uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
                -o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
            ready="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
                -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
            if [[ -n "${uid}" && "${uid}" != "${excluded_uid}" && "${ready}" == True ]]; then
                printf '%s\n' "${pod}"
                return 0
            fi
        done < <("${K[@]}" get pod -n "${NAMESPACE}" -l "${LABEL}" \
            --sort-by=.metadata.creationTimestamp -o name 2>/dev/null || true)
        sleep 2
    done
    return 1
}

check_host() {
    local runtime_service services plugin

    info "检查 RuntimeClass、admission 和宿主服务"
    "${K[@]}" get runtimeclass sysbox-runc >/dev/null
    "${K[@]}" get mutatingwebhookconfiguration sysbox-webhook-mutator >/dev/null
    "${K[@]}" wait -n "${HOST_DEBUG_NAMESPACE}" --for=condition=Ready \
        "pod/${HOST_DEBUG_POD}" --timeout="${TIMEOUT}" >/dev/null

    runtime_service="$(host_exec sh -c '
        if systemctl is-active --quiet k3s; then
            echo k3s
        elif systemctl is-active --quiet k3s-agent; then
            echo k3s-agent
        else
            exit 1
        fi
    ')" || die "k3s/k3s-agent 服务未运行"
    services="$(host_exec systemctl is-active "${runtime_service}" sysbox-snapshotter sysbox-fs sysbox-mgr)"
    [[ "$(grep -c '^active$' <<<"${services}" || true)" -eq 4 ]] || {
        printf '%s\n' "${services}" >&2
        die "k3s/sysbox 服务未全部 active"
    }

    host_exec command -v fuse-overlayfs >/dev/null
    plugin="$(host_exec k3s ctr plugins ls -d id==sysbox)"
    grep -q 'ID:.*sysbox' <<<"${plugin}" || die "containerd 未注册 sysbox snapshotter"
    grep -q 'address.*\/run\/sysbox-snapshotter.sock' <<<"${plugin}" || \
        die "sysbox snapshotter socket 配置错误"
    grep -q 'Capabilities:.*remap-ids' <<<"${plugin}" || \
        die "sysbox snapshotter 未声明 remap-ids"
    pass "宿主服务与 containerd plugin 正常"
}

create_workload() {
    info "创建 PVC 和 Deployment: ${NAMESPACE}/${NAME}"
    CREATED=true
    "${K[@]}" apply -n "${NAMESPACE}" -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${NAME}
  labels:
    app: ${NAME}
spec:
  accessModes:
  - ReadWriteOnce
  storageClassName: ${STORAGE_CLASS}
  resources:
    requests:
      storage: 1Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${NAME}
  labels:
    app: ${NAME}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${NAME}
  template:
    metadata:
      labels:
        app: ${NAME}
      annotations:
        sysbox/rootfs-rw-layer: '[{"name":"${CONTAINER}","volumeName":"rootfs","path":"system"}]'
    spec:
      runtimeClassName: sysbox-runc
      hostUsers: false
      terminationGracePeriodSeconds: 0
      containers:
      - name: ${CONTAINER}
        image: ${TEST_IMAGE}
        command: ["sleep", "infinity"]
      volumes:
      - name: rootfs
        persistentVolumeClaim:
          claimName: ${NAME}
EOF

    wait_for_pvc_bound || die "PVC 未在 ${TIMEOUT} 内 Bound"
    "${K[@]}" rollout status -n "${NAMESPACE}" "deployment/${NAME}" \
        --timeout="${TIMEOUT}" >/dev/null
    POD="$(latest_ready_pod)" || die "测试 Pod 未在 ${TIMEOUT} 内 Ready"
    pass "测试 Pod Ready: ${POD}"
}

check_pod_contract() {
    local pod="$1"
    local runtime host_users containers annotation

    runtime="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
        -o jsonpath='{.spec.runtimeClassName}')"
    host_users="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
        -o jsonpath='{.spec.hostUsers}')"
    containers="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
        -o jsonpath='{range .spec.containers[*]}{.name}{"\n"}{end}')"
    annotation="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
        -o jsonpath='{.metadata.annotations.sysbox/rootfs-rw-layer}')"

    [[ "${runtime}" == sysbox-runc ]] || die "runtimeClassName 不是 sysbox-runc"
    [[ "${host_users}" == false ]] || die "hostUsers 不是 false"
    grep -qx "${CONTAINER}" <<<"${containers}" || die "业务容器缺失"
    grep -qx 'sysbox-rootfs' <<<"${containers}" || die "sysbox-rootfs sidecar 未注入"
    grep -q '"volumeName":"rootfs"' <<<"${annotation}" || die "rootfs annotation 丢失"
    pass "Pod runtime、user namespace、annotation 和 sidecar 正常"
}

check_root_mount() {
    local pod="$1"
    local mountinfo pv fuse_processes

    mountinfo="$("${K[@]}" exec -n "${NAMESPACE}" "${pod}" -c "${CONTAINER}" -- \
        sh -c 'grep " / / " /proc/self/mountinfo')"
    grep -q ' - fuse\.fuse-overlayfs ' <<<"${mountinfo}" || {
        printf '%s\n' "${mountinfo}" >&2
        die "容器 / 未使用 fuse-overlayfs"
    }

    pv="$("${K[@]}" get pvc -n "${NAMESPACE}" "${NAME}" \
        -o jsonpath='{.spec.volumeName}')"
    [[ -n "${pv}" ]] || die "PVC 未绑定 PV"
    fuse_processes="$(host_exec pgrep -af '[f]use-overlayfs')"
    grep -q "${pv}/mount/system/upper" <<<"${fuse_processes}" || \
        die "未找到指向 PVC 的 upperdir"
    grep -q "${pv}/mount/system/work" <<<"${fuse_processes}" || \
        die "未找到指向 PVC 的 workdir"
    pass "容器 / 使用 PVC-backed fuse-overlayfs upper/work"
}

write_rootfs_state() {
    local pod="$1"

    info "写入、修改并删除 rootfs 文件"
    "${K[@]}" exec -n "${NAMESPACE}" "${pod}" -c "${CONTAINER}" -- \
        sh -c '
            set -eu
            test -f /etc/debian_version
            token="$1"
            printf "%s\n" "${token}" > /a.txt
            printf "%s\n" "${token}-config" > /etc/snapshot-test.conf
            mkdir -p /snapshot-test-dir
            printf "%s\n" "${token}-nested" > /snapshot-test-dir/value
            rm /etc/debian_version
            sync
        ' sh "${TOKEN}"
    pass "rootfs 测试状态已写入"
}

recreate_and_verify() {
    local old_uid actual

    old_uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${POD}" \
        -o jsonpath='{.metadata.uid}')"
    info "删除旧 Pod 并等待重建: ${POD} (${old_uid})"
    "${K[@]}" delete pod -n "${NAMESPACE}" "${POD}" --wait=true \
        --timeout="${TIMEOUT}" >/dev/null

    NEW_POD="$(latest_ready_pod "${old_uid}")" || \
        die "新 Pod 未在 ${TIMEOUT} 内 Ready"
    check_pod_contract "${NEW_POD}"
    check_root_mount "${NEW_POD}"

    actual="$("${K[@]}" exec -n "${NAMESPACE}" "${NEW_POD}" -c "${CONTAINER}" -- \
        sh -c '
            set -eu
            token="$1"
            cat /a.txt
            test "$(cat /etc/snapshot-test.conf)" = "${token}-config"
            test "$(cat /snapshot-test-dir/value)" = "${token}-nested"
            test ! -e /etc/debian_version
        ' sh "${TOKEN}")"
    [[ "${actual}" == "${TOKEN}" ]] || die "/a.txt 内容不一致: ${actual}"
    pass "Pod UID 已变化且 rootfs 写入、修改、删除状态全部持久化"
}

check_logs() {
    local logs services

    info "检查 Sysbox 服务日志"
    logs="$(host_exec journalctl -u sysbox-snapshotter -u sysbox-fs -u sysbox-mgr \
        --since "@${START_EPOCH}" --no-pager)"
    if grep -Eqi 'panic|fatal|slice bounds|sysbox sidecar oci spec unavailable|fuse-overlayfs not installed' <<<"${logs}"; then
        printf '%s\n' "${logs}" >&2
        die "Sysbox 服务日志包含严重错误"
    fi
    services="$(host_exec systemctl is-active sysbox-snapshotter sysbox-fs sysbox-mgr)"
    [[ "$(grep -c '^active$' <<<"${services}" || true)" -eq 3 ]] || {
        printf '%s\n' "${services}" >&2
        die "测试后 Sysbox 服务状态异常"
    }
    pass "相关服务日志无 panic/fatal/已知回归错误"
}

main() {
    command -v "${KUBECTL_BIN}" >/dev/null || die "缺少 ${KUBECTL_BIN}"
    [[ -r "${KUBECONFIG}" ]] || die "kubeconfig 不可读: ${KUBECONFIG}"
    [[ "${TIMEOUT}" =~ ^[1-9][0-9]*s$ ]] || die "TIMEOUT 必须是秒数，例如 300s"
    [[ -n "${STORAGE_CLASS}" ]] || die "STORAGE_CLASS 不能为空"

    "${K[@]}" get namespace "${NAMESPACE}" >/dev/null
    "${K[@]}" get storageclass "${STORAGE_CLASS}" >/dev/null
    check_host
    create_workload
    check_pod_contract "${POD}"
    check_root_mount "${POD}"
    write_rootfs_state "${POD}"
    recreate_and_verify
    check_logs
}

main "$@"
