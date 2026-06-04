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
            verify_systemd
            ;;
        *)
            clean_pod 2>/dev/null || true
            create_pod
            verify_memory
            verify_systemd
            info ""
            info "全部测试完成 🎉"
            info "进入容器:  $0 --exec"
            info "清理 pod:   $0 --clean"
            ;;
    esac
}

main "$@"
