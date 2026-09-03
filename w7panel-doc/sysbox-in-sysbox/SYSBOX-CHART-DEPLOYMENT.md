# w7panel-sysbox Chart 部署

本文只说明 `sysbox/charts/w7panel-sysbox` 的安装。`w7panel-ckm` chart 不需要修改；
它只负责创建/管理外层 CKM Server Pod。Sysbox chart 应安装在 CKM Server Pod 内部的
唯一 K3s（L1）中。

## 拓扑和职责

```text
L0 宿主 Kubernetes
└─ w7panel-ckm 创建 CKM Server Pod（runtimeClass=sysbox-runc，hostUsers=false）
   └─ L1 K3s
      └─ w7panel-sysbox chart（installMode=nested）
         ├─ runc-lite RuntimeClass
         ├─ sysbox-admission webhook
         └─ sysbox-snapshotter
```

本 chart 的当前目标是 `runc-lite + snapshotter + admission`。不安装 L2
`sysbox-fs`/`sysbox-mgr`，也不提供 proc 强隔离、视图隔离或 system workload。
L2 `hostUsers:false` 暂不作为本轮部署条件；L1 CKM 仍必须保持 `hostUsers:false`。

## 前置条件

在 L0 完成以下准备：

```bash
kubectl get runtimeclass sysbox-runc
kubectl get pods -A | grep -E 'k3k|w7panel-ckm'
kubectl get storageclass local-path
```

还需要：

- 已部署 `w7panel-ckm` 和 K3k controller；
- 已创建一个 `innerSysbox.enabled=true` 的 CKM；
- CKM Server Pod 为 `runtimeClassName=sysbox-runc`、`hostUsers=false`；
- L1 中可访问 `/dev/fuse`，并已准备 `/var/lib/rancher/k3s/runc-lite`；
- `sysbox-deploy-k3s` 镜像可被 L1 节点拉取。

检查 CKM Server Pod：

```bash
kubectl -n <ckm-namespace> get pod -l 'cluster=<ckm-name>,role=server' \
  -o jsonpath='{range .items[*]}{.metadata.name}{" runtime="}{.spec.runtimeClassName}{" hostUsers="}{.spec.hostUsers}{"\n"}{end}'
# 预期：runtime=sysbox-runc hostUsers=false
```

## 取得 L1 kubeconfig

L1 K3s API 通常只在 CKM Server Pod 内监听。先找到 Server Pod 和普通 K3s 容器：

```bash
export OUTER_KUBECONFIG=/root/.kube/218.config
export OUTER_NAMESPACE=k3k-console-164315
export L1_POD="$(kubectl --kubeconfig "$OUTER_KUBECONFIG" -n "$OUTER_NAMESPACE" \
  get pod -l 'cluster=ckm-6ur35,role=server' -o jsonpath='{.items[0].metadata.name}')"
export L1_CONTAINER=k3k-ckm-6ur35-server

kubectl --kubeconfig "$OUTER_KUBECONFIG" -n "$OUTER_NAMESPACE" \
  exec "$L1_POD" -c "$L1_CONTAINER" -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes
```

后续命令有两种方式：

1. 在 L0 使用仓库脚本，它会通过 `kubectl exec` 将渲染后的 YAML 送入 L1；
2. 若已把 `/etc/rancher/k3s/k3s.yaml` 复制到 L0，则直接使用 `--kubeconfig` 安装。

## 推荐安装：使用现有脚本

编辑 `w7panel-doc/sysbox-in-sysbox/config.sh`：

```bash
KUBECONFIG_218=/root/.kube/218.config
OUTER_NAMESPACE=k3k-console-164315
CKM_NAMESPACE=k3k-console-164315
CKM_NAME=ckm-6ur35
CHART_NAMESPACE=default
```

执行：

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
bash ./00-check-prereqs.sh
bash ./01-create-ckm.sh
bash ./04-install-ckm-chart.sh
```

脚本实际执行的是 Helm 等价配置：

```text
installMode=nested
runtimeClassName=runc-lite
installer.enabled=true
admission.enabled=true
snapshotter.enabled=true
```

默认 installer 镜像为 chart 的 `installer.image`。当前验证使用：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-51-centos9-runc-lite-fuse
digest sha256:9a4764e60c80282fa0804ee7545b3852030c612a018619d675ffd7df237f2ff8
```

如果使用其他镜像，运行脚本前覆盖：

```bash
export SYSBOX_IMAGE_REPO=docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s
export SYSBOX_IMAGE_TAG=v0.7.1-51-centos9-runc-lite-fuse
bash ./04-install-ckm-chart.sh
```

## 直接 Helm 安装

已取得 L1 kubeconfig 时，在 L0 执行：

```bash
helm upgrade --install w7panel-sysbox \
  /root/workspace/sysbox/charts/w7panel-sysbox \
  --kubeconfig "$L1_KUBECONFIG" \
  --namespace default \
  --set installMode=nested \
  --set runtimeClassName=runc-lite \
  --set installer.enabled=true \
  --set installer.image.repository=docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s \
  --set installer.image.tag=v0.7.1-51-centos9-runc-lite-fuse \
  --set installer.image.pullPolicy=Always \
  --set admission.enabled=true \
  --set snapshotter.enabled=true
```

如果 L1 没有 `default` namespace，先创建：

```bash
kubectl --kubeconfig "$L1_KUBECONFIG" create namespace default --dry-run=client -o yaml | \
  kubectl --kubeconfig "$L1_KUBECONFIG" apply -f -
```

## 安装后检查

```bash
kubectl --kubeconfig "$L1_KUBECONFIG" get runtimeclass runc-lite \
  -o jsonpath='handler={.handler}{"\n"}'
kubectl --kubeconfig "$L1_KUBECONFIG" -n default \
  rollout status deployment/w7panel-sysbox-admission --timeout=180s
kubectl --kubeconfig "$L1_KUBECONFIG" get mutatingwebhookconfiguration \
  sysbox-webhook-mutator
kubectl --kubeconfig "$L1_KUBECONFIG" -n default get pods -o wide
```

预期：`RuntimeClass/runc-lite.handler=runc-lite`、admission 为 `1/1` Ready，且
`sysbox-webhook-mutator` 存在。

L1 containerd 的 `runc-lite` 配置必须包含：

```toml
[proxy_plugins."sysbox"]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
  capabilities = ["remap-ids"]

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc-lite]
  runtime_type = "io.containerd.runc.v2"
  sandboxer = "podsandbox"
  snapshotter = "sysbox"
  pod_annotations = ["sysbox/rootfs-rw-layer", "sysbox/volume-init"]

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc-lite.options]
  BinaryName = "/var/lib/rancher/k3s/runc-lite"
```

不要把 runc-lite 复制覆盖 `/usr/bin/runc`；只通过 `RuntimeClass/runc-lite` 使用。

## nginx 功能验证

## CKM Agent 启动故障记录

在 `ckm-test` 的 L1 集群中，`default/w7panel-k3k-agent-console-164315-hck5m` 的
init 容器可以正常完成，但主容器停在 `CreateContainerError`。L1 事件的原始错误为：

```text
failed to generate spec: path "/run/k3s/containerd/io.containerd.runtime.v2.task/k8s.io"
is mounted on "/" but it is not a shared or slave mount
```

该 Pod 由 `w7panel-ckm` Agent DaemonSet 创建，并将宿主路径
`/run/k3s/containerd/io.containerd.runtime.v2.task/k8s.io` 以
`mountPropagation: HostToContainer` 挂载到 Agent。CKM Server 本身运行在外层
Sysbox 容器中，该路径在 L1 中不是 shared/slave mount，因此在容器创建阶段被
Sysbox/runc 拒绝；与 Agent 镜像或 `server:start` 进程无关。修复方向是调整
`w7panel-ckm` Agent 的 runtime 目录挂载传播方式（或取消该不必要的传播要求），
再重新创建 Agent Pod 验证。

## cert-manager CSI Driver 启动故障记录

`cert-manager/cert-manager-csi-driver-2z897` 在 CKM L1 中会出现主容器和
`node-driver-registrar` 重启。事件显示：

```text
path "/tmp/cert-manager-csi-driver" is mounted on "/" but it is not a shared mount
```

该 DaemonSet 将 `/tmp/cert-manager-csi-driver` 和 `/var/lib/kubelet/pods`
以 `mountPropagation: Bidirectional` 挂载。CKM Server 的 Sysbox mount namespace
无法为这些 HostPath 提供 shared mount，因此 CSI 驱动无法创建容器。该问题属于
需要双向 mount propagation 的系统组件兼容性限制，与 nginx rootfs 测试无关。CKM
Server 启动命令预先建立 shared bind mount 后，原始 mount propagation 报错已消失。

随后发现 runc-lite 在嵌套 user namespace 中不会创建默认设备节点，CSI driver
容器内的 `/dev/null` 缺失，导致其执行 tmpfs mount 时退出：

```text
Mount failed: open /dev/null: no such file or directory
```

作为兼容性验证，给 CSI DaemonSet 的三个容器增加宿主 `/dev/null` 的 `hostPath`
挂载后，`cert-manager-csi-driver` 稳定为 `3/3 Running`（重启次数为 0）。这说明
shared bind 修复有效，剩余问题是 runc-lite 默认 `/dev` 设备初始化能力，不应通过
禁用 CSI driver 规避。

同一缺陷也会影响需要访问 `/dev/null` 的普通工作负载；例如 Higress gateway
日志出现：

```text
/usr/local/bin/higress-proxy-start.sh: cannot redirect standard input from /dev/null
Envoy exited with error: open /dev/null: no such file or directory
```

因此该问题不是 cert-manager 特有，而是当前 runc-lite 在嵌套 user namespace
中无法创建或注入默认字符设备。通过 Pod `hostPath` 显式挂载宿主 `/dev/null`
可验证性地绕过该限制。

本地执行 `go test ./...`（`runc-lite`）时，官方 integration 测试中的
`TestUpdateDevices`、`TestUpdateDevicesSystemd` 和 checkpoint 测试也会因测试
rootfs 缺少 `/dev/null` 而失败（例如 `cat: can't open '/dev/null'`）。这些失败
与上述“嵌套 user namespace 中关闭默认设备初始化”的已知限制一致；在修复设备
注入前不能将完整 runc integration suite 视为通过。核心 rootfs/snapshotter
单测仍可单独通过。

Higress 还需要随机设备；同时挂载宿主 `/dev/random` 和 `/dev/urandom` 后，
gateway 已恢复为 `1/1 Running`，重启次数为 0。

CSI 实际证书挂载测试另发现 cert-manager 生成的 CertificateRequest 缺少
`cert-manager.io/private-key-secret-name` annotation，导致 CSI volume 一直处于
`ContainerCreating`。这是当前 cert-manager CSI 测试清单/版本兼容问题，和
runc-lite 的 shared mount 或 rootfs 持久化无关。

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
bash ./05-test-ckm-k3s.sh
```

成功输出：

```text
FUNCTIONAL PASS: rootfs persistence, empty-volume init and special bind mount verified
```

该测试验证：

- 空 PVC 首次获得 nginx 镜像默认文件；
- rootfs marker 在删除/重建 Pod 后保留；
- `/srv/data` 作为 special bind 挂载存在；
- runc-lite、snapshotter handoff 和 admission 链路正常。

重建 CKM Server Pod 后再次验证：原 nginx marker inode `419653`、属主 `0:0` 保持不变，
special bind 文件属主 `1234:2345` 保持不变；CSI driver 仍为 `3/3 Running`，snapshotter
socket 自动恢复。该结果证明 CKM Server 重启不会丢失 rootfs PVC 数据。

随后连续两轮删除并重建 nginx Pod，marker inode 均保持 `419653`，special bind 文件
inode 均保持 `394106`，内容和 UID/GID 每轮一致，未观察到 handoff 偶发丢失。

删除内层 `sysbox-system/w7panel-sysbox-installer`（该副本持续因版本/RBAC 检查
CrashLoop）后，Agent、admission、CSI、Higress 和 nginx 均保持运行；再次执行
`05-test-ckm-k3s.sh` 仍通过。内层实际使用的 installer 位于 `default` 命名空间，
不应重复部署到 `sysbox-system`。

多轮重建期间事件中反复出现一次性错误：

```text
failed to create containerd task: sysbox sidecar oci spec unavailable
```

同一 Deployment 随后通常可重试成功，但这表明 snapshotter handoff/sidecar OCI
spec 存在竞态，当前测试脚本只验证最终成功，尚未证明首次创建无重试即可稳定。

对不带 `sysbox/rootfs-rw-layer` 的普通 `runc-lite` Pod 连续创建 5 次，均在随后
事件中正常 `Created/Started`，未出现该错误；因此竞态目前可缩小到 rootfs
snapshotter handoff 路径，而非 runc-lite handler 的通用启动失败。

随后连续 3 轮执行 `05-test-ckm-k3s.sh`，每轮删除并重建 Deployment/Pod 后均输出
`FUNCTIONAL PASS`，rootfs marker 和 special bind 校验均成功。删除旧 Pod 时终端
可能显示一次 `command terminated with exit code 1`（对应被删除容器的 exec），不影响
脚本最终返回码；每轮实际 Deployment rollout 均成功。

最新复测中，删除并重建 nginx Pod 后再次通过 UID/GID 校验，并临时创建 ClusterIP
Service 由内层 busybox 访问 nginx，Service DNS/HTTP 请求成功（Pod `Running`
后命令退出码为 0）。测试 Pod 和 Service 已清理；CKM Server 仍为 `Running`、
`hostUsers=false`，内层 Agent、CSI Driver、Higress gateway 和 nginx 均保持就绪。

CKM Server 当前启动命令已在启动 K3s 前执行
`mount --make-rshared /var/lib/kubelet`（由 `w7panel-ckm/pkg/resources/k3s_deployment.go`
的 server prelude 注入），并继续对 `/var/lib/kubelet/pods` 和 CSI 临时目录建立
shared bind。当前 CSI Driver 为 `3/3 Running`，最近事件未再出现
`not a shared mount`；因此该命令确实解决了原先的 mount-propagation 阻塞。CSI
容器仍需宿主 `/dev/null` 等设备，这是独立的 runc-lite 设备初始化限制。

再次实际删除 CKM Server Pod 并等待重建后，新 L1 Pod `2/2 Running`；nginx
rootfs marker inode `419653`、UID/GID `0:0`，以及 special bind 文件 inode
`394106`、UID/GID `1234:2345` 均保持。CSI Driver 在新 Pod 就绪后为 `3/3 Running`；
Higress gateway 短暂处于未就绪，约 20 秒后恢复 `Ready`，未发生数据丢失。

## 升级和卸载

升级 chart 使用同一条 `helm upgrade --install` 命令。升级前确认 L1 中没有正在使用
旧 snapshotter 的 workload：

```bash
helm upgrade --install w7panel-sysbox /root/workspace/sysbox/charts/w7panel-sysbox \
  --kubeconfig "$L1_KUBECONFIG" -n default \
  --set installMode=nested --set runtimeClassName=runc-lite \
  --set installer.enabled=true --set admission.enabled=true --set snapshotter.enabled=true
```

卸载：

```bash
helm uninstall w7panel-sysbox --kubeconfig "$L1_KUBECONFIG" -n default
```

卸载 chart 前先删除使用 `runc-lite` 的 workload；不要删除 L0 的
`RuntimeClass/sysbox-runc`，也不要直接删除 CKM 的数据 PVC。

## 常见问题

- `RuntimeClass runc-lite not found`：确认 chart 安装目标是 L1 kubeconfig，而不是 L0。
- `fuse-overlayfs` 找不到 `/dev/fuse`：确认 CKM controller 使用含 `/dev/fuse` 的镜像并
  重建 Server Pod。
- handoff 不生成：检查 `snapshotter.enabled=true`、containerd proxy socket 和 Pod
  annotation 是否存在。
- marker 在重建后丢失：检查快照器是否使用稳定 PVC 路径，而不是
  `/var/lib/kubelet/pods/<podUID>/...`。
- `hostUsers:false` 的 L2 sandbox 报 `operation not permitted`：该特性按当前计划暂缓，
  不属于本轮 chart 验收；详见 [KNOWN-ISSUES.md](./KNOWN-ISSUES.md)。
