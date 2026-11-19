#!/usr/bin/env bats

setup() {
  export NODE_NAME=test-node
  sed -n '/^function delete_sysbox_pods()/,/^}/p' \
    "$BATS_TEST_DIRNAME/../../sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh" > "$BATS_TEST_TMPDIR/function.sh"
  source "$BATS_TEST_TMPDIR/function.sh"
  kubectl() {
    if [[ "$1" == get ]]; then
      printf '%s\n' '{"items":[{"metadata":{"name":"old","namespace":"default"},"spec":{"runtimeClassName":"sysbox-runc","nodeName":"test-node"}},{"metadata":{"name":"other","namespace":"default"},"spec":{"runtimeClassName":"runc","nodeName":"test-node"}}]}'
    else
      printf '%s\n' "$*" >> "$BATS_TEST_TMPDIR/deletes"
    fi
  }
}

@test "runtime Pod deletion does not wait on controller finalizers" {
  delete_sysbox_pods
  [ "$(cat "$BATS_TEST_TMPDIR/deletes")" = 'delete pod/old -n default --grace-period=1 --wait=false' ]
}
