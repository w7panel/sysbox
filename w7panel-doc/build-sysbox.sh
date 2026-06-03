#!/bin/bash
#
# build-sysbox.sh — 从源码构建 Sysbox 并配置 K3s/containerd
#
# 用法:
#   ./build-sysbox.sh           # 完整构建 + 安装
#   ./build-sysbox.sh --install # 安装已构建的二进制
#   ./build-sysbox.sh --config  # 仅配置 K3s containerd
#
# 环境变量:
#   GOPROXY   Go 模块代理 (默认: https://goproxy.cn,direct)
#   SYSBOX_DIR  源码目录 (默认: /tmp/sysbox)
#
# 已知限制:
#   - Kernel 6.x 上系统容器 (需挂载 sysfs) 不可用
#   - 简单容器 (ubuntu:22.04 + sleep) 可正常运行
#   详见: https://github.com/nestybox/sysbox/issues/1006

set -euo pipefail

# ─── 配置 ───────────────────────────────────────────────────────────
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
SYSBOX_DIR="${SYSBOX_DIR:-/tmp/sysbox}"
K3S_CONFIG_DIR="/var/lib/rancher/k3s/agent/etc/containerd"

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

    info "依赖安装完成"
}

# ─── 函数: 克隆源码 ─────────────────────────────────────────────────
clone_source() {
    if [[ -d "${SYSBOX_DIR}/.git" ]]; then
        info "源码已存在, 跳过克隆"
        return
    fi

    info "克隆 sysbox 源码 (通过 gh-proxy 代理)..."
    git clone --recursive \
        "https://gh-proxy.org/https://github.com/nestybox/sysbox.git" \
        "${SYSBOX_DIR}"
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

    # 打印版本
    echo ""
    /usr/bin/sysbox-runc --version 2>&1 | head -4
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
  disable_snapshot_annotations = true
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
  SystemdCgroup = false
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

# ─── 函数: 验证 ─────────────────────────────────────────────────────
verify() {
    echo ""
    info "===== 验证 Sysbox ====="

    echo ""
    info "1. Sysbox 服务状态:"
    systemctl is-active sysbox-fs sysbox-mgr

    echo ""
    info "2. Sysbox-runc 版本:"
    /usr/bin/sysbox-runc --version 2>&1 | head -3

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
        *)
            install_deps
            clone_source
            build
            install_binaries
            config_k3s
            verify
            info "全部完成! 🎉"
            ;;
    esac
}

main "$@"
