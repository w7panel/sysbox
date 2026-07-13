#!/usr/bin/env bash
# 从当前 K3s 节点卸载 Sysbox。执行前应先删除所有 sysbox-runc 工作负载并卸载 Helm release。

set -Eeuo pipefail

K3S_CONFIG_DIR="${K3S_CONFIG_DIR:-/var/lib/rancher/k3s/agent/etc/containerd}"
K3S_TEMPLATE="${K3S_CONFIG_DIR}/config-v3.toml.tmpl"
K3S_CONFIG="${K3S_CONFIG_DIR}/config.toml"
PURGE_SNAPSHOTTER_DATA="${PURGE_SNAPSHOTTER_DATA:-false}"
SNAPSHOTTER_DATA="/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox"

info() { printf '[INFO] %s\n' "$*"; }
pass() { printf '[PASS] %s\n' "$*"; }
die() { printf '[FAIL] %s\n' "$*" >&2; exit 1; }

strip_sysbox_toml() {
    local file="$1" tmp
    [[ -f "${file}" ]] || return 0

    tmp="$(mktemp "${file}.XXXXXX")"
    awk '
        /^\[/ {
            skip = ($0 ~ /runtimes\.sysbox-runc/ || $0 ~ /proxy_plugins.*sysbox/)
        }
        !skip { print }
    ' "${file}" >"${tmp}"
    chmod --reference="${file}" "${tmp}"
    chown --reference="${file}" "${tmp}"
    mv "${tmp}" "${file}"
}

detect_k3s_service() {
    if systemctl is-active --quiet k3s-agent; then
        printf 'k3s-agent\n'
    elif systemctl cat k3s.service >/dev/null 2>&1; then
        printf 'k3s\n'
    else
        die '未找到 k3s 或 k3s-agent systemd 服务'
    fi
}

remove_services() {
    local service unit_dir
    local services=(sysbox sysbox-fs sysbox-mgr sysbox-snapshotter)
    local helpers=(sysbox-installer-helper sysbox-removal-helper)
    local unit_dirs=(/etc/systemd/system /lib/systemd/system /usr/lib/systemd/system)

    info '停止并禁用 Sysbox 服务'
    for service in "${services[@]}"; do
        systemctl disable --now "${service}.service" >/dev/null 2>&1 || true
    done
    for service in "${helpers[@]}"; do
        systemctl disable --now "${service}.service" >/dev/null 2>&1 || true
    done

    for unit_dir in "${unit_dirs[@]}"; do
        for service in "${services[@]}" "${helpers[@]}"; do
            rm -f "${unit_dir}/${service}.service"
        done
    done
    systemctl daemon-reload
    systemctl reset-failed "${services[@]/%/.service}" >/dev/null 2>&1 || true
}

remove_files() {
    info '删除 Sysbox 二进制和宿主配置'
    rm -f \
        /usr/bin/sysbox /usr/bin/sysbox-runc /usr/bin/sysbox-fs \
        /usr/bin/sysbox-mgr /usr/bin/sysbox-snapshotter /usr/bin/sysbox-admission \
        /usr/local/bin/sysbox /usr/local/bin/sysbox-runc /usr/local/bin/sysbox-fs \
        /usr/local/bin/sysbox-mgr /usr/local/bin/sysbox-snapshotter /usr/local/bin/sysbox-admission \
        /usr/local/bin/sysbox-installer-helper.sh /usr/local/bin/sysbox-removal-helper.sh \
        /opt/bin/sysbox-runc /opt/bin/sysbox-fs /opt/bin/sysbox-mgr /opt/bin/sysbox-snapshotter \
        /opt/local/bin/sysbox-runc /opt/local/bin/sysbox-fs /opt/local/bin/sysbox-mgr \
        /opt/local/bin/sysbox-snapshotter \
        /etc/sysctl.d/99-sysbox-sysctl.conf /lib/sysctl.d/99-sysbox-sysctl.conf \
        /usr/lib/sysctl.d/99-sysbox-sysctl.conf \
        /etc/modules-load.d/50-sysbox-mod.conf /lib/modules-load.d/50-sysbox-mod.conf \
        /usr/lib/modules-load.d/50-sysbox-mod.conf \
        /run/sysbox-snapshotter.sock
    rm -rf /run/sysbox /run/sysbox-fs /run/sysbox-mgr /run/shiftfs-dkms
    rm -rf /var/lib/sysbox-deploy-k8s

    sed -i '/^sysbox:/d' /etc/subuid /etc/subgid 2>/dev/null || true
}

verify_removed() {
    local service="$1"

    systemctl is-active --quiet "${service}" || die "${service} 未恢复运行"
    if grep -Eq 'runtimes\.sysbox-runc|proxy_plugins.*sysbox' "${K3S_TEMPLATE}" "${K3S_CONFIG}" 2>/dev/null; then
        die 'K3s containerd 配置中仍存在 Sysbox 配置'
    fi
    [[ ! -e /usr/bin/sysbox-runc ]] || die '/usr/bin/sysbox-runc 仍然存在'
    [[ ! -e /usr/bin/sysbox-snapshotter ]] || die '/usr/bin/sysbox-snapshotter 仍然存在'
    pass "Sysbox 已卸载，${service} 正常运行"
}

main() {
    local k3s_service

    [[ "${EUID}" -eq 0 ]] || die '请以 root 身份运行'
    [[ "${PURGE_SNAPSHOTTER_DATA}" == true || "${PURGE_SNAPSHOTTER_DATA}" == false ]] || \
        die 'PURGE_SNAPSHOTTER_DATA 只能是 true 或 false'
    command -v systemctl >/dev/null || die '缺少 systemctl'

    k3s_service="$(detect_k3s_service)"
    remove_services
    remove_files

    info "清理 ${K3S_TEMPLATE} 中的 Sysbox runtime 和 proxy snapshotter 配置"
    strip_sysbox_toml "${K3S_TEMPLATE}"

    if [[ "${PURGE_SNAPSHOTTER_DATA}" == true ]]; then
        info "删除 snapshotter 数据 ${SNAPSHOTTER_DATA}"
        rm -rf "${SNAPSHOTTER_DATA}"
    fi

    info "重启 ${k3s_service} 以重新生成 containerd 配置"
    systemctl restart "${k3s_service}"
    verify_removed "${k3s_service}"
}

main "$@"
