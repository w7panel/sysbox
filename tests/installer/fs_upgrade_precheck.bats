#!/usr/bin/env bats

setup() {
  export host_run="$BATS_TEST_TMPDIR/run"
  export fake_proc="$BATS_TEST_TMPDIR/proc"
  export fs_active=true fs_args='sysbox-fs --session-recovery '
  export fs_store=65536 fs_preserve=yes fs_stored_fds=1
  mkdir -p "$host_run/sysbox" "$fake_proc/1" "$fake_proc/20"
  : > "$fake_proc/1/mountinfo"
  : > "$fake_proc/20/mountinfo"
  sed -n '/^function precheck_fs_upgrade()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" > "$BATS_TEST_TMPDIR/function.sh"
  source "$BATS_TEST_TMPDIR/function.sh"
  die() { echo "$*" >&2; exit 1; }
  systemctl() {
    if [[ "$1" == is-active ]]; then
      "$fs_active"
      return
    fi
    case "$4" in
      MainPID) echo 123 ;;
      FileDescriptorStoreMax) echo "$fs_store" ;;
      FileDescriptorStorePreserve) echo "$fs_preserve" ;;
      NFileDescriptorStore) echo "$fs_stored_fds" ;;
    esac
  }
  nsenter() {
    shift 8
    if [[ "$1" == sh ]]; then
      local script="${3//\/proc\//${fake_proc}\/}"
      sh -c "$script"
    else
      printf '%s' "$fs_args" | tr ' ' '\0'
    fi
  }
}

@test "new installation and empty legacy host can proceed" {
  fs_active=false
  precheck_fs_upgrade
  fs_active=true fs_args='sysbox-fs '
  precheck_fs_upgrade
}

@test "reject inactive service with a checkpoint" {
  fs_active=false
  touch "$host_run/sysbox/fs-restart.json"
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
  [[ "$output" == *"inactive sysbox-fs"* ]]
}

@test "find nested sysboxfs mount even when host init has none" {
  echo '9 1 0:20 / /unrelated rw - fuse other rw' > "$fake_proc/1/mountinfo"
  echo '10 1 0:21 /proc/meminfo /proc/meminfo ro - fuse sysboxfs rw' > "$fake_proc/20/mountinfo"
  fs_active=false
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
  [[ "$output" == *"inactive sysbox-fs"* ]]
  fs_active=true fs_args='sysbox-fs '
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
  [[ "$output" == *"legacy sysbox-fs"* ]]
}

@test "recovery service must retain a live FD store" {
  echo '10 1 0:21 /proc/meminfo /proc/meminfo ro - fuse sysboxfs rw' > "$fake_proc/20/mountinfo"
  precheck_fs_upgrade
  fs_preserve=no
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
  fs_preserve=yes fs_stored_fds=0
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
  fs_stored_fds=1 fs_store=0
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
  fs_store=65536 fs_args='sysbox-fs --session-recovery-disabled '
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
}

@test "refuse upgrade when a live mountinfo cannot be read" {
  mkdir "$BATS_TEST_TMPDIR/bin"
  printf '#!/bin/sh\nexit 2\n' > "$BATS_TEST_TMPDIR/bin/grep"
  chmod +x "$BATS_TEST_TMPDIR/bin/grep"
  export PATH="$BATS_TEST_TMPDIR/bin:$PATH"
  run precheck_fs_upgrade
  [ "$status" -eq 1 ]
  [[ "$output" == *"cannot inspect host mounts"* ]]
}
