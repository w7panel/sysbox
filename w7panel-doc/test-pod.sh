#!/bin/bash
#
# test-pod.sh — 测试 Sysbox 系统容器（K3s + sysbox-runc）
#
# 用法:
#   ./test-pod.sh           # 创建 pod 并验证
#   ./test-pod.sh --exec    # 进入容器
#   ./test-pod.sh --clean   # 删除 pod
#
# 环境变量:
#   POD_NAME  容器名称 (默认: test-sysbox)
#   IMAGE     测试镜像 (默认: docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker)
#

set -euo pipefail

POD_NAME="${POD_NAME:-test-sysbox}"
IMAGE="${IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker}"
KUBECTL="${KUBECTL:-k3s kubectl}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

# ─── 创建 pod ─────────────────────────────────────────────────────
create_pod() {
    info "创建测试 pod: ${POD_NAME}..."

    cat <<EOF | ${KUBECTL} apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${POD_NAME}
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  containers:
  - name: ubuntu
    image: ${IMAGE}
    command: ["/sbin/init"]
    resources:
      limits:
        memory: "512Mi"
      requests:
        memory: "256Mi"
  restartPolicy: Always
EOF

    info "等待 pod 就绪..."
    ${KUBECTL} wait --for=condition=Ready pod/${POD_NAME} --timeout=120s

    info "Pod 状态:"
    ${KUBECTL} get pod ${POD_NAME} -o wide
}

# ─── 验证内存隔离 ──────────────────────────────────────────────────
verify_memory() {
    info "===== 内存隔离验证 ====="

    echo ""
    info "1. kubectl top pod:"
    ${KUBECTL} top pod ${POD_NAME} || warn "    (metrics server 可能未安装)"

    echo ""
    info "2. 容器内 free -m:"
    ${KUBECTL} exec ${POD_NAME} -- free -m

    echo ""
    info "3. 容器内 /proc/meminfo (前5行):"
    ${KUBECTL} exec ${POD_NAME} -- cat /proc/meminfo | head -5

    echo ""
    info "4. cgroup 实际内存使用:"
    ${KUBECTL} exec ${POD_NAME} -- cat /sys/fs/cgroup/memory.current 2>/dev/null || \
        warn "    (cgroup v2 不可读)"
}

# ─── 验证 swap 视图隔离 ────────────────────────────────────────────
verify_swap() {
    info "===== swap 视图隔离验证 ====="

    echo ""
    info "1. 容器内 /proc/swaps:"
    ${KUBECTL} exec ${POD_NAME} -- cat /proc/swaps

    echo ""
    info "2. 容器内 /proc/meminfo swap 字段:"
    ${KUBECTL} exec ${POD_NAME} -- awk '/^SwapTotal:|^SwapFree:/ {print}' /proc/meminfo

    echo ""
    info "3. cgroup swap 限制:"
    swap_max="$(${KUBECTL} exec ${POD_NAME} -- sh -c '
if [ -r /sys/fs/cgroup/memory.swap.max ]; then
    cg_path="$(awk -F: '"'"'$1 == "0" {print $3}'"'"' /proc/self/cgroup)"
    cg_path="${cg_path#/}"
    while :; do
        swap_max_file="/sys/fs/cgroup/${cg_path:+$cg_path/}memory.swap.max"
        if [ -r "$swap_max_file" ]; then
            value="$(cat "$swap_max_file")"
            if [ -n "$value" ] && [ "$value" != "max" ]; then
                echo "$value"
                exit 0
            fi
        fi
        [ -z "$cg_path" ] && break
        case "$cg_path" in
            */*) cg_path="${cg_path%/*}" ;;
            *) cg_path="" ;;
        esac
    done
    echo max
elif [ -r /sys/fs/cgroup/memory/memory.memsw.limit_in_bytes ]; then
    cat /sys/fs/cgroup/memory/memory.memsw.limit_in_bytes
fi
' | tr -d '\r')"
    swap_current="$(${KUBECTL} exec ${POD_NAME} -- sh -c 'cat /sys/fs/cgroup/memory.swap.current 2>/dev/null || true' | tr -d '\r')"
    echo "    memory.swap.max=${swap_max:-<missing>}"
    echo "    memory.swap.current=${swap_current:-<missing>}"

    swap_rows="$(${KUBECTL} exec ${POD_NAME} -- awk 'NR > 1 {n++} END {print n + 0}' /proc/swaps | tr -d '\r')"
    swap_total_kb="$(${KUBECTL} exec ${POD_NAME} -- awk '/^SwapTotal:/ {print $2}' /proc/meminfo | tr -d '\r')"
    if [[ "${swap_max}" =~ ^[0-9]+$ ]] && [[ "${swap_max}" -ge 4611686018427387903 ]]; then
        swap_max=0
    fi

    case "${swap_max}" in
        ""|"max"|"0")
            if [[ "${swap_rows}" == "0" && "${swap_total_kb}" == "0" ]]; then
                info "   swap 默认隐藏正确: /proc/swaps 只有 header，SwapTotal=0 ✅"
            else
                error "   swap 视图异常: memory.swap.max=${swap_max:-<missing>}, rows=${swap_rows}, SwapTotal=${swap_total_kb}KB"
                return 1
            fi
            ;;
        *)
            expected_kb=$((swap_max / 1024))
            if [[ "${swap_rows}" -ge "1" && "${swap_total_kb}" == "${expected_kb}" ]]; then
                info "   swap 限制显示正确: ${swap_total_kb}KB ✅"
            else
                error "   swap 限制显示异常: expected=${expected_kb}KB, rows=${swap_rows}, SwapTotal=${swap_total_kb}KB"
                return 1
            fi
            ;;
    esac
}

# ─── 验证 systemd 容器 ──────────────────────────────────────────────
verify_systemd() {
    info "===== systemd 容器验证 ====="

    echo ""
    info "1. PID 1 (应为 /sbin/init):"
    ${KUBECTL} exec ${POD_NAME} -- ps aux | head -10

    echo ""
    info "2. systemd 服务状态:"
    ${KUBECTL} exec ${POD_NAME} -- systemctl is-system-running --wait 2>/dev/null || \
        warn "    (systemd 可能仍在启动中)"

    echo ""
    info "3. 内存限制:"
    TOTAL=$(${KUBECTL} exec ${POD_NAME} -- free -m | awk 'NR==2{print $2}')
    if [[ "${TOTAL}" == "512" ]]; then
        info "   内存限制正确: ${TOTAL}MB ✅"
    else
        warn "   内存限制异常: ${TOTAL}MB (期望 512MB)"
    fi
}

# ─── 进入容器 ─────────────────────────────────────────────────────
exec_pod() {
    info "进入容器 ${POD_NAME}... 输入 exit 退出"
    ${KUBECTL} exec -it ${POD_NAME} -- /bin/bash 2>/dev/null || \
    ${KUBECTL} exec -it ${POD_NAME} -- /bin/sh
}

# ─── 清理 ──────────────────────────────────────────────────────────
clean_pod() {
    info "删除 pod: ${POD_NAME}..."
    ${KUBECTL} delete pod ${POD_NAME} --now 2>/dev/null && \
        info "已删除 ✅" || \
        warn "pod 不存在"
}

# ─── 主流程 ─────────────────────────────────────────────────────────
main() {
    echo ""
    echo "=========================================="
    echo "  Sysbox 系统容器测试"
    echo "  Image: ${IMAGE}"
    echo "=========================================="
    echo ""

    case "${1:-}" in
        --exec|-e)
            exec_pod
            ;;
        --clean|-c)
            clean_pod
            ;;
        --verify|-v)
            verify_memory
            verify_swap
            verify_systemd
            ;;
        *)
            clean_pod 2>/dev/null || true
            create_pod
            verify_memory
            verify_swap
            verify_systemd
            info ""
            info "全部测试完成 🎉"
            info "进入容器:  $0 --exec"
            info "清理 pod:   $0 --clean"
            ;;
    esac
}

main "$@"
