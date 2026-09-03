# Sysbox-in-Sysbox 已知问题与能力边界

本文记录当前限制、待验证事项以及已经定位的问题根因。执行步骤以
[README.md](./README.md) 为准；旧 CKM、旧镜像和 L3 实验过程见
[HISTORY.md](./HISTORY.md)。

## 当前实施范围（2026-09-02）

步骤 3（L2 workload 的 `hostUsers:false`）暂不实施，也不作为本轮验收门槛。L1 CKM
Server 仍必须保持 `runtimeClassName=sysbox-runc` 与 `hostUsers:false`；L2 nginx 回归
不设置该字段。当前验收覆盖 rootfs 持久化、空目录初始化复制、特殊目录 bind 挂载，
以及 snapshotter/webhook 复用。明确放弃 proc 强隔离、视图隔离和 system workload；
实现基于官方 runc/libcontainer 局部修改，不引入 L2 `sysbox-fs` 或 `sysbox-mgr`。

### 2026-09-03 最新现场复测补充

当前现场 `ckm-test` 的 nginx `runc-lite` 容器直接读取到：

```text
uid_map: 0 3004104704 65536
gid_map: 0 3004104704 65536
```

这次 L2 workload 未设置 `hostUsers:false`，因此该结果是普通 nested userns 的
实际映射，不应与旧文档中 `0 0 65536` 的专用 inner-runtime 历史结果混用。当前
验收仍只覆盖 nginx rootfs/snapshotter 路径；L2 `hostUsers:false` 测试继续暂缓。

## 当前轻量 `runc-lite` 回归（2026-09-02，进行中）

> 本节是当前权威状态，优先于下方 2026-08-24 的旧 `sysbox-runc`/inner
> `sysbox-fs` 验收基线。后者保留为历史证据，**不能**用来宣称下面的轻量路径已经
> 通过 Docker、systemd、`/proc` 隔离或视图隔离。

本轮目标是仅保留官方 runc/libcontainer 的小幅改动、`sysbox-snapshotter` 和
admission/CKM 注入；内层不再启动 `sysbox-fs` 或 `sysbox-mgr`。验收 workload
缩小为 nginx，不测试 Docker 或 systemd。外层 CKM 仍是 Sysbox workload，因而
`hostUsers: false` 仍由 CKM controller 设置；本轮 L2 nginx manifest 没有设置
`hostUsers`。

当前对象：外层 218 集群（`/root/.kube/218.config`），CKM 为
`k3k-console-164315/ckm-6ur35`，L1 server Pod 为该 CKM 当前的
`k3k-ckm-6ur35-server-*`。部署时使用的镜像和 controller 为：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-51-centos9-runc-lite-fuse
sha256:9a4764e60c80282fa0804ee7545b3852030c612a018619d675ffd7df237f2ff8

docker.cnb.cool/i0358/zpk/w7panel-ckm:v0.7.1-runc-lite-fuse
sha256:837be1e5ee638ea111f5530c204b82e680ac821b82d48e5e5993d95a2032f917
```

CKM controller 现会为 `sysbox-runc` L1 Pod 显式注入 `/dev/fuse` 的
`hostPath`（`CharDevice`）及对应 mount；L1 中已实际确认它为字符设备 `10,229`。
这不能靠 outer admission 假设完成：controller 创建的 L1 Pod 在现场没有经过预期的
admission mutation，故注入必须在 controller template 内完成。

### 已通过的窄范围证据

- `runc-lite` 使用静态二进制构建；修改后的官方 libcontainer 单测已通过：

  ```sh
  cd /root/workspace/sysbox/runc-lite
  GOCACHE=/tmp/runc-lite-final-cache go test . ./libcontainer/specconv
  ```

- 内层 K3s 中一个不使用 rootfs PVC/snapshotter 的 direct workload
  `runc-lite-static-test` 曾到达 `Running`，日志为 `runc-lite-fuse-ok`，其
  `runtimeClassName` 为 `runc-lite`，`hostUsers` 字段为空。这证明当前静态
  runc-lite handler 可启动普通 nginx 类容器，**不证明** FUSE rootfs、初始化复制或
  special bind mount。

- nginx 测试脚本为 `05-test-ckm-k3s.sh`。它创建 `ckm-k3s-nginx` 和 rootfs PVC，
  设置 `sysbox/rootfs-rw-layer`（`persistentSpecialMounts: true`、
  `specialPath: ["/srv/data"]`），随后在 Pod 重建前后检查 marker 与 `/srv/data`：

  ```sh
  cd /root/workspace/sysbox
  bash w7panel-doc/sysbox-in-sysbox/05-test-ckm-k3s.sh
  ```

  该脚本曾确认 `/usr/share/nginx/html/.runc-lite-marker` 跨 Pod 重建保持为
  `persisted`。在目前失败轮次中，Pod 未能完成启动，故这项旧 marker 观察不能作为
  当前完整回归的成功结论。

### 当前 FUSE-rootfs 阻塞链（按实际出现顺序）

以下错误均发生在 `runc-lite` + sysbox snapshotter 的 nginx PVC 路径；每次修正前
一步后才暴露下一步，不能把后一个错误误写成 direct-runc 基础能力失败。

1. **root pivot bind mount 被拒绝。** FUSE rootfs 首次启动报
   `mount ., flags 0x84000: permission denied (EPERM)`。这是 runc pivot/rootfs
   bind 的权限兼容问题，不是 nginx image、PVC 或 userns 配置错误。已在
   runc-lite 的 pivot/bind 路径加入兼容处理；该精确错误不再是当前最前置失败。
2. **proc fd 在 child 侧不可用。** 上一步后报
   `error closing exec fds: open /proc/self/fd: no such file or directory`。
   该问题对应 runc init 子进程建立 rootfs 后再经 `/proc/self/fd` 关闭 exec fd 的
   时机；已继续调整相关路径，但它说明 FUSE rootfs 上不能假定 child 的 procfd
   入口已经可用。
3. **当前前置失败：父进程预绑定 procfd 为 ENOENT。** 为避开 child procfd，改为在
   父进程预打开/预绑定 fd 后，当前错误变为预绑定目标/源不存在（`ENOENT`）。因此
   nginx 尚未 `Running`，不得声明 rootfs 持久化、空目录初始化复制或 `/srv/data`
   special bind mount 已全部验收。

`persistentSpecialMounts` 也尚未取得 snapshotter handoff 的独立证据。runc-lite
读取 `/run/sysbox/rootfs-pvc-handoff/<sha256(container-id)>.json`；失败轮次中该目录
为空。已为 `runc-lite` handler 尝试配置 `snapshotter = "sysbox"`、proxy plugin 和
`pod_annotations`，并重启内层 containerd 让配置生效；后续回归必须同时确认该 JSON
出现，并在容器内检查 `/srv/data` 是实际 bind mount，而不是只检查目录存在。

### 传播/模板风险

当前 `SystemTemplate` 的旧 mount 设计将 `/` 和 `/run` 标为 `shared`。FUSE rootfs
路径会继承这个传播关系，导致 mount 操作向不应参与的父级传播。现场已尝试把相关
路径改为 `rslave`，但尚未完成 nginx 成功回归，不能声称它已解决。后续排查应在
修改 template 后重建 L1，分别记录 L1 与 nginx mountinfo，避免将临时容器内
`mount --make-rslave` 结果当作持久配置。

### 本轮验收仍需满足

在一次干净的 `05-test-ckm-k3s.sh` 运行中，需要同时取得：nginx `Running`、空 PVC
从 image rootfs 初始化后的预期内容、marker 在 Pod 重建后仍为 `persisted`、snapshotter
handoff JSON 存在，以及 `/srv/data` 的 bind-mount 证据。仅 direct workload 成功或
仅 PVC marker 存在均不足以通过。

## 当前验收基线（2026-08-24）

当前权威测试对象为 218 的
`k3k-console-164315/ckm-sysbox-manual`，CKM Server Pod 为
`k3k-ckm-sysbox-manual-server-647fddbd67-gmfpp`。本轮使用：

```text
image: docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-47-nested-tty-exec
digest: sha256:e10b0f5905fc1d0dbf913079fc396cea4a5984b69810ed1ce04d029555c946a2
sysbox-runc commit: 5208ebbfae1fb487f765df7f0ffec2f0cdbe2ffb
chart: w7panel-sysbox 0.7.1-15, installMode=nested
```

已经实跑通过：

- `05-test-ckm-k3s.sh`：独立 child userns、`uid_map=0 0 65536`、CNI、HTTP、nginx
  rootfs PVC marker/inode/属主在 Pod 重建后保持。
- `09-test-docker-rootfs.sh`：systemd/dockerd、腾讯云 nginx pull/run、Dockerfile build、
  `overlay2` 均通过；`/var/lib/docker` 为 PVC 上 `ext4 idmapped` special mount，Pod 重建后
  marker、inode、构建镜像 ID 和镜像层保持。
- `10-test-cgroup-delegation.sh`：L2 实际路径以 `sysbox.delegate/init.scope` 结尾，L1
  父边界为 `memory.max=2147483648`、`cpu.max=100000 100000`，Docker 子 cgroup 可创建，
  父边界前后不变。
- `11-test-nested-agent-lifecycle.sh`：nested-agent Pod 删除重建后 launcher=1、
  snapshotter=1、socket=listening；CKM K3s identity 保持 `376:2996`，新 nginx Sysbox Pod
  随后成功创建并可访问。
- `12-test-interactive-exec.sh`：nginx `/bin/sh` 与 systemd/Docker `/bin/bash` 均获得真实
  `/dev/pts/*`，可以接收命令并正常退出，退出后无残留 `sysbox-runc exec` 进程。

当前未解决但不阻塞本轮功能验收：

1. `/proc noexec` 强隔离已明确放弃；inner CNI 需要执行 `/proc/self/exe`。
2. CPU/内存的 Pod 内系统视图隔离已明确放弃；L2 仍看到宿主 CPU/内存视图，真实父
   cgroup 限制必须从 L1 验证。
3. 尚未完成长时间并发 Pod/agent 重启压力和 L0 宿主 Sysbox 服务重启后的全量恢复测试。
4. 历史大 rootfs 的 rsync single-flight、取消和 orphan 回收仍需单独压力验证。
5. 因前两项隔离能力缺失，该模式不能作为多租户或不可信负载的安全边界。

下方带更早日期的章节保留问题演进记录；其中“尚未验证”“只能使用 vfs”或“取消 child
userns”等结论只描述当时旧镜像/旧 PoC，不代表 v47 当前状态。

## 固定能力边界（2026-08-21 决定）

218 的 `k3k-console-164315/ckm-sysbox-manual` 实测无法提供 `/proc` 强隔离和 Pod 内
Sysbox 系统视图隔离。决定不是放弃 Sysbox-in-Sysbox，而是接受这两个能力缺口：继续构建、
部署和验证 nested runtime 的 K3s、CNI、Docker、rootfs、cgroup 实际边界与生命周期功能，
不再把 `/proc noexec` 和资源视图虚拟化作为目标。该方案不能用于多租户或不可信负载隔离。

测试对象为 L2 Deployment `default/ckm-k3s-nginx`，资源限制为 1 CPU/2GiB，
`runtimeClassName=sysbox-runc`。实测证据：

```text
/proc: rw,nosuid,nodev,relatime             # 缺少 noexec
L1 pid namespace: pid:[4026548708]
L2 pid namespace: pid:[4026541853]          # PID 隔离正常
L2 /proc/cpuinfo: 72 processors             # 期望 1
L2 /proc/meminfo: MemTotal 63457684 kB      # 期望不超过 2GiB
L2 /sys/fs/cgroup/cpu.max: max 100000
L2 /sys/fs/cgroup/memory.max: max
L1 所见 L2 祖先 cpu.max: 100000 100000
L1 所见 L2 祖先 memory.max: 2147483648
```

`/proc` 缺少 `noexec` 是当前 CNI 可用的必要降级。K3s/containerd 的 netns helper
需要执行 `/proc/self/exe`；恢复 `noexec` 会使 inner CNI 创建 network namespace
失败。`sysbox/allow-proc-exec: "true"` 或 runtime 无条件删除 `noexec` 都不满足强隔离，
不能作为解决方案。

视图失败也不是资源限额未生效。L2 的父 cgroup 已正确限制到 1 CPU/2GiB，实际 throttling
和 OOM 边界此前均已验证；但 nested-identity 代码明确跳过每个 L2 的 sysbox-fs 子挂载，
导致 `/proc/cpuinfo`、`/proc/meminfo` 和 `/sys/fs/cgroup` 暴露错误视图。即使单独补齐
FUSE 视图，也无法解决 `/proc noexec` 与 inner CNI 的冲突。

### 重复 seccomp user-notify listener 的直接根因

这里的“重复 notify”不是普通 seccomp filter 重复。普通 seccomp filter 可以逐层叠加；
Sysbox 使用的 seccomp user-notification 则需要一个 listener FD 和对应的用户态处理者。
L1 进程树会继承外层 L0 的 listener，L2 若再为同类 `mount`、`umount`、`openat2`
syscall 创建一套由 L1 `sysbox-fs` 处理的 listener，当前内核场景会返回：

```text
device or resource busy (EBUSY)
```

即使强行复用外层 listener，L0/L1 helper 也未必拥有 L2 mount/user namespace 中的权限，
可能把 L2 的请求处理成 `EPERM` 或 `EINVAL`。因此 nested-identity 当前跳过 L2 的完整
Sysbox-FS syscall trapping，优先保证 K3s、CNI、nginx 和 Docker 可以启动。

这个降级直接解释了 `free -h` 的结果：没有 L2 专属 Sysbox-FS `/proc` 视图时，
`free` 读取的 `/proc/meminfo` 仍是内核的宿主级内存统计，而不是 L1 的 2GiB cgroup
上限。实际 CPU/内存限制仍由 L1 父 cgroup 和 L2 delegated 子树执行，必须从 L1 cgroup
路径或压力测试验证，不能用 L2 内 `free -h` 判断限额是否生效。

仍不接受共享 L1 user namespace、跳过 `/proc`、使用 host network，或用 Pod Running
代替功能验证。当前明确接受的是 L2 `/proc` 不含 `noexec` 和资源视图不虚拟化；真实 cgroup
CPU/内存边界仍必须生效。方案状态为 **功能继续支持，强隔离与视图隔离不支持**。

## 当前问题清单（更新至 2026-08-24）

本节区分“仍未闭环”和“曾经出现但已经解决”。两项隔离能力已放弃，其余功能与稳定性
问题继续跟踪。

### 当前仍未解决或未闭环

1. **长时间及并发重启稳定性尚未完成。**

   v47 已通过 nested-agent 删除重建、服务单实例检查和随后 workload 重建，但还未覆盖
   多 CKM 并发、连续重启风暴或 24 小时以上运行。

2. **L0 宿主 Sysbox 服务重启后的完整恢复尚未在 v47 重跑。**

   nested-agent 自身重建不会重启 CKM K3s且已通过；L0 mgr/fs/snapshotter 重启属于更大的
   故障范围，仍需单独保存事件与 seccomp 注册日志后验收。

3. **L2 内再次部署完整 CKM 业务工作负载尚未验收。**

   当前默认拓扑已经验证 CKM K3s 创建普通 nginx 与 systemd/Docker Sysbox workload；
   “在该 workload 内再部署完整 CKM controller + K3s”属于额外三层业务场景，不是本轮默认流程。

4. **mgr 大目录 rsync 生命周期控制尚未完成。**

   `ckm-old4` CrashLoop 曾造成单次约 16.7GiB rsync 和并发写入。当前通过清理资源规避，但 rsync single-flight、取消、临时目录和 orphan 进程回收仍未完整实现或压力验证。

5. **CKM pre-install RBAC 仍有权限缺口。**

   `w7panel-ckmv3` ServiceAccount 曾缺少 `pods/exec` 权限。本轮只 skip 了已知历史 execution，正式部署仍需补齐最小 RBAC 并验证脚本范围。

6. **L2 cgroup 资源显示与实际限制存在视图差异。**

   实际边界已通过压力测试：512MiB 内存 Pod 被 `OOMKilled`，500m CPU 压测出现 throttling；但 L2 内部仍显示 `cpu.max=max`、`memory.max=max`。如果要求 L2 视图准确反映父级限制，还需继续改进 Sysbox cgroup 视图。

   2026-08-21 决定放弃该视图隔离能力，不再继续改进显示；后续只验证真实 CPU、内存
   和子 cgroup 边界仍然生效。

### 曾经遇到、目前已解决或已验证通过

- `innerSysbox.enabled=false` 导致 CKM 不创建 L1 nested runtime。
- L1/L2/L3 UID/GID mapping 不正确；当前统一验证为 `0 0 65536`，并使用独立 child userns。
- `sysbox-runc-inner`/`sysbox-runc-nested` wrapper 兼容问题。
- `nsenter -p` 后错误使用外层 PID 检查 daemon，已改为 socket/RPC 健康检查。
- stale PID、`/proc/*/exe` 的 `(deleted)` 后缀导致 daemon 被误判。
- L1/L2 mount namespace 看不到 containerd、mgr、fs、snapshotter socket。
- `/run/k3s`、`/run/sysbox` 未 shared 导致 nested-agent 报 `path is not a shared mount`。
- 极简 K3s 镜像缺少 `modprobe`、`iptables`、`mount.fuse3` 和 `rsync`。
- L2 CNI、IPAM、iptables、veth 回收和腾讯云 nginx HTTP 已通过。
- systemd、dockerd、Docker bridge、端口映射、overlay2 和 systemd cgroup driver 已通过。
- `rootfs-rw-layer` marker 在 L2 Pod 及 L1 Pod 重建后保持。
- overlay2 问题最终定位为 special mount/backing filesystem 处理，不是简单的 `make shared` 缺失。
- 镜像解包 `no space left on device`、`DiskPressure` 和磁盘占满问题已通过清理旧 BuildImage 资源、清理磁盘和扩容处理。
- snapshot/content-store 缺失 digest 导致 sandbox 创建失败的问题已通过恢复/重新拉取内容处理。
- L1 K3s identity 在 chart 安装和 nested-agent rollout 前后保持不变。
- cgroup 二次 delegation 的 CPU throttling、OOM 边界和子 cgroup 可写性已通过压力测试。
- v47 的 `/var/lib/docker` 已确认使用 PVC-backed `ext4 idmapped` special mount，dockerd
  使用 `overlay2`；Docker Pod 重建后构建镜像缓存保持。
- CKM nginx Deployment 的 rootfs marker、inode、属主与 HTTP 已在 218 Pod 重建后通过。
- nested-agent 生命周期清理已通过主动删除重建测试：单 launcher、单 snapshotter、socket
  监听，且 CKM K3s identity 不变。

### 当前判断

嵌套 userns、L2 CNI、Docker/overlay2、rootfs-rw-layer、实际 cgroup 资源边界和 nested-agent
单次重建生命周期均已有 v47 实测通过证据。当前功能流程已跑通；剩余工作是长期/并发压力、
L0 宿主服务故障恢复和额外完整 CKM 三层业务场景，不能把这些未覆盖范围外推为已验证。

该判断只覆盖功能和稳定性，不包含已经放弃的 `/proc` 强隔离与 Sysbox 系统视图隔离；
后续回归继续验证实际 cgroup 边界、K3s、CNI、Docker、rootfs 和生命周期。

## 历史验证快照：2026-08-21 `ckm-bzhrq`

CKM 新 Pod：

```text
k3k-ckm-bzhrq-server-7788d8fbf6-lj89t
```

本轮结果：

- `nested-chart-smoke.sh` 通过；L1 K3s identity `346:2407` 在 chart apply、nested-agent rollout 和 L2 nginx 测试前后保持不变。
- L2 腾讯云 nginx 镜像拉取成功，digest 为 `sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`；`uid_map=0:0:65536`，HTTP 和 CNI/IPAM 清理通过。
- L2 `nested-l2-k3s-final` 恢复为 `2/2 Running`，rootfs marker 保持：inode `576074`、owner `0:0`、内容 `bzhrq-rootfs-verified`。
- L3 腾讯云 nginx 连续两次通过：示例地址 `10.245.0.7`、`10.245.0.15`；L3 userns 独立、UID map 为 `0 0 65536`、HTTP 和 CNI 清理均通过。
- L2 `RuntimeClass/sysbox-runc.handler=sysbox-runc`，节点最终带有 `sysbox.w7panel.io/nested-runtime=ready`。

本轮遇到但已恢复的问题：

1. L2 admission webhook 曾缓存旧 endpoint `10.245.0.4:9443`，而当前 Service endpoint 已变为 `10.52.0.10:9443`，创建 L3 时出现 API server 502。删除并等待重建 `w7panel-sysbox-admission` 后 endpoint 更新为 `10.52.0.19:9443`，L3 创建恢复。
2. nested-agent 曾因 `CKM-prepared Sysbox daemon health check failed` 重启并短暂移除节点 ready label。删除单个 nested-agent Pod 后重新 rollout，Pod `w7panel-sysbox-nested-agent-7b9td` Ready，L3 再次通过。

因此本轮已证明：在 CKM 新 Pod 已能稳定运行的窗口内，L1 → L2 → L3 主链路、rootfs-rw-layer 和 CNI 均可通过；admission endpoint 刷新和 nested-agent daemon health check 的偶发抖动仍需作为稳定性问题继续观察。

> 2026-08-11 更新：专用 inner runtime 已实现显式 `nested-identity`、child userns `0:0:65536`、NoShift 和二次 cgroup v2 delegation。但真实 L1 Docker 验证发现 seccomp user-notify listener 不能在继承 L0 listener 的进程树中再次创建（`EBUSY`），所以该方案尚不能启动完整 L2 systemd/Docker。旧的“取消第三层 userns”路径仅保留为历史 PoC；下文涉及共享 outer userns 的结论只描述旧 PoC。

> 上游 Sysbox 不支持 Sysbox nesting。本文分析的方案仅用于 218 实验性
> PoC，不应作为生产能力或多租户隔离方案启用。

## 早期 PoC 结论（2026-08-11，已被后续实现替代）

Sysbox Pod 中再次运行 Sysbox，只能获得受限的嵌套容器能力，无法获得完整的
“系统容器套系统容器”语义。主要障碍不是单个程序缺陷，而是 Linux user
namespace、procfs/sysfs mount、cgroup、netfilter 和 seccomp 的权限都受
namespace 层级约束。

## 2026-08-11 nested-identity 实机结果

在本机以 Sysbox 启动 L1 systemd/Docker 容器，再运行本地构建的 L1
`sysbox-mgr`、`sysbox-fs` 与 `sysbox-runc`，已确认：

- L1 映射为 `0 165536 65536`，L2 allocator 返回相对 L1 的
  `0 0 65536`，且 `/etc/subuid`、`/etc/subgid` 哈希不变；
- L1 获得完整 cgroup v2 controller delegation；
- inner mgr/fs 可以在 L1 启动，`/dev/fuse` 和每容器 FUSE server 可用；
- cgroup-device BPF 不能由非 initial userns 加载，L2 必须继承 L1 device
  policy；CPU、内存、IO、PID controller 仍可按 L2 子树委托；
- L2 创建到 rootfs 阶段后，`procfs`、`sysfs` mount 会与继承的 L0
  Sysbox seccomp notifier 冲突；
- 尝试在 L2 安装 L1 sysbox-fs 的第二个 seccomp user-notify listener 时，
  内核明确返回 `device or resource busy (EBUSY)`。

最后一项是当前方案的硬阻塞。seccomp filter 和 listener 会沿进程树继承，
不能按“L0 sysbox-fs 一套、L1 sysbox-fs 再一套”的方式叠加。因此，仅修改
L1 mgr/runc/fs 无法实现完整 Sysbox nesting。后续架构必须二选一：

1. 由 L0 保留唯一 listener，并把 L2 注册、PID/userns 映射和请求路由到同一
   sysbox-fs；
2. L0 对专用 nested-host 容器不安装 notifier，再由 L1 接管，但这会失去 L1
   自身依赖 notifier 的普通 Sysbox mount 语义，必须补充等价实现。

在完成其中一种单 listener 架构前，不能把 systemd、Docker 或 K3s 验收标记
为通过，也不能用跳过 `/proc`、共享 L1 userns 或 Docker `vfs` 降级冒充完整
实现。

当前 PoC 为了让 inner Pod 启动并获得可用的 `/proc`，最终让
`sysbox-runc-inner` 不再创建第三层 user namespace。这样虽然绕过了 procfs
mount 的内核限制，但 inner root 与 outer Sysbox Pod root 位于同一个 user
namespace，失去了普通 Sysbox 最重要的一层隔离。因此该方案只能作为专用、
受限的 nested runtime，不能视为完整 Sysbox。

## 2026-08-11 PoC 运行层级（历史）

```text
宿主机
└─ outer Sysbox Pod user namespace
   ├─ inner K3s
   ├─ sysbox-mgr / sysbox-fs
   └─ inner Sysbox Pod
```

这里至少涉及三个权限视角：

1. 宿主机初始 user namespace；
2. outer Sysbox Pod 的 user namespace；
3. 原计划由 inner Sysbox Pod 创建的第三层 user namespace。

Linux capability 不是全局布尔值。即使进程在容器中显示为 UID 0，也只能管理
其 capability 生效范围内的资源。sysfs、cgroup、netfilter、mount superblock
等资源如果由更高层 user namespace 拥有，inner root 仍会得到 `EPERM`。
创建新的 mount 或 network namespace 也不会自动获得这些资源的管理权。

## 2026-08-11 PoC 核心局限（历史）

| 局限 | 根因 | PoC 处理 | 造成的损失 |
| --- | --- | --- | --- |
| 无法正常创建第三层 user namespace | 子 UID/GID mapping 必须完全落在父 mapping 内；outer 只暴露 `0..65535` | 专用 handler 不创建第三层 userns | inner root 与 outer Pod root 处于同一 userns，隔离弱于普通 Sysbox |
| procfs mount 返回 `EPERM` 或 `EINVAL` | inner userns 无权完成 procfs mount；outer Sysbox-FS 进入错误 userns 代理时语义不匹配 | 复用 outer userns 后挂载 `/proc` | 无法同时保留独立 userns 和可用 procfs |
| sysfs/cgroup 不可正常初始化 | `/sys` 与 controller 由更高层 namespace 拥有，inner capability 对其无效 | 跳过部分 sysfs、cgroup 操作 | systemd 无法完成完整启动和服务管理 |
| overlay mount 失败 | overlayfs mount 需要当前层级没有的挂载权限 | Docker 测试使用 `vfs` | 性能、空间利用率和语义都不同于正常 DIND |
| Docker iptables 失败 | inner Pod 无权管理外层拥有的 netfilter 表 | dockerd 关闭 iptables 和 bridge | 无 Docker bridge、端口映射及标准容器网络 |
| sysctl、只读路径检查无法执行 | proc/sys 与 sysfs 路径不具备普通系统容器语义 | nested handler 跳过这些步骤 | 不能保证 OCI sysctl 和只读路径配置生效 |
| 特殊目录无法按普通方式准备 | `/var/lib/docker` 等目录需要 UID shifting 和 chown，目标 ID 不在父 mapping 中 | 跳过隐式特殊目录 mount 和 rootfs shifting | 数据目录隔离、属主、持久化和性能语义不完整 |
| seccomp notifier 层级冲突 | inner runc 与 outer Sysbox-FS 同时处理 mount，请求 capability 和代理 namespace 不匹配 | 专用模式绕过部分 seccomp 路径 | 安全模型弱于普通 Sysbox |
| daemon 生命周期不完整 | inner mgr/fs 只是 outer Pod 内的后台命令，不是系统服务 | 启动脚本等待 Unix socket | 缺少标准重启、健康检查、升级及故障恢复 |

## 问题根因：UID/GID mapping

普通 Sysbox 通常需要至少 65536 个连续 subuid/subgid。outer Sysbox user
namespace 只向 K3s 容器暴露 `0..65535`，因此存在以下冲突：

- `100000:65536`：这些 ID 在父 namespace 中不可见；
- `0:65536`：inner root 再次使用父 ID 0 会破坏预期隔离，并被相关路径拒绝；
- `1:65535`：可以覆盖剩余父 ID，但不足普通 Sysbox 的完整 65536-ID 模型。

尝试使用父 namespace 无法表示的 ID 时，runc 在创建或处理 `exec.fifo`、
rootfs 和特殊目录时会出现：

```text
chown ...: invalid argument
```

这不是普通文件权限不足，而是内核无法把目标 UID/GID 翻译到父 namespace。
早期 PoC 将 mgr 与 runc 的专用范围同步为 `1:65535`；最终为了恢复 procfs，
专用 handler 进一步取消第三层 user namespace、OCI UID/GID mappings 和
rootfs UID shifting。

## 问题根因：procfs mount

inner runc 创建第三层 user namespace 后执行：

```text
mount("proc", "/proc", "proc", ...)
```

会被内核以 `EPERM` 拒绝。该请求还可能被 outer `sysbox-fs` 的 seccomp
notifier 截获：

- 直接让 inner 进程执行：返回 `EPERM`；
- 代理进入全部 child namespaces：userns 语义不匹配，返回 `EINVAL`；
- 在通用 `CAP_SYS_ADMIN` 预检中处理：inner runc 的 capability 对目标资源
  无效，请求在代理前就被拒绝。

早期通过跳过 procfs 让 Pod 进入 Running，但 `/proc` 是空目录，导致：

```text
free -h
top -b -n 1
ps -ef
```

全部失败。这种 Running 不能算验收成功。最终专用 handler 不再创建第三层
userns，才恢复可用 procfs。218 已验证 `/proc/meminfo` 和 PID 目录包含真实
数据。

## 问题根因：sysfs、cgroup 与 systemd

systemd 镜像可以让 `/sbin/init` 成为 PID 1，但这不代表 systemd 已完成系统
初始化。实测 Pod 状态为 `1/1 Running`、PID 1 为 systemd，同时：

```text
systemctl is-system-running
offline
```

原因是 inner 环境没有完整、可写且归当前 user namespace 管理的 sysfs/cgroup
层级。相关初始化可能出现：

```text
mkdir: cannot create directory 'cpu': Read-only file system
System has not been booted with systemd as init system
```

因此当前结果只能证明 `/sbin/init` 进程可以执行，不能证明 service manager、
unit、journald、cgroup delegation 或服务自动拉起可用。

## 问题根因：Docker daemon

在正确使用 `runtimeClassName: sysbox-runc-inner` 的 systemd 测试 Pod 中，
Docker daemon 不会自动启动。默认启动 dockerd 时实测出现：

```text
could not setup daemon root propagation: operation not permitted
failed to mount overlay: operation not permitted
iptables ... Permission denied (you must be root)
failed to create NAT chain DOCKER
```

这些错误分别来自 mount propagation、overlayfs 和 netfilter 的 namespace
权限边界。容器内显示为 root 并不能取得这些外层资源的管理能力。

为了只验证 image pull，PoC 使用：

```sh
dockerd \
  --storage-driver=vfs \
  --iptables=false \
  --bridge=none
```

该配置能启动 Docker API，但不具备标准 Docker-in-Docker 语义：

- 没有 overlay2；
- 没有 Docker bridge；
- 没有 iptables NAT；
- 没有端口映射；
- 未验证启动 Docker workload container。

因此成功执行 `docker pull` 只证明 registry 访问和 content store 的最小路径
可用，不能证明 DIND 已通过。

## 问题根因：inner CNI、DNS 与外网

inner K3s 使用的测试 CNI 配置为 `ipMasq:false`。Pod 地址位于
`10.244.0.0/16`，如果 outer K3s network namespace 没有对应 SNAT，外部网络
无法返回该源地址的响应。

systemd/Docker 测试中还观察到 inner Pod 的 `/etc/resolv.conf` 指向：

```text
nameserver 10.43.0.10
```

当 inner CoreDNS 未就绪时，registry DNS 查询会一直超时。218 的最终 pull
测试临时进行了两项环境修正：

1. 在一次性测试 Pod 中补充 registry hosts 解析；
2. 在 outer K3s Pod network namespace 为 `10.244.0.0/16` 增加临时
   MASQUERADE。

修正后成功拉取：

```text
ccr.ccs.tencentyun.com/afan-public/nginx:latest
sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f
```

DNS、CNI SNAT 和 registry 可达性属于测试环境问题，应与 inner runc 创建失败
分开判断。

## 问题根因：`/proc noexec` 与 inner CNI

outer Sysbox 默认将 `/proc` 挂为 `noexec`，inner CNI 创建 network namespace
时需要执行 `/proc/self/exe`，因此会因外层 mount flag 失败。

PoC 增加了严格 opt-in：

```yaml
metadata:
  annotations:
    sysbox/allow-proc-exec: "true"
```

containerd 必须把该 key 同时放入 `pod_annotations` 和
`container_annotations`，否则注解可能只到达 sandbox，实际 K3s workload
OCI spec 无法收到。该选项会降低 outer Pod 的 `/proc` 隔离，只能用于本 PoC。

## 问题根因：静态二进制与最小 K3s 镜像

K3s 镜像缺少普通发行版中的动态加载器和部分共享库。即使目标文件存在，执行
动态 ELF 时也可能得到：

```text
not found
exit status 127
```

这通常表示 ELF interpreter 或共享库不存在，而不是文件没有复制成功。当前
helper 必须提供静态构建的：

- `sysbox-runc`；
- `sysbox-fs`；
- `rsync`；
- `fusermount3`。

此外，K3s `/etc/os-release` 可能没有 `ID`，Sysbox 选择发行版路径前还需写入
generic distro ID。

## 问题根因：exec FIFO 卡死

早期 nested handler 为绕过缺失 procfs，不能再通过 `/proc/self/fd` 重新打开
`O_PATH` exec FIFO，因此改用 `O_RDWR` FIFO。一次修改过早关闭 writer，导致：

1. init 已写入启动握手字节；
2. writer 在 `runc start` 打开 reader 前关闭；
3. start 侧没有读到握手字节；
4. Pod 永久停在 `ContainerCreating`。

恢复 FIFO writer 的短暂保留后，pause Pod 才重新启动成功。这属于专用 nested
实现回归，不是网络或镜像拉取问题。

## 问题根因：交互式 exec 在附加终端前卡死

`v0.7.1-46` 中 nginx `/bin/sh` 和 systemd/Docker `/bin/bash` 均可执行非交互命令，
但 `kubectl exec -it` 会在终端附加前永久等待。根因是 nested-identity 同时启用了
`SkipSpecialMounts`，setns exec 路径因此跳过 `UnsafeCloseFrom()`，让目标 shell 继承了
runc 内部同步 socket。只要交互 shell 不退出，父 runc 就一直等待该 socket 的 EOF，
containerd 无法完成 PTY 附加。

`sysbox-runc` commit `5208ebb` 将 FD 清理策略与 special mount 策略分开：
nested-identity 的标准 init 和 setns exec 始终关闭内部 FD，只有旧的非 nested PoC 保留原行为。
该修复同时恢复了 nested init 的 FD 清理保护。镜像
`v0.7.1-47-nested-tty-exec` 已在 nested-agent 删除重建前后验证：

```bash
bash ./12-test-interactive-exec.sh
bash ./06-enter-ckm-shell.sh nginx
bash ./06-enter-ckm-shell.sh docker
```

两个容器都获得 `/dev/pts/*`、能接收输入并正常退出，退出后没有残留
`sysbox-runc exec --console-socket` 进程。命令参数是 `-it`，不是 `-ut`。

## 需要区分的环境与时序问题

以下错误会干扰测试，但不直接说明 Sysbox runtime 逻辑失败：

| 现象 | 分类 | 原因 |
| --- | --- | --- |
| `serviceaccount "default" not found` | inner K3s 启动时序 | outer Pod Running 不代表 inner API 和 controller 已初始化完成 |
| `RuntimeClass "sysbox-runc" not found` | outer 集群环境 | cluster-scoped RuntimeClass 被删除或尚未创建 |
| CoreDNS、基础组件拉取超时 | 外部 registry/network | Docker Hub 或 DNS 不可达 |
| registry 域名解析超时 | inner DNS | inner CoreDNS Service 无可用响应 |
| registry HTTPS 建连超时 | inner CNI | `ipMasq:false`，Pod 出站流量没有 SNAT |
| binary `not found` | 镜像运行环境 | K3s 镜像缺少 ELF interpreter 或共享库 |

测试记录必须同时保留 Pod phase、事件、runc/mgr/fs 日志和网络证据，不能将
“构建成功”“outer Pod Running”或“镜像已拉取”单独作为 inner Sysbox 验收
成功。

## 2026-08-11 当时已证明的能力

- outer Sysbox Pod 内可以命令方式启动 `sysbox-mgr` 和 `sysbox-fs`；
- inner K3s、containerd 和基础 CNI 可以启动；
- 专用 `sysbox-runc-inner` 可以启动最小 pause Pod；
- inner Pod 的 `/proc` 可供 `free`、`top`、`ps` 使用；
- systemd 测试镜像可以让 `/sbin/init` 成为 PID 1；
- 使用 `vfs + no iptables + no bridge` 的受限 dockerd 可以完成指定
  `docker pull`。

## 2026-08-11 当时尚未证明的能力

- 独立的第三层 user namespace；
- 与普通 Sysbox 等价的 root 隔离；
- 完整 systemd service manager；
- cgroup delegation 和 controller 管理；
- Docker overlay2；
- Docker bridge、iptables、端口映射；
- 标准 Sysbox sysctl、sysfs、只读路径和特殊目录语义；
- 普通 Docker workload container 的创建与运行；
- 多租户安全与生产可用性；
- daemon 的标准生命周期、升级和故障恢复。

## 当前生产判断

v47 已恢复独立 child user namespace、`0 0 65536` 映射、Docker `overlay2` 和二次
cgroup delegation，但仍主动放弃 `/proc noexec` 强隔离和 Pod 内资源视图隔离。
因此当前方案可用于受控的功能性 CKM 工作负载，不能作为多租户或不可信负载的安全边界。
正式扩大使用范围前仍需完成长时间并发重启、L0 服务恢复和大 rootfs rsync 生命周期压力测试。

## 相关文档

- [README.md](./README.md)：当前人工验证流程；
- [HISTORY.md](./HISTORY.md)：218 PoC 实现和逐轮测试记录；
- [../README.md](../README.md)：w7panel 文档索引。
# runc-lite 边界

## 218 hostUsers=false sandbox 阻塞（2026-09-01）

在 L1 CKM 的 runc-lite workload 设置 `hostUsers:false` 后，Pod 尚未进入
runc-lite，inner containerd 先创建 sandbox network namespace 即失败：

```text
failed to create network namespace for sandbox: failed to start noop process for unshare:
fork/exec /proc/self/exe: operation not permitted
```

该错误来自 containerd CRI sandbox userns helper，不是 runc-lite 的 OCI create。
L1 的 `/proc/self/uid_map` 为 `0 3540385792 65536`，而 helper 请求的 child
映射使用 L0 偏移 UID（此前观测为 `231072`），不落在 L1 可见映射内。
`/etc/subuid` 在 L1 中虽为 `root:0:65536`，仍无法改变 helper 传入的范围。

已提供针对 CKM 使用的 containerd v2.2.5-k3s2 的最小补丁
`containerd-userns-local.patch`；它只修改 CRI sandbox 映射并可独立构建
containerd，不需要重新编译 K3s。应用补丁后需替换 CKM 数据卷中的
containerd 并重启 L1 Pod。

因此当前无需重编译 K3s 的可行项仅是让 containerd sandbox helper 支持
namespace-local（`0:0:65536`）映射，或关闭该 helper 并由 OCI runtime 创建
userns；现有 containerd 配置没有对应开关。除非修改/升级内嵌 containerd，
218 集群无法完成 hostUsers=false 的端到端验收。

CKM operator 的旧启动命令还会把 `/var/lib/rancher/k3s/runc-lite` 复制到
`/usr/local/bin/runc`。该路径是 K3s 默认 runtime；若复制未启用 seccomp 的
构建，会导致所有 Pod 报 `seccomp not supported`。runc-lite 应只通过
RuntimeClass 的 `BinaryName` 使用，不能替换默认 runc。

`runc-lite` 只服务内层 K3s，要求 Pod 使用 `hostUsers: false`，并依赖
`sysbox-snapshotter` 与 `sysbox-admission` 提供 rootfs/PVC 元数据。proc 强隔离、
系统视图隔离和 system container 功能不在支持范围内。

## 最新 218 验证（2026-09-01）

独立构建的 containerd v2.2.5 patched 已使 sandbox userns 和 overlayfs chown
阶段通过；随后失败进入 OCI create：`mounting "proc" to rootfs ... invalid
argument`。这是嵌套 userns 中 proc 挂载限制，尚未进入 runc-lite 进程，端到端
测试仍阻塞。后续需在 containerd/runc spec 中跳过该 proc 挂载（本项目已放弃
proc 强隔离）或改用可用的宿主 proc bind 挂载。

后续重测中 runc-lite 已跳过 proc/sysfs 挂载并成功创建 `hostUsers:false` Pod；
启用 sysbox-snapshotter 后，CKM 镜像缺少 `/dev/fuse`，`fuse-overlayfs` 自检失败，
特殊目录 PVC handoff 因此尚未验证。需要为 CKM 注入可用的 FUSE 设备。

## 218 patched containerd 替换限制（2026-09-01）

按 `containerd-userns-local.patch` 独立编译出的 `/tmp/containerd-patched6`
可启动，但 CKM 的 `/bin/k3s server` 仍会启动内嵌 containerd（日志中的 PID 362），
并占用 `/run/k3s/containerd/containerd.sock`。手工启动 patched containerd 只能
与其并存，强杀内嵌进程会连带 K3s 退出；因此当前部署未真正使用 patched binary，
workload 仍报 runc bundle rootfs `no such file or directory`。要在不重编译 K3s
的前提下生效，必须让 K3s 启动参数/配置指向独立 containerd socket，或替换其
内嵌 containerd；现有启动命令未提供该切换。

## L2 nginx `hostUsers:false` 复测（2026-09-02）

在已验证的 `runc-lite + sysbox-snapshotter` nginx Deployment 上设置 Pod
`spec.hostUsers: false` 后，Pod 保持 `ContainerCreating`，重复事件为：

```text
FailedCreatePodSandBox: failed to create network namespace for sandbox:
failed to start noop process for unshare: fork/exec /proc/self/exe: operation not permitted
```

该失败发生在内嵌 K3s 的 containerd CRI sandbox userns helper，早于 admission、
snapshotter、FUSE rootfs 和 runc-lite OCI create。因此不能将此结果归因为本轮
runc-lite/rootfs 改动。L1 CKM Pod 仍是 `runtimeClassName=sysbox-runc` 且
`hostUsers=false`；L2 的持久化 nginx 回归临时保持不设置该字段，直到 K3s 内嵌
containerd 应用 namespace-local userns 映射补丁。

进一步检查 containerd v2.2.5-k3s2 源码发现 sandbox helper 的
`UnshareAfterEnterUserns` 使用 `os.StartProcess` 携带 `Ptrace: true`。L1 进程状态
显示 `Seccomp: 2`，因此该 `PTRACE_TRACEME` 路径也可能被 Sysbox seccomp filter 拒绝。
但 `Ptrace` 同时用于在 exec 前暂停子进程；简单删除会破坏 helper 的同步语义，不能
直接部署。要完成 hostUsers 测试，仍需设计不依赖 ptrace 的握手、处理 namespace-local
UID/GID 映射，构建与 K3s 版本匹配的 patched containerd，并通过
`--container-runtime-endpoint` 让内层 K3s 使用它，再做完整回归。

## 持久化路径与真实 root 验证（2026-09-02）

快照器原先将 PVC upper/work 放在含 Pod UID 的 kubelet mount path，重建 Pod 后
marker 丢失。修复后从已受信任 sidecar OCI mount source 的 `/proc/self/mountinfo`
读取 Sysbox `/rootfs/special/...` bind root，并映射为 L1 可见、稳定的 PVC 路径。
当前 handoff 与 fuse-overlayfs 均使用：

```text
/var/lib/rancher/k3s/storage/pvc-74a3e152-7dc5-4579-becc-3406d7ac5301_default_ckm-k3s-nginx-rootfs/nginx/{upper,work}
```

marker 在该 upper 中跨 Pod 重建保留。验证脚本不再使用 `kubectl exec`（其 setns
root 仍错误），而是以 Pod `containerID` 在 L1 `/proc/*/cgroup` 精确查找进程，再
以 `nsenter -m -r/proc/<pid>/root` 检查真实 root；已确认 image `index.html`、marker
`persisted-v2` 和 `/srv/data` bind 均存在。
