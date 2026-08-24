#!/usr/bin/env bash
set -euo pipefail

# Backward-compatible nginx entry point. The generic helper also supports the
# retained systemd/Docker test Deployment.
exec "$(dirname "$0")/06-enter-ckm-shell.sh" nginx
