#!/usr/bin/env bash
# Build the smallest image needed for a nested-runtime regression, then run
# the numbered functional checks. It never changes the CKM controller image or
# recreates a CKM Server Pod: apply the printed bootstrap image deliberately.
set -euo pipefail

FLOW_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${FLOW_DIR}/../.." && pwd)"
BUILD_PROFILE="${BUILD_PROFILE:-test}"
RUN_FUNCTIONAL_TESTS="${RUN_FUNCTIONAL_TESTS:-true}"

log() { printf '[sysbox-in-sysbox] %s\n' "$*"; }
die() { printf '[sysbox-in-sysbox] ERROR: %s\n' "$*" >&2; exit 1; }

case "${BUILD_PROFILE}" in test|release) ;; *) die 'BUILD_PROFILE must be test or release' ;; esac

if [ "${BUILD_PROFILE}" = test ]; then
  : "${TEST_COMPONENTS:?set TEST_COMPONENTS for BUILD_PROFILE=test}"
  case "${TEST_TARGETS:-bootstrap}" in deploy|bootstrap|deploy,bootstrap|bootstrap,deploy) ;; *) die 'invalid TEST_TARGETS' ;; esac
fi

log "building with BUILD_PROFILE=${BUILD_PROFILE}"
(cd "${REPO_DIR}" && bash ./w7panel-doc/release.sh)

if [ "${BUILD_PROFILE}" = test ]; then
  # shellcheck disable=SC1091
  source "${REPO_DIR}/dist/test-images.env"
  log "fast image artifacts: ${REPO_DIR}/dist/test-images.env"
  [ -z "${CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE:-}" ] || cat <<EOF
To make the new bootstrap image effective, update the CKM controller explicitly:
  CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE=${CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE}
Then recreate the selected CKM Server Pod through its normal rollout.
EOF
else
  metadata="${REPO_DIR}/dist/image-metadata.txt"
  [ -r "$metadata" ] || die "missing release metadata: $metadata"
  image="$(awk -F= '$1 == "image" { print $2; exit }' "$metadata")"
  bootstrap="$(awk -F= '$1 == "bootstrap_image" { print $2; exit }' "$metadata")"
  [ -n "$image" ] || die "release metadata has no deploy image"
  export SYSBOX_IMAGE_REPO="${image%:*}"
  export SYSBOX_IMAGE_TAG="${image##*:}"
  log "release deploy image: ${image}"
  [ -z "$bootstrap" ] || cat <<EOF
To make the release bootstrap effective, update the CKM controller explicitly:
  CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE=${bootstrap}
Then recreate the selected CKM Server Pod through its normal rollout.
EOF
fi

[ "${RUN_FUNCTIONAL_TESTS}" = true ] || exit 0

for step in 00-check-prereqs.sh 02-test-l0-runtimeclasses.sh 03-test-l0-rootfs.sh 04-install-ckm-chart.sh 05-test-ckm-k3s.sh; do
  if [ "${step}" = 04-install-ckm-chart.sh ] && [ "${CREATE_CKM:-false}" = true ]; then
    log 'creating or selecting the requested CKM'
    "${FLOW_DIR}/01-create-ckm.sh"
  fi
  log "running ${step}"
  "${FLOW_DIR}/${step}"
done

log 'FUNCTIONAL PASS: L0 runtime/rootfs and L1/L2 CKM regression completed'
