#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

need_cmd make
need_cmd docker
log "building Sysbox deploy image ${SYSBOX_IMAGE_REPO}:${SYSBOX_IMAGE_TAG} from local Sysbox sources"
IMAGE_REPO="$SYSBOX_IMAGE_REPO" IMAGE_TAG="$SYSBOX_IMAGE_TAG" \
  PUSH_IMAGE="${PUSH_IMAGE:-false}" \
  VERIFY_IMAGE="${VERIFY_IMAGE:-true}" \
  bash "$REPO_DIR/w7panel-doc/release.sh"
log 'PASS: Sysbox deploy image build completed'
