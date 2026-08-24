#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
log "outer context: $(outer_kubectl config current-context 2>/dev/null || true)"
log "outer namespace: $OUTER_NAMESPACE"
if configured_ckm_exists; then
  select_existing_ckm
  log "CKM target: $CKM_NAMESPACE/$CKM_NAME"
else
  log "configured CKM $CKM_NAMESPACE/$CKM_NAME does not exist; 01-create-ckm.sh will create it"
fi
api_resources="$(outer_kubectl api-resources)"
grep -E '^ckms[[:space:]].*ckm.w7.cc' <<<"$api_resources" >/dev/null || die 'CKM CRD is not installed'
grep -E '^clusters[[:space:]].*k3k.io' <<<"$api_resources" >/dev/null || die 'K3k CRD is not installed'
outer_kubectl -n "$CKM_NAMESPACE" get systemtemplate k3s.v1 >/dev/null || die 'SystemTemplate k3s.v1 is missing'
if [ -n "${L1_POD:-}" ]; then discover_l1; else log 'L1 Pod will be discovered by the next stage.'; fi
log 'PASS: local tools, kubeconfig, CRDs and k3s.v1 template are available'
