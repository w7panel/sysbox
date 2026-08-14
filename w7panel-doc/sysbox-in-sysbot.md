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

当前 218 nested 验证镜像为：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-8-nested-specialdirs4
digest: sha256:4e5119f01dc66e8c2c29a230bcaab5c19c5ea209fcf79596aacbdfb9c681947a
```

该验证镜像中的 runc、mgr、fs 均为静态链接。不得把本机较新 glibc 上直接生成的
动态二进制覆盖进 CentOS 7 deploy 镜像；218 已实际捕获过 `GLIBC_2.32` 和
`GLIBC_2.34 not found`，静态构建后已消除。

固化的 L2 K3s 测试镜像为：

```text
docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:v1.35.6-20260814-1
digest: sha256:f22ed40d625e550a9517ed661159fe18d6eb3eae97941276481b2a630dec0b76
```

该镜像直接基于 `rancher/k3s:v1.35.6-k3s1`，包含 K3s、containerd、
`containerd-shim-runc-v2`、CNI plugins、iptables 和 CA bundle，并内置
`snapshotter=native`、`10.245.0.0/16` bridge CNI 与 `ipMasq:true`。构建定义在
`w7panel-doc/Dockerfile.nested-k3s-test`，不会再依赖从 L1 `/proc/<pid>/root`
现场复制资产。

外层集群安装：

```sh
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n sysbox-system --create-namespace \
  --set installMode=host \
  --set installer.image.tag=v0.7.1-8-nested-specialdirs4
```

在 L1 内 K3s 安装：

```sh
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n sysbox-system --create-namespace \
  --set installMode=nested \
  --set installer.image.tag=v0.7.1-8-nested-specialdirs4
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
- nested agent 拥有 L1 PID namespace、但有独立 mount namespace。mgr 只对
  `/run/netns/*` 和 `/var/run/netns/*` 启用 `/proc/1/root` 解析，以读取 L1 K3s
  创建的瞬时 CNI handle；标准模式和任意其他路径均不允许该回退。
- inner data root 为 `/var/lib/rancher/k3s/sysbox-inner`，218 上实际 backing 是 ext4，
  不再把 Docker special backing 放到 L1 的 FUSE rootfs。

### sysbox-runc

- 仅在 `nested-identity` 接受 `HostID=0`，并校验完整的
  `0:0:65536` UID/GID mapping。
- 对外 handler 始终是 `sysbox-runc`，并强制创建 child user namespace。实现内部
  生成名为 `sysbox-runc-inner` 的 wrapper，仅用于给真实 `sysbox-runc` 传入
  `--mapping-mode nested-identity`；它不是用户应创建或引用的 RuntimeClass。
- nested rootfs 和 bind mount 选择 `NoShift`。
- nested identity 不再跳过已有 special mounts；`/var/lib/docker`、
  `/var/lib/rancher/k3s`、`/var/lib/kubelet` 和
  `/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs` 均使用已有类型，不新增
  special 目录类型。
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
- 预注册 CNI netns 时与 mgr 使用同样的受限 L1-root 解析；每个 L2 仍创建独立 FUSE
  server，不共享任意 L0 路径。

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
L1 Pod:     sysbox-inner-k3s-command-poc-8b49c9fcf-kr9fc（记录时）
L1 UID:     8cc2eec7-a1ad-4f9d-ba22-2d0c79370c8d
L1 IP:      10.42.0.66
L1 K3s PID: 268
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
| L1 CNI Service/外网 | 已通过；CoreDNS、ClusterIP、跨 Pod HTTP、外网 HTTPS 以及 CNI DEL/ADD 后重新分配 IP 均正常 |
| ServiceLinks 隔离 | 已通过；L1 Deployment 和 nested agent 均为 `enableServiceLinks:false`，L2 无冲突的 `SYSBOX_*` 环境变量 |
| rootfs rw layer 持久化 | 已通过；多轮 Pod 重建及 inner daemon 切换后 marker inode/mtime/content/owner 未变 |
| L2 systemd/Docker | 已通过；systemd `running`，dockerd 使用 systemd cgroup driver、Docker bridge/iptables 和端口映射正常 |
| Docker overlay2 | 已通过；不加 tmpfs 的 `/var/lib/docker` 自动使用 ext4-backed 现有 special mount，dockerd 自动选择 `overlay2` |
| 腾讯云 Docker nginx | 已通过；实际 pull digest `sha256:29cf9892...dd159f`，容器 `172.17.0.2`，`-p 18080:80` 返回 HTTP 200 |
| 固化 L2 K3s 测试镜像 | 镜像已构建推送；本地 K3s 8 秒 Ready、CRI NetworkReady、腾讯云 nginx、HTTP 200 和 CNI 回收通过；仍需用当前最终 runtime 镜像做一次完整组合回归 |

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

上述历史轮次刻意禁用了 CoreDNS；后续最终回归已启用 CoreDNS，并确认 Service
ClusterIP、跨 Pod HTTP、外部域名解析和外网 HTTPS 均正常。

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

### rootfs rw layer 与 Docker overlay2 最终结论

问题不是 `sysbox/rootfs-rw-layer`，也不是 mount 缺少 `make-shared`：复现 vfs
降级的 `nested-docker` 没有 rootfs annotation，且 `/var/lib/docker` 已经是 shared。
真正原因是 nested identity 曾跳过 Sysbox 既有 special mounts，导致 Docker 的
upper/work 落到 `fuse.fuse-overlayfs` 根文件系统；kernel overlay 不能把该 FUSE
文件系统作为 upper/work backing。

最终修复是在 nested identity 下重新启用已有 special mounts，并保持 `NoShift`；
inner mgr data root 改为 L1 ext4 路径 `/var/lib/rancher/k3s/sysbox-inner`。218 实测：

```text
/var/lib/docker                                      ext4, Docker overlay2
/var/lib/rancher/k3s                                 ext4
/var/lib/kubelet                                     ext4
/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs  ext4
/                                                     fuse.fuse-overlayfs
```

因此不需要新增 special 类型。普通 `/var/lib/containerd` 仍可位于 FUSE rootfs；只有
overlay snapshotter 的 upper/work 子目录使用现有 `MntVarLibContainerdOvfs`。
Docker 默认 bridge 为 `172.17.0.0/16`，腾讯云 nginx 容器 IP `172.17.0.2`，
`-p 18080:80` 返回 HTTP 200。

`sysbox/rootfs-rw-layer` 的独立持久化也未受影响。inner daemon 完整切换后，重建的
Pod 仍读取到：

```text
path=/rootfs-persistence-marker
inode=21379250 mtime=1786691954 size=35 owner=0:0
content=nested-rootfs-marker-20260814-0719
```

相关单元测试已通过：

```sh
cd sysbox-runc
go test ./libsysbox/syscont ./libcontainer/specconv
go test -vet=off ./libcontainer

cd ../sysbox-mgr
go test ./...
```

`sysbox-mgr` 的既有 shiftfs 用例若因缺少 `/mnt/scratch` 失败，不属于本次 nested
改动。`sysbox-fs` 当前全量测试通过：

```sh
cd sysbox-fs
go test ./...
```

## 未解决或待验证

### 1. 最终 runtime 与固化 L2 K3s 镜像的组合回归

测试镜像资产已经固化到
`docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:v1.35.6-20260814-1`，不再需要
现场复制。镜像在本地 privileged Docker 中已验证 K3s `v1.35.6` 8 秒 Ready、
containerd `2.2.5-k3s2`、CRI NetworkReady、bridge CNI、`ipMasq:true`、腾讯云
nginx HTTP 200，以及删除后的 veth/IPAM/NAT 回收。该验证证明镜像资产和内置配置
完整；此前也已在 218 分步通过 L2 K3s、CoreDNS、L3 CNI 和 nginx。仍需用当前最终
runtime 镜像 `v0.7.1-8-nested-specialdirs4` 与该固化镜像再跑一次从创建到回收的
完整组合回归，避免把跨版本的分步证据当作单次发布验收。

### 2. 外层 rollout 的 sysbox-fs seccomp 注册问题

重建 L1 Deployment 时，新 Pod 曾卡在：

```text
Rejected seccomp session for unregistered container ...
Unable to receive expected seccomp-notif-ack
```

已执行 rollout undo，当前 L1 继续可用。该问题是旧 218 Deployment 的历史现场，
尚未闭环；网络/L3 验证期间不要把普通复测和首次 chart 迁移混在一起随意 rollout。
首次迁移确需加载新增 handler 时，应保存日志并从 L0 控制器受控滚动重建 L1 K3s
Pod；handler 不变的后续二进制升级只滚动 nested agent，不重启 K3s。

### 3. 完整二次 cgroup delegation 资源边界

systemd、默认 Docker bridge/iptables、端口映射、systemd cgroup driver 和 cgroup v2
均已通过。尚待验证的是资源边界而非基本启动：L0 给 L1 设置 CPU/内存上限后，L1
再向 L2 委托子树，需用 Docker 与 K3s 压力负载确认 L2 只能修改自身子树，不能提高
或绕过 L1 的上限，并验证 OOM、CPU throttling 和 Pod 删除后的 cgroup 回收。

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

### 5. 快速验证 Docker overlay2

`nested-docker.yaml` 不包含 rootfs-rw-layer annotation，也不为 `/var/lib/docker`
挂 tmpfs；它用于验证现有 special mount 本身：

```sh
kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec -i "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default apply -f - \
  < w7panel-doc/tests/nested-docker.yaml

kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default exec \
  nested-docker -- sh -lc '
    systemctl is-system-running
    systemctl is-active docker
    docker info --format "{{.Driver}} {{.CgroupDriver}}"
    stat -f -c "%T %t" /var/lib/docker
    findmnt -T /var/lib/docker
    docker pull ccr.ccs.tencentyun.com/afan-public/nginx:latest
    docker run -d --name nested-nginx -p 18080:80 \
      ccr.ccs.tencentyun.com/afan-public/nginx:latest
    curl -fsS http://127.0.0.1:18080/ >/dev/null
  '
```

成功条件是 `overlay2 systemd`、`/var/lib/docker` 为 ext4、容器位于 Docker bridge
且 `18080` 返回 HTTP 200。若 driver 是 `vfs`，先检查 special backing，不要通过
新增 tmpfs 或 `make-shared` 把失败隐藏掉。

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

### 固化测试镜像

无需再向运行中的 L2 复制 K3s 资产，直接使用：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nested-l2-k3s
  annotations:
    sysbox/allow-proc-exec: "true"
spec:
  runtimeClassName: sysbox-runc
  containers:
    - name: k3s
      image: docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:v1.35.6-20260814-1
      imagePullPolicy: IfNotPresent
```

镜像默认启动单节点 K3s server；测试 CNI 为 `10.245.0.0/16`，Service CIDR 为
`10.246.0.0/16`，并使用 ZPK 中的 `nested-pause:20260810-1` 作为 sandbox 镜像。
本地快速构建只增加一个小层，缓存构建约 0.4 秒：

```sh
docker build -f w7panel-doc/Dockerfile.nested-k3s-test \
  -t docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:v1.35.6-20260814-1 .
```

### nested chart 一键 smoke

集群恢复后执行以下脚本。它会记录 Chart 安装前后 L1 K3s PID/starttime，断言没有
重启，等待 nested agent Ready，然后用腾讯云 nginx 验证 L2 child userns、
`0 0 65536` 映射、CNI、HTTP 和删除后的 IPAM/NAT 回收：

```sh
cd /root/workspace/sysbox
KUBECONFIG_218=/home/.kubeconfig \
NAMESPACE=k3k-console-164315 \
DEPLOYMENT=sysbox-inner-k3s-command-poc \
bash w7panel-doc/tests/nested-chart-smoke.sh
```

脚本不会执行 `rollout restart`、`systemctl` 或重启 K3s。首次迁移旧 L1 时若
containerd 尚未加载新 handler，脚本会保存 agent 状态并失败退出，由 L0 管理员决定
是否受控滚动 L1 Pod。

## 部署与安全注意事项

- 当前 218 nested 验证使用
  `docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-8-nested-specialdirs4`（digest
  `sha256:4e5119f01dc66e8c2c29a230bcaab5c19c5ea209fcf79596aacbdfb9c681947a`）。
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
