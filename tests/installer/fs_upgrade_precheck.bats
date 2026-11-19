#!/usr/bin/env bats

setup() {
  export host_run="$BATS_TEST_TMPDIR/run"
  export host_var_lib_sysbox_deploy_k8s="$BATS_TEST_TMPDIR/state"
  export NODE_NAME=server1
  export fake_proc="$BATS_TEST_TMPDIR/proc"
  export fs_active=true fs_args='sysbox-fs --session-recovery '
  export fs_store=65536 fs_preserve=yes fs_stored_fds=1
  mkdir -p "$host_run/sysbox" "$fake_proc/1" "$fake_proc/20"
  : > "$fake_proc/1/mountinfo"
  : > "$fake_proc/20/mountinfo"
  sed -n '/^function precheck_fs_upgrade()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" > "$BATS_TEST_TMPDIR/function.sh"
  sed -n '/^function restore_legacy_fs_node()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" >> "$BATS_TEST_TMPDIR/function.sh"
  sed -n '/^function verify_legacy_fs_drained()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" >> "$BATS_TEST_TMPDIR/function.sh"
  sed -n '/^function migrate_legacy_fs_pods()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" >> "$BATS_TEST_TMPDIR/function.sh"
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
  kubectl() {
    case "$1 $2" in
      'get pod')
        if [[ "$3" == -A ]]; then
          if [[ -e "$BATS_TEST_TMPDIR/deleted" && "${respawn:-false}" == false ]]; then
            echo '{"items":[]}'
          else
            cat "$BATS_TEST_TMPDIR/pods.json"
          fi
        elif [[ -e "$BATS_TEST_TMPDIR/deleted" ]]; then
          :
        else
          echo old-uid
        fi
        ;;
      'get node') echo false ;;
      'get ReplicaSet') echo "${controller_uid:-owner-uid}" ;;
      'cordon server1'|'uncordon server1') echo "$1" >> "$BATS_TEST_TMPDIR/node-actions" ;;
      'delete pod')
        [[ "${delete_fails:-false}" == false ]] || return 1
        touch "$BATS_TEST_TMPDIR/deleted"
        ;;
      *) return 1 ;;
    esac
  }
  cat > "$BATS_TEST_TMPDIR/pods.json" <<'EOF'
{"items":[{"metadata":{"namespace":"default","name":"ckm","uid":"old-uid","ownerReferences":[{"kind":"ReplicaSet","name":"ckm-rs","uid":"owner-uid","controller":true}]},"spec":{"nodeName":"server1","runtimeClassName":"sysbox-runc"}}]}
EOF
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
  precheck_fs_upgrade
  [ "$legacy_fs_migration_required" == true ]
}

@test "legacy migration removes controlled Pods and preserves stale checkpoint" {
  fs_args='sysbox-fs '
  echo '10 1 0:21 /proc/meminfo /proc/meminfo ro - fuse sysboxfs rw' > "$fake_proc/20/mountinfo"
  touch "$host_run/sysbox/fs-restart.json"
  # A deleted Pod releases the mount in this fixture.
  nsenter() {
    if [[ -e "$BATS_TEST_TMPDIR/deleted" ]]; then
      : > "$fake_proc/20/mountinfo"
    fi
    shift 8
    if [[ "$1" == sh ]]; then
      local script="${3//\/proc\//${fake_proc}\/}"
      sh -c "$script"
    else
      printf '%s' "$fs_args" | tr ' ' '\0'
    fi
  }
  precheck_fs_upgrade
  migrate_legacy_fs_pods
  [ "$legacy_fs_migration_required" == false ]
  [ ! -e "$host_run/sysbox/fs-restart.json" ]
  [ -f "$(find "$host_var_lib_sysbox_deploy_k8s" -name fs-restart.json)" ]
  restore_legacy_fs_node
  [ "$(cat "$BATS_TEST_TMPDIR/node-actions")" = $'cordon\nuncordon' ]
  legacy_fs_cordoned=false
}

@test "unmanaged Pod refuses migration without deleting it" {
  fs_args='sysbox-fs '
  echo '10 1 0:21 /proc/meminfo /proc/meminfo ro - fuse sysboxfs rw' > "$fake_proc/20/mountinfo"
  sed -i 's/"kind":"ReplicaSet"/"kind":"Pod"/' "$BATS_TEST_TMPDIR/pods.json"
  run migrate_legacy_fs_pods
  [ "$status" -eq 1 ]
  [[ "$output" == *"migrate it manually"* ]]
  [ ! -e "$BATS_TEST_TMPDIR/deleted" ]
  [ ! -e "$BATS_TEST_TMPDIR/node-actions" ]
}

@test "DaemonSet Pod refuses migration because it can bypass cordon" {
  sed -i 's/"kind":"ReplicaSet"/"kind":"DaemonSet"/' "$BATS_TEST_TMPDIR/pods.json"
  run migrate_legacy_fs_pods
  [ "$status" -eq 1 ]
  [[ "$output" == *"migrate it manually"* ]]
  [ ! -e "$BATS_TEST_TMPDIR/deleted" ]
  [ ! -e "$BATS_TEST_TMPDIR/node-actions" ]
}

@test "replaced controller refuses migration before cordoning" {
  controller_uid=other-uid
  run migrate_legacy_fs_pods
  [ "$status" -eq 1 ]
  [[ "$output" == *"missing or replaced"* ]]
  [ ! -e "$BATS_TEST_TMPDIR/deleted" ]
  [ ! -e "$BATS_TEST_TMPDIR/node-actions" ]
}

@test "legacy service with stale checkpoint but no mounts still migrates" {
  fs_args='sysbox-fs '
  touch "$host_run/sysbox/fs-restart.json"
  precheck_fs_upgrade
  [ "$legacy_fs_migration_required" == true ]
  migrate_legacy_fs_pods
  [ ! -e "$host_run/sysbox/fs-restart.json" ]
  [ -f "$(find "$host_var_lib_sysbox_deploy_k8s" -name fs-restart.json)" ]
  legacy_fs_cordoned=false
}

@test "delete failure preserves old FS and restores scheduling" {
  fs_args='sysbox-fs '
  echo '10 1 0:21 /proc/meminfo /proc/meminfo ro - fuse sysboxfs rw' > "$fake_proc/20/mountinfo"
  delete_fails=true
  run migrate_legacy_fs_pods
  [ "$status" -eq 1 ]
  [[ "$output" == *"cannot delete legacy sysbox Pod"* ]]
  [ "$(cat "$BATS_TEST_TMPDIR/node-actions")" = $'cordon\nuncordon' ]
}

@test "residual nested mount prevents replacement" {
  fs_args='sysbox-fs '
  echo '10 1 0:21 /proc/meminfo /proc/meminfo ro - fuse sysboxfs rw' > "$fake_proc/20/mountinfo"
  run migrate_legacy_fs_pods
  [ "$status" -eq 1 ]
  [[ "$output" == *"FUSE mounts remain"* ]]
  [ "$(cat "$BATS_TEST_TMPDIR/node-actions")" = $'cordon\nuncordon' ]
}

@test "last check rejects a Pod that appears before service stop" {
  touch "$BATS_TEST_TMPDIR/deleted"
  respawn=true
  run verify_legacy_fs_drained
  [ "$status" -eq 1 ]
  [[ "$output" == *"sysbox Pods remain"* ]]
}

@test "recovery service must retain a live FD store" {
  echo '10 1 0:21 /proc/meminfo /proc/meminfo ro - fuse sysboxfs rw' > "$fake_proc/20/mountinfo"
  precheck_fs_upgrade
  fs_preserve=no
  precheck_fs_upgrade
  [ "$legacy_fs_migration_required" == true ]
  fs_preserve=yes fs_stored_fds=0
  precheck_fs_upgrade
  [ "$legacy_fs_migration_required" == true ]
  fs_stored_fds=1 fs_store=0
  precheck_fs_upgrade
  [ "$legacy_fs_migration_required" == true ]
  fs_store=65536 fs_args='sysbox-fs --session-recovery-disabled '
  precheck_fs_upgrade
  [ "$legacy_fs_migration_required" == true ]
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
