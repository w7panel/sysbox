#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
[ -x "$REPO_DIR/w7panel-doc/tests/nested-l3-smoke.sh" ] || die 'nested-l3-smoke.sh is missing'
log "running L3 smoke with ${TEST_IMAGE}"
KUBECONFIG_218="$KUBECONFIG_218" NAMESPACE="$OUTER_NAMESPACE" \
L1_POD="$L1_POD" L1_CONTAINER="$L1_CONTAINER" L2_POD="$L2_POD" \
TEST_POD="$L3_POD" TEST_IMAGE="$TEST_IMAGE" \
  bash "$REPO_DIR/w7panel-doc/tests/nested-l3-smoke.sh"
log 'PASS: L3 user namespace, uid/gid mapping, HTTP and CNI cleanup passed'
