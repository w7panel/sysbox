#!/usr/bin/env bash
# Verify that an empty PVC receives image directory content once, then keeps
# user data across a Sysbox Pod recreation.

set -Eeuo pipefail

KUBECONFIG="${KUBECONFIG:-/home/.kubeconfig}"
NAMESPACE="${NAMESPACE:-default}"
STORAGE_CLASS="${STORAGE_CLASS:-longhorn}"
TEST_IMAGE="${TEST_IMAGE:-nginx:latest}"
NODE_NAME="${NODE_NAME:-}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-300}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"

RUN_ID="$(date +%Y%m%d%H%M%S)-$$"
NAME="sysbox-volume-init-${RUN_ID}"
LABEL="app=${NAME}"
TOKEN="volume-init-${RUN_ID}"
K=("${KUBECTL_BIN}" --kubeconfig "${KUBECONFIG}" --request-timeout=30s)

info() { printf '[INFO] %s\n' "$*"; }
pass() { printf '[PASS] %s\n' "$*"; }
die() { printf '[FAIL] %s\n' "$*" >&2; exit 1; }

cleanup() {
    info "清理临时资源 ${NAMESPACE}/${NAME}"
    "${K[@]}" delete deployment -n "${NAMESPACE}" "${NAME}" \
        --ignore-not-found --wait=true --timeout="${TIMEOUT_SECONDS}s" >/dev/null 2>&1 || true
    "${K[@]}" delete pvc -n "${NAMESPACE}" "${NAME}" \
        --ignore-not-found --wait=true --timeout="${TIMEOUT_SECONDS}s" >/dev/null 2>&1 || true
}
trap cleanup EXIT

ready_pod() {
    local excluded_uid="${1:-}"
    local deadline=$((SECONDS + TIMEOUT_SECONDS))
    local pod uid ready

    while ((SECONDS < deadline)); do
        while read -r pod; do
            [[ -n "${pod}" ]] || continue
            uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
            ready="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
            if [[ -n "${uid}" && "${uid}" != "${excluded_uid}" && "${ready}" == True ]]; then
                printf '%s\n' "${pod}"
                return 0
            fi
        done < <("${K[@]}" get pod -n "${NAMESPACE}" -l "${LABEL}" \
            --sort-by=.metadata.creationTimestamp -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
        sleep 2
    done
    return 1
}

create_workload() {
    info "创建空 PVC 和 Sysbox Deployment: ${NAMESPACE}/${NAME}"
    "${K[@]}" apply -n "${NAMESPACE}" -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${NAME}
  labels:
    app: ${NAME}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${STORAGE_CLASS}
  resources:
    requests:
      storage: 128Mi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${NAME}
  labels:
    app: ${NAME}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${NAME}
  template:
    metadata:
      labels:
        app: ${NAME}
    spec:
      runtimeClassName: sysbox-runc
      hostUsers: false
${NODE_NAME:+      nodeName: ${NODE_NAME}}
      containers:
      - name: nginx
        image: ${TEST_IMAGE}
        volumeMounts:
        - name: webroot
          mountPath: /usr/share/nginx/html
      volumes:
      - name: webroot
        persistentVolumeClaim:
          claimName: ${NAME}
EOF
    "${K[@]}" rollout status -n "${NAMESPACE}" "deployment/${NAME}" \
        --timeout="${TIMEOUT_SECONDS}s" >/dev/null
}

main() {
    local pod uid annotation replacement content

    "${K[@]}" get runtimeclass sysbox-runc >/dev/null || die 'RuntimeClass sysbox-runc 不存在'
    create_workload
    pod="$(ready_pod)" || die "Pod 未在 ${TIMEOUT_SECONDS}s 内 Ready"
    uid="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" -o jsonpath='{.metadata.uid}')"

    annotation="$("${K[@]}" get pod -n "${NAMESPACE}" "${pod}" \
        -o jsonpath='{.metadata.annotations.sysbox/volume-init}')"
    grep -q '"name":"nginx"' <<<"${annotation}" || die 'admission 未写入 nginx volume-init intent'
    grep -q '"volumeName":"webroot"' <<<"${annotation}" || die 'volume-init intent 未引用 webroot'
    grep -q '"mountPath":"/usr/share/nginx/html"' <<<"${annotation}" || die 'volume-init intent 挂载路径错误'
    pass 'admission 已写入 volume-init annotation'

    "${K[@]}" exec -n "${NAMESPACE}" "${pod}" -c nginx -- \
        sh -c 'test -f /usr/share/nginx/html/index.html && test -f /usr/share/nginx/html/50x.html'
    "${K[@]}" exec -n "${NAMESPACE}" "${pod}" -c nginx -- \
        grep -q '<title>Welcome to nginx!</title>' /usr/share/nginx/html/index.html
    pass '空 PVC 已获得镜像中的 Nginx 默认文件'

    "${K[@]}" exec -n "${NAMESPACE}" "${pod}" -c nginx -- \
        sh -c "printf '%s' '${TOKEN}' > /usr/share/nginx/html/index.html"
    "${K[@]}" delete pod -n "${NAMESPACE}" "${pod}" --wait=true >/dev/null
    replacement="$(ready_pod "${uid}")" || die "重建 Pod 未在 ${TIMEOUT_SECONDS}s 内 Ready"
    content="$("${K[@]}" exec -n "${NAMESPACE}" "${replacement}" -c nginx -- cat /usr/share/nginx/html/index.html)"
    [[ "${content}" == "${TOKEN}" ]] || die '重建后 PVC 内容被镜像重新覆盖'
    pass '重建 Pod 后 PVC 原有内容未被覆盖'
}

main "$@"
