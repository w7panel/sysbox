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
#   ./test-pod.sh all       # 创建 pod → 安装工具 → 验证 → 清理（默认）
#   ./test-pod.sh create    # 仅创建 pod
#   ./test-pod.sh verify    # 仅验证（pod 须已运行）
#   ./test-pod.sh exec      # 进入容器
#   ./test-pod.sh clean     # 删除 pod
#
# 环境变量:
#   POD_NAME  容器名称 (默认: test-sysbox)
#   IMAGE     测试镜像 (默认: docker.cnb.cool/...)
#   KUBECONFIG kubeconfig 文件 (默认: /home/.kubeconfig)
#

#set -euo pipefail  # failures are tracked via PASS/FAIL counters

POD_NAME="${POD_NAME:-test-sysbox}"
IMAGE="${IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker}"
NODE_NAME="${NODE_NAME:-}"
KUBECONFIG="${KUBECONFIG:-/home/.kubeconfig}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
K=("${KUBECTL_BIN}" --kubeconfig "${KUBECONFIG}")

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0; FAIL=0

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

check() {
    local desc="$1" file="$2" expected="$3"
    local out
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c "cat ${file} 2>/dev/null || true" 2>/dev/null | tr -d '\r')"
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
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c "cat ${file} 2>/dev/null || true" 2>/dev/null | tr -d '\r')"
    if [ -n "$out" ]; then
        echo "  ✅ ${desc} (${file})"
        PASS=$((PASS+1))
    else
        echo "  ❌ ${desc} (${file}) — empty output"
        FAIL=$((FAIL+1))
    fi
}

# ─── 创建 pod ─────────────────────────────────────────────────────
create_pod() {
    info "创建测试 pod: ${POD_NAME}..."
    cat <<EOF | "${K[@]}" apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${POD_NAME}
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
${NODE_NAME:+  nodeName: ${NODE_NAME}}
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
    [ "${PIPESTATUS[1]}" -eq 0 ] || return 1
    info "等待 pod 就绪..."
    "${K[@]}" wait --for=condition=Ready "pod/${POD_NAME}" --timeout=120s || return 1
    info "Pod 状态:"
    "${K[@]}" get pod "${POD_NAME}" -o wide || return 1
}

# ─── 安装工具 ──────────────────────────────────────────────────────
install_tools() {
    info "安装测试工具 (procps, util-linux)..."
    "${K[@]}" exec "${POD_NAME}" -- sh -c '
        set -e
        apt-get update -qq >/dev/null
        apt-get install -y -qq procps util-linux >/dev/null
    ' || return 1
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

    # 磁盘
    check_exact  "diskstats 可读"      "/proc/diskstats"

    # 内存（需显示 512Mi 限制）
    check        "meminfo MemTotal=524288 kB"  "/proc/meminfo"  "MemTotal:.*524288"

    # CPU 统计
    check_exact  "stat 可读"           "/proc/stat"
    verify_cpu_topology

    verify_swap
    verify_swapon

    # 运行时间
    check_exact  "uptime 可读"         "/proc/uptime"

    # Slab 信息
    check        "slabinfo 显示版本"   "/proc/slabinfo"        "slabinfo - version"
    verify_slabinfo

    # PSI — pressure stall indicators
    check        "pressure/io 可读"    "/proc/pressure/io"     "some avg10="
    check        "pressure/cpu 可读"   "/proc/pressure/cpu"    "some avg10="
    check        "pressure/memory 可读" "/proc/pressure/memory" "some avg10="
    verify_pressure

    # CPU 拓扑
    check_exact  "cpu/online 可读"     "/sys/devices/system/cpu/online"
    check_exact  "cpu/present 可读"    "/sys/devices/system/cpu/present"

    verify_loadavg

    echo ""
    info "结果: ${PASS} 通过, ${FAIL} 失败"
    if [ "$FAIL" -gt 0 ]; then return 1; fi
}

# ─── 验证 CPU 拓扑视图一致性 ────────────────────────────────────────
verify_cpu_topology() {
    echo ""
    info "========== CPU 拓扑一致性验证 =========="

    local out
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c '
fail=0

bad() {
    echo "FAIL $1: $2"
    fail=1
}

ok() {
    echo "PASS $1"
}

count_range() {
    awk -v range="$1" "
    BEGIN {
        n = split(range, parts, \",\")
        total = 0
        for (i = 1; i <= n; i++) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, \"\", parts[i])
            if (parts[i] == \"\") continue
            if (parts[i] ~ /^[0-9]+-[0-9]+$/) {
                split(parts[i], b, \"-\")
                if (b[2] >= b[1]) total += b[2] - b[1] + 1
            } else if (parts[i] ~ /^[0-9]+$/) {
                total++
            }
        }
        print total
    }"
}

online="$(tr -d "[:space:]" < /sys/devices/system/cpu/online 2>/dev/null)"
present="$(tr -d "[:space:]" < /sys/devices/system/cpu/present 2>/dev/null)"
cpuinfo_count="$(grep -c "^processor[[:space:]]*:" /proc/cpuinfo 2>/dev/null || echo 0)"
stat_count="$(grep -Ec "^cpu[0-9]+[[:space:]]" /proc/stat 2>/dev/null || echo 0)"
online_count="$(count_range "$online")"
present_count="$(count_range "$present")"

echo "VALUE cpu_online=${online:-<empty>} cpu_present=${present:-<empty>} cpuinfo_count=${cpuinfo_count} stat_count=${stat_count}"

[ "$online_count" -gt 0 ] || bad "cpu online range non-empty" "$online"
[ "$present_count" -gt 0 ] || bad "cpu present range non-empty" "$present"
[ "$cpuinfo_count" -eq "$online_count" ] || bad "cpuinfo count matches online" "$cpuinfo_count != $online_count"
[ "$stat_count" -eq "$online_count" ] || bad "proc stat cpu lines match online" "$stat_count != $online_count"
[ "$present_count" -ge "$online_count" ] || bad "present covers online" "$present_count < $online_count"

[ "$fail" -eq 0 ] && ok "cpu topology views are consistent"
exit "$fail"
' 2>/dev/null | tr -d '\r')"

    echo "$out"
    if echo "$out" | grep -q '^FAIL '; then
        FAIL=$((FAIL+1))
    else
        PASS=$((PASS+1))
    fi
}

# ─── 验证 slabinfo 视图来源 ─────────────────────────────────────────
verify_slabinfo() {
    echo ""
    info "========== slabinfo 视图验证 =========="

    local out
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c '
fail=0

bad() {
    echo "FAIL $1: $2"
    fail=1
}

ok() {
    echo "PASS $1"
}

slab_header="$(head -1 /proc/slabinfo 2>/dev/null)"
slab_rows="$(awk "NR > 2 {n++} END {print n + 0}" /proc/slabinfo 2>/dev/null)"
source="host-fallback"

if [ -r /sys/fs/cgroup/memory/memory.kmem.slabinfo ]; then
    source="cgroup-v1"
    cg_header="$(head -1 /sys/fs/cgroup/memory/memory.kmem.slabinfo 2>/dev/null)"
    [ "$slab_header" = "$cg_header" ] || bad "slabinfo cgroup header matches" "$slab_header != $cg_header"
elif [ "$slab_rows" -eq 0 ]; then
    bad "slabinfo fallback has rows" "rows=$slab_rows"
fi

echo "VALUE slabinfo_source=${source} slab_rows=${slab_rows}"
echo "$slab_header" | grep -q "^slabinfo - version:" || bad "slabinfo version header" "$slab_header"

[ "$fail" -eq 0 ] && ok "slabinfo source is valid"
exit "$fail"
' 2>/dev/null | tr -d '\r')"

    echo "$out"
    if echo "$out" | grep -q '^FAIL '; then
        FAIL=$((FAIL+1))
    else
        PASS=$((PASS+1))
    fi
}

# ─── 验证 PSI pressure 视图来源 ──────────────────────────────────────
verify_pressure() {
    echo ""
    info "========== PSI pressure 视图验证 =========="

    local out
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c '
fail=0

bad() {
    echo "FAIL $1: $2"
    fail=1
}

ok() {
    echo "PASS $1"
}

avg_fields() {
    grep "^$1 " "$2" | tr " " "\n" | grep -E "^avg(10|60|300)=" | tr "\n" " "
}

check_pressure() {
    name="$1"
    cgroup_file="$2"
    proc_file="/proc/pressure/$name"

    [ -s "$proc_file" ] || { bad "pressure/$name readable" "empty"; return; }
    grep -q "^some avg10=" "$proc_file" || bad "pressure/$name has some line" "$(head -1 "$proc_file")"

    if [ -r "/sys/fs/cgroup/$cgroup_file" ]; then
        source="cgroup-v2"
        cg_file="/sys/fs/cgroup/$cgroup_file"
    elif [ -r "/sys/fs/cgroup/$name/$cgroup_file" ]; then
        source="cgroup-v1"
        cg_file="/sys/fs/cgroup/$name/$cgroup_file"
    elif [ "$name" = "io" ] && [ -r "/sys/fs/cgroup/blkio/$cgroup_file" ]; then
        source="cgroup-v1"
        cg_file="/sys/fs/cgroup/blkio/$cgroup_file"
    else
        source="host-fallback"
        cg_file=""
    fi

    if [ -n "$cg_file" ]; then
        proc_some="$(avg_fields some "$proc_file")"
        cg_some="$(avg_fields some "$cg_file")"
        [ "$proc_some" = "$cg_some" ] || bad "pressure/$name some avg matches cgroup" "$proc_some != $cg_some"

        if grep -q "^full " "$proc_file" && grep -q "^full " "$cg_file"; then
            proc_full="$(avg_fields full "$proc_file")"
            cg_full="$(avg_fields full "$cg_file")"
            [ "$proc_full" = "$cg_full" ] || bad "pressure/$name full avg matches cgroup" "$proc_full != $cg_full"
        fi
    fi

    echo "VALUE pressure_${name}_source=${source}"
}

check_pressure io io.pressure
check_pressure cpu cpu.pressure
check_pressure memory memory.pressure

[ "$fail" -eq 0 ] && ok "pressure views are valid"
exit "$fail"
' 2>/dev/null | tr -d '\r')"

    echo "$out"
    if echo "$out" | grep -q '^FAIL '; then
        FAIL=$((FAIL+1))
    else
        PASS=$((PASS+1))
    fi
}

# ─── 验证 loadavg 视图会反映容器内负载 ──────────────────────────────
verify_loadavg() {
    echo ""
    info "========== loadavg 视图验证 =========="

    local out
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c '
fail=0

bad() {
    echo "FAIL $1: $2"
    fail=1
}

ok() {
    echo "PASS $1"
}

check_idle_top() {
    idle_ok=0
    idle_seen=0
    i=0
    while [ "$i" -lt 3 ]; do
        load="$(cat /proc/loadavg 2>/dev/null)"
        parsed="$(printf "%s\n" "$load" | awk "{ split(\$4, tasks, \"/\"); printf \"%s %s %s %s %s %s\", \$1, \$2, \$3, tasks[1], tasks[2], \$5 }")"
        set -- $parsed
        load1="$1"; load5="$2"; load15="$3"; running="$4"; total="$5"; lastpid="$6"

        top_header="$(top -b -n1 | head -5 | tr -d "\r")"
        top_load="$(printf "%s\n" "$top_header" | awk -F"load average: " "/load average:/ {print \$2; exit}" | tr -d " ")"
        proc_load="$(printf "%s,%s,%s" "$load1" "$load5" "$load15")"

        echo "VALUE idle_loadavg_sample_${i}=${load}"
        echo "VALUE idle_top_load_sample_${i}=${top_load:-<missing>}"

        task_count=0
        for task_dir in /proc/[0-9]*/task/[0-9]*; do
            [ -e "$task_dir" ] || continue
            task_count=$((task_count + 1))
        done
        max_total=$((task_count + 64))
        echo "VALUE idle_task_count_sample_${i}=${task_count}"

        [ -n "$top_load" ] || bad "idle top loadavg line" "$top_header"
        [ "$top_load" = "$proc_load" ] || bad "idle top loadavg matches proc" "$top_load != $proc_load"
        [ "${total:-0}" -gt 0 ] 2>/dev/null || bad "idle loadavg total task count" "$load"
        [ "${total:-0}" -le "$max_total" ] 2>/dev/null || bad "idle loadavg total not host-wide" "loadavg_total=$total task_count=$task_count"
        [ "${lastpid:-0}" -gt 0 ] 2>/dev/null || bad "idle loadavg last pid" "$load"
        [ "${running:-0}" -ge 0 ] 2>/dev/null || bad "idle loadavg running task count" "$load"

        if [ "$fail" -eq 0 ]; then
            idle_ok=1
        fi
        idle_seen=$((idle_seen + 1))
        sleep 3
        i=$((i + 1))
    done

    [ "$idle_seen" -gt 0 ] || bad "idle top sampled" "no samples"
    [ "$idle_ok" -eq 1 ] && ok "idle top loadavg remains valid without extra workers"
}

cleanup() {
    if [ -n "${pids:-}" ]; then
        kill $pids >/dev/null 2>&1 || true
        wait $pids >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

before="$(cat /proc/loadavg 2>/dev/null)"
[ -n "$before" ] || bad "loadavg readable" "empty"
echo "VALUE loadavg_before=${before}"

check_idle_top

workers="$(( $(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2) * 2 ))"
[ "$workers" -lt 4 ] && workers=4
[ "$workers" -gt 16 ] && workers=16

pids=""
i=0
while [ "$i" -lt "$workers" ]; do
    sh -c "while :; do :; done" &
    pids="$pids $!"
    i=$((i + 1))
done

max=0
i=0
while [ "$i" -lt 8 ]; do
    current="$(cat /proc/loadavg 2>/dev/null)"
    centi="$(printf "%s\n" "$current" | awk "{ split(\$1, p, \".\"); print (p[1] * 100) + p[2] }")"
    echo "VALUE loadavg_sample_${i}=${current}"
    [ "$centi" -gt "$max" ] && max="$centi"
    [ "$max" -gt 0 ] && break
    sleep 1
    i=$((i + 1))
done

[ "$max" -gt 0 ] || bad "loadavg reflects runnable tasks" "max load1 stayed 0.00"
[ "$fail" -eq 0 ] && ok "loadavg reflects container load"
exit "$fail"
' 2>/dev/null | tr -d '\r')"

    echo "$out"
    if echo "$out" | grep -q '^FAIL '; then
        FAIL=$((FAIL+1))
    else
        PASS=$((PASS+1))
    fi
}

# ─── 验证 swap 视图隔离 ────────────────────────────────────────────
verify_swap() {
    echo ""
    info "========== swap 视图验证 =========="

    local out
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c '
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

# ─── 验证 pod 内 swapon 行为 ───────────────────────────────────────
verify_swapon() {
    echo ""
    info "========== pod 内 swapon 验证 =========="

    local host_before host_after out
    host_before="$(tr -d '\r' < /proc/swaps 2>/dev/null)"

    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c '
set -u

fail=0
swapfile="/tmp/sysbox-swapon-test.swap"

bad() {
    echo "FAIL $1: $2"
    fail=1
}

ok() {
    echo "PASS $1"
}

cleanup() {
    swapoff "$swapfile" >/dev/null 2>&1 || true
    rm -f "$swapfile" \
        /tmp/sysbox-swapon-test.mkswap.out \
        /tmp/sysbox-swapon-test.mkswap.err \
        /tmp/sysbox-swapon-test.swapon.out \
        /tmp/sysbox-swapon-test.swapon.err \
        /tmp/sysbox-swapon-test.swapoff.out \
        /tmp/sysbox-swapon-test.swapoff.err
}

cleanup

echo "--- before /proc/swaps ---"
cat /proc/swaps
before_rows="$(awk "NR > 1 {n++} END {print n + 0}" /proc/swaps)"
before_swap_total="$(awk "/^SwapTotal:/ {print \$2}" /proc/meminfo)"
before_swap_free="$(awk "/^SwapFree:/ {print \$2}" /proc/meminfo)"

if command -v fallocate >/dev/null 2>&1; then
    fallocate -l 16M "$swapfile" || bad "create swapfile" "fallocate failed"
else
    dd if=/dev/zero of="$swapfile" bs=1M count=16 >/dev/null 2>&1 || bad "create swapfile" "dd failed"
fi

chmod 600 "$swapfile" 2>/dev/null || bad "chmod swapfile" "chmod failed"
mkswap "$swapfile" >/tmp/sysbox-swapon-test.mkswap.out 2>/tmp/sysbox-swapon-test.mkswap.err
mkswap_rc=$?
[ "$mkswap_rc" -eq 0 ] && ok "mkswap swapfile" || bad "mkswap swapfile" "$(cat /tmp/sysbox-swapon-test.mkswap.err)"

swapon "$swapfile" >/tmp/sysbox-swapon-test.swapon.out 2>/tmp/sysbox-swapon-test.swapon.err
swapon_rc=$?
swapon_err="$(cat /tmp/sysbox-swapon-test.swapon.err)"
echo "VALUE swapon_rc=${swapon_rc} swapon_err=${swapon_err}"

case "$swapon_rc:$swapon_err" in
    0:*)
        ok "swapon accepted by syscall virtualization"
        ;;
    *"Operation not permitted"*|*"Not superuser"*)
        ok "swapon denied safely"
        ;;
    *)
        bad "swapon result" "rc=${swapon_rc} err=${swapon_err}"
        ;;
esac

echo "--- after /proc/swaps ---"
cat /proc/swaps
after_rows="$(awk "NR > 1 {n++} END {print n + 0}" /proc/swaps)"
after_swap_total="$(awk "/^SwapTotal:/ {print \$2}" /proc/meminfo)"
after_swap_free="$(awk "/^SwapFree:/ {print \$2}" /proc/meminfo)"

[ "$after_rows" -eq "$before_rows" ] || bad "swapon does not add proc swaps row" "rows ${before_rows} -> ${after_rows}"
[ "$after_swap_total" = "$before_swap_total" ] || bad "swapon does not change SwapTotal" "${before_swap_total} -> ${after_swap_total}"
[ "$after_swap_free" = "$before_swap_free" ] || bad "swapon does not change SwapFree" "${before_swap_free} -> ${after_swap_free}"

swapoff "$swapfile" >/tmp/sysbox-swapon-test.swapoff.out 2>/tmp/sysbox-swapon-test.swapoff.err
swapoff_rc=$?
swapoff_err="$(cat /tmp/sysbox-swapon-test.swapoff.err)"
echo "VALUE swapoff_rc=${swapoff_rc} swapoff_err=${swapoff_err}"

case "$swapoff_rc:$swapoff_err" in
    0:*)
        ok "swapoff accepted by syscall virtualization"
        ;;
    *"Operation not permitted"*|*"Not superuser"*|*"Invalid argument"*)
        ok "swapoff denied or no-op safely"
        ;;
    *)
        bad "swapoff result" "rc=${swapoff_rc} err=${swapoff_err}"
        ;;
esac

cleanup
exit "$fail"
' 2>/dev/null | tr -d '\r')"

    host_after="$(tr -d '\r' < /proc/swaps 2>/dev/null)"

    echo "$out"
    if echo "$out" | grep -q '^FAIL '; then
        FAIL=$((FAIL+1))
    elif [ "$host_before" != "$host_after" ]; then
        echo "FAIL host /proc/swaps changed after pod swapon"
        FAIL=$((FAIL+1))
    else
        echo "PASS host /proc/swaps unchanged after pod swapon"
        PASS=$((PASS+1))
    fi
}

# ─── 严格验证资源视图格式与可打印性 ────────────────────────────────
verify_strict_resource_view() {
    echo ""
    info "========== 资源视图严格验证 =========="

    local out
    out="$("${K[@]}" exec "${POD_NAME}" -- sh -c '
set -u
fail=0

ok() { echo "PASS $1"; }
bad() { echo "FAIL $1: $2"; fail=1; }

count_range() {
    awk -v range="$1" "BEGIN {
        n = split(range, parts, \",\")
        for (i = 1; i <= n; i++) {
            if (parts[i] ~ /^[0-9]+-[0-9]+$/) {
                split(parts[i], bounds, \"-\")
                total += bounds[2] - bounds[1] + 1
            } else if (parts[i] ~ /^[0-9]+$/) {
                total++
            }
        }
        print total + 0
    }"
}

paths="/proc/cpuinfo /proc/diskstats /proc/meminfo /proc/stat /proc/swaps /proc/uptime /proc/slabinfo /proc/pressure/io /proc/pressure/cpu /proc/pressure/memory /sys/devices/system/cpu/online /sys/devices/system/cpu/present /proc/loadavg"
for path in $paths; do
    [ -r "$path" ] && ok "$path readable" || bad "$path readable" "not readable"
    badhex="$(dd if="$path" bs=4096 count=1 2>/dev/null | od -An -v -tx1 | awk '\''{
        for (i = 1; i <= NF; i++) {
            if ($i != "09" && $i != "0a" && ($i < "20" || $i > "7e")) {
                print $i
                exit
            }
        }
    }'\'')"
    [ -z "$badhex" ] && ok "$path printable" || bad "$path printable" "bad byte $badhex"
done

online="$(tr -d "[:space:]" < /sys/devices/system/cpu/online)"
present="$(tr -d "[:space:]" < /sys/devices/system/cpu/present)"
visible="$(count_range "$online")"
echo "$online" | grep -Eq "^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$" && ok "cpu online format" || bad "cpu online format" "$online"
echo "$present" | grep -Eq "^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$" && ok "cpu present format" || bad "cpu present format" "$present"
[ "$online" = "$present" ] && ok "cpu online/present aligned" || bad "cpu online/present aligned" "$online != $present"

cpuinfo_count="$(grep -c "^processor" /proc/cpuinfo || true)"
stat_count="$(grep -Ec "^cpu[0-9]+ " /proc/stat || true)"
[ "$cpuinfo_count" -eq "$visible" ] && ok "cpuinfo count matches online" || bad "cpuinfo count matches online" "$cpuinfo_count != $visible"
[ "$stat_count" -eq "$visible" ] && ok "stat count matches online" || bad "stat count matches online" "$stat_count != $visible"
awk -F: '\''BEGIN {next_id=0} /^processor/ {gsub(/[[:space:]]/, "", $2); if ($2 != next_id++) exit 1}'\'' /proc/cpuinfo && ok "cpuinfo ids sequential" || bad "cpuinfo ids sequential" "unexpected processor id"
awk '\''/^btime [0-9]+$/ {found=1} END {exit found ? 0 : 1}'\'' /proc/stat && ok "stat btime" || bad "stat btime" "missing"

grep -Eq "^[0-9]+\\.[0-9]{2} [0-9]+\\.[0-9]{2}$" /proc/uptime && ok "uptime format" || bad "uptime format" "$(cat /proc/uptime)"
grep -Eq "^[0-9]+\\.[0-9]{2} [0-9]+\\.[0-9]{2} [0-9]+\\.[0-9]{2} [0-9]+/[0-9]+ [0-9]+$" /proc/loadavg && ok "loadavg format" || bad "loadavg format" "$(cat /proc/loadavg)"

for key in MemTotal MemFree MemAvailable Cached Active Inactive AnonPages Slab KernelStack PageTables SwapTotal SwapFree; do
    grep -q "^$key:" /proc/meminfo && ok "meminfo $key" || bad "meminfo $key" "missing"
done

head -1 /proc/slabinfo | grep -Eq "^slabinfo - version: 2\\.1" && ok "slabinfo header" || bad "slabinfo header" "$(head -1 /proc/slabinfo)"
for path in /proc/pressure/io /proc/pressure/cpu /proc/pressure/memory; do
    grep -q "^some avg10=" "$path" && ok "$path format" || bad "$path format" "$(cat "$path")"
done

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
    "${K[@]}" top pod "${POD_NAME}" 2>/dev/null || warn "    (metrics server 可能未安装)"

    echo ""
    info "2. 容器内 free -m:"
    "${K[@]}" exec "${POD_NAME}" -- free -m

    TOTAL=$("${K[@]}" exec "${POD_NAME}" -- free -m | awk 'NR==2{print $2}' | tr -d '\r')
    if [ "${TOTAL}" = "512" ]; then
        info "   ✅ 内存限制正确: ${TOTAL}MB"
        PASS=$((PASS+1))
    else
        error "  ❌ 内存限制异常: ${TOTAL}MB (期望 512MB)"
        FAIL=$((FAIL+1))
    fi

    echo ""
    info "3. cgroup 实际内存使用:"
    USAGE=$("${K[@]}" exec "${POD_NAME}" -- cat /sys/fs/cgroup/memory.current 2>/dev/null | tr -d '\r' || echo "")
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

    PID1=$("${K[@]}" exec "${POD_NAME}" -- cat /proc/1/comm 2>/dev/null | tr -d '\r')
    if [ "${PID1}" = "systemd" ]; then
        info "  ✅ PID 1 = systemd"
        PASS=$((PASS+1))
    else
        warn "  ⚠️  PID 1 = ${PID1}"
    fi

    info "systemd 运行状态:"
    "${K[@]}" exec "${POD_NAME}" -- systemctl is-system-running --wait 2>/dev/null || \
        warn "    (仍在启动中)"
}

# ─── 进入容器 ─────────────────────────────────────────────────────
exec_pod() {
    info "进入容器 ${POD_NAME}..."
    "${K[@]}" exec -it "${POD_NAME}" -- /bin/bash 2>/dev/null || \
    "${K[@]}" exec -it "${POD_NAME}" -- /bin/sh
}

# ─── 清理 ──────────────────────────────────────────────────────────
clean_pod() {
    info "删除 pod: ${POD_NAME}..."
    if "${K[@]}" delete pod "${POD_NAME}" --now 2>/dev/null; then
        info "已删除 ✅"
    else
        warn "pod 不存在"
    fi
}

# ─── 主流程 ─────────────────────────────────────────────────────────
run_verifications() {
    verify_proc_sys || true
    verify_strict_resource_view
    verify_memory
    verify_systemd
    echo ""
    info "总计: ${PASS} 通过, ${FAIL} 失败"
    [ "$FAIL" -eq 0 ]
}

main() {
    echo ""
    echo "=========================================="
    echo "  Sysbox 系统容器测试"
    echo "  Image: ${IMAGE}"
    echo "=========================================="

    case "${1:-all}" in
        create)
            create_pod
            ;;
        exec) exec_pod ;;
        clean) clean_pod ;;
        verify)
            run_verifications
            ;;
        all)
            clean_pod 2>/dev/null || true
            trap 'clean_pod >/dev/null 2>&1 || true' EXIT
            create_pod || return 1
            install_tools || return 1
            status=0
            run_verifications || status=$?
            clean_pod
            trap - EXIT
            return "$status"
            ;;
        *)
            error "用法: $0 [create|verify|exec|clean|all]"
            return 2
            ;;
    esac
}

main "$@"
