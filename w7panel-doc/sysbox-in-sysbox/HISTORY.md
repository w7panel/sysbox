# Sysbox-in-Sysbox 历史验证记录

> 2026-09-02 范围决策：步骤 3（L2 `hostUsers:false`）暂缓，不纳入当前验收；优先完成
> rootfs 持久化、空目录初始化、特殊 bind 以及 snapshotter/webhook 复用。L1 CKM
> 仍保持 `hostUsers:false`。

> 本文是 218 上旧镜像、旧 CKM 和 L2/L3 实验的时间线归档。文中的“当前”、待办、
> Pod 名称和命令只代表对应日期，不作为现行操作依据。当前人工流程见 [README.md](./README.md)，
> 当前能力边界与未解决问题见 [KNOWN-ISSUES.md](./KNOWN-ISSUES.md)。

> 能力边界（2026-08-21）：**Sysbox-in-Sysbox 方案继续保留，只放弃 `/proc` 强隔离
> 和 Pod 内 Sysbox 系统视图隔离。** 218 确认 L2 `/proc` 为可执行挂载，且
> 1 CPU/2GiB Pod 仍看到 72 CPU/63457684kB 内存。K3s、Docker、CNI、L3 和持久化
> 等功能验证继续有效，但不能据此声称具备这两项隔离能力。详细边界见
> [KNOWN-ISSUES.md](./KNOWN-ISSUES.md) 与 [README.md](./README.md)。

## 2026-09-02 轻量 runc-lite nginx 回归记录

> **证据范围说明（2026-09-04 修订）：** 本节的 `FUNCTIONAL PASS` 是 2026-09-03
> 某一组 CKM、bootstrap 镜像和运行时配置下的成功轮次，属于历史现场证据，不是当前
> 分支的持续保证。其后合并了 w7panel 子模块、切换了扁平化 CentOS 9 bootstrap 镜像、
> 修正了 `runc-lite` snapshotter 配置并调整了 exec/rootfs 路径；这些变化会改变
> containerd、sidecar handoff 和 FUSE rootfs 的启动顺序。当前状态、最新失败点和可复现
> 验收门槛以 [KNOWN-ISSUES.md](./KNOWN-ISSUES.md) 为准。L2 workload 的
> `hostUsers:false` 按范围决定不实现、不测试；L1 CKM Server 的 `hostUsers:false`
> 仍是硬性要求。

### 2026-09-04 轻量模式修复后复测

将 `SYSBOX_INNER_START_DAEMONS=false` 改为“跳过 inner mgr/fs、保留 snapshotter”，并将
Server readiness 收敛为 K3s API + snapshotter socket 后，使用 flat9 bootstrap 在
`ckm-test` 重新创建 Server。`05-test-ckm-k3s.sh` 返回：

```text
FUNCTIONAL PASS: rootfs persistence, empty-volume init and special bind mount verified
```

本轮确认空 PVC 初始化、rootfs marker 重建持久化、special bind、snapshotter handoff 和
admission webhook 均正常；L2 不设置 `hostUsers:false`。Server 保留 `hostUsers=false`，
inner mgr/fs 未启动，仅 snapshotter socket 存在。

新实验从完整 nested Sysbox 迁移到 `runc-lite + sysbox-snapshotter + admission`，
不再把 inner `sysbox-fs`/`sysbox-mgr`、Docker 或 systemd 作为目标。CKM
`k3k-console-164315/ckm-6ur35` 的 L1 已注入 `/dev/fuse`（`10,229`）；普通
`runc-lite-static-test` 已 Running 并输出 `runc-lite-fuse-ok`。nginx 已通过
`runc-lite → sysbox-snapshotter → fuse-overlayfs` 启动。快照器不再使用含 Pod UID
的 kubelet mount path，而是将 Sysbox mountinfo 中 `/rootfs/special/...` 的 bind root
解析为稳定 PVC 路径；重建 Pod 后 marker 保留在同一 upperdir。`05-test-ckm-k3s.sh`
使用 Pod containerID/cgroup 精确关联真实 nginx PID，以 nsenter 检查 root，已通过
空卷镜像初始化、marker 持久化和 `/srv/data` special bind。

L2 `hostUsers:false` 单独复测仍在内嵌 containerd 创建 sandbox userns 时失败，尚未进入
runc-lite；L1 CKM Pod 保持 `hostUsers=false`。完整限制见
[KNOWN-ISSUES.md](./KNOWN-ISSUES.md)。

## 2026-08-24 CKM 单 K3s 最终回归快照

本次流程以 `ckm-sysbox-manual` 为 L1，不再额外创建第二个 K3s。使用
`sysbox-deploy-k3s:v0.7.1-47-nested-tty-exec`（digest
`sha256:e10b0f5905fc1d0dbf913079fc396cea4a5984b69810ed1ce04d029555c946a2`，
`sysbox-runc=5208ebb`）完成以下验证：

- L1 K3s 安装 `w7panel-sysbox` chart `0.7.1-15`，`installMode=nested`，不重启 K3s。
- L2 nginx 使用独立 child userns、`uid_map=0 0 65536`，CNI/HTTP 和 PVC rootfs 重建保持。
- L2 systemd/Docker 使用 `overlay2`；`/var/lib/docker` 为 PVC 上 `ext4 idmapped` special
  mount，Pod 重建后 marker、inode、构建镜像 ID 和镜像缓存保持。
- L2 cgroup 实际位于 `sysbox.delegate/init.scope`；L1 父边界保持 1 CPU/2GiB。
- nested-agent Pod 重建后 launcher/snapshotter 各一份、socket 正在监听，K3s identity
  保持 `376:2996`，随后 Sysbox nginx 可重新创建。
- nginx 和 systemd/Docker 容器的双层 `kubectl exec -it` 均获得真实 PTY，可交互并正常退出。

可重复执行的权威流程见 [README.md](./README.md)，新增检查为
历史脚本（包括已移除的 `09-test-docker-rootfs.sh`、`10-test-cgroup-delegation.sh`）、
`11-test-nested-agent-lifecycle.sh` 和 `12-test-interactive-exec.sh`。本文件后续较早日期的镜像和结论保留为历史记录。

## 目标与层级

目标是在一个 Sysbox Pod 中再次启动 Sysbox Pod，并继续验证内层 K3s、Docker
和普通业务 Pod：

```text
L0 Kubernetes 节点
└─ L1 Sysbox Pod（runtimeClassName: sysbox-runc，hostUsers: false）
   └─ L1 内 K3s/containerd
      └─ L2 Sysbox Pod（runtimeClassName: sysbox-runc）
         └─ L2 内 K3s/Docker
            └─ L3 Sysbox Pod（runtimeClassName: sysbox-runc）
```

### L0、L1、L2、L3 分别是什么

- **L0** 是物理机或虚拟机节点及其外层 Kubernetes。它运行在 initial user
  namespace，L0 的 UID 0 才是宿主 root；物理磁盘、最外层 cgroup 上限、外层
  containerd、宿主 sysbox-mgr/fs/runc 和 `installMode=host` chart 都属于 L0。
- **L1** 是 L0 Kubernetes 直接创建的 Sysbox Pod。它使用
  `runtimeClassName: sysbox-runc` 和标准 Kubernetes `hostUsers:false`，因此拥有第一层
  非 initial user namespace；L1 内可运行 systemd、Docker 或 K3s。本方案把 L1
  作为“内层节点”，在其中用 `installMode=nested` 安装独立的 mgr、fs、snapshotter
  和 `sysbox-runc` handler。若要保存 L1 根文件系统及 inner K3s data-dir，PVC
  rootfs annotation 必须配置在这个 L1 Pod 上。
- **L2** 是 L1 内 K3s/containerd 再创建的 Sysbox Pod。它仍对外使用
  `runtimeClassName: sysbox-runc`，但 runtime 必须创建 L1 user namespace 的 child
  user namespace；L2 看到的 `uid_map`/`gid_map` 是 `0 0 65536`，这里第二列的 0 是
  **L1 坐标中的 root**，不是 L0 initial-userns root。L2 可继续运行 systemd、Docker
  或 K3s。
- **L3** 是 L2 内 K3s 使用统一的 `runtimeClassName: sysbox-runc` 启动的业务 Pod，
  例如本文用于验证的 nginx。L3 会再创建一个 child user namespace，并继续使用
  `0 0 65536` 映射；它用来验证第三层 Sysbox procfs、第二次 CNI、镜像拉取、HTTP、
  cgroup 和生命周期回收。历史普通 runc L3 结果只能作为 CNI 基线，不能替代本项。

从宿主 `/proc` 观察，L1 的映射通常形如 `0 <L0-subuid> 65536`；从 L1 内观察 L2
则是 `0 0 65536`。因此不能跨层直接比较数字 0，更不能把 nested identity 中的
`HostID=0` 解读成获得 L0 宿主 root。

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
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-46-current-binaries
digest: sha256:0b85c10dad9599c407fc29b555377f615f398d07b3580e342ed50bb3b2b44423
```

该镜像继承已扁平化的 base，避免复用旧 inner containerd overlay lower chain；正式
nested-agent DaemonSet 已在 L1 和 L2 成功滚动到该版本。

该验证镜像中的 runc、mgr、fs 均为静态链接。不得把本机较新 glibc 上直接生成的
动态二进制覆盖进 CentOS 7 deploy 镜像；218 已实际捕获过 `GLIBC_2.32` 和
`GLIBC_2.34 not found`，静态构建后已消除。

固化的 L2 K3s 测试镜像为：

```text
docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:v1.35.6-20260814-4
digest: sha256:758cab020bd6f41a9d5aa33bc2d50062a62bb96d7cb03b0ad51d9085878894ff
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
  --set installer.image.tag=v0.7.1-46-current-binaries
```

在 L1 内 K3s 安装：

```sh
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n sysbox-system --create-namespace \
  --set installMode=nested \
  --set installer.image.tag=v0.7.1-46-current-binaries \
  --set admission.enabled=true
```

218 的正式 smoke 使用 `admission.enabled=true`；admission Deployment、Service 和
Webhook 必须先 Ready，`sysbox/rootfs-rw-layer` 工作负载才具备自动校验与 sidecar
注入前置。该 admission 安装已实测不会重启 L1 K3s。

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

### sysbox-snapshotter

- `LocalPreparer` 会比较自身与 PID 1 的 mount namespace。两者不同时，仅对
  `/var/lib/kubelet/pods` 下已解析出的 PVC access path，通过 `/proc/1/root` 进入
  L1 K3s 可见的真实路径创建 rootfs `upper`/`work`。
- 返回给 runc 的 overlay mount option 仍保留原始 L1 kubelet path，不带
  `/proc/1/root` 前缀；该前缀只用于 snapshotter 本地 prepare I/O，避免把 agent
  私有 mount namespace 的路径泄漏到后续 runtime mount。
- 非 PVC kubelet 路径、标准模式以及 self/PID 1 mount namespace 相同的场景保持
  原逻辑。相关 `sysbox-snapshotter/rootfs` 单测已通过。

### cgroup

- 设计是 L0 向 L1 委托 cgroup v2 子树，再由 L1 向 L2 委托自己的子树。
- 当前 L2 已确认 cgroup2 可写且能看到 delegated controllers。
- L2 不能因此修改 L1 的资源上限；systemd、Docker、K3s 在二次 delegation 下的
  完整资源控制仍需继续验收。

## 218 已解决并验证

当前访问入口与最新 smoke 现场：

```text
kubeconfig: /root/.kube/218.config
namespace:  k3k-console-164315
deployment: sysbox-inner-k3s-rootfs-poc
L1 Pod:     按 Deployment label 动态发现
L1 K3s:     按 /proc 中实际 /bin/k3s server 进程动态发现
```

宿主重启后 Pod 名、UID、IP 和 inner 集群身份均已变化，测试时必须按 label 发现
Running Pod，不应复用重启前记录。

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
| 固化镜像 `nested-l2-k3s` | 重启前曾通过 child userns/netns 与 bridge/veth probe；PoC inner 集群已在宿主重启后重建，当前需重新做完整组合回归 |
| nested loopback 修复 | 已通过；全新 `nested-final-check` 中 `lo` 自动为 `UP,LOWER_UP` |
| L2 K3s | 已通过；K3s `v1.35.6`、containerd `2.2.5-k3s2`、native snapshotter、Node Ready |
| L3 CNI | 已通过；bridge 网段 `10.245.0.0/16`，nginx Pod IP `10.245.0.30` |
| 腾讯云 nginx | 已通过；`nginx-ccr` `1/1 Running`，L2 访问 HTTP 200，nginx `1.29.0` |
| L3 生命周期回收 | 已通过；删除 Pod 后 veth、IPAM 文件和对应 NAT 规则消失 |
| L3 `sysbox-runc` | 2026-08-19 已通过 v11 发布镜像无热替换回归；全新 L3 `nested-l3-v11-smoke` 获得 child userns、UID/GID `0 0 65536`、IP `10.245.0.80`，HTTP、CNI/IPAM/NAT 回收均通过 |
| L1 CNI Service/外网 | 已通过；CoreDNS、ClusterIP、跨 Pod HTTP、外网 HTTPS 以及 CNI DEL/ADD 后重新分配 IP 均正常 |
| ServiceLinks 隔离 | 已通过；L1 Deployment 和 nested agent 均为 `enableServiceLinks:false`，L2 无冲突的 `SYSBOX_*` 环境变量 |
| flat2 DaemonSet 正式滚动 | 已通过；单层镜像滚动无 `EIO`，L1 K3s PID/starttime 不变，mgr/fs/snapshotter 与三个 socket 均 live |
| admission | 已通过；`admission.enabled=true` 安装成功、Webhook Ready，L1 K3s 未重启 |
| nested chart 从零 smoke | 已通过；本地 chart、v11、`installMode=nested`，L2 腾讯云 nginx、child userns、HTTP 和 CNI 回收均通过，L1 K3s PID/starttime 不变 |
| rootfs rw layer 首次初始化与重建持久化 | 已通过；宿主重启后的全新 PVC 自动创建 `upper/work`，marker 在 L2 Pod 重建后保持 |
| L1 rootfs 与 inner K3s data-dir 持久化 | 已通过；L1 Pod 重建后 rootfs、`/var/lib/rancher/k3s`、etcd ConfigMap、inner Node UID 均保持 |
| L2 systemd/Docker | 已通过；systemd `running`，dockerd 使用 systemd cgroup driver、Docker bridge/iptables 和端口映射正常 |
| Docker overlay2 | 已通过；不加 tmpfs 的 `/var/lib/docker` 自动使用 ext4-backed 现有 special mount，dockerd 自动选择 `overlay2` |
| 腾讯云 Docker nginx | 已通过；实际 pull digest `sha256:29cf9892...dd159f`，容器 `172.17.0.2`，`-p 18080:80` 返回 HTTP 200 |
| Docker 与 rootfs rw layer 组合 | 已通过；腾讯云 nginx pull/run、HTTP 200，L2 marker 经 L2 Pod 重建和整个 L1 Pod 重建后均保持 |
| CKM `ckm-k3s-nginx` Deployment rootfs-rw-layer | 2026-09-03 已通过 | `ckm-test` 中使用 `ccr.ccs.tencentyun.com/afan-public/nginx:latest`；空 PVC 初始化、marker/special bind 跨 Pod 重建、owner `0:0`、snapshotter handoff 和 webhook 均通过 |
| 固化 L2 K3s 测试镜像 | 镜像已构建推送；本地 K3s 8 秒 Ready、CRI NetworkReady、腾讯云 nginx、HTTP 200 和 CNI 回收通过；仍需用当前最终 runtime 镜像做一次完整组合回归 |

L3 procfs 原先通过继承的 `mount(2)` seccomp notify 路径，外层 helper 对更深层 userns
返回 `EPERM`。`sysbox-runc` 的 nested procfs helper 优先使用
`fsopen`/`fsmount`/`move_mount`，在 L3 自己的 user/mount/PID namespace 中创建并附加
procfs。218 的 L1 策略会拒绝 L2 的 `fsmount(proc)`，但允许 legacy `mount(2)`；因此 v11
只在新 mount API 返回明确 `EPERM` 时回退到 `mount(2)`，其他错误保持失败。这样 L2
创建可用，同时 L3 仍绕过继承的 `mount(2)` seccomp-notify 路径。该兼容修复不需要重启
L0 宿主或共享 L1 user namespace。

同一 Pod UID 多次重建 sandbox 时，containerd 会短暂保留多个 `sysbox-rootfs` sidecar
记录。snapshotter 现优先选择唯一 Running sidecar task，避免仅按记录数量返回
`sysbox sidecar oci spec ambiguous`。

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

### ckm-bzhrq 复测（2026-08-19）

本轮使用 CKM `ckm-bzhrq`，目的是在另一份 CKM 实例上重复确认 L1、独立 nested
chart 和 L2 网络链路。该轮是分步回归，不能替代已完成的 L3 全链路记录。

| 阶段 | 结果 | 现场问题与处理 |
| --- | --- | --- |
| CKM 初始启动 | 阻塞 | `spec.innerSysbox.enabled` 初始为 `false`；patch 为 `true` 后才创建带 L1 nested runtime 的 Deployment。 |
| 全局 pre-install | 跳过 | `w7panel-ckmv3` ServiceAccount 缺少 `pods/exec` RBAC 权限，执行报 `forbidden`；本轮仅将该 CKM 的历史 execution 标记为 `skip`，没有放宽全局权限或执行未知脚本。 |
| L1 mount 与独立 chart | 通过 | L1 `/var/lib/rancher/k3s` 为 shared mount；在 L1 内以 `installMode=nested` 安装 `w7panel-sysbox`，nested-agent 成功 Ready，节点出现 `sysbox.w7panel.io/nested-runtime=ready`。 |
| L2 nginx | 通过 | L2 使用统一 `runtimeClassName: sysbox-runc`，UID/GID map 均为 `0 0 65536`；腾讯云 nginx 镜像拉取成功，CNI 分配地址、L1 到 L2 HTTP 访问均正常。 |
| 带 `sysbox/rootfs-rw-layer` 的 L2 K3s | 失败 | K3s 启动阶段报 `rsync: ACLs are not supported on this client`，Pod 未 Ready。该错误来自当前测试镜像/rsync ACL 能力，不应记录为 CNI 或 userns 映射失败。 |
| 去掉持久 rootfs 后的 L2 K3s | 通过 | 去掉 `sysbox/rootfs-rw-layer` 后 L2 K3s Node 进入 `Ready`，证明基础 nested runtime 可用。 |
| L2 内独立 nested chart | 阻塞 | Chart 已创建 L2 `RuntimeClass/sysbox-runc` 与 agent DaemonSet。L2 能解析 `docker.cnb.cool` 且有默认路由，但完整 deploy 镜像解包报 `copy_file_range failed: no space left on device`；kubelet 可用 ephemeral storage 低于 1Gi 后进入 `DiskPressure` 并驱逐 agent。L1 已缓存镜像也已尝试导出导入 L2，但磁盘不足时不能完成解包，因此不能归因为 handler 或 CNI。 |
| L1 重建影响 | 已恢复 | L2 内安装 Chart 的 exec 会话期间 L1 被 K3k 重建，旧 websocket 返回 EOF；L1 K3s、L1 Chart 和无 rootfs 的 L2 K3s 均从 L1 持久数据恢复，但测试命令必须每次按 label 重新发现 L1 Pod。 |
| 清理后重建 L2 | 阻塞 | 删除临时 L2 后重建时，L1 containerd 创建 Sysbox sandbox 报 `failed to get reader from content store: content digest ... not found`。这是 snapshot/content-store 内容缺失，继续重试只会重复失败 sandbox；须先修复或重新拉取该 sandbox 所依赖的内容。 |
| L3 | 未完成 | L2 nested-agent 尚未 Ready，不能创建并验收 L3 Sysbox Pod；本轮不能从 `ckm-bzhrq` 单独宣称 L3 通过。 |

本轮新增或确认的问题：

1. CKM 模板必须显式开启 `innerSysbox.enabled`，否则不会部署 L1 nested runtime。
2. `w7panel-ckmv3` 的 pre-install 执行权限缺少 `pods/exec`；临时 skip 只适用于本次
   已知历史 execution，正式部署仍需补齐最小 RBAC 并单独验证脚本范围。
3. `rootfs-rw-layer` 路径触发的 rsync ACL 错误尚未修复；需要在测试镜像提供支持 ACL
   的 rsync，或让该初始化路径在不支持 ACL 的客户端上采用明确的兼容策略。
4. L2 K3s Ready 不代表 L3 可用；必须先在 L2 内独立安装 `w7panel-sysbox`
   (`installMode=nested`)，确认 `sysbox-runc` handler 和 nested-agent Ready 后再做 L3。
5. L2 侧 deploy 镜像拉取长期无进度时，先记录 `ContainerCreating/Pulling`、DNS、路由和
   CRI 事件；本轮最终根因是 L2 节点 ephemeral storage 低于 1Gi，镜像解包失败并被
   `DiskPressure` 驱逐，不是 DNS 或 handler 失败。不能用 L1 的网络通过状态替代 L2
   镜像分发验证。
6. 清理或 GC 后若新 Sysbox sandbox 报 content digest not found，先恢复缺失的 sandbox
   镜像内容或修复 snapshot/content-store 一致性，再创建 L2；反复重新创建 Pod 无效。

### ckm-bzhrq 修复后最终复测（2026-08-19）

前表中的 ACL、磁盘和 content-store 错误属于修复前现场；完成 bootstrap 镜像、持久
rootfs 依赖探测和运行时二进制保留修复后，在同一 CKM `ckm-bzhrq` 重新创建 L2
并完成以下结果：

| 检查项 | 结果 | 现场证据 |
| --- | --- | --- |
| L2 K3s 重建 | 通过 | `nested-l2-k3s-final` 新 Pod `2/2 Running`，Node `nested-rootfs-poc` 为 `Ready`，K3s `v1.35.6+k3s1`。 |
| L2 独立 chart | 通过 | `sysbox-system` admission 与 nested-agent 均 `1/1 Running`；`RuntimeClass/sysbox-runc.handler=sysbox-runc`，节点带 `sysbox.w7panel.io/nested-runtime=ready`。 |
| L2 rootfs-rw-layer 持久化 | 通过 | `/rootfs-persistence-bzhrq` 重建前后 inode `43947400`、size `29`、owner `0:0` 和内容 `nested-bzhrq-rootfs-20260819` 全部一致。 |
| L3 nginx/CNI | 通过 | 使用 `ccr.ccs.tencentyun.com/afan-public/nginx:latest`，L3 UID/GID `0 0 65536`、child userns、Pod IP `10.245.0.7`、L2 HTTP、IPAM/veth/NAT 回收均通过；镜像 digest 为 `sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`。 |

因此当前 CKM 实例的 L0 → L1 → L2 → L3 主链路已通过。历史记录中关于“L3 未完成”
和“带 rootfs 的 L2 K3s 失败”的结论只适用于修复前轮次，不应覆盖本节最终复测结果。

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

`sysbox/rootfs-rw-layer` 在 PVC upper/work 已正确准备后的重建持久化也未受影响。
inner daemon 完整切换后，重建的 Pod 仍读取到：

```text
path=/rootfs-persistence-marker
inode=21379250 mtime=1786691954 size=35 owner=0:0
content=nested-rootfs-marker-20260814-0719
```

### nested Docker 与 rootfs rw layer 组合复测

218 使用 `w7panel-doc/tests/nested-docker-rootfs-persistence.yaml` 创建同名
`nested-docker-rootfs-persistence` Deployment 和 2 Gi `local-path` PVC。Deployment
采用 `Recreate`，业务容器名为 `system`，并设置：

```yaml
sysbox/rootfs-rw-layer: '[{"name":"system","volumeName":"rootfs","path":"system"}]'
```

首次成功运行的 Pod UID 前缀为 `03610...`。L2 的 UID/GID mapping 均为
`0 0 65536`，`systemctl is-system-running` 返回 `running`，
`systemctl is-active docker` 返回 `active`；`docker info` 显示 storage driver 为
`overlay2`、cgroup driver 为 `systemd`，`/var/lib/docker` 的 backing filesystem 为
ext4。随后从腾讯云拉取 nginx，容器地址为 `172.17.0.2`，通过
`-p 18080:80` 暴露后 HTTP 返回 200。

在 `/root/docker-rootfs-marker` 写入标记并记录 inode、mtime、owner 和 content，
再重建 Deployment。新 Pod UID 前缀为 `703fd...`，四项数据全部与旧 Pod 一致，
证明 PVC-backed rootfs writable layer 跨 Pod 重建保持完整。

持久化边界也已实测确认：重建后 `/var/lib/docker` 对应的 Sysbox special backing
路径发生变化，先前拉取的 nginx image 和已创建 container 均不存在。这不是
rootfs-rw-layer 数据丢失；当前 annotation 没有启用
`persistentSpecialMounts:true`，因此只持久化容器 rootfs upper/work，
`/var/lib/docker` 仍是 Pod 生命周期内的独立 ext4 special backing。若产品要求同时
保留 Docker image/container，需另行启用并验收 persistent special mounts，不能把
本轮 rootfs marker 结果外推为 Docker data-root 持久化。

### 全新 PVC 自动初始化热验证

修复后的 `sysbox-snapshotter` 已在 218 以热替换方式验证。新建此前从未使用过的
`nested-docker-rootfs-auto` PVC 和同名 Deployment，未在 sidecar 或 PVC 中手工创建
任何目录，Pod 直接达到 `2/2 Running`。首次 Pod UID 前缀为 `53ceb100...`，业务
容器的 UID/GID mapping 均为 `0 0 65536`，Docker 为 `overlay2` storage driver 和
`systemd` cgroup driver。

写入 marker 后重建 Deployment，新 Pod UID 前缀为 `6408bb2a...`，复核结果为：

```text
inode=20636721
mtime=1786698388
size=19
owner=0:0
content=<与重建前一致>
```

inode、mtime、size、owner 和 content 全部保持，且新 Pod 仍为 `2/2 Running`、
`overlay2/systemd`、UID/GID mapping `0 0 65536`。这证明
`LocalPreparer` 的 `/proc/1/root` prepare 路径修复消除了首次新 PVC 必须由 sidecar
手工创建 `upper`/`work` 的代码缺口；它不改变返回给 runc 的原始 kubelet mount
option，也不依赖 Docker special backing。

### flat2 正式滚动与从零 smoke

最终镜像 `v0.7.1-9-nested-rootfs-flat2` 为单层镜像。nested-agent DaemonSet 已正式
滚动成功，期间没有再出现 path1 轮次的 inner containerd overlay lower-chain
`EIO`；滚动前后 L1 K3s 的 PID/starttime 保持不变。新 Pod 内 mgr、fs、snapshotter
进程以及 sysmgr、sysfs、snapshotter 三个 socket 均 live，运行中的 snapshotter
SHA-256 为 `5cfe...bce0`。

flat2 下普通 rootfs PVC Pod 再次完成重建，marker 保持为：

```text
inode=21379250 mtime=1786691954 size=35 owner=0:0
content=<与重建前一致>
```

Docker 与 rootfs-rw-layer 组合也确认 systemd `running`、dockerd `active`、
`overlay2/systemd`、`/var/lib/docker` ext4 和旧 marker 正常。第一次腾讯云 nginx pull
曾被 L0 外部正常 reboot 中断；恢复后已经用全新 PVC 重跑 pull/run、Docker bridge、
HTTP 和 Pod 重建，不再是待验证项。

宿主恢复后又从零使用本地 chart、`installMode=nested`、flat2 和
`admission.enabled=true` 完成 smoke。安装前后 L1 K3s `PID:starttime=1:12` 保持，
证明 chart 未重启 K3s；admission/Webhook Ready。L2 从腾讯云拉取 nginx，digest
为 `sha256:29cf...159f`，`uid_map` 为 `0 0 65536`，user namespace 是 L1 的 child，
Pod IP 为 `10.244.0.6`，HTTP 与删除后的 CNI/IPAM/NAT 回收均通过。

### 宿主重启、全新 PVC 与 L1 持久化回归

本次宿主事件是 systemd 正常 reboot，不是 Sysbox、Docker 或 chart 触发的异常重启。
重启并清理后宿主根盘使用量从约 852 GiB 降到 217 GiB（25%），释放约 635 GiB。
此前 runaway 的根因是 `ckm-old4` CrashLoop：每个新 container 都触发约 16.7 GiB
rsync，多个实例并发吞吐约 70 MiB/s。`ckm-old4` 已 pause，相关 BuildImage CR、Job
和 CRD 已删除。

旧 PoC L1 的 `/var/lib/rancher/k3s` 没有持久化，所以宿主重启后 inner Kubernetes
成为新集群；这属于旧 PoC data-dir 的生命周期边界，不是 PVC marker 丢失。恢复后
已在新 inner 集群用全新 PVC 完成正式回归：snapshotter 自动创建 `upper/work`，L2
UID/GID mapping 均为 `0 0 65536`，marker 在 L2 Pod 重建后保持：

```text
inode=43005061 mtime=1786702799 size=28 owner=0:0
content=flat2-fresh-rootfs-20260814
```

随后使用 `w7panel-doc/tests/l1-rootfs-persistence.yaml` 给 L1 Pod 本身增加 5 Gi PVC，
并设置：

```yaml
metadata:
  annotations:
    sysbox/rootfs-rw-layer: >-
      [{"name":"k3s","volumeName":"rootfs","path":"rootfs",
        "persistentSpecialMounts":true}]
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  enableServiceLinks: false
```

这里 annotation 配置在 **L1**，因此既保存 L1 rootfs upper/work，也把
`/var/lib/rancher/k3s` 映射到 PVC 的 `rootfs/special/var/lib/rancher/k3s`。L1 `/`
仍是 FUSE rootfs，该 K3s data-dir backing 为 ext4。L1 Pod 重建后以下证据全部保持：

```text
L1 rootfs marker: inode=43814136 mtime=1786703401
  content=l1-rootfs-flat2-20260814
L1 K3s data marker: inode=43814137 mtime=1786703401
  content=l1-k3s-data-flat2-20260814
inner Node UID: e5418a17-9cc8-46fa-b98a-aef9bc51eb6a
ConfigMap: l1-persistence-proof=l1-k3s-etcd-flat2-20260814
```

Pod IP 会随 L1 重建变化，持久化 etcd 的 peer URL 仍可能指向旧 IP。单节点恢复必须在
正式启动前执行一次 `k3s server --cluster-reset`、清除 `reset-flag`，并用 Downward
API 的 `status.podIP` 设置当前 `--node-ip`；不能通过删除 etcd 来规避。该逻辑加入测试
YAML 后，L1 重建约 28 秒恢复 API，etcd 数据和 Node UID 均未丢失。

在这个持久化 L1 中又创建了 L2 `nested-docker-rootfs-persistence`。L2 的 systemd、
dockerd、`overlay2`、systemd cgroup driver、ext4 `/var/lib/docker`、腾讯云 nginx、
`172.17.0.2` 和 HTTP 200 均通过。L2 marker：

```text
inode=43936263 mtime=1786704066 owner=0:0
content=l2-docker-rootfs-flat2-20260814
```

该 marker 先经过 L2 Pod 重建，再经过整个 L1 Pod 重建，仍保持不变。L2 annotation
没有启用 `persistentSpecialMounts`，因此 Docker image/container 在 L2 Pod 重建后不
持久是当前预期边界；这不影响 L2 rootfs marker 的持久化结论。L1 恢复后再次运行
chart smoke，真实 K3s PID/starttime 为 `388:3811` 且前后不变，L2 nginx HTTP 与
CNI/IPAM/NAT 删除回收再次通过。

相关单元测试已通过：

```sh
cd sysbox-runc
go test ./libsysbox/syscont ./libcontainer/specconv
go test -vet=off ./libcontainer

cd ../sysbox-mgr
go test ./...

cd ../sysbox-snapshotter
go test ./rootfs
```

`sysbox-mgr` 的既有 shiftfs 用例若因缺少 `/mnt/scratch` 失败，不属于本次 nested
改动。`sysbox-fs` 当前全量测试通过：

```sh
cd sysbox-fs
go test ./...
```

## 历史未解决或待验证快照

> 本节已经冻结，不代表当前待办。当前状态只看 [KNOWN-ISSUES.md](./KNOWN-ISSUES.md)。

### 1. 最终 runtime 与固化 L2 K3s 镜像的组合回归

测试镜像资产已经固化到
`docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:v1.35.6-20260814-4`，不再需要
现场复制。镜像在本地 privileged Docker 中已验证 K3s `v1.35.6` 8 秒 Ready、
containerd `2.2.5-k3s2`、CRI NetworkReady、bridge CNI、`ipMasq:true`、腾讯云
nginx HTTP 200，以及删除后的 veth/IPAM/NAT 回收。该验证证明镜像资产和内置配置
完整；此前也已在 218 分步通过 L2 K3s、CoreDNS、L3 CNI 和 nginx。当前代码已通过
热替换完成 L2 重建、L3 `sysbox-runc`、CNI、HTTP 和回收组合回归；
本次 runc procfs fallback 与 snapshotter Running-sidecar 选择修复已经构建进 v11 发布
镜像，并完成 L1/L2 nested-agent 无热替换与全新 L2/L3 Pod 回归。

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

### 4. mgr 大目录 rsync 生命周期控制

`ckm-old4` CrashLoop 已证明每个新 container 都可能触发约 16.7 GiB rsync，并在重启
风暴中形成约 70 MiB/s 的并发写入。当前仅通过 pause 工作负载和删除相关
BuildImage 资源停止 runaway；mgr 对同一目标的 rsync single-flight、container
取消时的中止，以及 orphan 临时目录/进程回收尚未实现或完成压力验证。

## 历史快速回归命令（已停用）

> 下列命令依赖已经删除的旧 L2/L3 流程，仅用于理解历史现场，禁止作为当前验收步骤。

### 1. 创建可持久化的 L1 并运行 chart smoke

先确认 namespace 中已有 `sysbox-nested-registries` ConfigMap，再创建带 L1 rootfs、
persistent special mounts 和持久化 K3s data-dir 的 Deployment：

```sh
cd /root/workspace/sysbox
kubectl --kubeconfig /root/.kube/218.config apply \
  -f w7panel-doc/tests/l1-rootfs-persistence.yaml

KUBECONFIG_218=/root/.kube/218.config \
NAMESPACE=k3k-console-164315 \
DEPLOYMENT=sysbox-inner-k3s-rootfs-poc \
IMAGE_TAG=v0.7.1-11-nested-proc-fallback \
ADMISSION_ENABLED=true \
bash w7panel-doc/tests/nested-chart-smoke.sh
```

测试 YAML 使用专用 copy helper，而不是把含 `SYSBOX_*` 环境变量的 deploy 镜像直接
作为 Sysbox init container；runtime 会按约束拒绝后者。K3s 由 shell 包装且 PID 不
固定为 1，smoke 会从 `/proc` 定位真实 `/bin/k3s server` PID/starttime，并在启用
admission 时等待 admission Deployment endpoint Ready 后再创建 L2 工作负载。

### 2. 运行现有 L2 pause 回归脚本

该脚本会发现 Running 的 L1 Pod，等待 inner Node 和 default ServiceAccount，创建
`sysbox-runc` RuntimeClass 及 pause Pod，并检查 runtime、uid map 和 procfs：

```sh
cd /root/workspace/sysbox
KUBECONFIG_218=/root/.kube/218.config \
NAMESPACE=k3k-console-164315 \
DEPLOYMENT=sysbox-inner-k3s-rootfs-poc \
bash w7panel-doc/tests/sysbox-in-sysbox-218.sh
```

脚本已使用 `awk` 对 `/proc/self/uid_map` 做数值比较，不受列对齐空格影响。也可用
下面的命令独立复核：

```sh
KUBECONFIG_218=/root/.kube/218.config
NS=k3k-console-164315
DEPLOY=sysbox-inner-k3s-rootfs-poc
L1_POD="$(kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" get pod \
  -l "app=$DEPLOY" --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')"

kubectl --kubeconfig "$KUBECONFIG_218" -n "$NS" exec "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default exec \
  nested-sysbox-hostusers -- cat /proc/self/uid_map | \
  awk 'NF == 3 && $1 == 0 && $2 == 0 && $3 == 65536 { ok=1 }
       END { exit !ok }'
```


### 3. 核对 L1 与 L2 namespace

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

### 4. 快速 bridge/veth probe

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

### 5. 快速验证 L2 出口

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

### 6. 快速验证 Docker overlay2

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
  runtimeClassName: sysbox-runc
  enableServiceLinks: false
  containers:
    - name: nginx
      image: ccr.ccs.tencentyun.com/afan-public/nginx:latest
      imagePullPolicy: IfNotPresent
```

复测必须同时记录：

1. L2 K3s node `Ready`；
2. nginx Pod `Running` 且获得 `10.245.x.x`，不是 `hostNetwork`；
3. Pod 的 UID/GID map 均为 `0 0 65536`，`/proc` 为可用 procfs；
4. L2 出现对应 `cni3`/veth；
5. L2 访问 nginx Pod HTTP 返回 200；
6. 镜像确由指定腾讯云地址拉取并记录 image ID；
7. DNS 是独立待测项；启用 CoreDNS 后才可记录为通过；
8. 删除 Pod 后对应 veth、IPAM 文件和 NAT 规则消失；`cni3` 保留属正常。

218 上必须通过 L0 → L1 K3s → L2 Pod 两层 exec 进入 L2 K3s。直接运行外层
`kubectl apply` 会把同名测试 Pod 错建到 L0 集群，不能作为 L3 结果：

```sh
kubectl -n k3k-console-164315 exec "$L1_POD" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default exec \
  nested-l2-k3s-final -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n default \
  get pod nested-l3-sysbox-nginx -o wide
```

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

脚本默认使用 v11，并要求 `admission.enabled=true`；先等待 admission Deployment 和
Webhook Ready，再创建需要 admission 的工作负载。脚本会记录 Chart 安装前后 L1
K3s PID/starttime，断言没有重启，等待 nested agent Ready，然后用腾讯云 nginx
验证 L2 child userns、
`0 0 65536` 映射、CNI、HTTP 和删除后的 IPAM/NAT 回收：

```sh
cd /root/workspace/sysbox
KUBECONFIG_218=/root/.kube/218.config \
NAMESPACE=k3k-console-164315 \
DEPLOYMENT=sysbox-inner-k3s-rootfs-poc \
IMAGE_TAG=v0.7.1-11-nested-proc-fallback \
ADMISSION_ENABLED=true \
bash w7panel-doc/tests/nested-chart-smoke.sh
```

该命令已在重启后的新 inner 集群以及持久化 L1 重建后执行并 PASS。当前 v11 无热替换
回归中 L1 K3s `PID:starttime=164:1312` 前后保持，Webhook Ready，L2 nginx、child userns、
`0 0 65536` 映射、HTTP 和 CNI cleanup 均通过。

脚本不会执行 `rollout restart`、`systemctl` 或重启 K3s。首次迁移旧 L1 时若
containerd 尚未加载新 handler，脚本会保存 agent 状态并失败退出，由 L0 管理员决定
是否受控滚动 L1 Pod。

### L3 一键 smoke

`nested-l3-smoke.sh` 固定通过 L0 -> L1 K3s -> L2 K3s 的两层 exec 创建一个新的
L3 Sysbox nginx。它验证 `sysbox-runc` handler、L3 child user namespace、UID/GID
`0 0 65536`、L2 到 L3 HTTP、IPAM/veth 状态以及删除后的 IPAM/NAT 回收。脚本不修改
L1/L2 Pod 或 PVC，也不会重启任何 K3s：

```sh
cd /root/workspace/sysbox
KUBECONFIG_218=/root/.kube/218.config \
NAMESPACE=k3k-console-164315 \
DEPLOYMENT=sysbox-inner-k3s-rootfs-poc \
L2_POD=nested-l2-k3s-final \
bash w7panel-doc/tests/nested-l3-smoke.sh
```

2026-08-19 v11 的实际结果：L3 `nested-l3-v11-smoke` 使用 child user namespace
`user:[4026545482]`，IP `10.245.0.80`，腾讯云 nginx digest
`sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`，HTTP 和
CNI 回收全部通过。不要将 L0 的普通 `kubectl` 输出作为该结论的证据。

## 部署与安全注意事项

- 当前 218 的 L1 与 L2 nested-agent DaemonSet 均已使用
  `docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-11-nested-proc-fallback`
  （digest
  `sha256:93192f52ce7c45cf455b67424c757a9f875ae4f235af07a0dfc723f53f69a472`）。
  launcher 正常运行，二进制来自镜像复制，不再存在 test-only `SIGSTOP` 或手工 hotfix 状态。
- path1 的热替换、`SIGSTOP`、旧 DaemonSet tag 和 lower-chain `EIO` 只属于历史排障
  现场；flat2 正式滚动已无 `EIO`，不得再把这些状态描述为当前状态。
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

## ckm-bzhrq 当前复测（2026-08-20）

本轮证据来自 218 的真实 CKM，外层 namespace 为 `k3k-console-164315`。CKM
controller 在测试期间重建过 L1，因此旧 Pod
`k3k-ckm-bzhrq-server-5dc666d59-cn5x9` 不能继续作为证据；当前 L1 必须按
`cluster=ckm-bzhrq,role=server` 重新发现（本轮最终为
`k3k-ckm-bzhrq-server-5dc666d59-r24g6`）。

### 已确认

| 检查项 | 结果 | 证据 |
| --- | --- | --- |
| 静态 runc 注入 | 通过 | `/opt/sysbox/bin/generic/sysbox-runc` 为静态 ELF，无 `INTERP`；digest `6feb5160f57b895efd003f6f70301a5030593908f4eb0a27f99690c2754d2a95`。 |
| L1 daemon 恢复 | 通过 | stale `/run/sysbox/sysmgr.pid` 清理后，inner mgr/fs/snapshotter socket 和 Ready 均恢复。 |
| L2 rootfs-rw-layer | 通过 | `nested-l2-k3s-final` 重建后仍为 `2/2 Running`；`/rootfs-persistence-bzhrq` 内容保持，inode `576074`、owner `0:0`。 |
| L2 nested chart | 通过 | 真正 L2 K3s 内 `RuntimeClass/sysbox-runc.handler=sysbox-runc`，admission 与 nested-agent `1/1 Running`，节点 `Ready` 且带 `sysbox.w7panel.io/nested-runtime=ready`。nested-agent 最终使用 v23 digest `sha256:387f5ae2b347d021fc04a5eb9ea01828460d7e7b9bbf17a8fffd787ae2498206`。 |
| L2 containerd handler | 通过 | `sysbox-runc` 使用 snapshotter `sysbox`，`BinaryName=/var/lib/sysbox-inner/bin/sysbox-runc-nested`。 |
| L3 腾讯云 nginx | 通过 | 通过完整 L0 -> L1 -> L2 API 路径检查 `nested-l3-bzhrq-real-nginx`：`1/1 Running`、IP `10.245.3.91`，`uid_map/gid_map` 均为 `0 0 65536`，L2 userns `4026549034` 与 L3 userns `4026547267` 不同；镜像 digest `sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`。 |
| L3 HTTP/CNI | 通过 | L2 可访问 `10.245.3.91` 的 nginx HTTP。v23 自动脚本 `nested-l3-v23-pass` 获得 `10.245.3.95` 并输出 PASS；删除后 `/var/lib/cni/networks` IPAM 文件和对应 iptables 状态均回收。 |

### 本轮已解决问题

1. 纠正了早期少一层 `exec nested-l2-k3s-final -c k3s` 的测试路径。此前记录的
   `10.52.x.x` 是 L1 K3s 中的 L2 Pod 地址，不能作为 L3 证据；真正 L3 CNI 地址为
   `10.245.x.x`。`nested-l3-smoke.sh` 现在默认优先选择
   `cluster=ckm-bzhrq,role=server`，避免同时存在的旧 PoC Deployment 被误选。
2. nested-agent 的私有 mount namespace 原先看到空的 `/run/sysbox` 或外层
   `/run/k3s`，L2 containerd 因此报
   `dial unix /run/sysbox/sysbox-snapshotter.sock: timeout`。v23 通过
   `nsenter -m -t 1` 在 K3s PID 1 的 mount namespace 启动、检查 mgr、fs 和
   snapshotter，并在相同 namespace 查询 containerd handler。
3. 极简 K3s rootfs 缺少 `modprobe` 和 `iptables`。launcher 现在把 image 中的
   `kmod`、`xtables-multi` 安装到共享 `/var/lib/sysbox-inner/bin`，并在 L2 rootfs
   暴露标准命令入口。
4. 修复了 mount-namespace socket 检查的 shell 参数错位，以及 snapshotter 启动分支
   仍在 agent 自身 namespace 检查 containerd socket 的问题。旧的
   `nested-l3-real` 和 `nested-l3-bzhrq-real-nginx` 在更新真正的 L2 agent 后无需重建
   即从 `ContainerCreating` 自动恢复到 `Running`。

### 当前未完成或需固化

1. 当前固化的 `sysbox-nested-k3s-test:v1.35.6-20260814-4` 镜像没有
   `systemctl`/`docker` 客户端，因此本轮只能证明 K3s、containerd、nested chart 和
   cgroup v2 delegation；L2 systemd、dockerd、overlay2 组合需要使用带这些工具的
   测试镜像重新验收，不能沿用极简镜像结果。
2. 该镜像的 entrypoint 把 CNI 清单写到 `/etc/cni/net.d`，而 containerd 模板读取
   `/var/lib/rancher/k3s/agent/etc/cni/net.d`。本轮通过持久 rootfs 补齐目标文件恢复
   `Node Ready`；源码 `nested-k3s-entrypoint.sh` 已改为同时写两个目录，仍需重建并
   推送最终测试镜像后再做一次干净重启验证。

### 快速复测

```bash
K=/root/.kube/218.config
NS=k3k-console-164315
L1="$(kubectl --kubeconfig "$K" -n "$NS" get pod \
  -l cluster=ckm-bzhrq,role=server --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')"

# L2 状态、RuntimeClass、rootfs marker
kubectl --kubeconfig "$K" -n "$NS" exec "$L1" -c k3k-ckm-bzhrq-server -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml \
  get pod nested-l2-k3s-final -o wide
kubectl --kubeconfig "$K" -n "$NS" exec "$L1" -c k3k-ckm-bzhrq-server -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml \
  exec nested-l2-k3s-final -c k3s -- sh -ec \
  'cat /rootfs-persistence-bzhrq; cat /proc/self/uid_map; \
   /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get runtimeclass sysbox-runc'

# L3 nginx、child userns、HTTP 与 CNI 回收
cd /root/workspace/sysbox
KUBECONFIG_218="$K" NAMESPACE="$NS" L1_POD="$L1" \
  L2_POD=nested-l2-k3s-final TEST_POD=nested-l3-bzhrq-final \
  bash w7panel-doc/tests/nested-l3-smoke.sh
```

## 2026-08-21 nested chart 回归（218 / command PoC）

本轮使用 `sysbox-inner-k3s-command-poc-8b49c9fcf-kr9fc` 作为 L1，外层 namespace
为 `k3k-console-164315`。测试镜像和 chart agent 已推送：

| 组件 | 镜像/摘要 |
| --- | --- |
| L1/L2 nested-agent | `docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-43-handler-compat`，digest `sha256:3d81d504e0646d4b9f8c761f59a0f2d568c87430166cc3e1b4109013010cd67a` |
| L2 K3s entrypoint | `docker.cnb.cool/i0358/zpk/sysbox-nested-k3s-test:v1.35.6-20260820-9`，digest `sha256:15de5c99e815cd96844e10e322ddbc9e69c5f38d23e590015700d66e06a2fe03` |

### 已完成

1. L1 内独立安装 `w7panel-sysbox`（`installMode=nested`）通过
   `nested-chart-smoke.sh`：L1 K3s PID/starttime `269:455` 在 apply、agent rollout
   和 L2 nginx 测试前后不变；nested-agent `1/1 Running`。
2. L1 中 L2 腾讯云 nginx 使用
   `ccr.ccs.tencentyun.com/afan-public/nginx:latest`，镜像 digest
   `sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`，
   `uid_map=0:0:65536`、独立 userns、HTTP 和 CNI/IPAM 回收均通过。
3. 在保留 PVC `nested-l2-k3s-final-rootfs` 上重建 L2 Pod 后，L2 内再次安装同一
   chart；受控重建一次让 containerd 加载 handler，最终
   `BinaryName=/var/lib/sysbox-inner/bin/sysbox-runc-nested`，节点带
   `sysbox.w7panel.io/nested-runtime=ready`，L2 nested-agent `1/1 Running`。
4. L2 内 L3 nginx 回归连续通过（示例 IP `10.245.0.7`/`10.245.0.8`）：
   `uid_map/gid_map=0:0:65536`、L2/L3 userns 不同、HTTP 成功，删除后 CNI IPAM 和
   iptables 状态清理。
5. L2 rootfs-rw-layer marker `/rootfs-persistence-marker` 重建前后保持：
   inode `43030632`、size `20`、owner `0:0`、内容 `nested-l2-v9-marker`。

### 本轮修复并验证

- `nsenter -p` 后不能用外层 `$!` PID 做 L2 `kill -0`；改为 socket/RPC 和目标
  namespace 进程检查，snapshotter 不再把 L1 nsenter PID 写入 L2 pidfile。
- 旧 daemon 的 `/proc/*/exe` 可能显示 `(deleted)`；清理和健康检查现在会去掉该后缀，
  可回收 stale mgr/fs/snapshotter。
- K3s `/bin/aux/modprobe` 指向 `/bin/busybox`；hotfix 镜像补齐 busybox，mgr preflight
  不再报 `modprobe is not installed`。
- L2 entrypoint 在 bind 前创建 `/run/k3s`、`/run/sysbox`，并将
  `/var/lib/rancher/k3s` 及 socket 子目录设为 shared，nested-agent 的
  `path ... is not a shared mount` 和 entrypoint mount 失败已解决。
- 旧 CKM 配置可能仍使用 `sysbox-runc-inner` wrapper；agent 兼容识别该 wrapper，仍要求
  显式 `nested-identity`，Pod RuntimeClass 名保持 `sysbox-runc`。

### 尚未完成

- Admission、L2 systemd、dockerd/overlay2、真实 CKM v43 smoke 已在本轮补测通过；
  L1 资源上限向 L2 的实际 CPU/内存边界压力验证已在后续压力测试中通过（详见下节）。
- L2 中再部署 CKM 业务工作负载尚未验收；当前只验证了 L2 K3s、Docker、L3 nginx、
  CNI 和 rootfs marker。
- 代码、子模块和文档已提交并推送；后续增量修改仍需执行 `helm lint/template`、Go
  测试和 `git diff --check`，并确认不删除 `.gitmodules.swp`。

## 2026-08-21 真实 ckm-bzhrq 与 Docker 回归补充

在 CKM 重建后的 L1 Pod
`k3k-ckm-bzhrq-server-7788d8fbf6-vlj48`（容器
`k3k-ckm-bzhrq-server`）上使用 v43 chart agent 完成了真实 CKM 回归：

- `nested-chart-smoke.sh` 使用 `L1_POD/L1_CONTAINER` 显式指定 CKM L1，
  `admission.enabled=true`；Admission、nested-agent、RuntimeClass 全部 Ready。
- L1 K3s identity `345:2287` 在 apply、rollout 和 L2 nginx 测试前后保持不变。
- L2 nginx 使用腾讯云镜像，IP `10.52.0.18`，`uid_map=0:0:65536`，HTTP/CNI 清理通过。
- L2 systemd/Docker 回归通过：PID1 为 `systemd`，`systemctl is-active docker=active`，
  Docker `overlay2`、`CgroupDriver=systemd`，`/var/lib/docker` 为 ext4 idmapped mount。
  Docker bridge 端口映射 `18080:80`、腾讯云 nginx HTTP 和容器删除清理均通过。
- L2 cgroup v2 可创建自己的子 cgroup，`cpu.max` 与 `memory.max` 可写，controllers
  包含 `cpuset cpu io memory ...`。但带 Pod `limits.cpu=500m,memory=512Mi` 的测试中，
  L2 视图的父级 `cpu.max/memory.max` 仍显示 `max`；因此“L1 上限对 L2 生效且不可绕过”
  尚未证明，当前标记为未完成，不能宣称资源边界验收通过。

`nested-chart-smoke.sh` 已支持：

```sh
L1_POD=k3k-ckm-bzhrq-server-7788d8fbf6-vlj48 \
L1_CONTAINER=k3k-ckm-bzhrq-server \
KUBECONFIG_218=/root/.kube/218.config \
NAMESPACE=k3k-console-164315 \
IMAGE_TAG=v0.7.1-43-handler-compat ADMISSION_ENABLED=true \
bash w7panel-doc/tests/nested-chart-smoke.sh
```

## 2026-08-21 cgroup 边界压力验证与 CKM 重建注意事项

在最新 CKM L1 `k3k-ckm-bzhrq-server-6d9ffc878f-gqsmr`（随后控制器重建为
`6d9ffc878f-4kc6z`）中重新创建带有 `limits.cpu=500m,memory=512Mi` 的 L2 Pod，
并从 L1 的实际 cgroup 树和压力结果交叉验证：

| 检查 | 结果 |
| --- | --- |
| L2 可创建自己的子 cgroup、写入 `cpu.max/memory.max` | 通过 |
| L1 Pod cgroup 的实际上限 | `cpu.max=50000 100000`、`memory.max=536870912` |
| 内存压力 | Python 分配 700MiB 的 L2 Pod 被 `OOMKilled`（512MiB 父级上限生效） |
| CPU 压力 | 500m busy-loop 后父 cgroup `nr_throttled=141`、`throttled_usec≈6.6s`，子 cgroup 同样出现 throttling |
| L2 `/sys/fs/cgroup` 直接视图 | 仍显示 `cpu.max=max 100000`、`memory.max=max`；这是 Sysbox 的隔离视图，不代表父级限制可绕过 |

因此 cgroup delegation 的实际资源边界已通过压力测试；后续若要检查可见视图，必须
同时读取 L1/宿主侧 Pod cgroup，不能只依据 L2 的 `max`。

本轮还发现 CKM 模板的一个重建契约：L1 启动脚本必须在 K3s 启动前建立
`/run/k3s -> /var/lib/rancher/k3s/agent/k3s-run` 和
`/run/sysbox -> /var/lib/rancher/k3s/agent/sysbox-run` 的 shared bind mount，
否则 CKM Pod 重建后 nested-agent 会找不到 containerd/snapshotter socket，节点
`sysbox.w7panel.io/nested-runtime=ready` 会抖动，L2 Pod 会长期 Pending。修复已写入
`w7panel-ckm/pkg/resources/k3s_deployment.go` 及两份 kodata 模板；部署模板更新后应
做一次受控 CKM Pod 重建，确认两个 socket 路径和 nested-agent Ready。

重建过程中若连续删除多个 Sysbox Pod，218 宿主可能暂时出现
`sysbox sidecar oci spec unavailable` 或 `sysbox-fs ... seccomp-notif-ack ... EOF`。
这是宿主 Sysbox daemon/旧容器状态的恢复窗口，不是 cgroup 测试结论；应先确认
`sysbox-mgr`、`sysbox-fs`、`sysbox-snapshotter` 均 active，再重试单个 CKM Pod，避免
并发滚动造成更多 OCI 残留。

### CKM 恢复后复测（Pod `k3k-ckm-bzhrq-server-7788d8fbf6-lj89t`）

- v43 `nested-chart-smoke.sh` 通过；L1 K3s identity `346:2407` 未变化。
- L2 腾讯云 nginx、`uid_map=0:0:65536`、HTTP、CNI/IPAM 清理通过。
- `nested-l2-k3s-final` 为 `2/2 Running`，rootfs marker inode `576074`、owner `0:0`、内容 `bzhrq-rootfs-verified` 保持。
- L3 腾讯云 nginx 两次通过，示例 IP `10.245.0.7` 和 `10.245.0.15`，独立 userns、HTTP、CNI 清理通过。
- 期间遇到一次 L2 admission webhook 旧 endpoint 缓存（`10.245.0.4:9443`），重建 admission Pod 后恢复；nested-agent 也出现 daemon health check 重启，单 Pod rollout 后恢复 Ready。
