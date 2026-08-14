# Helm 安装、测试与卸载

本文以 `v0.7.0-11` release chart、K3s 和 snapshotter 持久化模式为例。所有集群命令显式使用 `/home/.kubeconfig`。

## 前置条件

```bash
export KUBECONFIG=/home/.kubeconfig
export VERSION=v0.7.0-11
export CHART=/tmp/w7panel-sysbox-0.7.0-11.tgz

kubectl --kubeconfig "$KUBECONFIG" get nodes
helm version
```

目标节点必须带有安装标签：

```bash
kubectl --kubeconfig "$KUBECONFIG" label node <node-name> sysbox-runtime=running --overwrite
```

## 下载 release chart

```bash
curl -fL \
  -o "$CHART" \
  "https://github.com/w7panel/sysbox/releases/download/${VERSION}/w7panel-sysbox-${VERSION#v}.tgz"

helm show chart "$CHART"
helm show values "$CHART"
```

生产使用时建议同时下载 release 中的 `SHA256SUMS` 并校验：

```bash
curl -fL \
  -o /tmp/sysbox-SHA256SUMS \
  "https://github.com/w7panel/sysbox/releases/download/${VERSION}/SHA256SUMS"

(cd /tmp && sha256sum -c sysbox-SHA256SUMS --ignore-missing)
```

## 安装或升级

```bash
helm upgrade --install w7panel-sysbox "$CHART" \
  --namespace kube-system \
  --create-namespace \
  --kubeconfig "$KUBECONFIG" \
  --set installMode=host \
  --set snapshotter.enabled=true \
  --set admission.enabled=true \
  --set nodeSelector.sysbox-runtime=running \
  --wait \
  --timeout 10m
```

安装器会修改目标节点的 K3s containerd 模板并重启 k3s。DaemonSet Ready 后仍应等待 API Server 恢复：

```bash
kubectl --kubeconfig "$KUBECONFIG" \
  rollout status daemonset/w7panel-sysbox-installer \
  -n kube-system --timeout=10m

kubectl --kubeconfig "$KUBECONFIG" \
  wait --for=condition=Ready node/<node-name> --timeout=180s

kubectl --kubeconfig "$KUBECONFIG" get --raw='/readyz?verbose'
```

确认 release chart 和镜像版本：

```bash
helm --kubeconfig "$KUBECONFIG" status w7panel-sysbox -n kube-system

kubectl --kubeconfig "$KUBECONFIG" get daemonset,deployment \
  -n kube-system \
  -l app.kubernetes.io/instance=w7panel-sysbox \
  -o wide
```

## Snapshotter 持久化测试

先查看实际 StorageClass：

```bash
kubectl --kubeconfig "$KUBECONFIG" get storageclass
```

执行完整测试；将 `disk-default` 替换为当前集群可用的 StorageClass：

```bash
KUBECONFIG="$KUBECONFIG" \
STORAGE_CLASS=disk-default \
bash w7panel-doc/tests/snapshot-test.sh
```

测试会验证：

- k3s、`sysbox-fs`、`sysbox-mgr`、`sysbox-snapshotter` 服务状态；
- containerd `sysbox` proxy plugin 和 `remap-ids` capability；
- 容器 `/` 使用 PVC-backed `fuse-overlayfs`；
- Pod 重建后 `/a.txt`、文件修改和 whiteout 状态仍然存在；
- 服务日志没有 panic、fatal 或已知回归错误；
- 测试 Deployment 和 PVC 自动清理。

## 卸载

先删除所有使用 `runtimeClassName: sysbox-runc` 的业务工作负载，避免运行中的容器引用即将删除的 runtime。然后删除 Helm release：

```bash
helm uninstall w7panel-sysbox \
  --namespace kube-system \
  --kubeconfig "$KUBECONFIG" \
  --wait \
  --timeout 10m
```

Helm 只删除 Kubernetes 资源，不会撤销已经写入节点的二进制、systemd 服务和 K3s containerd 配置。必须在每个安装过 Sysbox 的节点上执行：

```bash
sudo bash w7panel-doc/uninstall.sh
```

脚本会执行以下操作：

1. 停止并禁用 `sysbox`、`sysbox-fs`、`sysbox-mgr`、`sysbox-snapshotter`；
2. 删除 Sysbox 二进制、systemd、sysctl、modules-load 和运行时文件；
3. 从 `config-v3.toml.tmpl` 精确移除 `sysbox-runc` runtime 与 `sysbox` proxy plugin；
4. 重启 `k3s` 或 `k3s-agent`，使 containerd 重新生成配置；
5. 验证 K3s 正常且生成后的 containerd 配置不再包含 Sysbox。

默认保留 containerd 的 Sysbox snapshotter 数据。如确认不再需要历史镜像层，可在所有 Sysbox Pod 删除后执行彻底清理：

```bash
sudo PURGE_SNAPSHOTTER_DATA=true bash w7panel-doc/uninstall.sh
```

卸载后验证：

```bash
systemctl is-active k3s || systemctl is-active k3s-agent
systemctl is-active sysbox sysbox-fs sysbox-mgr sysbox-snapshotter || true

grep -En 'sysbox-runc|proxy_plugins.*sysbox' \
  /var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl \
  /var/lib/rancher/k3s/agent/etc/containerd/config.toml || true

kubectl --kubeconfig "$KUBECONFIG" get runtimeclass sysbox-runc
```

最后一条命令应返回 `NotFound`，Sysbox 服务应为 inactive 或 not-found，K3s 应为 active。

## 单节点完整测试 Shell

以下脚本会完成安装、持久化测试、Helm 卸载和宿主卸载，最终环境中不保留 Sysbox。仅在测试节点执行：

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

KUBECONFIG=/home/.kubeconfig
VERSION=v0.7.0-11
CHART=/tmp/w7panel-sysbox-${VERSION#v}.tgz
NODE_NAME="$(kubectl --kubeconfig "$KUBECONFIG" get nodes \
  -o jsonpath='{.items[0].metadata.name}')"
STORAGE_CLASS="${STORAGE_CLASS:-disk-default}"

curl -fL -o "$CHART" \
  "https://github.com/w7panel/sysbox/releases/download/${VERSION}/w7panel-sysbox-${VERSION#v}.tgz"

kubectl --kubeconfig "$KUBECONFIG" label node "$NODE_NAME" \
  sysbox-runtime=running --overwrite

helm upgrade --install w7panel-sysbox "$CHART" \
  --namespace kube-system \
  --create-namespace \
  --kubeconfig "$KUBECONFIG" \
  --set installMode=host \
  --set snapshotter.enabled=true \
  --set admission.enabled=true \
  --set nodeSelector.sysbox-runtime=running \
  --wait --timeout 10m

kubectl --kubeconfig "$KUBECONFIG" \
  rollout status daemonset/w7panel-sysbox-installer \
  -n kube-system --timeout=10m
kubectl --kubeconfig "$KUBECONFIG" \
  wait --for=condition=Ready "node/${NODE_NAME}" --timeout=180s
kubectl --kubeconfig "$KUBECONFIG" get --raw='/readyz' >/dev/null

KUBECONFIG="$KUBECONFIG" STORAGE_CLASS="$STORAGE_CLASS" \
  bash w7panel-doc/tests/snapshot-test.sh

helm uninstall w7panel-sysbox \
  --namespace kube-system \
  --kubeconfig "$KUBECONFIG" \
  --wait --timeout 10m

sudo bash w7panel-doc/uninstall.sh
kubectl --kubeconfig "$KUBECONFIG" label node "$NODE_NAME" sysbox-runtime-
```

多节点集群不要直接使用该脚本；Helm release 只卸载一次，但 `uninstall.sh` 必须登录每个安装过 Sysbox 的节点分别执行。
