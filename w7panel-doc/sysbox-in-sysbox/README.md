# Sysbox-in-Sysbox 人工验证流程

本文和同目录脚本用于按步骤验证 CKM Pod 内唯一 K3s 的 Sysbox chart：

```text
L0 宿主 Kubernetes
└── CKM Server Pod（外层 RuntimeClass=sysbox-runc）
    └── CKM 自己创建的 K3s
        └── 在该 K3s 安装 w7panel-sysbox（installMode=nested）
            └── Sysbox workload Pod（腾讯云 nginx）
```

脚本不会假设 `helm` 可以连接到内层 API。Chart 统一在操作者机器上用
`helm template` 渲染，再通过 `kubectl exec` 把 YAML 送进 CKM Pod 内的 K3s；这样不需要把
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
- `local-path` StorageClass（用于 CKM K3s 的系统盘 PVC）。

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

### 2.2 测试镜像说明

`02-build-images.sh` 中的 `Dockerfile.nested-k3s-test` 只用于历史三层实验，不是当前
CKM 单 K3s 流程的必需镜像。当前 CKM K3s workload 直接使用：

- `ccr.ccs.tencentyun.com/afan-public/nginx:latest`。

如需执行历史 L2/L3 三层实验，才需要构建并推送 `NESTED_K3S_IMAGE`；当前流程可以跳过。

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

## 3. 在 CKM 自己的 K3s 中安装 Sysbox Chart

L0 和 CKM 内 K3s 使用同一个 chart，但安装模式不同：

| 层级 | Helm 值 | 作用 |
| --- | --- | --- |
| L0 | `installMode=host` | 安装宿主 Sysbox，提供 CKM Pod 的 `sysbox-runc` |
| CKM 内 K3s | `installMode=nested` | 安装 nested-agent、mgr/fs/snapshotter 和 admission，提供 Sysbox workload 的 `sysbox-runc` |

先在 CKM Pod 内的 K3s 安装：

```bash
bash ./04-install-ckm-chart.sh
```

兼容旧流程也可以执行：

```bash
bash ./03-install-l1-chart.sh
# 或旧文件名：bash ./04-create-l2-k3s.sh
```

脚本会检查 CKM Pod 的 `runtimeClassName=sysbox-runc`、`hostUsers=false`，然后等待：

```text
daemonset/w7panel-sysbox-nested-agent successfully rolled out
deployment/w7panel-sysbox-admission successfully rolled out
RuntimeClass/sysbox-runc.handler=sysbox-runc
node label sysbox.w7panel.io/nested-runtime=ready
```

Chart 安装不应重启 L0 宿主。首次把一个已经运行的 CKM K3s 迁移到 nested
handler 时，若 containerd 不能热加载，按已有 CKM 控制器做一次“只重建 L1 Pod”的
受控迁移；不要重启物理节点，也不要并发删除多个 CKM Pod。

## 4. 在 CKM K3s 中运行 Sysbox workload

```bash
bash ./05-test-ckm-k3s.sh
```

该脚本通过 L0 kubeconfig → CKM Pod → CKM 内 K3s API 创建 nginx Pod，验证：

```yaml
spec:
  runtimeClassName: sysbox-runc
  enableServiceLinks: false
```

必须同时满足：

- CKM K3s 中的 `RuntimeClass/sysbox-runc.handler=sysbox-runc`；
- workload 使用独立 user namespace，`uid_map=0 0 65536`；
- nginx Pod 获得 CNI IP，CKM K3s 能访问 HTTP；
- 删除 workload 后 CNI bridge/IPAM/iptables 状态回收。

## 5. 三层 L2/L3 实验（可选历史流程）

`05-install-l2-chart.sh`、`06-test-l3.sh`、`07-test-rootfs-persistence.sh` 仍保留用于
历史的 L2 K3s/L3 Sysbox 实验，但不属于“CKM Pod 内单个 K3s 安装 chart”的默认流程。
默认测试不要执行这些脚本，也不要创建第二个 K3s。

## 6. 清理

清理默认只删除当前测试 workload 和 chart，保留 CKM：

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
| 找不到 CKM K3s | 检查 `CKM_NAMESPACE/CKM_NAME`，以及 CKM 是否 `innerSysbox.enabled=true` |
| nested-agent 不 Ready | 检查 CKM K3s 的 `/run/k3s`、`/run/sysbox` 和 K3s 数据目录是否 shared |
| 镜像 `no space left on device` | 检查 CKM K3s 节点 ephemeral storage 和节点磁盘 |
| admission 访问旧 IP | 删除 CKM K3s 中的 `w7panel-sysbox-admission` Pod，等待 Service endpoint 重建 |
| `sidecar spec unavailable` 或 seccomp notify 错误 | 先确认 L0 `sysbox-mgr/fs/snapshotter` 健康；单 Pod、串行重建，不要并发删除 CKM |
| workload 无 IP | 检查 CKM K3s 的 `/var/lib/cni/networks`、bridge、`registries.yaml` 和 CNI plugin symlink |
| CKM K3s 内看到 `memory.max=max` | 该视图不代表父 cgroup 无限制；用实际压力测试验证父级限制 |

每完成一个脚本就保存输出和 `kubectl get events --sort-by=.lastTimestamp`，这样可以
区分 CKM 创建、镜像拉取、nested chart、CKM K3s workload 和 CNI 各阶段的问题。
