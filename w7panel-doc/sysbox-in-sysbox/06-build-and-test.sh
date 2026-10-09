#!/usr/bin/env bash
# Build the smallest image needed for a nested-runtime regression, then run
# the numbered functional checks. The nested Chart installs the runtime image.
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
fi

log "building with BUILD_PROFILE=${BUILD_PROFILE}"
(cd "${REPO_DIR}" && bash ./w7panel-doc/release.sh)

if [ "${BUILD_PROFILE}" = test ]; then
  # shellcheck disable=SC1091
  source "${REPO_DIR}/dist/test-images.env"
  log "fast image artifacts: ${REPO_DIR}/dist/test-images.env"
else
  metadata="${REPO_DIR}/dist/image-metadata.txt"
  [ -r "$metadata" ] || die "missing release metadata: $metadata"
  image="$(awk -F= '$1 == "image" { print $2; exit }' "$metadata")"
  [ -n "$image" ] || die "release metadata has no deploy image"
  export SYSBOX_IMAGE_REPO="${image%:*}"
  export SYSBOX_IMAGE_TAG="${image##*:}"
  log "release deploy image: ${image}"
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
