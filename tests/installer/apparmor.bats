#!/usr/bin/env bats

setup() {
  export host_etc="$BATS_TEST_TMPDIR/etc"
  export parser_calls="$BATS_TEST_TMPDIR/parser-calls"
  export apparmor_enabled=true parser_status=0
  mkdir -p "$host_etc/apparmor.d/local"
  touch "$host_etc/apparmor.d/fusermount3"
  sed -n '/^function config_sysbox_apparmor()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" > "$BATS_TEST_TMPDIR/function.sh"
  source "$BATS_TEST_TMPDIR/function.sh"
  nsenter() {
    [[ " $* " == *" --root "* ]] || return 99
    if [[ " $* " == *" apparmor_parser "* ]]; then
      echo reload >> "$parser_calls"
      return "$parser_status"
    fi
    "$apparmor_enabled"
  }
  export -f nsenter
}

@test "skip hosts without a fusermount3 profile" {
  rm "$host_etc/apparmor.d/fusermount3"
  config_sysbox_apparmor
  [ ! -e "$host_etc/apparmor.d/local/fusermount3" ]
  [ ! -e "$parser_calls" ]
}

@test "respect disabled AppArmor and disabled fusermount3 profiles" {
  apparmor_enabled=false
  config_sysbox_apparmor
  [ ! -e "$host_etc/apparmor.d/local/fusermount3" ]
  apparmor_enabled=true
  mkdir "$host_etc/apparmor.d/disable"
  ln -s ../fusermount3 "$host_etc/apparmor.d/disable/fusermount3"
  config_sysbox_apparmor
  [ ! -e "$parser_calls" ]
}

@test "preserve local policy and reconcile without duplicate rules" {
  printf '# administrator rule without trailing newline' > "$host_etc/apparmor.d/local/fusermount3"
  config_sysbox_apparmor
  cp "$host_etc/apparmor.d/local/fusermount3" "$BATS_TEST_TMPDIR/first"
  config_sysbox_apparmor
  cmp "$BATS_TEST_TMPDIR/first" "$host_etc/apparmor.d/local/fusermount3"
  grep -qxF '# administrator rule without trailing newline' "$host_etc/apparmor.d/local/fusermount3"
  grep -qxF 'mount fstype=fuse options=(rw,nosuid,nodev) sysboxfs -> /var/lib/sysboxfs/*/,' "$host_etc/apparmor.d/local/fusermount3"
  grep -qxF 'umount /var/lib/sysboxfs/*/,' "$host_etc/apparmor.d/local/fusermount3"
  [ "$(wc -l < "$parser_calls")" -eq 2 ]
}

@test "parser failure stops installation and retries reload" {
  parser_status=1
  run bash -e -c 'source "$1"; config_sysbox_apparmor; echo unexpected-success' _ "$BATS_TEST_TMPDIR/function.sh"
  [ "$status" -eq 1 ]
  [[ "$output" != *unexpected-success* ]]
  parser_status=0
  config_sysbox_apparmor
  [ "$(wc -l < "$parser_calls")" -eq 2 ]
}
