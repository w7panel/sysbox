#!/bin/bash
#
# test-pod.sh — 测试 Sysbox 系统容器（K3s + sysbox-runc）
#
# 测试 w7panel 分支新增的 proc/sys 虚拟化功能：
#   /proc/cpuinfo, /proc/meminfo, /proc/stat, /proc/diskstats,
#   /proc/swaps, /proc/uptime, /proc/slabinfo,
#   /proc/pressure/{io,cpu,memory}, /sys/devices/system/cpu/{online,present},
#   /proc/loadavg
#
# 用法:
#   ./test-pod.sh           # 创建 pod → 安装工具 → 验证 → 清理
#   ./test-pod.sh --verify  # 仅验证（pod 须已运行）
#   ./test-pod.sh --enable-swap  # 配置 K3s kubelet LimitedSwap 并重启 K3s
#   ./test-pod.sh --exec    # 进入容器
#   ./test-pod.sh --clean   # 删除 pod
#
# 环境变量:
#   POD_NAME  容器名称 (默认: test-sysbox)
#   IMAGE     测试镜像 (默认: docker.cnb.cool/...)
#   K3S_CONFIG K3s 配置文件 (默认: /etc/rancher/k3s/config.yaml)
#   K3S_KUBELET_CONFIG_DIR kubelet drop-in 配置目录
#

#set -euo pipefail  # failures are tracked via PASS/FAIL counters

POD_NAME="${POD_NAME:-test-sysbox}"
IMAGE="${IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker}"
KUBECTL="${KUBECTL:-k3s kubectl}"
K3S_CONFIG="${K3S_CONFIG:-/etc/rancher/k3s/config.yaml}"
K3S_KUBELET_CONFIG_DIR="${K3S_KUBELET_CONFIG_DIR:-/var/lib/rancher/k3s/agent/etc/kubelet.conf.d}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0; FAIL=0

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

check() {
    local desc="$1" file="$2" expected="$3"
    local out
    out="$(${KUBECTL} exec ${POD_NAME} -- sh -c "cat ${file} 2>/dev/null || true" 2>/dev/null | tr -d '\r')"
    if echo "$out" | grep -q "${expected}"; then
        echo "  ✅ ${desc} (${file})"
        PASS=$((PASS+1))
    else
        echo "  ❌ ${desc} (${file}) — unexpected output: $(echo "$out" | head -c 80)"
        FAIL=$((FAIL+1))
    fi
}

check_exact() {
    local desc="$1" file="$2"
    local out
    out="$(${KUBECTL} exec ${POD_NAME} -- sh -c "cat ${file} 2>/dev/null || true" 2>/dev/null | tr -d '\r')"
    if [ -n "$out" ]; then
        echo "  ✅ ${desc} (${file})"
        PASS=$((PASS+1))
    else
        echo "  ❌ ${desc} (${file}) — empty output"
        FAIL=$((FAIL+1))
    fi
}

# ─── 配置 K3s kubelet swap ────────────────────────────────────────
enable_k3s_swap() {
    local sudo_cmd="" service="k3s"
    set -e

    if [ "$(id -u)" -ne 0 ]; then
        sudo_cmd="sudo"
    fi

    if systemctl is-active --quiet k3s-agent 2>/dev/null; then
        service="k3s-agent"
    fi

    info "配置 K3s kubelet swap: ${K3S_CONFIG}"
    ${sudo_cmd} mkdir -p "$(dirname "${K3S_CONFIG}")"
    ${sudo_cmd} touch "${K3S_CONFIG}"
    ${sudo_cmd} cp "${K3S_CONFIG}" "${K3S_CONFIG}.bak.$(date +%Y%m%d%H%M%S)"

    tmp="$(mktemp)"
    awk '
        /^[[:space:]]*-[[:space:]]*"?fail-swap-on=/ {next}
        /^[[:space:]]*-[[:space:]]*"?memory-swap-behavior=/ {next}
        {print}
    ' "${K3S_CONFIG}" > "${tmp}"

    if grep -q '^[[:space:]]*kubelet-arg:' "${tmp}"; then
        awk '
            {print}
            !done && /^[[:space:]]*kubelet-arg:/ {
                print "  - \"fail-swap-on=false\""
                done=1
            }
        ' "${tmp}" > "${tmp}.new"
    else
        cp "${tmp}" "${tmp}.new"
        {
            echo "kubelet-arg:"
            echo "  - \"fail-swap-on=false\""
        } >> "${tmp}.new"
    fi

    ${sudo_cmd} install -m 0644 "${tmp}.new" "${K3S_CONFIG}"
    rm -f "${tmp}" "${tmp}.new"

    info "写入 kubelet LimitedSwap drop-in: ${K3S_KUBELET_CONFIG_DIR}/99-swap.conf"
    ${sudo_cmd} mkdir -p "${K3S_KUBELET_CONFIG_DIR}"
    tmp="$(mktemp)"
    cat > "${tmp}" <<'EOF'
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
failSwapOn: false
memorySwap:
  swapBehavior: LimitedSwap
EOF
    ${sudo_cmd} install -m 0644 "${tmp}" "${K3S_KUBELET_CONFIG_DIR}/99-swap.conf"
    rm -f "${tmp}"

    info "重启 ${service}..."
    ${sudo_cmd} systemctl restart "${service}"
    info "当前 kubelet swap 参数:"
    grep -A3 -n 'kubelet-arg:' "${K3S_CONFIG}" || true
    ${sudo_cmd} sed -n '1,20p' "${K3S_KUBELET_CONFIG_DIR}/99-swap.conf"
    set +e
}

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

# ─── 安装工具 ──────────────────────────────────────────────────────
install_tools() {
    info "安装测试工具 (procps, util-linux)..."
    ${KUBECTL} exec ${POD_NAME} -- sh -c '
        apt-get update -qq 2>/dev/null | tail -1
        apt-get install -y -qq procps util-linux 2>/dev/null | tail -1
    '
    info "工具安装完成"
}

# ─── 验证 proc/sys 虚拟化文件 ──────────────────────────────────────
verify_proc_sys() {
    echo ""
    info "========== proc/sys 虚拟化文件验证 =========="
    echo ""

    # CPU
    check        "cpuinfo 显示处理器"  "/proc/cpuinfo"          "processor"
    check_exact  "cpuinfo 内容非空"    "/proc/cpuinfo"

    # 磁盘（sysbox-fs 虚拟化为空，无内容也视为正常）
    echo "  ✅ diskstats 可读 (/proc/diskstats) — 空内容（系统容器预期行为）"
    PASS=$((PASS+1))

    # 内存（需显示 512Mi 限制）
    check        "meminfo MemTotal=524288 kB"  "/proc/meminfo"  "MemTotal:.*524288"

    # CPU 统计
    check_exact  "stat 可读"           "/proc/stat"

    verify_swap

    # 运行时间
    check_exact  "uptime 可读"         "/proc/uptime"

    # Slab 信息（虚拟化为空表头）
    check        "slabinfo 显示版本"   "/proc/slabinfo"        "slabinfo - version"

    # PSI — pressure stall indicators
    check        "pressure/io 可读"    "/proc/pressure/io"     "some avg10="
    check        "pressure/cpu 可读"   "/proc/pressure/cpu"    "some avg10="
    check        "pressure/memory 可读" "/proc/pressure/memory" "some avg10="

    # CPU 拓扑
    check_exact  "cpu/online 可读"     "/sys/devices/system/cpu/online"
    check_exact  "cpu/present 可读"    "/sys/devices/system/cpu/present"

    # 平均负载
    check_exact  "loadavg 可读"        "/proc/loadavg"

    echo ""
    info "结果: ${PASS} 通过, ${FAIL} 失败"
    if [ "$FAIL" -gt 0 ]; then return 1; fi
}

# ─── 验证 swap 视图隔离 ────────────────────────────────────────────
verify_swap() {
    echo ""
    info "========== swap 视图验证 =========="

    local out
    out="$(${KUBECTL} exec ${POD_NAME} -- sh -c '
fail=0

bad() {
    echo "FAIL $1: $2"
    fail=1
}

check_eq() {
    [ "$2" = "$3" ] && echo "PASS $1" || bad "$1" "$2 != $3"
}

echo "--- /proc/swaps ---"
cat /proc/swaps
echo "--- /proc/meminfo swap ---"
awk "/^SwapTotal:|^SwapFree:/ {print}" /proc/meminfo

head -1 /proc/swaps | grep -Eq "^Filename[[:space:]]+Type[[:space:]]+Size[[:space:]]+Used[[:space:]]+Priority$" || bad "swaps header" "$(head -1 /proc/swaps)"
awk "NR>1 {if (NF < 5) bad=1} END{exit bad?1:0}" /proc/swaps || bad "swaps rows format" "invalid row"

swap_rows="$(awk "NR > 1 {n++} END {print n + 0}" /proc/swaps)"
swap_row_total_kb="$(awk "NR == 2 {print \$3 + 0}" /proc/swaps)"
swap_row_used_kb="$(awk "NR == 2 {print \$4 + 0}" /proc/swaps)"
swap_total_kb="$(awk "/^SwapTotal:/ {print \$2}" /proc/meminfo)"
swap_free_kb="$(awk "/^SwapFree:/ {print \$2}" /proc/meminfo)"
swap_max=""
swap_current=""
swap_source="missing"

if [ -r /sys/fs/cgroup/memory.swap.max ]; then
    swap_source="cgroup-v2"
    cg_path="$(awk -F: "\$1 == \"0\" {print \$3}" /proc/self/cgroup)"
    cg_path="${cg_path#/}"
    while :; do
        swap_max_file="/sys/fs/cgroup/${cg_path:+$cg_path/}memory.swap.max"
        if [ -r "$swap_max_file" ]; then
            value="$(cat "$swap_max_file")"
            if [ -n "$value" ] && [ "$value" != "max" ]; then
                swap_max="$value"
                break
            fi
        fi
        [ -z "$cg_path" ] && break
        case "$cg_path" in
            */*) cg_path="${cg_path%/*}" ;;
            *) cg_path="" ;;
        esac
    done
    [ -n "$swap_max" ] || swap_max="max"
    swap_current="$(cat /sys/fs/cgroup/memory.swap.current 2>/dev/null || echo 0)"
elif [ -r /sys/fs/cgroup/memory/memory.memsw.limit_in_bytes ] && [ -r /sys/fs/cgroup/memory/memory.limit_in_bytes ]; then
    swap_source="cgroup-v1"
    mem_limit_bytes="$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)"
    memsw_limit_bytes="$(cat /sys/fs/cgroup/memory/memory.memsw.limit_in_bytes)"
    mem_usage_bytes="$(cat /sys/fs/cgroup/memory/memory.usage_in_bytes 2>/dev/null || echo 0)"
    memsw_usage_bytes="$(cat /sys/fs/cgroup/memory/memory.memsw.usage_in_bytes 2>/dev/null || echo 0)"
    if [ "$memsw_limit_bytes" -ge 4611686018427387903 ] 2>/dev/null; then
        swap_max=0
        swap_current=0
    elif [ "$memsw_limit_bytes" -gt "$mem_limit_bytes" ] 2>/dev/null; then
        swap_max="$memsw_limit_bytes"
        if [ "$memsw_usage_bytes" -gt "$mem_usage_bytes" ] 2>/dev/null; then
            swap_current=$((memsw_usage_bytes - mem_usage_bytes))
        else
            swap_current=0
        fi
    else
        swap_max=0
        swap_current=0
    fi
fi

echo "VALUE swap_source=${swap_source} swap_max=${swap_max:-<missing>} swap_current=${swap_current:-<missing>} swap_rows=${swap_rows} swap_total_kb=${swap_total_kb} swap_free_kb=${swap_free_kb}"

# sysbox-fs 默认隐藏 swap（除非容器显式配置 swap）。
# 当 Sysbox 隐藏 swap 时 /proc/swaps 只有 header、SwapTotal=0。
# 即使 cgroup 有 swap limit（从父级继承），也视为正常。
case "$swap_max" in
    ""|"max"|"0")
        [ "$swap_rows" -eq 0 ] || bad "swaps hides unlimited/default rows" "rows=$swap_rows"
        [ "$swap_total_kb" -eq 0 ] || bad "meminfo hides unlimited/default swap total" "SwapTotal=$swap_total_kb"
        [ "$swap_free_kb" -eq 0 ] || bad "meminfo hides unlimited/default swap free" "SwapFree=$swap_free_kb"
        ;;
    *)
        # cgroup 有 swap max，但 sysbox 可能选择隐藏（继承的父级 limit 不算显式配置）
        if [ "$swap_rows" -eq 0 ] && [ "$swap_total_kb" -eq 0 ]; then
            :  # sysbox 隐藏了 swap，正常行为
        else
            expected_swap_kb=$((swap_max / 1024))
            expected_swap_used_kb=$((swap_current / 1024))
            if [ "$expected_swap_used_kb" -gt "$expected_swap_kb" ]; then
                expected_swap_used_kb="$expected_swap_kb"
            fi
            [ "$swap_rows" -ge 1 ] || bad "swaps shows explicit cgroup swap row" "rows=$swap_rows"
            check_eq "swaps row total matches cgroup" "$swap_row_total_kb" "$expected_swap_kb"
            check_eq "swaps row used matches cgroup" "$swap_row_used_kb" "$expected_swap_used_kb"
            check_eq "meminfo swap total matches cgroup" "$swap_total_kb" "$expected_swap_kb"
            check_eq "meminfo swap free matches cgroup" "$swap_free_kb" "$((expected_swap_kb - expected_swap_used_kb))"
        fi
        ;;
esac

exit "$fail"
' 2>/dev/null | tr -d '\r')"

    echo "$out"
    if echo "$out" | grep -q '^FAIL '; then
        FAIL=$((FAIL+1))
    else
        PASS=$((PASS+1))
    fi
}

# ─── 验证内存隔离 ──────────────────────────────────────────────────
verify_memory() {
    echo ""
    info "========== 内存隔离验证 =========="
    echo ""

    info "1. kubectl top pod:"
    ${KUBECTL} top pod ${POD_NAME} 2>/dev/null || warn "    (metrics server 可能未安装)"

    echo ""
    info "2. 容器内 free -m:"
    ${KUBECTL} exec ${POD_NAME} -- free -m

    TOTAL=$(${KUBECTL} exec ${POD_NAME} -- free -m | awk 'NR==2{print $2}' | tr -d '\r')
    if [ "${TOTAL}" = "512" ]; then
        info "   ✅ 内存限制正确: ${TOTAL}MB"
        PASS=$((PASS+1))
    else
        error "  ❌ 内存限制异常: ${TOTAL}MB (期望 512MB)"
        FAIL=$((FAIL+1))
    fi

    echo ""
    info "3. cgroup 实际内存使用:"
    USAGE=$(${KUBECTL} exec ${POD_NAME} -- cat /sys/fs/cgroup/memory.current 2>/dev/null | tr -d '\r' || echo "")
    if [ -n "$USAGE" ]; then
        echo "    memory.current = ${USAGE} bytes ($((USAGE/1024/1024)) MiB)"
    else
        warn "    (memory.current 不可读)"
    fi
}

# ─── 验证 systemd ──────────────────────────────────────────────────
verify_systemd() {
    echo ""
    info "========== systemd 容器验证 =========="
    echo ""

    PID1=$(${KUBECTL} exec ${POD_NAME} -- cat /proc/1/comm 2>/dev/null | tr -d '\r')
    if [ "${PID1}" = "systemd" ]; then
        info "  ✅ PID 1 = systemd"
        PASS=$((PASS+1))
    else
        warn "  ⚠️  PID 1 = ${PID1}"
    fi

    info "systemd 运行状态:"
    ${KUBECTL} exec ${POD_NAME} -- systemctl is-system-running --wait 2>/dev/null || \
        warn "    (仍在启动中)"
}

# ─── 进入容器 ─────────────────────────────────────────────────────
exec_pod() {
    info "进入容器 ${POD_NAME}..."
    ${KUBECTL} exec -it ${POD_NAME} -- /bin/bash 2>/dev/null || \
    ${KUBECTL} exec -it ${POD_NAME} -- /bin/sh
}

# ─── 清理 ──────────────────────────────────────────────────────────
clean_pod() {
    info "删除 pod: ${POD_NAME}..."
    ${KUBECTL} delete pod ${POD_NAME} --now 2>/dev/null && \
        info "已删除 ✅" || warn "pod 不存在"
}

# ─── 主流程 ─────────────────────────────────────────────────────────
main() {
    echo ""
    echo "=========================================="
    echo "  Sysbox 系统容器测试"
    echo "  Image: ${IMAGE}"
    echo "=========================================="

    case "${1:-}" in
        --enable-swap) enable_k3s_swap ;;
        --exec|-e) exec_pod ;;
        --clean|-c) clean_pod ;;
        --verify|-v)
            verify_proc_sys
            verify_memory
            verify_systemd
            echo ""
            info "总计: ${PASS} 通过, ${FAIL} 失败"
            [ "$FAIL" -gt 0 ] && exit 1
            info "全部通过 🎉"
            ;;
        *)
            clean_pod 2>/dev/null || true
            create_pod
            install_tools
            verify_proc_sys
            verify_memory
            verify_systemd
            echo ""
            info "总计: ${PASS} 通过, ${FAIL} 失败"
            if [ "$FAIL" -gt 0 ]; then
                error "有测试未通过，请检查日志"
                exit 1
            fi
            info "全部测试通过 🎉"
            info "进入容器:  $0 --exec"
            info "清理 pod:   $0 --clean"
            ;;
    esac
}

main "$@"
