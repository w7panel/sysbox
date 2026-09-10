# w7panel-sysbox Chart 部署与验收

本文说明如何使用 **Sysbox GitHub Release 发布的 Helm chart** 部署当前轻量运行时，并在
218 外层集群与 CKM 内层 K3s 完成回归。镜像和配置使用 tag；不要将 digest 固化到 values
或 CKM 配置中。

当前交付仅包含 `sysbox-runc`、`sysbox-runc-lite`、snapshotter 与 admission。
`w7panel-ckm` chart 负责创建 CKM Server（L1），不代替本 chart 安装 Sysbox。

## 架构、RuntimeClass 与边界

```text
L0：218 外层 Kubernetes
├─ w7panel-sysbox（installMode=host）
│  ├─ RuntimeClass: sysbox-runc
│  ├─ RuntimeClass: sysbox-runc-lite
│  ├─ sysbox-snapshotter
│  └─ sysbox-admission
└─ CKM Server Pod（L1）
   ├─ runtimeClassName: sysbox-runc，hostUsers: false
   └─ L1 K3s
      └─ w7panel-sysbox（installMode=nested）
         ├─ RuntimeClass: sysbox-runc-lite
         ├─ sysbox-snapshotter + admission
         └─ L2 nginx（runtimeClassName: sysbox-runc-lite）
```

| 位置 | RuntimeClass | `hostUsers` | 说明 |
| --- | --- | --- | --- |
| L0 普通 Sysbox workload | `sysbox-runc` | CKM Server 必须为 `false` | 外层持久 rootfs / CKM |
| L1 CKM Server | `sysbox-runc` | 必须为 `false` | 承载嵌套 K3s |
| L2 workload | `sysbox-runc-lite` | 本轮不设置、不验收 | nginx、rootfs 与普通 CSI 卷 |

L0 admission 不会为普通 `sysbox-runc` Pod 自动挂载 `/dev/fuse`。L1 的 FUSE 设备由
CKM bootstrap/Server 路径提供，不能将它改为通用 webhook 注入规则。

不提供 proc 强隔离、宿主资源视图隔离或 system/Docker workload 支持。L3 仅是历史实验。
限制与现场问题见 [KNOWN-ISSUES.md](./KNOWN-ISSUES.md)。

## 获取并校验发布制品

以下使用已验证的 `v0.7.1-11` 示例。升级时替换 `RELEASE_TAG`；它同时对应 chart
appVersion 和 deploy image tag。

```bash
export RELEASE_TAG=v0.7.1-11
export CHART_VERSION="${RELEASE_TAG#v}"
export RELEASE_DIR="/tmp/sysbox-${RELEASE_TAG}"
export CHART_FILE="w7panel-sysbox-${CHART_VERSION}.tgz"
export RELEASE_URL="https://github.com/w7panel/sysbox/releases/download/${RELEASE_TAG}"

mkdir -p "$RELEASE_DIR"
curl -fL -o "$RELEASE_DIR/$CHART_FILE" \
  "https://gh-proxy.org/${RELEASE_URL}/${CHART_FILE}"
curl -fL -o "$RELEASE_DIR/SHA256SUMS" \
  "https://gh-proxy.org/${RELEASE_URL}/SHA256SUMS"
(
  cd "$RELEASE_DIR"
  grep " $CHART_FILE$" SHA256SUMS | sha256sum -c -
)
helm show chart "$RELEASE_DIR/$CHART_FILE"
```

预期 checksum 为 `OK`，chart 的 `version` 和 `appVersion` 都是 `${CHART_VERSION}`。
可直连 GitHub 时去掉 `https://gh-proxy.org/` 前缀。

发布还包含静态 L1 runtime 二进制：

```bash
export RUNC_LITE_FILE="sysbox-runc-lite-${RELEASE_TAG}-amd64"
curl -fL -o "$RELEASE_DIR/$RUNC_LITE_FILE" \
  "https://gh-proxy.org/${RELEASE_URL}/${RUNC_LITE_FILE}"
chmod 0755 "$RELEASE_DIR/$RUNC_LITE_FILE"
"$RELEASE_DIR/$RUNC_LITE_FILE" --version
```

发布 `image-metadata.txt` 是镜像来源的权威记录。`v0.7.1-11` 对应：

```text
ghcr.io/w7panel/sysbox-deploy-k3s:v0.7.1-11
```

## 前置条件与 CKM 选择

- 本机有 `helm`、`kubectl`、`curl`、`sha256sum`。
- 218 kubeconfig 可访问，例如 `/root/.kube/218.config`。
- L0/L1 节点可以拉取发布镜像。
- 已安装 CKM CRD、K3k controller 与 `w7panel-ckm`，目标 CKM 为 Ready。
- L1 Server 必须由 CKM controller 创建为 `runtimeClassName: sysbox-runc` 和
  `hostUsers: false`；不要在 L2 nginx 中添加 `hostUsers: false`。

```bash
export KUBECONFIG_218=/root/.kube/218.config
export OUTER_NAMESPACE=k3k-console-164315
export CKM_NAMESPACE=k3k-console-164315
export CKM_NAME=ckm-test

kubectl --kubeconfig "$KUBECONFIG_218" get storageclass local-path
kubectl --kubeconfig "$KUBECONFIG_218" -n "$CKM_NAMESPACE" get ckm "$CKM_NAME"
kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" get pods \
  -l "cluster=${CKM_NAME},role=server"
```

## L0：安装 host chart

L0 installer 会更新宿主 runtime 与 containerd。安装过程中 k3s API 短暂不可用属于受控
重启；不要同时触发 CKM 重建或 L1 workload rollout。

```bash
export SYSBOX_IMAGE_REPO=ghcr.io/w7panel/sysbox-deploy-k3s
export SYSBOX_IMAGE_TAG="$RELEASE_TAG"

helm --kubeconfig "$KUBECONFIG_218" upgrade --install w7panel-sysbox \
  "$RELEASE_DIR/$CHART_FILE" --namespace default \
  --set installMode=host \
  --set installer.enabled=true \
  --set installer.image.repository="$SYSBOX_IMAGE_REPO" \
  --set installer.image.tag="$SYSBOX_IMAGE_TAG" \
  --set installer.image.pullPolicy=Always \
  --set admission.enabled=true \
  --set admission.image.repository="$SYSBOX_IMAGE_REPO" \
  --set admission.image.tag="$SYSBOX_IMAGE_TAG" \
  --set admission.image.pullPolicy=Always \
  --set snapshotter.enabled=true --wait --timeout 5m

kubectl --kubeconfig "$KUBECONFIG_218" get runtimeclass sysbox-runc sysbox-runc-lite
kubectl --kubeconfig "$KUBECONFIG_218" -n default get pods \
  -l app.kubernetes.io/instance=w7panel-sysbox -o wide
kubectl --kubeconfig "$KUBECONFIG_218" -n default rollout status \
  deployment/w7panel-sysbox-admission --timeout=180s
```

### L0 smoke：两个 RuntimeClass 均可用且不注入 FUSE

仓库脚本会分别创建 `sysbox-runc` 和 `sysbox-runc-lite` Pod，并清理它们：

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
export KUBECONFIG_218=/root/.kube/218.config
bash ./02-test-l0-runtimeclasses.sh
```

预期末行：

```text
FUNCTIONAL PASS: L0 sysbox-runc and sysbox-runc-lite are runnable without automatic FUSE injection
```

### L0 rootfs / local-path 功能回归

`03-test-l0-rootfs.sh` 以 `sysbox-runc-lite` 验证 L0 的 rootfs PVC 重建持久化、无
`sysbox/volume-init` 注解的 local-path 空卷初始化复制，以及 `/srv/data` special bind。

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
export KUBECONFIG_218=/root/.kube/218.config
bash ./03-test-l0-rootfs.sh
```

手动单个 handler 检查可使用：

```bash
kubectl --kubeconfig "$KUBECONFIG_218" -n default run sysbox-release-outer-check \
  --image=busybox:1.36 --restart=Never \
  --overrides='{"apiVersion":"v1","spec":{"runtimeClassName":"sysbox-runc","containers":[{"name":"sysbox-release-outer-check","image":"busybox:1.36","command":["/bin/sh","-c","sleep 300"]}]}}'
kubectl --kubeconfig "$KUBECONFIG_218" -n default wait \
  --for=condition=Ready pod/sysbox-release-outer-check --timeout=120s
kubectl --kubeconfig "$KUBECONFIG_218" -n default get pod sysbox-release-outer-check \
  -o jsonpath='runtimeClass={.spec.runtimeClassName}{"\n"}volumes={range .spec.volumes[*]}{.name}{" "}{end}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG_218" -n default delete pod sysbox-release-outer-check --wait=true
```

预期：Pod 为 `Running`、`runtimeClass=sysbox-runc`，volumes 仅有 Kubernetes
service-account 项目；不得有 `sysbox-fuse` 或 `/dev/fuse` mount。

## L1：选择 CKM Server 并安装 nested chart

`OUTER_NAMESPACE` 是 Server Pod 所在的外层 namespace，不一定等于 CKM CR namespace。

```bash
export L1_POD="$(kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" get pod \
  -l "cluster=${CKM_NAME},role=server" --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')"
export L1_CONTAINER="$(kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" \
  get pod "$L1_POD" -o jsonpath='{.spec.containers[0].name}')"

kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" get pod "$L1_POD" \
  -o jsonpath='runtimeClass={.spec.runtimeClassName}{" hostUsers="}{.spec.hostUsers}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" exec "$L1_POD" \
  -c "$L1_CONTAINER" -- /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes
```

预期第一条输出 `runtimeClass=sysbox-runc hostUsers=false`。不满足时先修复 CKM template，
不要继续 nested 安装。

在 L0 渲染发布 chart，再通过 exec 安装到 L1；`installer.enabled=false` 避免 nested
chart 重装/重启 L0 installer：

```bash
export CHART_NAMESPACE=default
helm template w7panel-sysbox "$RELEASE_DIR/$CHART_FILE" --namespace "$CHART_NAMESPACE" \
  --set installMode=nested --set runtimeClassName=sysbox-runc-lite \
  --set installer.enabled=false \
  --set installer.image.repository="$SYSBOX_IMAGE_REPO" \
  --set installer.image.tag="$SYSBOX_IMAGE_TAG" \
  --set admission.enabled=true \
  --set admission.image.repository="$SYSBOX_IMAGE_REPO" \
  --set admission.image.tag="$SYSBOX_IMAGE_TAG" \
  --set snapshotter.enabled=true > "$RELEASE_DIR/l1-chart.yaml"

kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" exec -i "$L1_POD" \
  -c "$L1_CONTAINER" -- /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml \
  apply -f - < "$RELEASE_DIR/l1-chart.yaml"
```

写入同一 release 的静态 runc-lite 到 L1 持久 K3s 数据卷；绝不能覆盖 `/usr/bin/runc`：

```bash
base64 -w0 "$RELEASE_DIR/$RUNC_LITE_FILE" | \
  kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" exec -i "$L1_POD" \
    -c "$L1_CONTAINER" -- sh -c \
    'base64 -d > /var/lib/rancher/k3s/sysbox-runc-lite && chmod 0755 /var/lib/rancher/k3s/sysbox-runc-lite'
```

L1 containerd 必须含以下 handler 配置（CKM bootstrap 会创建；首次安装可由
`04-install-ckm-chart.sh` 写入）：

```toml
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc-lite]
  runtime_type = "io.containerd.runc.v2"
  sandboxer = "podsandbox"
  snapshotter = "sysbox"
  pod_annotations = ["sysbox/rootfs-rw-layer"]

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc-lite.options]
  BinaryName = "/var/lib/rancher/k3s/sysbox-runc-lite"
```

首次增加该区块时，需在正常 CKM rollout 中重启 L1 Server 让 containerd 加载 handler。
仅替换同一路径的二进制不需要单独重启。验证：

```bash
kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" exec "$L1_POD" \
  -c "$L1_CONTAINER" -- /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml \
  get runtimeclass sysbox-runc-lite -o jsonpath='handler={.handler}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" exec "$L1_POD" \
  -c "$L1_CONTAINER" -- /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml \
  -n "$CHART_NAMESPACE" rollout status deployment/w7panel-sysbox-admission --timeout=180s
```

预期 handler 是 `sysbox-runc-lite`，admission 为 `1/1 Ready`。

## L2：功能回归与清理

`05-test-ckm-k3s.sh` 创建 runtimeClass 为 `sysbox-runc-lite` 的 nginx，并验证：

1. rootfs PVC 在 Pod 删除重建后持久化；
2. 普通空 CSI PVC 首次复制 nginx 默认文件，且没有 `sysbox/volume-init` 注解；
3. `/srv/data` special bind mount 的 marker 在重建后保留。

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
export KUBECONFIG_218=/root/.kube/218.config
export OUTER_NAMESPACE=k3k-console-164315
export CKM_NAMESPACE=k3k-console-164315
export CKM_NAME=ckm-test
export CHART_NAMESPACE=default
bash ./05-test-ckm-k3s.sh
```

预期末行：

```text
FUNCTIONAL PASS: rootfs persistence, annotation-free CSI empty-volume init and special bind mount verified
```

脚本保留测试 Deployment/PVC 便于检查。结束时清理：

```bash
bash ./99-cleanup.sh
```

默认只删除 L1 测试资源；只有 `DELETE_CKM=true` 才会删除 CKM。

## 开发模式与排障

- `04-install-ckm-chart.sh` 用于本地源码开发：它渲染工作区 chart 并上传本地构建二进制，
  不是发布制品验收入口。
- 发布验收必须按本文使用下载的 `.tgz` 与 release binary，避免未提交的本地内容被误测。
- `RuntimeClass sysbox-runc-lite not found`：确认目标是 L1 K3s，不是 L0；检查
  `sysbox-runc-lite.toml`，必要时正常重启 CKM Server。
- `failed to pull image`：检查 L0/L1 网络与 pull secret；镜像来源以 release
  `image-metadata.txt` 为准。
- outer Pod 有 `sysbox-fuse`：这是过期 admission；升级 L0 chart 至当前 release。
- L2 的 `hostUsers:false` 失败：该项不在本轮 nginx 验收范围内。

本文流程已按 218 的 `v0.7.1-11` release chart 实测：L0 `sysbox-runc` smoke 成功且未
注入 FUSE；`ckm-test` L1 使用发布 image 与发布静态二进制，L2 nginx 三项功能回归均通过。
