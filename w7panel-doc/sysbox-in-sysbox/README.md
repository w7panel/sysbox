# Sysbox-in-Sysbox 人工验证流程

## 当前验收范围（2026-09-24）

本轮先实现并验证 rootfs 持久化、空 PVC 初始化复制、特殊目录 bind 挂载，以及
snapshotter/webhook 复用。L2 workload 的 `hostUsers:false`（原步骤 3）暂不实施，
不作为本轮阻塞条件；L1 CKM Server 仍保持 `hostUsers:false`。proc 强隔离、视图隔离
和 system workload 明确放弃。运行时采用官方 runc/libcontainer 的局部修改，不依赖
L2 `sysbox-fs`/`sysbox-mgr`。

内层 K3s 的普通 Pod 不设置 RuntimeClass，使用原生 `runc` 和 overlayfs；需使用精简
运行时的 Pod 显式设置 `runtimeClassName: sysbox-runc-lite`。外层 CKM 仍使用
`sysbox-runc`。`sysbox-runc-lite` 仅提供官方 runc 加 rootfs 持久化、空 PVC
初始化和 special bind mount，不提供 proc/视图隔离或 system container 支持。

> **能力边界（2026-08-24）：Sysbox-in-Sysbox 方案继续保留，只放弃 `/proc` 强隔离和
> Pod 内 Sysbox 系统视图隔离。** K3s、CNI、Docker、rootfs 和 child user namespace
> 等功能链路继续测试和维护；该方案不承诺 `/proc noexec` 或按 Pod 限额虚拟化 CPU/内存
> 视图，不能作为多租户或不可信负载的安全隔离边界。

本文和同目录脚本用于按步骤验证 CKM Pod 内唯一 K3s 的 Sysbox chart：

```text
L0 宿主 Kubernetes
└── CKM Server Pod（外层 RuntimeClass=sysbox-runc）
    └── CKM 自己创建的 K3s
        └── 在该 K3s 安装 w7panel-sysbox（installMode=nested）
            └── Sysbox workload Pod（腾讯云 nginx）
```

`04-install-ckm-chart.sh` 会把本机 Helm CLI 与 chart 包临时复制到选定的 CKM Server，
再连接 `/etc/rancher/k3s/k3s.yaml` 执行真实的 `helm upgrade --install`。因此内层 K3s
保留可查询、可升级的 `w7panel-sysbox` release，不再使用 `helm template + kubectl apply`。

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [README.md](./README.md) | 当前唯一操作手册和脚本顺序 |
| [SYSBOX-CHART-DEPLOYMENT.md](./SYSBOX-CHART-DEPLOYMENT.md) | w7panel-sysbox chart 部署 |
| [KNOWN-ISSUES.md](./KNOWN-ISSUES.md) | 当前能力边界、未解决项和问题根因 |
| [HISTORY.md](./HISTORY.md) | 旧镜像、旧 CKM 和 L2/L3 实验时间线 |

## 当前部署模型

内层 `w7panel-sysbox` Chart 使用 deploy 镜像运行 `inner-agent`。该 agent 在 CKM
Server 的 K3s 数据目录安装脚本和二进制、写入 containerd 模板，配置变化时重启内层
K3s，再核对实际加载的 CRI handler。CKM 已移除 bootstrap initContainer、Server
启动前的 `sysbox-inner-k3s.sh` 调用和 `spec.innerSysbox.enabled` 开关；外层
`sysbox-runc` Server 仍挂载 `/dev/fuse`。再次安装相同配置不应重复重启 K3s。

测试集群迁移须覆盖每个现有 CKM，逐实例安装、等待 K3s 恢复并验证普通 `runc` 与
`sysbox-runc-lite` workload；固定旧 SystemTemplate 的实例也要检查没有旧启动注入。
过去 bootstrap 镜像的发布号、CKM 和测试结果保留在 [HISTORY.md](./HISTORY.md)；
已知能力边界与故障记录见 [KNOWN-ISSUES.md](./KNOWN-ISSUES.md)。

## 层级名称

- **L0**：物理节点上的宿主 Kubernetes，运行 CKM controller、K3k controller 和 CKM Pod。
- **L1**：CKM Server Pod 及其内部唯一 K3s。当前 `04-install-ckm-chart.sh` 的目标就是这一层的 K3s。
- **L2**：L1 K3s 创建的 Sysbox workload Pod，例如 `ckm-k3s-nginx`。它有独立 user namespace，
  但不是第二个 K3s 集群。
- **L3**：历史实验中由 L2 Pod 再启动的 K3s 或 workload。当前流程不创建或测试 L3。

文档中若看到“L2 K3s”字样，指的是旧实验脚本的命名，不是当前 CKM 的实际拓扑；当前 CKM
自有 K3s 始终只有一个。

## 脚本索引（当前流程）

按顺序执行以下脚本，每一步完成后保存输出和最近事件：

| 顺序 | 脚本 | 作用 |
| --- | --- | --- |
| 0 | `00-check-prereqs.sh` | 检查 kubeconfig、CRD、SystemTemplate 和本地工具 |
| 1 | `01-create-ckm.sh` | 复用或按 `config.sh` 名称创建 CKM，并发现 Server Pod |
| 2 | `02-test-l0-runtimeclasses.sh` | 验证 L0 两个标准 RuntimeClass 都能启动且不自动注入 FUSE |
| 3 | `03-test-l0-rootfs.sh` | 回归 L0 lite rootfs、无注解 local-path 初始化复制及 special bind |
| 4 | `04-install-ckm-chart.sh` | 在 CKM 自有 K3s 创建/升级真实 Helm release；Chart agent 安装 runtime 配置并按需重启内层 K3s |
| 5 | `05-test-ckm-k3s.sh` | 创建并回归 sysbox-runc-lite workload；rootfs 结果以最新现场状态为准 |
| 6 | `06-build-and-test.sh` | 快速增量构建或完整 release 后，按 00/02/03/04/05 运行验收 |
| 6 | `99-cleanup.sh` | 清理测试资源，默认保留 CKM |

脚本不会自动跳过失败步骤。Chart 安装或升级可能因内层 K3s 重启短暂中断 API；
重新连接后检查 Helm release、agent 与 CRI 状态。此流程不重启 L0 宿主。

当前功能回归命令：

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
bash ./00-check-prereqs.sh
bash ./01-create-ckm.sh
bash ./02-test-l0-runtimeclasses.sh
bash ./03-test-l0-rootfs.sh
bash ./04-install-ckm-chart.sh
bash ./05-test-ckm-k3s.sh
bash ./99-cleanup.sh
```

日常修改使用已验证的 deploy 镜像作为基线，只构建变更组件：

```bash
BUILD_PROFILE=test \
TEST_COMPONENTS=runc-lite,snapshotter,inner-script \
TEST_TARGETS=deploy \
TEST_BASE_IMAGE=docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:<known-good> \
TEST_IMAGE=docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:<test-tag> \
PUSH_IMAGE=true \
bash ./06-build-and-test.sh
```

将输出的 deploy 镜像仓库和 tag 作为 `SYSBOX_IMAGE_REPO`、`SYSBOX_IMAGE_TAG`
传给内层 Chart 安装脚本；它同时设置 `installer.image` 与 `admission.image`。
Chart 或发布链路改动使用 `BUILD_PROFILE=release bash ./06-build-and-test.sh`。

查看 Deployment、标签和资源限制（命令在 CKM 内 K3s 执行）：

```bash
source ./config.sh
source ./_common.sh
check_common
discover_l1
l1_kubectl -n default get deployment ckm-k3s-nginx -o yaml
```

直接从 L0 进入 workload 必须给两层 `kubectl exec` 都分配 TTY；推荐使用
当前仓库未提供 `06-enter-ckm-shell.sh` 或 `12-test-interactive-exec.sh`；需要交互验证
时按实际 Pod 手工执行两层 `kubectl exec -it`，不要把历史文档中的脚本名当作当前入口。

## 0. 准备工作

需要 Sysbox 仓库位于：

```text
/root/workspace/sysbox
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
CKM_NAME=ckm-sysbox-manual
CHART_NAMESPACE=default
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
- CKM Server Pod 使用 `hostUsers=false`。

如果要强制创建一次性 CKM，设置 `CREATE_CKM=true`，并使用一个当前不存在的新名称：

```bash
CKM_NAME=ckm-sysbox-manual-$(date +%s) \
CKM_NAMESPACE=k3k-console-164315 \
CREATE_CKM=true bash ./01-create-ckm.sh
```

不要复用已删除 CKM 的名称。local-path PVC 若仍在 Terminating 状态，controller 会把它误作
新 Server 磁盘并尝试扩容；`01-create-ckm.sh` 会拒绝这种情况，保留旧 PVC 不做修改。

新建对象的关键字段是：

```yaml
spec:
  runtimeClass: sysbox-runc
  workload:
    apiVersion: k3k.io/v1beta1
    kind: Cluster
```

CKM 仅负责外层 Server 的 `/dev/fuse` 和 Sysbox RuntimeClass；内层运行时须安装
`w7panel-sysbox` Chart 才能使用。
无论是复用还是新建，脚本都会按配置的 CKM 名称定位 Server Pod；如果同名 CKM 已存在
但状态不满足要求会直接失败，不会改用其他 CKM。

确认 L1 的边界：

```bash
kubectl --kubeconfig "$KUBECONFIG_218" -n "$OUTER_NAMESPACE" \
  get pod "$L1_POD" -o jsonpath='{.spec.runtimeClassName}{" hostUsers="}{.spec.hostUsers}{"\n"}'
# 预期：sysbox-runc hostUsers=false
```

## 2. 构建镜像

验证现有 CKM 和 Chart 时可直接使用 `config.sh` 中已发布的 deploy 镜像。
变更了 `runc-lite`、snapshotter、admission 或内层脚本时，使用上文
`BUILD_PROFILE=test` 构建 deploy 镜像；Chart、发布链路或多组件变更使用
`BUILD_PROFILE=release`。内层 Chart 的 agent 和 admission 都从 deploy 镜像运行。

## 3. 在 CKM 自己的 K3s 中安装精简运行时组件

L0 和 CKM 内 K3s 使用同一个 chart，但安装模式不同：

| 层级 | Helm 值 | 作用 |
| --- | --- | --- |
| L0 | `installMode=host` | 安装宿主 Sysbox，提供 CKM Pod 的 `sysbox-runc` |
| CKM 内 K3s 普通 Pod | 不设置 RuntimeClass | 使用原生 runc 和 overlayfs |
| CKM 内 K3s rootfs Pod | `runtimeClassName=sysbox-runc-lite` | 使用精简 runtime、snapshotter 和 admission，提供 rootfs/PVC 功能 |

先在 CKM Pod 内的 K3s 安装：

```bash
bash ./04-install-ckm-chart.sh
```

脚本会检查 CKM Pod 的 `runtimeClassName=sysbox-runc`、`hostUsers=false`，然后等待 admission 与 sysbox-runc-lite：

```text
deployment/w7panel-sysbox-admission successfully rolled out
RuntimeClass/sysbox-runc-lite.handler=sysbox-runc-lite
```

Chart 安装不重启 L0 宿主。首次安装或配置更新会由 inner-agent 重启内层 K3s，
Helm 连接可能暂时中断；重连后核对 release、agent Ready、`crictl info` 中的
`sysbox-runc-lite` handler，再执行 workload 验证。重复安装相同配置不应重启。
迁移现有 CKM 时逐实例执行，并检查旧 bootstrap initContainer 和启动 prelude
已从 Server Pod 消失。

卸载时先移除内层所有 `sysbox-runc-lite` Pod，再运行
`bash ./07-uninstall-ckm-chart.sh`。脚本等待 Helm 卸载完成后重建 CKM Server，
使 containerd 加载恢复后的模板。直接执行 `helm uninstall` 只完成模板与载荷清理，
仍需重启 CKM Server。

## 4. 在 CKM K3s 中运行 Sysbox workload

```bash
bash ./05-test-ckm-k3s.sh
```

该脚本通过 L0 kubeconfig → CKM Pod → CKM 内 K3s API 在 `default` namespace 创建 nginx Deployment，
chart 和 workload 都使用 `default` namespace，验证：

```yaml
spec:
  strategy:
    type: Recreate
  template:
    metadata:
      annotations:
        sysbox/rootfs-rw-layer: '[{"name":"nginx","volumeName":"rootfs","path":"nginx"}]'
    spec:
      runtimeClassName: sysbox-runc-lite
      enableServiceLinks: false
      containers:
      - name: nginx
        resources:
          limits:
            cpu: "1"
            memory: 2Gi
      volumes:
      - name: rootfs
        persistentVolumeClaim:
          claimName: ckm-k3s-nginx-rootfs
```

必须同时满足：

- CKM K3s `default` namespace 中的 chart 资源和 `RuntimeClass/sysbox-runc-lite.handler=sysbox-runc-lite`；
- Deployment、selector 和 Pod labels 都包含 `w7.cc/group-name: ckm-k3s-nginx`；
- workload 使用独立 user namespace，`uid_map=0 0 65536`；
- nginx Deployment 的 Pod 获得 CNI IP，CKM K3s 能访问 HTTP；
- `/` 使用 PVC-backed `fuse-overlayfs`，删除 Pod 后新 Pod UID 变化，rootfs marker 的
  内容、inode、属主和大小保持，重建后的 nginx HTTP 仍可访问；
- 默认保留 Deployment 供人工进入 shell 和后续检查；设置 `KEEP_TEST_DEPLOYMENT=false` 时才会删除，
  并检查 CNI bridge/IPAM/iptables 状态回收，同时删除测试 PVC。

## 5. 当前范围外的历史实验

当前流程只验证 CKM 内 K3s 的 `sysbox-runc-lite` workload、rootfs 持久化、空目录初始化和
特殊 bind mount。Docker/systemd、L3、proc/视图隔离以及 `hostUsers:false` 的 L2 场景
不再作为验收步骤；历史结果仅供追溯，见 `HISTORY.md` 和 `KNOWN-ISSUES.md`。

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
| 找不到 CKM K3s | 检查 `CKM_NAMESPACE/CKM_NAME`、Server Ready 和 Chart agent 状态 |
| inner-agent 不 Ready | 检查 agent 日志、`/var/lib/rancher/k3s` 挂载与内层 K3s 重启状态 |
| inner-agent `CrashLoopBackOff` | 执行下方只读诊断，检查 snapshotter socket、CRI handler 和 agent 日志 |
| 镜像 `no space left on device` | 检查 CKM K3s 节点 ephemeral storage 和节点磁盘 |
| admission 访问旧 IP | 删除 CKM K3s `default` 中的 `w7panel-sysbox-admission` Pod，等待 Service endpoint 重建 |
| `sidecar spec unavailable` 或 seccomp notify 错误 | 先确认 L0 `sysbox-mgr/fs/snapshotter` 健康；单 Pod、串行重建，不要并发删除 CKM |
| workload 无 IP | 检查 CKM K3s 的 `/var/lib/cni/networks`、bridge、`registries.yaml` 和 CNI plugin symlink |
| CKM K3s 内看到 `memory.max=max` | 该视图不代表父 cgroup 无限制；用实际压力测试验证父级限制 |

每完成一个脚本就保存输出和 `kubectl get events --sort-by=.lastTimestamp`，这样可以
区分 CKM 创建、镜像拉取、nested chart、CKM K3s workload 和 CNI 各阶段的问题。

### inner-agent 只读诊断

以下命令只读取状态，不删除 Pod、不重启 CKM：

```bash
source ./config.sh
source ./_common.sh
check_common
discover_l1
l1_kubectl -n default get pods -o wide
l1_kubectl -n "$CHART_NAMESPACE" describe pod -l app.kubernetes.io/component=inner-agent
l1_kubectl -n "$CHART_NAMESPACE" logs daemonset/w7panel-sysbox-inner-agent --tail=200
outer_kubectl -n "$OUTER_NAMESPACE" exec "$L1_POD" -c "$L1_CONTAINER" -- \
  /bin/sh -ec '
    ls -l /run/sysbox/sysbox-snapshotter.sock 2>/dev/null || true
    ss -xl 2>/dev/null | grep sysbox-snapshotter || true
    ps -ef | grep "sysbox-snapshotter" | grep -v grep || true
    pgrep -af sysbox-snapshotter || true
    pgrep -af "k3s (server|init)" || true
  '
```

若日志出现 `timed out waiting for /run/sysbox/sysbox-snapshotter.sock`，先查看
Chart agent 日志并确认内层 K3s 是否已重启。等待 agent 重新建立 socket；
仍失败时保存进程、socket、CRI 和 agent 日志，再定位安装或重启失败的原因。
