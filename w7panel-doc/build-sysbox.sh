#!/bin/bash
#
# build-sysbox.sh — 从源码构建 Sysbox 并配置 K3s/containerd
#
# 用法:
#   ./build-sysbox.sh           # 完整构建 + 安装
#   ./build-sysbox.sh --install # 安装已构建的二进制
#   ./build-sysbox.sh --config  # 仅配置 K3s containerd
#   ./build-sysbox.sh --debug-deploy # 构建 runc/snapshotter/admission，经 node debug Pod 部署并测试
#
# 环境变量:
#   GOPROXY   Go 模块代理 (默认: https://goproxy.cn,direct)
#   SYSBOX_DIR  源码目录 (默认: 当前 sysbox 仓库)
#   KUBECONFIG  目标集群 kubeconfig (--debug-deploy 必填)
#   TARGET_NODE 目标节点名 (--debug-deploy 必填)
#   DEBUG_IMAGE debug Pod 镜像（构建后推送，默认见下方配置）
#   TEST_NAMESPACE / TEST_DEPLOYMENT / TEST_CONTAINER 端到端测试目标
#   KEEP_DEBUG_POD=true 失败或结束后保留 debug Pod
#
# 仓库: https://github.com/w7panel/sysbox (w7panel 分支)
# 已知限制:
#   - Kernel 6.x 上系统容器 (需挂载 sysfs) 不可用
#   - 简单容器 (ubuntu:22.04 + sleep) 可正常运行
#   详见: https://github.com/nestybox/sysbox/issues/1006

set -euo pipefail

# ─── 配置 ───────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
SYSBOX_DIR="${SYSBOX_DIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
K3S_CONFIG_DIR="/var/lib/rancher/k3s/agent/etc/containerd"
KUBECONFIG="${KUBECONFIG:-}"
TARGET_NODE="${TARGET_NODE:-}"
DEBUG_NAMESPACE="${DEBUG_NAMESPACE:-default}"
DEBUG_IMAGE="${DEBUG_IMAGE:-docker.cnb.cool/i0358/zpk/sysbox-debug-deploy:rootfs-handoff-20260731-1}"
ADMISSION_NAMESPACE="${ADMISSION_NAMESPACE:-default}"
ADMISSION_DEPLOYMENT="${ADMISSION_DEPLOYMENT:-w7panel-sysbox-admission}"
KEEP_DEBUG_POD="${KEEP_DEBUG_POD:-false}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

# ─── 检查 root ──────────────────────────────────────────────────────
if [[ $EUID -ne 0 ]]; then
    error "请以 root 身份运行 (sudo)"
    exit 1
fi

# ─── 函数: 安装依赖 ─────────────────────────────────────────────────
install_deps() {
    info "安装编译依赖..."
    apt-get update -qq
    apt-get install -y -qq make gcc pkg-config libseccomp-dev \
        protobuf-compiler golang-go 2>&1 | tail -1

    info "配置 Go 代理..."
    go env -w GOPROXY="${GOPROXY}"

    if ! command -v protoc-gen-go &>/dev/null; then
        info "安装 protoc-gen-go (旧版, 支持 plugins=grpc)..."
        go install github.com/golang/protobuf/protoc-gen-go@v1.5.3
        ln -sf "$(go env GOPATH)/bin/protoc-gen-go" /usr/local/bin/protoc-gen-go
    fi

    info "编译依赖安装完成"
}

# ─── 函数: 安装运行依赖 (sysbox-mgr 需要) ──────────────────────────
install_runtime_deps() {
    local missing=0

    # rsync — sysbox-mgr 容器创建时 preflight 检测必需
    if ! command -v rsync &>/dev/null; then
        info "安装 rsync (sysbox-mgr 运行依赖)..."
        apt-get install -y -qq rsync 2>&1 | tail -1
    fi
    if command -v rsync &>/dev/null; then
        info "  rsync: $(rsync --version 2>&1 | head -1) ✅"
    else
        warn "  rsync: 未安装 ❌"
        missing=1
    fi

    # iptables — 容器网络隔离必需
    if ! command -v iptables &>/dev/null; then
        info "安装 iptables (容器网络依赖)..."
        apt-get install -y -qq iptables 2>&1 | tail -1
    fi
    if command -v iptables &>/dev/null; then
        info "  iptables: $(iptables --version 2>&1) ✅"
    else
        warn "  iptables: 未安装 ❌"
        missing=1
    fi

    # fuse — sysbox-fs FUSE 挂载必需
    if ! command -v fusermount &>/dev/null && ! command -v fusermount3 &>/dev/null; then
        info "安装 fuse (sysbox-fs FUSE 依赖)..."
        apt-get install -y -qq fuse fuse3 2>&1 | tail -1
    fi
    if command -v fusermount3 &>/dev/null; then
        info "  fuse: $(fusermount3 --version 2>&1) ✅"
    elif command -v fusermount &>/dev/null; then
        info "  fuse: $(fusermount --version 2>&1) ✅"
    else
        warn "  fuse: 未安装 ❌"
        missing=1
    fi

    if [[ $missing -eq 1 ]]; then
        warn "部分运行依赖缺失，Sysbox 可能无法正常工作"
    else
        info "运行依赖检查全部通过 ✅"
    fi
}

# ─── 函数: 克隆源码 ─────────────────────────────────────────────────
clone_source() {
    if [[ -d "${SYSBOX_DIR}/.git" ]]; then
        info "源码已存在, 跳过克隆"
        return
    fi

    info "克隆 sysbox 源码 (w7panel 分支, 通过 gh-proxy 代理)..."

    # 先克隆主仓库（不含子模块）
    git clone -b w7panel \
        "https://gh-proxy.org/https://github.com/w7panel/sysbox.git" \
        "${SYSBOX_DIR}"

    # 修改子模块 URL，全部走 gh-proxy 代理
    pushd "${SYSBOX_DIR}" >/dev/null
    sed -i 's|https://github.com/|https://gh-proxy.org/https://github.com/|g' .gitmodules

    # 先初始化所有第一级子模块
    git submodule update --init --depth 1

    # sysbox-fs 的 bazil 子模块使用相对路径 ../fuse.git，
    # 从 w7panel/sysbox-fs 解析为 w7panel/fuse，但实际应为 nestybox/fuse
    if [[ -f sysbox-fs/.gitmodules ]]; then
        info "修正 sysbox-fs/bazil 子模块 URL (nestybox/fuse)..."
        git -C sysbox-fs config submodule.bazil.url \
            "https://gh-proxy.org/https://github.com/nestybox/fuse.git"
    fi

    # 递归克隆嵌套子模块
    GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=echo \
        git submodule update --init --recursive --depth 1

    # 恢复原始子模块 URL
    sed -i 's|https://gh-proxy.org/https://github.com/|https://github.com/|g' .gitmodules
    popd >/dev/null

    info "克隆完成"
}

# ─── 函数: 编译 ─────────────────────────────────────────────────────
build() {
    info "开始编译 sysbox 组件..."
    export GOPROXY="${GOPROXY}"

    pushd "${SYSBOX_DIR}" >/dev/null

    info "1/4: 编译 sysbox-ipc..."
    make sysbox-ipc

    info "2/4: 编译 sysbox-runc..."
    make -C sysbox-runc

    info "3/4: 编译 sysbox-fs..."
    make -C sysbox-fs

    info "4/4: 编译 sysbox-mgr..."
    make -C sysbox-mgr

    popd >/dev/null

    info "编译完成!"
    ls -lh "${SYSBOX_DIR}/sysbox-runc/build/amd64/sysbox-runc"
    ls -lh "${SYSBOX_DIR}/sysbox-fs/build/sysbox-fs"
    ls -lh "${SYSBOX_DIR}/sysbox-mgr/build/sysbox-mgr"
}

# ─── 函数: 构建快速部署组件 ─────────────────────────────────────────
build_debug_components() {
    info "构建快速部署组件 (sysbox-runc / snapshotter / admission)..."
    export GOPROXY="${GOPROXY}"

    pushd "${SYSBOX_DIR}" >/dev/null
    make -C sysbox-ipc
    make -C sysbox-runc
    make -C sysbox-snapshotter
    make -C sysbox-admission
    popd >/dev/null

    local files=(
        "${SYSBOX_DIR}/sysbox-runc/build/amd64/sysbox-runc"
        "${SYSBOX_DIR}/sysbox-snapshotter/build/amd64/sysbox-snapshotter"
        "${SYSBOX_DIR}/sysbox-admission/build/amd64/sysbox-admission"
    )
    local file
    for file in "${files[@]}"; do
        [[ -x "${file}" ]] || { error "构建产物不存在: ${file}"; return 1; }
        sha256sum "${file}"
    done
}

build_and_push_debug_image() {
    [[ "${DEBUG_IMAGE}" == docker.cnb.cool/i0358/zpk/*:* ]] || {
        error "DEBUG_IMAGE 必须使用 docker.cnb.cool/i0358/zpk/<name>:<tag>: ${DEBUG_IMAGE}"
        return 1
    }
    command -v docker >/dev/null 2>&1 || { error "本机未安装 docker"; return 1; }

    info "构建小型 debug 部署镜像: ${DEBUG_IMAGE}"
    docker build \
        -f "${SCRIPT_DIR}/Dockerfile.sysbox-debug-deploy" \
        -t "${DEBUG_IMAGE}" \
        "${SYSBOX_DIR}"
    info "推送 debug 部署镜像..."
    docker push "${DEBUG_IMAGE}"
    docker image inspect "${DEBUG_IMAGE}" --format '  image={{.Id}} size={{.Size}}'
}

# ─── 函数: 安装二进制 ──────────────────────────────────────────────
install_binaries() {
    local bin_dir="${SYSBOX_DIR}"

    info "停止 Sysbox 服务..."
    systemctl stop sysbox sysbox-fs sysbox-mgr 2>/dev/null || true

    info "安装二进制到 /usr/bin/..."

    if [[ -f "${bin_dir}/sysbox-runc/build/amd64/sysbox-runc" ]]; then
        cp "${bin_dir}/sysbox-runc/build/amd64/sysbox-runc" /usr/bin/sysbox-runc
    elif [[ -f "${bin_dir}/sysbox-runc/sysbox-runc" ]]; then
        cp "${bin_dir}/sysbox-runc/sysbox-runc" /usr/bin/sysbox-runc
    else
        error "sysbox-runc 二进制未找到!"
        exit 1
    fi

    cp "${bin_dir}/sysbox-fs/build/sysbox-fs" /usr/bin/sysbox-fs
    cp "${bin_dir}/sysbox-mgr/build/sysbox-mgr" /usr/bin/sysbox-mgr

    # 安装 systemd 服务文件（如果未安装）
    local sysbox_svc_dir="${SYSBOX_DIR}/sysbox-pkgr/systemd"
    if [[ ! -f /usr/lib/systemd/system/sysbox-fs.service ]]; then
        info "安装 systemd 服务文件..."
        cp "${sysbox_svc_dir}/sysbox.service" /usr/lib/systemd/system/
        cp "${sysbox_svc_dir}/sysbox-fs.service" /usr/lib/systemd/system/
        cp "${sysbox_svc_dir}/sysbox-mgr.service" /usr/lib/systemd/system/
        systemctl daemon-reload
    fi

    # 安装 sysbox 辅助脚本
    if [[ ! -f /usr/bin/sysbox ]]; then
        info "安装 sysbox 辅助脚本..."
        cp "${SYSBOX_DIR}/scr/sysbox" /usr/bin/sysbox
        chmod +x /usr/bin/sysbox
    fi

    info "启动 Sysbox 服务..."
    systemctl start sysbox-fs sysbox-mgr

    # 验证
    sleep 2
    if systemctl is-active sysbox-fs sysbox-mgr >/dev/null 2>&1; then
        info "Sysbox 服务运行中 ✅"
    else
        error "Sysbox 服务启动失败!"
        systemctl status sysbox-fs sysbox-mgr --no-pager
        exit 1
    fi

    # 打印版本（主分支编译的 sysbox-runc 不支持 --version 标志）
    echo ""
    strings /usr/bin/sysbox-runc | grep -E "^[0-9]+\.[0-9]+\.[0-9]" | head -1
    ls -lh /usr/bin/sysbox-runc /usr/bin/sysbox-fs /usr/bin/sysbox-mgr \
        | awk '{printf "  %s %s (%s)\n", $NF, $5, $6 " " $7}'
}

# ─── 函数: 配置 K3s containerd ─────────────────────────────────────
config_k3s() {
    if ! command -v k3s &>/dev/null; then
        warn "K3s 未安装, 跳过 containerd 配置"
        return
    fi

    info "配置 K3s containerd (config-v3.toml.tmpl)..."

    mkdir -p "${K3S_CONFIG_DIR}"

    cat > "${K3S_CONFIG_DIR}/config-v3.toml.tmpl" << 'CONFIG'
version = 3
imports = ["/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/*.toml"]
root = "/var/lib/rancher/k3s/agent/containerd"
state = "/run/k3s/containerd"

[grpc]
  address = "/run/k3s/containerd/containerd.sock"

[plugins.'io.containerd.internal.v1.opt']
  path = "/var/lib/rancher/k3s/agent/containerd"

[plugins.'io.containerd.grpc.v1.cri']
  stream_server_address = "127.0.0.1"
  stream_server_port = "10010"

[plugins.'io.containerd.cri.v1.runtime']
  enable_selinux = false
  enable_unprivileged_ports = true
  enable_unprivileged_icmp = true
  device_ownership_from_security_context = false

[plugins.'io.containerd.cri.v1.images']
  snapshotter = "overlayfs"
  disable_snapshot_annotations = false
  use_local_image_pull = true

[plugins.'io.containerd.cri.v1.images'.pinned_images]
  sandbox = "rancher/mirrored-pause:3.6"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
  SystemdCgroup = true

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc.options]
  SystemdCgroup = true
  BinaryName = "/usr/bin/sysbox-runc"

[plugins.'io.containerd.cri.v1.images'.registry]
  config_path = "/var/lib/rancher/k3s/agent/etc/containerd/certs.d"
CONFIG

    info "重启 K3s..."
    systemctl restart k3s

    # 验证配置
    sleep 5
    if grep -q "sysbox-runc" "${K3S_CONFIG_DIR}/config.toml" 2>/dev/null; then
        info "containerd 配置生效 ✅"
    else
        warn "containerd 配置未生成, 检查 K3s 日志"
    fi

    # 创建 RuntimeClass
    info "创建 RuntimeClass: sysbox-runc..."
    cat <<EOF | kubectl apply -f - 2>/dev/null || k3s kubectl apply -f -
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: sysbox-runc
handler: sysbox-runc
EOF

    info "K3s 配置完成 ✅"
}

# ─── 函数: 通过 node debug Pod 快速部署 ────────────────────────────
DEBUG_POD=""
KUBECTL=()

cleanup_debug_pod() {
    if [[ -n "${DEBUG_POD}" && "${KEEP_DEBUG_POD}" != "true" ]]; then
        "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" delete pod "${DEBUG_POD}" \
            --ignore-not-found --wait=false >/dev/null 2>&1 || true
    elif [[ -n "${DEBUG_POD}" ]]; then
        warn "保留 debug Pod: ${DEBUG_NAMESPACE}/${DEBUG_POD}"
    fi
}

wait_target_api() {
    local attempt
    for attempt in $(seq 1 60); do
        if "${KUBECTL[@]}" get node "${TARGET_NODE}" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    error "等待目标集群 API 恢复超时"
    return 1
}

create_node_debug_pod() {
    local output prefix
    prefix="node-debugger-${TARGET_NODE//./-}-"
    info "创建 node debug Pod: node/${TARGET_NODE} (${DEBUG_IMAGE})..."
    output="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" debug "node/${TARGET_NODE}" \
        --image="${DEBUG_IMAGE}" --image-pull-policy=Always \
        --profile=sysadmin --attach=false -- sleep infinity 2>&1)"
    echo "${output}"
    DEBUG_POD="$(grep -Eo 'node-debugger-[a-z0-9-]+' <<<"${output}" | tail -1 || true)"
    if [[ -z "${DEBUG_POD}" ]]; then
        DEBUG_POD="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" get pods \
            --field-selector="spec.nodeName=${TARGET_NODE}" \
            --sort-by=.metadata.creationTimestamp -o name \
            | sed 's@^pod/@@' | grep "^${prefix}" | tail -1 || true)"
    fi
    [[ -n "${DEBUG_POD}" ]] || { error "无法取得 node debug Pod 名称"; return 1; }
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" wait --for=condition=Ready \
        "pod/${DEBUG_POD}" --timeout=120s
}

install_binaries_from_debug_image() {
    info "从 debug 镜像原子安装三个宿主二进制..."
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- sh -c '
        set -eu
        for name in sysbox-runc sysbox-snapshotter sysbox-admission; do
            source="/sysbox-bin/${name}"
            temporary="/host/usr/bin/.${name}.new"
            test -x "${source}"
            cp "${source}" "${temporary}"
            chmod 0755 "${temporary}"
            mv -f "${temporary}" "/host/usr/bin/${name}"
        done
    '
}

ensure_remote_containerd_annotations() {
    local config_dir="/host/var/lib/rancher/k3s/agent/etc/containerd"
    local live
    live="$("${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
        sh -c "grep -h 'pod_annotations' '${config_dir}/config.toml' 2>/dev/null || true")"
    if grep -q 'sysbox/persistent-special-mounts' <<<"${live}"; then
        info "containerd 已转发 persistent-special-mounts，跳过 K3s 重启"
        return 0
    fi

    info "补充 containerd Pod annotation 并重启目标 K3s..."
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- sh -c \
        "for f in '${config_dir}/config-v3.toml.tmpl' '${config_dir}/config.toml'; do
            [ -f \"\${f}\" ] || continue
            sed -i 's@pod_annotations = \[\"sysbox/rootfs-rw-layer\"\]@pod_annotations = [\"sysbox/rootfs-rw-layer\", \"sysbox/persistent-special-mounts\"]@' \"\${f}\"
        done"
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
        chroot /host systemctl restart k3s >/dev/null 2>&1 || true
    wait_target_api
}

verify_remote_debug_deploy() {
    info "校验目标宿主二进制与服务..."
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- sh -c \
        'sha256sum /host/usr/bin/sysbox-runc /host/usr/bin/sysbox-snapshotter /host/usr/bin/sysbox-admission'
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
        chroot /host systemctl is-active sysbox-fs sysbox-mgr sysbox-snapshotter

    if [[ -n "${TEST_DEPLOYMENT:-}" ]]; then
        info "运行特殊目录端到端测试: ${TEST_NAMESPACE:-default}/${TEST_DEPLOYMENT}"
        KUBECONFIG="${KUBECONFIG}" \
        NAMESPACE="${TEST_NAMESPACE:-default}" \
        DEPLOYMENT="${TEST_DEPLOYMENT}" \
        CONTAINER="${TEST_CONTAINER:-${TEST_DEPLOYMENT}}" \
            bash "${SCRIPT_DIR}/persistent-special-mount-test.sh"
    else
        warn "未设置 TEST_DEPLOYMENT，仅完成宿主部署校验；设置后会自动运行特殊目录测试"
    fi
}

debug_deploy() {
    [[ -n "${KUBECONFIG}" ]] || { error "--debug-deploy 需要设置 KUBECONFIG"; return 1; }
    [[ -n "${TARGET_NODE}" ]] || { error "--debug-deploy 需要设置 TARGET_NODE"; return 1; }
    [[ -f "${KUBECONFIG}" ]] || { error "kubeconfig 不存在: ${KUBECONFIG}"; return 1; }

    KUBECTL=(kubectl --kubeconfig "${KUBECONFIG}")
    create_node_debug_pod
    trap cleanup_debug_pod EXIT

    install_binaries_from_debug_image

    info "重启 snapshotter 与 admission..."
    "${KUBECTL[@]}" -n "${DEBUG_NAMESPACE}" exec "${DEBUG_POD}" -- \
        chroot /host systemctl restart sysbox-snapshotter
    ensure_remote_containerd_annotations
    "${KUBECTL[@]}" -n "${ADMISSION_NAMESPACE}" rollout restart \
        "deployment/${ADMISSION_DEPLOYMENT}"
    "${KUBECTL[@]}" -n "${ADMISSION_NAMESPACE}" rollout status \
        "deployment/${ADMISSION_DEPLOYMENT}" --timeout=180s

    verify_remote_debug_deploy
    cleanup_debug_pod
    trap - EXIT
    info "node debug Pod 快速部署与测试完成 ✅"
}

# ─── 函数: 验证 ─────────────────────────────────────────────────────
verify() {
    echo ""
    info "===== 验证 Sysbox ====="

    echo ""
    info "1. Sysbox 服务状态:"
    systemctl is-active sysbox-fs sysbox-mgr

    echo ""
    info "2. Sysbox 版本:"
    strings /usr/bin/sysbox-runc | grep -E "^[0-9]+\.[0-9]+\.[0-9]" | head -1

    echo ""
    info "3. containerd 运行时注册:"
    if command -v k3s &>/dev/null; then
        k3s crictl info 2>/dev/null | grep -A10 "sysbox-runc" | head -10 || echo "    (K3s 未运行)"
    fi

    echo ""
    info "4. RuntimeClass:"
    (kubectl get runtimeclass sysbox-runc 2>/dev/null || \
     k3s kubectl get runtimeclass sysbox-runc 2>/dev/null) || \
        echo "    (RuntimeClass 未创建)"

    echo ""
    warn "===== 已知限制 ====="
    warn "Kernel $(uname -r) 上系统容器(需挂载 sysfs)不可用"
    warn "详情: https://github.com/nestybox/sysbox/issues/1006"
    echo ""
}

# ─── 主流程 ─────────────────────────────────────────────────────────
main() {
    echo ""
    echo "=========================================="
    echo "  Sysbox 构建 & 安装脚本"
    echo "  Kernel: $(uname -r)"
    echo "=========================================="
    echo ""

    case "${1:-}" in
        --install)
            install_runtime_deps
            install_binaries
            verify
            ;;
        --config)
            config_k3s
            verify
            ;;
        --verify)
            verify
            ;;
        --debug-deploy)
            build_debug_components
            build_and_push_debug_image
            debug_deploy
            ;;
        *)
            install_deps
            clone_source
            build
            install_runtime_deps
            install_binaries
            config_k3s
            verify
            info "全部完成! 🎉"
            ;;
    esac
}

main "$@"
