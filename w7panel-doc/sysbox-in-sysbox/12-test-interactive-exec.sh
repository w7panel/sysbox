#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
need_cmd script
need_cmd timeout

run_tty_probe() {
  local target="$1" marker output
  marker="SYSBOX_${target^^}_TTY_OK"
  log "testing interactive exec for $target"
  output="$(printf 'echo %s; test -t 0; test -t 1; tty; exit\n' "$marker" | \
    timeout 45 script -qefc "${FLOW_DIR}/06-enter-ckm-shell.sh $target" /dev/null)" ||
    die "$target interactive exec timed out or failed"
  printf '%s\n' "$output"
  printf '%s\n' "$output" | grep -q "$marker" ||
    die "$target interactive shell did not process terminal input"
  printf '%s\n' "$output" | grep -q '/dev/pts/' ||
    die "$target interactive shell did not receive a PTY"
}

run_tty_probe nginx
run_tty_probe docker
log 'PASS: nginx and Docker interactive kubectl exec sessions accepted input and exited cleanly'
