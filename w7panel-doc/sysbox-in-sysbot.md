# Sysbox-in-Sysbox 218 验证记录

> 状态日期：2026-08-14。本文只描述 `w7panel-sysboxin` 当前实现和 218
> 测试现场。Sysbox nesting 仍是实验性功能，不可据此视为生产可用。

## 目标与层级

目标是在一个 Sysbox Pod 中再次启动 Sysbox Pod，并继续验证内层 K3s、Docker
和普通业务 Pod：

```text
L0 Kubernetes 节点
└─ L1 Sysbox Pod（runtimeClassName: sysbox-runc，hostUsers: false）
   └─ L1 内 K3s/containerd
      └─ L2 Sysbox Pod（runtimeClassName: sysbox-runc）
         └─ L2 内 K3s/Docker
            └─ L3 普通 Pod
```

硬性约束：

- L1 必须设置 `hostUsers: false`，运行在非 initial user namespace。
- L2 必须创建新的 child user namespace，不允许直接复用 L1 user namespace。
- L2 的 UID/GID 映射必须都是 `0 0 65536`。
- `0` 是 L1 user namespace 坐标中的 root，不是 L0 宿主 initial-userns root。
- L2 的 rootfs 和 bind mount 使用 `NoShift`，不得进行第二次 chown、shiftfs
  或 idmapped mount。

历史 PoC 曾尝试共享 L1 user namespace、`1:65535` 映射、跳过 `/proc` 或设置
`hostNetwork: true`。这些路径均不是当前设计，也不能作为验收通过的依据。

### 统一 chart、RuntimeClass 与重启边界

L0 和 L1 使用同一个 `w7panel-sysbox` chart，必须通过 `installMode` 显式选择安装
目标；该值默认为空，遗漏时 Helm 会直接拒绝安装。chart 不根据当前 user namespace
自动猜测；host installer 仍会强制检查 initial user namespace，在 L1 中即使误传
`installMode=host` 也会在写文件或重启 K3s 前失败：

| 安装位置 | 参数 | 安装内容 | 对外 RuntimeClass/handler |
| --- | --- | --- | --- |
| L0 物理宿主集群 | `installMode=host` | 宿主 installer、systemd 服务和 containerd 配置 | `sysbox-runc` / `sysbox-runc` |
| L1 内 K3s | `installMode=nested` | L1 内常驻 nested agent、独立 mgr/fs/snapshotter 和 containerd 配置 | `sysbox-runc` / `sysbox-runc` |

当前发布镜像为：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-2
digest: sha256:e337c1346a53f35f84156d2761b3ccd22224d6630cfd7bb0a487ee8d52d67df4
```

外层集群安装：

```sh
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n sysbox-system --create-namespace \
  --set installMode=host \
  --set installer.image.tag=v0.7.1-2
```

在 L1 内 K3s 安装：

```sh
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n sysbox-system --create-namespace \
  --set installMode=nested \
  --set installer.image.tag=v0.7.1-2
```

**内部 chart 安装不会重启 L0 物理宿主，也不会自行重启 L1 K3s。** nested agent
只会写入 L1 K3s 的 containerd template、安装当前镜像内的二进制并启动独立服务。
若这是把一个已经运行的旧 L1 首次迁移到 nested chart，containerd 不能动态重载新
增的 runtime handler，因此 agent 会保持 NotReady。此时只需从 L0 控制器对该 L1
K3s Pod 做一次受控滚动重建，让 K3s/containerd 随 Pod 重建读取新模板；不需要重启
L0 宿主，也不要单独 kill 或 restart L1 containerd，否则会把整套 L1 K3s 一起带停。

新建 L1 应在 K3s 首次启动前准备好 template，不需要这次迁移重建。后续发布若
handler 名和 containerd 配置均未变化，只升级 runc/mgr/fs/snapshotter 二进制，也
不需要重启 K3s；nested agent 滚动更新并重启自己管理的内层服务即可。安装或升级后
检查：

```sh
kubectl -n sysbox-system rollout status daemonset/w7panel-sysbox-nested-agent
kubectl get node -l sysbox.w7panel.io/nested-runtime=ready
kubectl get runtimeclass sysbox-runc
```

### L1 启动前置

nested agent 从部署镜像把静态 Sysbox 二进制和工具安装到
`/var/lib/sysbox-inner/bin`，以 `nested-identity` 启动 inner `sysbox-mgr`、
`sysbox-fs` 和 `sysbox-snapshotter`。启动前必须确认：

```sh
test -c /dev/fuse
command -v rsync
command -v fusermount3
```

K3s 镜像通常没有完整动态库，runc、fs、`fusermount3` 等使用部署镜像中的静态
产物。inner containerd template 使用 `SystemdCgroup = false` 并禁用 idmapped
mount。CNI 需要执行 `/proc/self/exe`，L1 的
`sysbox/allow-proc-exec: "true"` 注解必须同时通过 containerd 的
`pod_annotations` 和 `container_annotations` 传入 sandbox/workload；该例外只用于
本 PoC，不应成为生产默认值。

## 当前实现

### sysbox-mgr

- 能识别自身处于非 initial user namespace。
- 普通 sub-ID 配置失败时，`auto` 模式可切换到 `nested-identity`；也可显式指定
  `--mapping-mode nested-identity`。
- `nested-identity` 分配固定的 `0:0:65536`，不修改或依赖 L1 的
  `/etc/subuid`、`/etc/subgid`，允许多个 L2 容器复用同一映射。
- 注册和更新请求显式携带 `MappingMode`，mgr 会校验一致性，不通过
  `HostID == 0` 猜测模式。
- netns 更新允许用迁移后的 inode 替换旧 inode；同一 Pod 的 workload 因此能复用
  sandbox 的新 userns/netns。

### sysbox-runc

- 仅在 `nested-identity` 接受 `HostID=0`，并校验完整的
  `0:0:65536` UID/GID mapping。
- 对外 handler 始终是 `sysbox-runc`，并强制创建 child user namespace。实现内部
  生成名为 `sysbox-runc-inner` 的 wrapper，仅用于给真实 `sysbox-runc` 传入
  `--mapping-mode nested-identity`；它不是用户应创建或引用的 RuntimeClass。
- nested rootfs 和 bind mount 选择 `NoShift`。
- 对 CRI sandbox 的持久 CNI netns 实现了迁移：先进入 CNI 创建的旧 netns，创建
  child userns 及其拥有的新 netns，再由 L1 权限的父 runc 迁移非 loopback 接口，
  恢复地址、链路状态、直连路由及 gateway/default 路由，最后把
  `/run/netns` 或 `/var/run/netns` 的 handle 重绑到新 netns。
- 创建的新 netns 会显式启用 loopback。旧实现留下 `lo DOWN`，会导致 L2 K3s 对
  `127.0.0.1:6443` 的 API 自连接超时；现场手工执行 `ip link set lo up` 后 K3s
  立即恢复启动。修复位于 `libcontainer/nested_network_linux.go` 的
  `enableNestedLoopback()`，已有回归测试并通过现有 `libcontainer` 单测。
- 此迁移只触发于 `nested-identity`、CRI sandbox，以及受限的持久 CNI netns 路径；
  不对任意宿主 netns 或路径开放。

### sysbox-fs

- L1 内使用 `/dev/fuse`，每个 L2 容器仍有独立 FUSE server。
- 注册协议携带 mapping mode，并接受 nested identity 的 `Uid=0/Gid=0`。
- nested identity 的 ownership 按 L1 user namespace 坐标解释，不再把 UID 0
  等同于 L0 宿主 root。
- seccomp notify、PID 查询和 namespace 进入仍限制到 L1 可见的 L2 进程。

### cgroup

- 设计是 L0 向 L1 委托 cgroup v2 子树，再由 L1 向 L2 委托自己的子树。
- 当前 L2 已确认 cgroup2 可写且能看到 delegated controllers。
- L2 不能因此修改 L1 的资源上限；systemd、Docker、K3s 在二次 delegation 下的
  完整资源控制仍需继续验收。

## 218 已解决并验证

当前外层现场：

```text
kubeconfig: /root/.kube/218.config
namespace:  k3k-console-164315
deployment: sysbox-inner-k3s-command-poc
L1 Pod:     sysbox-inner-k3s-command-poc-587779ddd9-rh9qn（记录时）
L1 IP:      10.42.0.74
```

Pod 名会随部署变化，测试时应按 label 发现 Running Pod，不应固定使用上述后缀。

| 项目 | 当前结论 |
| --- | --- |
| L1 `runtimeClassName=sysbox-runc`、`hostUsers=false` | 已通过 |
| L1 内 `/dev/fuse`、inner mgr/fs、K3s/containerd、RuntimeClass | 已通过 |
| L2 child userns | 已通过；实际 `uid_map` 为 `0 0 65536` |
| L2 sandbox/workload namespace 共享 | 已通过；两者共享迁移后的 userns/netns，不复用 L1 userns |
| nested CNI bridge/veth | 已通过；L2 内可创建、启用、加入并删除 bridge/veth |
| L2 网络连通性 | 已通过；曾从 `10.244.0.18` ping `10.244.0.1` 和 L1 `10.42.0.74` |
| CNI 持久 handle 重绑 | 已通过；handle 与 sandbox 进程 netns inode 一致 |
| CNI 生命周期回收 | 已通过；删除 L2 后 handle、veth、IPAM 均释放，L1 `cni0` slave 数恢复 |
| L2 cgroup v2 | 已确认可写且 controllers 已委托 |
| 当前 `nested-l2-k3s` | Running；child userns/netns、bridge/veth probe 已通过，`/bin/k3s` 已补齐 |
| nested loopback 修复 | 已通过；全新 `nested-final-check` 中 `lo` 自动为 `UP,LOWER_UP` |
| L2 K3s | 已通过；K3s `v1.35.6`、containerd `2.2.5-k3s2`、native snapshotter、Node Ready |
| L3 CNI | 已通过；bridge 网段 `10.245.0.0/16`，nginx Pod IP `10.245.0.30` |
| 腾讯云 nginx | 已通过；`nginx-ccr` `1/1 Running`，L2 访问 HTTP 200，nginx `1.29.0` |
| L3 生命周期回收 | 已通过；删除 Pod 后 veth、IPAM 文件和对应 NAT 规则消失 |

一次完整网络验收的现场证据为：

```text
L2 Pod:       nested-sysbox-hostusers
L2 IP:        10.244.0.18
sandbox PID:  15709
workload PID: 15931
userns:       user:[4026538117]
netns:        net:[4026538235]
uid_map:      0 0 65536
handle:       5:4026538235
process:      5:4026538235
```

首版路由恢复曾因先添加 default route 而报
`restore route on eth0: network is unreachable`；现已改为先恢复无 gateway 的直连路由，
再恢复 gateway/default route，并完成上述实测。

### L3 K3s/nginx 最终现场

为 L1 临时增加 `10.244.0.0/16` MASQUERADE 后，L2 到腾讯云 registry 的 TLS
连接成功。随后在 L2 启动：

```text
K3s:        v1.35.6
containerd: 2.2.5-k3s2
snapshotter: native
L3 CNI:     bridge, 10.245.0.0/16
Node:       Ready
```

极简 L2 测试镜像中还需补齐 containerd、containerd-shim、CNI plugins、iptables
和 CA bundle。这些是测试镜像资产依赖，不是 nested CNI 实现缺陷。

指定镜像已完成真实拉取和运行：

```text
image:    ccr.ccs.tencentyun.com/afan-public/nginx:latest
image ID: sha256:9a9a9fd723f1d...
size:     72.2 MB
Pod:      nginx-ccr, 1/1 Running
Pod IP:   10.245.0.30
L2 link:  cni3 + veth48b8f6bc
HTTP:     200, nginx/1.29.0
```

L2 同时生成了该 CNI 的 SNAT 规则。从 L2 执行 `wget` 访问 L3 nginx 返回 HTTP
200。删除 `nginx-ccr` 后，`veth48b8f6bc` 消失、`10.245.0.30` 的 IPAM 文件释放、
对应 NAT 规则消失；`cni3` bridge 保留是 bridge CNI 的正常持久状态。

DNS **未验证**：本轮刻意禁用 CoreDNS，Pod 的 `resolv.conf` 指向
`10.246.0.10`，不能把 DNS 记为通过。

### loopback 修复端到端复测

新 runc 先经 node-debugger 写入宿主的仅测试路径
`/opt/sysbox-nested-build/sysbox-runc.new`，再安装到当前 L1 的
`/opt/sysbox/bin/generic/sysbox-runc`；未修改宿主 `/usr/bin/sysbox-runc`。产物：

```text
SHA-256: 96b6e231ba7d81bc42d848a2f7536bd4eec66e3d8961bd6ac4795b351fb9111c
```

用该最终产物新建 L2 Pod `nested-final-check`，结果为 `1/1 Running`，IP
`10.244.0.23`，`uid_map` 为 `0 0 65536`；`lo` 无需手工操作即为
`UP,LOWER_UP`，`eth0` 及 default route 正常，并能通过自动 CNI SNAT ping
`223.5.5.5`。这证明
`enableNestedLoopback()` 已实际部署生效，而不仅是手工 `ip link set lo up` 的行为
推断。Pod 已删除，资源正常回收。

相关单元测试已通过：

```sh
cd sysbox-runc
go test -vet=off ./libcontainer/specconv ./libcontainer

cd ../sysbox-mgr
go test ./...
```

`sysbox-mgr` 的既有 shiftfs 用例若因缺少 `/mnt/scratch` 失败，不属于本次 nested
改动。`sysbox-fs` 的既有未提交修复曾通过：

```sh
cd sysbox-fs
go test ./seccomp ./process ./nsenter
```

## 未解决或待验证

### 1. 出口 SNAT 配置已验证，Deployment 待固化

L1 bridge CNI 原配置为 `ipMasq:false`，L2 源网段 `10.244.0.0/16` 没有 SNAT。
现场把当前 L1 的 CNI 配置改为 `ipMasq:true` 后，新 L2 `nested-egress-check`
自动生成只匹配 `10.244.0.21/32` 的 CNI MASQUERADE 规则。删除此前人工添加的
`10.244.0.0/16` 规则后，该 L2 仍能 ping `223.5.5.5`、通过该 DNS 解析腾讯云
registry，并访问 registry `/v2/` 获得预期 HTTP 401。删除 Pod 后，其 IPAM 文件和
对应 NAT 规则均已释放。

因此 `ipMasq:true` 是已实测通过的配置方案。尚需把 218 Deployment 内联生成 CNI
配置的 `ipMasq:false` 正式改为 `true`；当前不能为此直接 rollout L1，因为外层
sysbox-fs seccomp 注册问题尚未闭环。

### 2. 极简测试镜像资产待固化

L2 K3s 现场通过 L1 `/proc/<pid>/root` 补齐了 K3s 及运行资产。正式 helper/镜像需
预置并校验 containerd、containerd-shim、CNI plugins、iptables 和 CA bundle，避免
依赖现场复制。这是镜像资产问题，不是 nested CNI 缺陷。

### 3. DNS 尚未验证

本轮刻意禁用 CoreDNS，L3 Pod 的 `resolv.conf` 指向 `10.246.0.10`。虽然 nginx
镜像拉取、Pod IP 数据面和 HTTP 已通过，但 DNS 没有验收证据，仍需启用 CoreDNS
后单独验证 service name 和外部域名解析。

### 4. overlay-on-FUSE 尚未解决

- `/`、`/var/lib/docker`、`/var/lib/rancher/k3s` 已确认是 shared mount。
- ext4 特殊目录上 kernel overlay mount 成功。
- FUSE rootfs（例如其 `/tmp`）上的 overlay mount 失败。

所以缺少 `make-shared` 不是根因，重复执行 `mount --make-shared` 不会解决
overlay-on-FUSE。L3 K3s 续测应先使用 `snapshotter=native`；Docker 可用 `vfs`
隔离验证网络，但这只是规避方案，不表示 overlay 已解决。

### 5. 外层 rollout 的 sysbox-fs seccomp 注册问题

重建 L1 Deployment 时，新 Pod 曾卡在：

```text
Rejected seccomp session for unregistered container ...
Unable to receive expected seccomp-notif-ack
```

已执行 rollout undo，当前 L1 继续可用。该问题是旧 218 Deployment 的历史现场，
尚未闭环；网络/L3 验证期间不要把普通复测和首次 chart 迁移混在一起随意 rollout。
首次迁移确需加载新增 handler 时，应保存日志并从 L0 控制器受控滚动重建 L1 K3s
Pod；handler 不变的后续二进制升级只滚动 nested agent，不重启 K3s。

### 6. systemd、Docker 和完整二次 cgroup delegation

- 早期曾以 `dockerd --iptables=false --storage-driver=vfs --bridge=none` 成功拉取
  腾讯云 nginx 镜像；这只证明受限 daemon 可拉取，不是完整 Docker-in-Docker
  验收。
- systemd、默认 Docker bridge/iptables、K3s service/cgroup 上限隔离及压力回收仍待
  验证。
- CoreDNS 镜像拉取失败必须与 runtime/CNI 故障分开记录。

## 快速回归测试

### 1. 运行现有 L2 pause 回归脚本

该脚本会发现 Running 的 L1 Pod，等待 inner Node 和 default ServiceAccount，创建
`sysbox-runc` RuntimeClass 及 pause Pod，并检查 runtime、uid map 和 procfs：

```sh
cd /root/workspace/sysbox
KUBECONFIG_218=/root/.kube/218.config \
NAMESPACE=k3k-console-164315 \
DEPLOYMENT=sysbox-inner-k3s-command-poc \
bash w7panel-doc/tests/sysbox-in-sysbox-218.sh
```

脚本已使用 `awk` 对 `/proc/self/uid_map` 做数值比较，不受列对齐空格影响。也可用
下面的命令独立复核：

```sh
KUBECONFIG_218=/root/.kube/218.config
NS=k3k-console-164315
DEPLOY=sysbox-inner-k3s-command-poc
L1_POD="$(kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" get pod \
  -l "app=$DEPLOY" --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')"

kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default exec \
  nested-sysbox-hostusers -- cat /proc/self/uid_map | \
  awk 'NF == 3 && $1 == 0 && $2 == 0 && $3 == 65536 { ok=1 }
       END { exit !ok }'
```


### 2. 核对 L1 与 L2 namespace

```sh
kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" get pod "$L1_POD" \
  -o jsonpath='{.spec.runtimeClassName}{" "}{.spec.hostUsers}{"\n"}'

kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default exec \
  nested-l2-k3s -- sh -c '
    cat /proc/self/uid_map
    readlink /proc/self/ns/user
    readlink /proc/self/ns/net
    ip -o link show lo
    ip -o link show lo | grep -q "<LOOPBACK,UP"
    mount | grep cgroup2
    cat /sys/fs/cgroup/cgroup.controllers
  '
```

预期第一条输出 `sysbox-runc false`；L2 映射必须为 `0 0 65536`，user/net namespace
不得与 L1 相同，`lo` 必须为 UP，cgroup2 可写且 controllers 非空。
`nested-final-check` 已用包含 `enableNestedLoopback()` 的最终 runc 完成过该端到端验证；
后续回归仍应保留此断言。

### 3. 快速 bridge/veth probe

在 `nested-l2-k3s` 内执行一次可回收探测：

```sh
kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default exec \
  nested-l2-k3s -- sh -c '
    set -eu
    trap "ip link del nested-veth-a 2>/dev/null || true; ip link del cni3 2>/dev/null || true" EXIT
    ip link add cni3 type bridge
    ip link set cni3 up
    ip link add nested-veth-a type veth peer name nested-veth-b
    ip link set nested-veth-a master cni3
    ip link set nested-veth-a up
    ip link set nested-veth-b up
    ip -d link show cni3
    ip -d link show nested-veth-a
  '
```

成功只证明 L2 child userns 拥有自己的 netns 并具备 bridge/veth 管理能力。删除 L2
Pod 后还应检查持久 netns handle、L1 `cni0` slave 和 host-local IPAM 是否恢复，才算
生命周期验收完成。

### 4. 快速验证 L2 出口

L1 bridge CNI 必须为 `10.244.0.0/16` 提供受控 MASQUERADE。验证 L1 公网、DNS
和 L2 出口：

```sh
# L1 视角
kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec "$L1_POD" -c k3s -- \
  sh -c 'ping -c 1 -W 2 223.5.5.5; nslookup ccr.ccs.tencentyun.com 10.43.0.10'

# L2 视角
kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default exec \
  nested-l2-k3s -- sh -c '
    ping -c 1 -W 2 10.244.0.1
    ping -c 1 -W 2 10.42.0.74
    ping -c 1 -W 2 223.5.5.5
  '
```

若最后一条失败，检查 L1 bridge CNI 的 `ipMasq` 和 `10.244.0.0/16` 对应的
masquerade 规则。成功条件是 L2 能访问公网并建立到
`ccr.ccs.tencentyun.com` 的 TLS 连接；只改 `/etc/resolv.conf` 而未恢复出口不算
解决。

## L3 K3s/nginx 快速复测

先确认 L2 的 `10.244.0.0/16` 已有 SNAT/出口，再按已通过配置启动 K3s；不要用
Docker Hub 镜像判断 CNI：

- K3s `v1.35.6`；
- `snapshotter=native`；
- `flannel-backend=none`；
- bridge CNI 使用独立 `10.245.0.0/16` 网段；
- 初次隔离验证可关闭 kube-proxy 和 network-policy。

确认 L2 能访问腾讯云 registry 后，向 L2 K3s 提交：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nginx-ccr
  namespace: default
spec:
  containers:
    - name: nginx
      image: ccr.ccs.tencentyun.com/afan-public/nginx:latest
      imagePullPolicy: IfNotPresent
```

复测必须同时记录：

1. L2 K3s node `Ready`；
2. nginx Pod `Running` 且获得 `10.245.x.x`，不是 `hostNetwork`；
3. L2 出现对应 `cni3`/veth；
4. L2 访问 nginx Pod HTTP 返回 200；
5. 镜像确由指定腾讯云地址拉取并记录 image ID；
6. DNS 是独立待测项；启用 CoreDNS 后才可记录为通过；
7. 删除 Pod 后对应 veth、IPAM 文件和 NAT 规则消失；`cni3` 保留属正常。

## 部署与安全注意事项

- 正式部署使用
  `docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-2`（digest
  `sha256:e337c1346a53f35f84156d2761b3ccd22224d6630cfd7bb0a487ee8d52d67df4`）。
  不要把此前 `/opt/sysbox-nested-build` 或 `/opt/sysbox/bin/generic` 的现场测试文件
  当作发布安装路径。
- nested agent 将内层二进制安装到 `/var/lib/sysbox-inner/bin`。内部
  `sysbox-runc-inner` wrapper 只是固定注入 `nested-identity` 的实现细节；Pod YAML、
  RuntimeClass 名和 handler 均统一使用 `sysbox-runc`。
- inner mgr 启动时 `PATH` 必须包含内层二进制目录；当前 agent 已负责设置：

  ```sh
  PATH=/var/lib/sysbox-inner/bin:$PATH
  ```

  否则会误报 `rsync is not installed on host`。
- **内部 chart 安装不要求也不会触发 L0 宿主重启。**不要为了内层网络复测替换或
  重启 L0 的 `sysbox-runc`、sysbox-mgr；只有首次迁移旧 L1、需要让 containerd 加载
  新 handler 时，才从 L0 控制器滚动重建对应 L1 K3s Pod。
- 临时资源删除前先保存 inner mgr/fs/runc 和 K3s 日志；删除后检查 netns、veth、
  IPAM 和 cgroup 回收。
- 每次复测记录时间、二进制或 helper 版本、L1/L2/L3 phase、uid map、namespace
  inode、关键事件、错误归类及清理结果。构建成功、Pod `Running` 和完整功能验收是
  三个不同状态，不得混写。
