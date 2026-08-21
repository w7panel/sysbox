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
TEST_DEPLOYMENT="$CKM_TEST_DEPLOYMENT" TEST_ROOTFS_PVC="$CKM_TEST_ROOTFS_PVC" \
ROOTFS_STORAGE_CLASS="$ROOTFS_STORAGE_CLASS" TEST_IMAGE="$TEST_IMAGE" \
  bash "$REPO_DIR/w7panel-doc/tests/nested-chart-smoke.sh"
log 'FUNCTIONAL PASS: workload, user namespace, HTTP, network and rootfs Pod-recreation persistence checks passed; Deployment/PVC retained'
log 'Run 08-check-isolation.sh only when auditing the unsupported proc/view isolation capabilities.'
