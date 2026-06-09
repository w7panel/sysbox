#!/usr/bin/env bash
#
# swaps.sh - Sysbox + K3s swap view verification helper.
#
# Usage:
#   ./w7panel-doc/swaps.sh --verify
#   ./w7panel-doc/swaps.sh --all
#   ./w7panel-doc/swaps.sh --enable-k3s
#   ./w7panel-doc/swaps.sh --create
#   ./w7panel-doc/swaps.sh --clean
#   ./w7panel-doc/swaps.sh --host-swapon-test
#
# Environment:
#   POD_NAME   test pod name (default: test-sysbox)
#   IMAGE      test image
#   KUBECTL    kubectl command (default: k3s kubectl)
#   SWAPFILE   temporary host swapfile for --host-swapon-test

set -u

POD_NAME="${POD_NAME:-test-sysbox}"
IMAGE="${IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker}"
KUBECTL="${KUBECTL:-k3s kubectl}"
SWAPFILE="${SWAPFILE:-/root/workspace/sysbox-host-swapon-test.swap}"
K3S_CONFIG="${K3S_CONFIG:-/etc/rancher/k3s/config.yaml}"
K3S_KUBELET_CONFIG_DIR="${K3S_KUBELET_CONFIG_DIR:-/var/lib/rancher/k3s/agent/etc/kubelet.conf.d}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0
FAIL=0

info() { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
bad() {
    echo -e "${RED}[FAIL]${NC} $*"
    FAIL=$((FAIL + 1))
}
ok() {
    echo -e "${GREEN}[PASS]${NC} $*"
    PASS=$((PASS + 1))
}

need_root() {
    if [ "$(id -u)" -ne 0 ]; then
        bad "需要 root 权限"
        exit 1
    fi
}

enable_k3s_swap() {
    need_root
    info "写入 K3s swap 配置"

    mkdir -p "$(dirname "$K3S_CONFIG")" "$K3S_KUBELET_CONFIG_DIR"
    touch "$K3S_CONFIG"
    cp "$K3S_CONFIG" "$K3S_CONFIG.bak.$(date +%Y%m%d%H%M%S)"

    tmp="$(mktemp)"
    awk '
        /^[[:space:]]*-[[:space:]]*"?fail-swap-on=/ {next}
        {print}
    ' "$K3S_CONFIG" > "$tmp"

    if grep -q '^[[:space:]]*kubelet-arg:' "$tmp"; then
        awk '
            {print}
            !done && /^[[:space:]]*kubelet-arg:/ {
                print "  - \"fail-swap-on=false\""
                done=1
            }
        ' "$tmp" > "$tmp.new"
    else
        cp "$tmp" "$tmp.new"
        {
            echo "kubelet-arg:"
            echo "  - \"fail-swap-on=false\""
        } >> "$tmp.new"
    fi
    install -m 0644 "$tmp.new" "$K3S_CONFIG"
    rm -f "$tmp" "$tmp.new"

    cat > "$tmp" <<'EOF'
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
failSwapOn: false
memorySwap:
  swapBehavior: LimitedSwap
EOF
    install -m 0644 "$tmp" "$K3S_KUBELET_CONFIG_DIR/99-swap.conf"
    rm -f "$tmp"

    info "重启 k3s"
    systemctl restart k3s
    systemctl is-active --quiet k3s && ok "k3s active" || bad "k3s 未启动"
}

create_pod() {
    info "重建测试 pod: $POD_NAME"
    ${KUBECTL} delete pod "$POD_NAME" --now --ignore-not-found >/dev/null 2>&1 || true
    ${KUBECTL} apply -f - <<EOF
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
    ${KUBECTL} wait --for=condition=Ready "pod/${POD_NAME}" --timeout=120s
}

clean_pod() {
    info "删除测试 pod: $POD_NAME"
    ${KUBECTL} delete pod "$POD_NAME" --now --ignore-not-found
}

host_swapon_test() {
    need_root
    info "宿主临时 swapon 测试: $SWAPFILE"

    swapoff "$SWAPFILE" >/dev/null 2>&1 || true
    rm -f "$SWAPFILE"

    echo "--- host /proc/swaps before ---"
    cat /proc/swaps

    dd if=/dev/zero of="$SWAPFILE" bs=1M count=64 status=none || {
        bad "创建 swapfile 失败"
        return
    }
    chmod 600 "$SWAPFILE"
    mkswap "$SWAPFILE" >/dev/null || {
        rm -f "$SWAPFILE"
        bad "mkswap 失败"
        return
    }
    if swapon "$SWAPFILE"; then
        ok "宿主 swapon 成功"
    else
        rm -f "$SWAPFILE"
        bad "宿主 swapon 失败；检查文件系统是否支持 swapfile"
        return
    fi

    echo "--- host /proc/swaps after swapon ---"
    cat /proc/swaps
    if ${KUBECTL} get pod "$POD_NAME" >/dev/null 2>&1; then
        echo "--- pod swap view while host swapfile is active ---"
        ${KUBECTL} exec "$POD_NAME" -- sh -c 'cat /proc/swaps; awk "/^SwapTotal:|^SwapFree:/ {print}" /proc/meminfo'
    fi

    swapoff "$SWAPFILE" || bad "宿主 swapoff 失败: $SWAPFILE"
    rm -f "$SWAPFILE"
    echo "--- host /proc/swaps after cleanup ---"
    cat /proc/swaps
}

verify_k3s() {
    info "验证 K3s swap 配置"

    if grep -q 'fail-swap-on=false' "$K3S_CONFIG" 2>/dev/null; then
        ok "kubelet fail-swap-on=false 已配置"
    else
        bad "缺少 kubelet fail-swap-on=false"
    fi

    if grep -q 'swapBehavior:[[:space:]]*LimitedSwap' "$K3S_KUBELET_CONFIG_DIR/99-swap.conf" 2>/dev/null; then
        ok "kubelet memorySwap LimitedSwap 已配置"
    else
        bad "缺少 memorySwap.swapBehavior: LimitedSwap"
    fi

    if systemctl is-active --quiet k3s 2>/dev/null; then
        ok "k3s active"
    elif ${KUBECTL} get nodes >/dev/null 2>&1; then
        ok "k3s kubectl 可访问节点"
    else
        bad "k3s 未启动或 kubectl 不可访问"
    fi
}

verify_pod_swap() {
    info "验证 pod swap 视图: $POD_NAME"

    ${KUBECTL} get pod "$POD_NAME" >/dev/null 2>&1 || {
        bad "pod 不存在: $POD_NAME"
        return
    }
    ${KUBECTL} wait --for=condition=Ready "pod/${POD_NAME}" --timeout=60s >/dev/null || {
        bad "pod 未 Ready"
        return
    }

    out="$(${KUBECTL} exec "$POD_NAME" -- sh -c '
set -u
echo "--- /proc/swaps ---"
cat /proc/swaps
echo "--- /proc/meminfo swap ---"
awk "/^SwapTotal:|^SwapFree:/ {print}" /proc/meminfo
echo "--- cgroup swap ---"
swap_max="$(cat /sys/fs/cgroup/memory.swap.max 2>/dev/null || echo missing)"
swap_current="$(cat /sys/fs/cgroup/memory.swap.current 2>/dev/null || echo missing)"
swap_rows="$(awk "NR > 1 {n++} END {print n + 0}" /proc/swaps)"
swap_total_kb="$(awk "/^SwapTotal:/ {print \$2 + 0}" /proc/meminfo)"
swap_free_kb="$(awk "/^SwapFree:/ {print \$2 + 0}" /proc/meminfo)"
echo "VALUE swap_max=${swap_max} swap_current=${swap_current} swap_rows=${swap_rows} swap_total_kb=${swap_total_kb} swap_free_kb=${swap_free_kb}"
' 2>/dev/null | tr -d '\r')"
    echo "$out"

    value_line="$(echo "$out" | awk '/^VALUE / {print}')"
    swap_max="$(echo "$value_line" | sed -n 's/.*swap_max=\([^ ]*\).*/\1/p')"
    swap_rows="$(echo "$value_line" | sed -n 's/.*swap_rows=\([^ ]*\).*/\1/p')"
    swap_total_kb="$(echo "$value_line" | sed -n 's/.*swap_total_kb=\([^ ]*\).*/\1/p')"

    case "$swap_max" in
        ""|"missing")
            bad "无法读取 pod cgroup memory.swap.max"
            ;;
        "max"|"0")
            if [ "${swap_total_kb:-0}" -eq 0 ] && [ "${swap_rows:-0}" -eq 0 ]; then
                ok "pod swap 未配置时隐藏 swap"
            else
                bad "memory.swap.max=$swap_max 但 pod 暴露了 swap"
            fi
            ;;
        *)
            expected_kb=$((swap_max / 1024))
            if [ "${swap_rows:-0}" -ge 1 ] && [ "${swap_total_kb:-0}" -eq "$expected_kb" ]; then
                ok "pod /proc/swaps 与 cgroup memory.swap.max 匹配"
            else
                bad "pod swap 视图不匹配：expected ${expected_kb}kB, rows=${swap_rows:-?}, SwapTotal=${swap_total_kb:-?}kB"
            fi
            ;;
    esac
}

summary() {
    echo ""
    info "结果: $PASS 通过, $FAIL 失败"
    [ "$FAIL" -eq 0 ]
}

case "${1:---verify}" in
    --enable-k3s)
        enable_k3s_swap
        summary
        ;;
    --create)
        create_pod
        ;;
    --clean)
        clean_pod
        ;;
    --host-swapon-test)
        host_swapon_test
        summary
        ;;
    --verify)
        verify_k3s
        verify_pod_swap
        summary
        ;;
    --all)
        enable_k3s_swap
        create_pod
        verify_k3s
        verify_pod_swap
        summary
        ;;
    *)
        echo "usage: $0 [--verify|--all|--enable-k3s|--create|--clean|--host-swapon-test]" >&2
        exit 2
        ;;
esac
