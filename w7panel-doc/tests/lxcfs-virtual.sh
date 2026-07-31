#!/usr/bin/env bash
#
# Enable or disable LXCFS /proc/loadavg virtualization.
#
# Usage:
#   ./w7panel-doc/tests/lxcfs-virtual.sh on
#   ./w7panel-doc/tests/lxcfs-virtual.sh off
#   ./w7panel-doc/tests/lxcfs-virtual.sh status

set -eu

LXCFS_MOUNT="${LXCFS_MOUNT:-/var/lib/lxcfs}"
OVERRIDE_DIR="/etc/systemd/system/lxcfs.service.d"
OVERRIDE_FILE="${OVERRIDE_DIR}/override.conf"

info() {
    printf '[INFO] %s\n' "$*"
}

die() {
    printf '[ERROR] %s\n' "$*" >&2
    exit 1
}

sudo_cmd() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    else
        sudo "$@"
    fi
}

enable_virtualization() {
    info "Enable LXCFS loadavg virtualization"
    sudo_cmd mkdir -p "${OVERRIDE_DIR}"

    tmp="$(mktemp)"
    cat > "${tmp}" <<EOF
[Service]
ExecStart=
ExecStart=/usr/bin/lxcfs -l ${LXCFS_MOUNT}
EOF
    sudo_cmd install -m 0644 "${tmp}" "${OVERRIDE_FILE}"
    rm -f "${tmp}"

    sudo_cmd systemctl daemon-reload
    sudo_cmd systemctl restart lxcfs
    status
}

disable_virtualization() {
    info "Disable LXCFS loadavg virtualization"
    if [ -f "${OVERRIDE_FILE}" ]; then
        sudo_cmd rm -f "${OVERRIDE_FILE}"
    fi

    sudo_cmd systemctl daemon-reload
    sudo_cmd systemctl restart lxcfs
    status
}

status() {
    sudo_cmd systemctl status lxcfs --no-pager || true
    printf '\n'
    sudo_cmd systemctl cat lxcfs
    printf '\n'

    host_load="$(cat /proc/loadavg 2>/dev/null || true)"
    lxcfs_load="$(cat "${LXCFS_MOUNT}/proc/loadavg" 2>/dev/null || true)"

    info "host_loadavg=${host_load:-<empty>}"
    info "lxcfs_loadavg=${lxcfs_load:-<empty>}"

    if sudo_cmd systemctl status lxcfs --no-pager | grep -q -- '/usr/bin/lxcfs -l '; then
        info "loadavg virtualization: on"
    else
        info "loadavg virtualization: off"
    fi
}

usage() {
    sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
}

case "${1:-status}" in
    on|enable)
        enable_virtualization
        ;;
    off|disable)
        disable_virtualization
        ;;
    status|check)
        status
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        usage
        die "unknown argument: $1"
        ;;
esac
