#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
log "testing a Sysbox workload directly in CKM K3s with ${TEST_IMAGE}"
KUBECONFIG_218="$KUBECONFIG_218" NAMESPACE="$OUTER_NAMESPACE" \
L1_POD="$L1_POD" L1_CONTAINER="$L1_CONTAINER" \
IMAGE_TAG="$SYSBOX_IMAGE_TAG" \
TEST_NAMESPACE=default \
INNER_NAMESPACE="$CHART_NAMESPACE" \
TEST_DEPLOYMENT="${CKM_TEST_DEPLOYMENT:-ckm-k3s-nginx}" TEST_IMAGE="$TEST_IMAGE" \
  bash "$REPO_DIR/w7panel-doc/tests/nested-chart-smoke.sh"
log 'PASS: CKM K3s chart workload, user namespace, HTTP and CNI cleanup passed'
