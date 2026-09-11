#!/usr/bin/env bash
set -euo pipefail

stage="${1:-}"
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
flow_dir="$repo_dir/w7panel-doc/sysbox-in-sysbox"

case "$stage" in
  preflight) script=00-check-prereqs.sh ;;
  ckm) script=01-create-ckm.sh ;;
  l0-smoke) script=02-test-l0-runtimeclasses.sh ;;
  l0-rootfs) script=03-test-l0-rootfs.sh ;;
  l1-install) script=04-install-ckm-chart.sh ;;
  l2) script=05-test-ckm-k3s.sh ;;
  cleanup) script=99-cleanup.sh ;;
  *)
    echo "usage: $0 {preflight|ckm|l0-smoke|l0-rootfs|l1-install|l2|cleanup}" >&2
    exit 2
    ;;
esac

exec "$flow_dir/$script"
