# Sysbox-in-Sysbox 人工验证流程

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

脚本不会假设 `helm` 可以连接到内层 API。Chart 统一在操作者机器上用
`helm template` 渲染，再通过 `kubectl exec` 把 YAML 送进 CKM Pod 内的 K3s；这样不需要把
Helm 二进制塞进测试镜像，也能清楚区分当前操作落在哪一层。

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [README.md](./README.md) | 当前唯一操作手册和脚本顺序 |
| [KNOWN-ISSUES.md](./KNOWN-ISSUES.md) | 当前能力边界、未解决项和问题根因 |
| [HISTORY.md](./HISTORY.md) | 旧镜像、旧 CKM 和 L2/L3 实验时间线 |
| [skills/sysbox-in-sysbox-analysis/SKILL.md](./skills/sysbox-in-sysbox-analysis/SKILL.md) | 分支差异、user namespace 实现、测试证据分析 Skill |

## 当前基线（2026-08-24）

- 镜像：`v0.7.1-47-nested-tty-exec`，digest
  `sha256:e10b0f5905fc1d0dbf913079fc396cea4a5984b69810ed1ce04d029555c946a2`。
- Chart：`w7panel-sysbox 0.7.1-15`，`installMode=nested`；runtimeClass 统一为
  `sysbox-runc`。
- 已通过：child userns `0 0 65536`、CNI/HTTP、nginx 和 Docker rootfs 持久化、
  Docker `overlay2`、二次 cgroup delegation、nested-agent 重建、双层交互 exec。
- 明确不支持：`/proc noexec` 强隔离、Pod 内 CPU/内存视图隔离、多租户或不可信负载
  安全边界。运行 `08-check-isolation.sh` 预期返回非零。
- 尚待验证：长时间并发重启、L0 Sysbox 服务重启恢复、大 rootfs rsync 生命周期压力。

详细证据和根因见 [KNOWN-ISSUES.md](./KNOWN-ISSUES.md)，历史测试结果不作为当前验收依据。

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
| 2 | `02-build-sysbox-deploy.sh` | 可选：从当前源码构建 Sysbox deploy 镜像 |
| 4 | `04-install-ckm-chart.sh` | 在 CKM 自有 K3s 的 `default` 安装 nested chart |
| 5 | `05-test-ckm-k3s.sh` | 创建并验证保留的 nginx Deployment |
| 6 | `06-enter-ckm-shell.sh` | 从真实 TTY 进入保留的 nginx 或 Docker shell |
| 8 | `08-check-isolation.sh` | 只读审计两项已放弃的隔离能力；当前实现预期返回非零 |
| 9 | `09-test-docker-rootfs.sh` | 验证 Docker overlay2、构建及 rootfs/special mount 重建持久性 |
| 10 | `10-test-cgroup-delegation.sh` | 验证 L2 delegated 子树与 L1 的 1 CPU/2GiB 父边界 |
| 11 | `11-test-nested-agent-lifecycle.sh` | 重建 nested-agent，检查服务单实例和新 workload 创建 |
| 12 | `12-test-interactive-exec.sh` | 用真实 PTY 自动验证 nginx、Docker 交互式 exec |
| 99 | `99-cleanup.sh` | 清理 chart 和当前测试资源，默认保留 CKM |

脚本不会自动跳过失败步骤。需要重建 CKM Server Pod 或删除资源时，应先人工确认；这些操作
可能短暂中断 CKM 内 K3s，但不应重启 L0 宿主。

当前功能回归命令：

```bash
cd /root/workspace/sysbox/w7panel-doc/sysbox-in-sysbox
bash ./00-check-prereqs.sh
bash ./01-create-ckm.sh
bash ./04-install-ckm-chart.sh
bash ./05-test-ckm-k3s.sh
bash ./09-test-docker-rootfs.sh
bash ./10-test-cgroup-delegation.sh
bash ./11-test-nested-agent-lifecycle.sh
bash ./12-test-interactive-exec.sh
# 隔离能力审计（当前实现预期返回非零，不影响功能结论）：
bash ./08-check-isolation.sh
# 保留 Deployment 后，在真实终端进入 nginx 或 Docker：
bash ./06-enter-ckm-shell.sh nginx
bash ./06-enter-ckm-shell.sh docker
```

查看 Deployment、标签和资源限制（命令在 CKM 内 K3s 执行）：

```bash
source ./config.sh
source ./_common.sh
check_common
discover_l1
l1_kubectl -n default get deployment ckm-k3s-nginx -o yaml
```

直接从 L0 进入 workload 必须给两层 `kubectl exec` 都分配 TTY；推荐使用
`06-enter-ckm-shell.sh nginx|docker`，避免手工命令漏掉外层 `-it`。参数是 `-it`，
不是 `-ut`。自动回归可执行 `12-test-interactive-exec.sh`。

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

当前 CKM K3s workload 直接使用
`ccr.ccs.tencentyun.com/afan-public/nginx:latest`。只有修改了 `sysbox-runc`、
`sysbox-mgr`、`sysbox-fs` 或 nested-agent 时才需要重建 deploy 镜像：

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

### user namespace 与内嵌 containerd 的边界

user namespace 的创建和 `0 0 65536` 映射由 `sysbox-mgr`、`sysbox-runc` 完成，containerd
只负责 CRI/OCI 调度并调用 `io.containerd.runc.v2` 的 `BinaryName`。因此修改 nested identity
逻辑不需要重新编译 containerd。只有首次新增 runtime handler 或 `sysbox` snapshotter 时，
才需要重建 L1 K3s Pod，让 K3s 启动其内嵌 containerd 并重新读取
`/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl`；这仍然不是重新编译
containerd。后续 handler 名和配置不变时，滚动 nested-agent/运行时二进制即可。

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
        sysbox/allow-proc-exec: "true"
    spec:
      runtimeClassName: sysbox-runc
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

- CKM K3s `default` namespace 中的 chart 资源和 `RuntimeClass/sysbox-runc.handler=sysbox-runc`；
- Deployment、selector 和 Pod labels 都包含 `w7.cc/group-name: ckm-k3s-nginx`；
- workload 使用独立 user namespace，`uid_map=0 0 65536`；
- nginx Deployment 的 Pod 获得 CNI IP，CKM K3s 能访问 HTTP；
- `/` 使用 PVC-backed `fuse-overlayfs`，删除 Pod 后新 Pod UID 变化，rootfs marker 的
  内容、inode、属主和大小保持，重建后的 nginx HTTP 仍可访问；
- 默认保留 Deployment 供人工进入 shell 和后续检查；设置 `KEEP_TEST_DEPLOYMENT=false` 时才会删除，
  并检查 CNI bridge/IPAM/iptables 状态回收，同时删除测试 PVC。

## 5. Docker、cgroup 和 nested-agent 生命周期

nginx smoke 通过后依次执行：

```bash
bash ./09-test-docker-rootfs.sh
bash ./10-test-cgroup-delegation.sh
bash ./11-test-nested-agent-lifecycle.sh
```

`09` 会保留 `default/nested-docker-rootfs-persistence` Deployment/PVC，主动删除并重建
一次 Pod。`10` 依赖该资源，通过 L1 可见的 CRI PID 检查真实 cgroup 父路径，不能用
L2 内被虚拟化为 `max` 的视图替代。`11` 会删除并重建 nested-agent 和 nginx Pod，
但断言 CKM K3s server PID/starttime identity 不变；它不会重启 L1 Pod 或 L0 节点。

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
| nested-agent `CrashLoopBackOff` | 先执行下方只读诊断；重点检查 snapshotter socket 是否只有文件但无监听进程，以及是否存在多个残留 launcher |
| 镜像 `no space left on device` | 检查 CKM K3s 节点 ephemeral storage 和节点磁盘 |
| admission 访问旧 IP | 删除 CKM K3s `default` 中的 `w7panel-sysbox-admission` Pod，等待 Service endpoint 重建 |
| `sidecar spec unavailable` 或 seccomp notify 错误 | 先确认 L0 `sysbox-mgr/fs/snapshotter` 健康；单 Pod、串行重建，不要并发删除 CKM |
| workload 无 IP | 检查 CKM K3s 的 `/var/lib/cni/networks`、bridge、`registries.yaml` 和 CNI plugin symlink |
| CKM K3s 内看到 `memory.max=max` | 该视图不代表父 cgroup 无限制；用实际压力测试验证父级限制 |

每完成一个脚本就保存输出和 `kubectl get events --sort-by=.lastTimestamp`，这样可以
区分 CKM 创建、镜像拉取、nested chart、CKM K3s workload 和 CNI 各阶段的问题。

### nested-agent 只读诊断

以下命令只读取状态，不删除 Pod、不重启 CKM：

```bash
source ./config.sh
source ./_common.sh
check_common
discover_l1
l1_kubectl -n default get pods -o wide
l1_kubectl -n default describe pod -l app.kubernetes.io/name=w7panel-sysbox-nested-agent
l1_kubectl -n default logs daemonset/w7panel-sysbox-nested-agent --tail=200
outer_kubectl -n "$OUTER_NAMESPACE" exec "$L1_POD" -c "$L1_CONTAINER" -- \
  /bin/sh -ec '
    ls -l /run/sysbox/sysbox-snapshotter.sock 2>/dev/null || true
    ss -xl 2>/dev/null | grep sysbox-snapshotter || true
    ps -ef | grep "sysbox-inner-k3s.sh" | grep -v grep || true
    pgrep -af sysbox-snapshotter || true
    pgrep -af "k3s (server|init)" || true
  '
```

若日志出现 `timed out waiting for /run/sysbox/sysbox-snapshotter.sock`，先运行
`11-test-nested-agent-lifecycle.sh`。v47 会回收旧 launcher/snapshotter 并等待新 socket 真正监听；
只有该脚本仍失败时才保存进程、socket 和 agent 日志，再判断是否需要受控重建 CKM Server Pod。
