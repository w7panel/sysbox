# Sysbox-in-Sysbox 人工验证流程

本文和同目录脚本用于按步骤验证完整链路：

```text
L0 宿主 Kubernetes
└── L1 CKM Server Pod（外层 RuntimeClass=sysbox-runc）
    └── L2 K3s（在 L1 Pod 内安装 w7panel-sysbox，RuntimeClass=sysbox-runc）
        └── L3 Sysbox Pod（腾讯云 nginx）
```

脚本不会假设 `helm` 可以连接到内层 API。Chart 统一在操作者机器上用
`helm template` 渲染，再通过 `kubectl exec` 把 YAML 送进 L1/L2 K3s；这样不需要把
Helm 二进制塞进测试镜像，也能清楚区分当前操作落在哪一层。

## 0. 准备工作

需要以下目录位于同一个工作区：

```text
/root/workspace/sysbox
/root/workspace/w7panel-ckm
```

外层集群必须已经安装：

- `ckms.ckm.w7.cc`（w7panel-ckm CRD 和 controller）；
- `clusters.k3k.io`（K3k controller）；
- `SystemTemplate/k3s.v1`；
- L0 的 `RuntimeClass/sysbox-runc` 及可用的 Sysbox host chart；
- `local-path` StorageClass（用于 L1/L2 的 K3s 数据 PVC）。

编辑 [config.sh](./config.sh)，最少确认以下值：

```bash
KUBECONFIG_218=/root/.kube/218.config
OUTER_NAMESPACE=k3k-console-164315
CKM_NAMESPACE=$OUTER_NAMESPACE
CKM_NAME=ckm-bzhrq
```

`CKM_NAME` 是唯一选择依据，脚本不会在多个 CKM 中随机选择；`CKM_SELECTOR` 会根据
该名称自动生成为 `cluster=<CKM_NAME>,role=server`。也可以临时覆盖配置：

```bash
CKM_NAME=ckm-bzhrq CKM_NAMESPACE=k3k-console-164315 \
  bash ./00-check-prereqs.sh
```

先执行前置检查：

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
bash ./00-check-prereqs.sh
```

## 1. 选择已有 CKM 或显式新建 CKM

`w7panel-ckm` 中的已有 CKM 已经自带一个 K3s，该 K3s 是本流程的 L1。执行
`01-create-ckm.sh` 时只使用 `config.sh` 的 `CKM_NAMESPACE/CKM_NAME`：已有 CKM 就复用，
不存在就用这个名称创建，不会随机选择其他 CKM：

```bash
bash ./01-create-ckm.sh
```

必须满足：

- `status.clusterPhase=Ready`；
- `spec.runtimeClass=sysbox-runc`；
- `spec.innerSysbox.enabled=true`；
- CKM Server Pod 使用 `hostUsers=false`。

如果要强制创建一次性 CKM，设置 `CREATE_CKM=true`，并使用一个当前不存在的新名称：

```bash
CKM_NAME=ckm-sysbox-manual-$(date +%s) \
CKM_NAMESPACE=k3k-console-164315 \
CREATE_CKM=true bash ./01-create-ckm.sh
```

新建对象的关键字段是：

```yaml
spec:
  runtimeClass: sysbox-runc
  innerSysbox:
    enabled: true
  workload:
    apiVersion: k3k.io/v1beta1
    kind: Cluster
```

`innerSysbox.enabled=true` 只负责让 controller 在 L1 K3s 启动前准备 nested
handler、`/dev/fuse` 和 Sysbox 二进制；它不会自动安装 `w7panel-sysbox` Helm Chart。
无论是复用还是新建，脚本都会按配置的 CKM 名称定位 Server Pod；如果同名 CKM 已存在
但状态不满足要求会直接失败，不会改用其他 CKM。

确认 L1 的边界：

```bash
kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" \
  get pod "$L1_POD" -o jsonpath='{.spec.runtimeClassName}{" hostUsers="}{.spec.hostUsers}{"\n"}'
# 预期：sysbox-runc hostUsers=false
```

## 2. 构建镜像

### 2.1 构建 CKM controller 镜像

`02-build-images.sh` 使用本地 `w7panel-ckm/Dockerfile` 构建 controller 镜像。镜像
默认名为：

```text
docker.cnb.cool/i0358/zpk/w7panel-ckmv3:manual-sysboxin
```

单独构建并推送：

```bash
CKM_IMAGE_TAG=$(git -C /root/workspace/w7panel-ckm rev-parse --short HEAD) \
PUSH_IMAGES=true bash ./02-build-images.sh
```

推送前先登录目标 registry；不要把 token 写入脚本或 YAML：

```bash
docker login docker.cnb.cool -u cnb -p "$CNB_TOKEN"
```

将新镜像用于已经部署的 operator（deployment 名称按实际 Helm release 调整）：

```bash
kubectl --kubeconfig "$KUBECONFIG_218" -n ckm-system \
  set image deployment/ckm-operator-controller-manager \
  manager="docker.cnb.cool/i0358/zpk/w7panel-ckmv3:${CKM_IMAGE_TAG}"
kubectl --kubeconfig "$KUBECONFIG_218" -n ckm-system \
  rollout status deployment/ckm-operator-controller-manager
```

如果 controller 是 Helm 管理的，优先在 Helm values 中更新 image，再执行
`helm upgrade`，避免下次 reconcile 被旧 values 覆盖。

### 2.2 构建 L2 K3s 测试镜像

同一个脚本还会构建 `Dockerfile.nested-k3s-test`。它包含：

- K3s server；
- CNI multicall 插件和 `registries.yaml`；
- `nested-k3s-entrypoint.sh`，负责建立 CNI、containerd、`/run/k3s`、
  `/run/sysbox` 和 shared mount；
- 与当前 nested chart 兼容的 Sysbox 工具。

构建并推送：

```bash
NESTED_K3S_IMAGE=docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:manual-$(date +%Y%m%d%H%M) \
PUSH_IMAGES=true bash ./02-build-images.sh
```

如果只想使用已经发布的镜像，跳过本步骤，并在 `config.sh` 保留可拉取的
`NESTED_K3S_IMAGE`。Kubernetes 节点不能直接使用操作者 Docker daemon 中的本地
镜像，因此真实 CKM 测试必须把镜像推到节点可访问的 registry，或预先导入每个节点。

### 2.3 从当前 Sysbox 源码重建 deploy 镜像（可选）

如果本轮修改了 `sysbox-runc`、`sysbox-mgr`、`sysbox-fs` 或 nested-agent，再执行：

```bash
# 快速确认五个本地二进制能编译
bash ../build.sh local

# 完整构建 K3s deploy 镜像；默认不推送
PUSH_IMAGE=false bash ./02-build-sysbox-deploy.sh

# 确认 registry 登录后再推送，并把同一 tag 写入 config.sh
docker login docker.cnb.cool -u cnb -p "$CNB_TOKEN"
SYSBOX_IMAGE_TAG=v0.7.1-manual-$(git -C ../.. rev-parse --short HEAD) \
PUSH_IMAGE=true bash ./02-build-sysbox-deploy.sh
```

该步骤比复用已发布镜像慢很多；只是验证 CKM/chart 流程时可跳过，直接使用
`config.sh` 中的稳定 `SYSBOX_IMAGE_TAG`。

## 3. 在 L1 安装独立 Sysbox Chart

L0 和 L1 使用同一个 chart，但安装模式不同：

| 层级 | Helm 值 | 作用 |
| --- | --- | --- |
| L0 | `installMode=host` | 安装宿主 Sysbox，提供 L1 的 `sysbox-runc` |
| L1 | `installMode=nested` | 在 L1 K3s 安装 nested-agent、mgr/fs/snapshotter 和 admission，提供 L2 的 `sysbox-runc` |
| L2 | `installMode=nested` | 在 L2 K3s 再安装一套 nested runtime，提供 L3 的 `sysbox-runc` |

先执行 L1 安装：

```bash
bash ./03-install-l1-chart.sh
```

脚本会检查 L1 `runtimeClassName=sysbox-runc`、`hostUsers=false`，然后等待：

```text
daemonset/w7panel-sysbox-nested-agent successfully rolled out
deployment/w7panel-sysbox-admission successfully rolled out
RuntimeClass/sysbox-runc.handler=sysbox-runc
node label sysbox.w7panel.io/nested-runtime=ready
```

L1 Chart 安装不应重启 L0 宿主。首次把一个已经运行的旧 L1 迁移到 nested
handler 时，若 containerd 不能热加载，按已有 CKM 控制器做一次“只重建 L1 Pod”的
受控迁移；不要重启物理节点，也不要并发删除多个 CKM Pod。

## 4. 创建 L2 K3s

`04-create-l2-k3s.sh` 不创建 CKM，也不切换 CKM 版本；它复用配置中 CKM 自己创建的
L1 K3s，在 L1 的 `default` namespace 创建 L2：

- `nested-l2-k3s-manual-rootfs`，5 Gi `local-path` PVC；
- `nested-l2-k3s-manual`，`runtimeClassName: sysbox-runc`；
- `enableServiceLinks: false`，避免 `SYSBOX_*` ServiceLinks 污染；
- `/dev/fuse`；
- `sysbox/rootfs-rw-layer`，把 PVC 用作持久 rootfs 层。

执行并等待 L2 API：

```bash
bash ./04-create-l2-k3s.sh
```

因此版本关系是：

```text
已有 CKM 的 K3s = L1
04-create-l2-k3s.sh 创建的 K3s = L2
```

CKM controller、SystemTemplate 或 K3s 镜像版本需要单独升级并完成 L1 受控重建后，
再重新执行本流程；不能通过 `04-create-l2-k3s.sh` 切换 CKM 版本。

检查 L2 使用的是独立 user namespace，而不是加入 L1：

```bash
kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" \
  exec "$L1_POD" -c "$L1_CONTAINER" -- readlink /proc/self/ns/user
```

## 5. 在 L2 再安装 Chart

```bash
bash ./05-install-l2-chart.sh
```

脚本仍然从本地 `charts/w7panel-sysbox` 渲染，目标改为 L2 K3s。成功条件与 L1
相同：admission 和 nested-agent Ready，节点有
`sysbox.w7panel.io/nested-runtime=ready`，且 `RuntimeClass/sysbox-runc.handler` 为
`sysbox-runc`。工作负载 YAML 在 L1 和 L2 都只写：

```yaml
spec:
  runtimeClassName: sysbox-runc
```

不要在 Pod 中使用历史的 `sysbox-runc-inner` 或 `sysbox-runc-nested` 名称。

## 6. 创建 L3 并验证网络、映射和清理

```bash
bash ./06-test-l3.sh
```

该脚本调用现有的 [nested-l3-smoke.sh](../tests/nested-l3-smoke.sh)，完整路径是
L0 kubeconfig → L1 `kubectl exec` → L2 `kubectl exec` → L3 Pod。默认镜像是：

```text
ccr.ccs.tencentyun.com/afan-public/nginx:latest
```

必须同时满足：

- L3 `uid_map` 和 `gid_map` 都是 `0 0 65536`；
- L3 user namespace 与 L2 不同；
- L3 获得 Pod IP，L2 能访问 nginx HTTP；
- CNI bridge/IPAM/iptables 在删除 L3 后回收；
- L3 Pod 使用 `enableServiceLinks: false` 和 `runtimeClassName=sysbox-runc`。

## 7. 验证 rootfs-rw-layer 持久化

先只验证写入（不重建 Pod）：

```bash
bash ./07-test-rootfs-persistence.sh
```

确认 marker 写入成功后，再做一次可控的 L2 Pod 重建：

```bash
RECREATE_L2=true bash ./07-test-rootfs-persistence.sh
```

PVC 不删除，脚本会重新创建 L2 Pod 并检查 marker 内容仍一致。若需要保留 inode、属主
和大小证据，将脚本输出保存到测试记录中。

## 8. 清理

清理默认只删除 L2 Pod/PVC 和 L1/L2 chart，保留 CKM，避免误删真实测试集群：

```bash
bash ./99-cleanup.sh
```

只有明确要删除本次临时 CKM 时才执行：

```bash
DELETE_CKM=true bash ./99-cleanup.sh
```

## 常见故障定位

| 现象 | 优先检查 |
| --- | --- |
| 找不到 L1 | `CKM_SELECTOR` 是否为 `cluster=<CKM_NAME>,role=server`，以及 CKM 是否 `innerSysbox.enabled=true` |
| L1 agent 不 Ready | L1 `/run/k3s`、`/run/sysbox` 和 K3s 数据目录是否 shared；查看 `kubectl -n sysbox-system logs daemonset/w7panel-sysbox-nested-agent` |
| 镜像 `no space left on device` | 检查 L1/L2 ephemeral storage 和节点磁盘；清理旧 image/container 后重新执行对应阶段 |
| admission 访问旧 IP | 删除 L1/L2 `w7panel-sysbox-admission` Pod，等待 Service endpoint 重建后再测 |
| `sidecar spec unavailable` 或 seccomp notify 错误 | 先确认 L0 `sysbox-mgr/fs/snapshotter` 健康；单 Pod、串行重建，不要并发删除 CKM |
| L3 无 IP | 检查 L2 `/var/lib/cni/networks`、`cni3` bridge、`registries.yaml` 和 CNI plugin symlink |
| L2 内看到 `memory.max=max` | 该视图不代表父 cgroup 无限制；用实际压力测试验证父级 `memory.max`/CPU throttling |

每完成一个脚本就保存输出和 `kubectl get events --sort-by=.lastTimestamp`，这样可以
区分 CKM 创建、镜像拉取、nested chart、L2 K3s、L3 CNI 各阶段的问题。
