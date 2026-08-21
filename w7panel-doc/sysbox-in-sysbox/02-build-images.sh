#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

need_cmd docker
need_cmd make
need_cmd git
CKM_CONTEXT="${CKM_CONTEXT:-$REPO_DIR/../w7panel-ckm}"
[ -d "$CKM_CONTEXT" ] || die "w7panel-ckm checkout not found: $CKM_CONTEXT"
log "building CKM operator image ${CKM_IMAGE_REPO}:${CKM_IMAGE_TAG}"
docker build -t "${CKM_IMAGE_REPO}:${CKM_IMAGE_TAG}" "$CKM_CONTEXT"
log "building nested K3s test image ${NESTED_K3S_IMAGE}"
docker build -f "$REPO_DIR/w7panel-doc/Dockerfile.nested-k3s-test" \
  -t "$NESTED_K3S_IMAGE" "$REPO_DIR"
if [ "${PUSH_IMAGES:-false}" = true ]; then
  docker push "${CKM_IMAGE_REPO}:${CKM_IMAGE_TAG}"
  docker push "$NESTED_K3S_IMAGE"
else
  log 'PUSH_IMAGES=false; images are local only. Set PUSH_IMAGES=true after registry login to push.'
fi
docker image inspect "${CKM_IMAGE_REPO}:${CKM_IMAGE_TAG}" >/dev/null
docker image inspect "$NESTED_K3S_IMAGE" >/dev/null
log 'PASS: CKM and nested K3s images built'
