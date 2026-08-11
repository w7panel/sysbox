# CKM 内层 K3s 运行 Sysbox（实验性 PoC）

> 上游 Sysbox 不支持 Sysbox nesting。本方案仅用于 218 验证，不可作为生产功能启用。

## 目标与边界

外层 CKM server Pod 使用 `runtimeClassName: sysbox-runc` 和 `hostUsers: false`；其内运行的 K3s 不含 systemd。目标是在不维护派生 K3s 镜像的前提下，在该 K3s 内启动 Sysbox runtime。

二进制由 initContainer 从 Sysbox deploy 镜像复制到 `emptyDir`，再挂载到 K3s 主容器的 `/opt/sysbox`。主容器必须额外挂载字符设备 `/dev/fuse`。

## 启动器

`sysbox-pkgr/k8s/scripts/sysbox-inner-k3s.sh` 是 K3s 专用 POSIX `sh` 启动器：

1. 校验 `/dev/fuse`、`rsync`、`modprobe`、`iptables`；
2. 为无 `ID` 的 K3s `os-release` 写入 generic distro ID；
3. 写入 inner K3s 的 containerd `sysbox-runc` handler；
4. 以非 systemd 方式启动 `sysbox-mgr` 与 `sysbox-fs`；
5. 等待 `/run/sysbox/sysmgr.sock`、`sysfs.sock`。

启动器只注册 containerd handler；inner API 仍需创建一次 `RuntimeClass`：`handler: sysbox-runc`。

inner template 设置 `SystemdCgroup = false`，并禁用 idmapped mount 路径；否则 K3s 镜像无 systemd、嵌套 user namespace 也无法通过 idmapped-mount 预检。

外层 Sysbox 的 user namespace 只向 K3s 容器暴露 `0:65536`。启动器因此在启动 inner `sysbox-mgr` 前将 `sysbox` 的 subuid/subgid 固定为同一父 namespace 可见范围；不能使用常规的 `100000:65536`，否则内层 runc 在创建 `exec.fifo` 时尝试 `chown` 一个未映射 UID 并返回 `EINVAL`。

新版 runc 的默认 procfs/sysfs 挂载在嵌套场景均会被内核以 `EPERM` 拒绝。`sysbox/skip-special-mounts: "true"` 因而省略 inner Pod 的 procfs 与 sysfs mount；仅适用于不依赖它们的 PoC pause 工作负载。普通 Sysbox 容器仍保持默认路径。

同一注解还会阻止 parent bind-mount helper 为进入 child PID namespace 而重新挂载 procfs；该操作在该 namespace 层级也会被拒绝。

由于 `/sys` 已省略，注解还会跳过其下的 Sysbox-FS emulated-path overmount（例如 CPU online 文件）；此路径仅用于 pause PoC。

`sysbox-mgr` 强制依赖 `rsync`。deploy 镜像的 Dockerfile 通过构建阶段加入静态 `rsync`，initContainer 应复制它和 Sysbox 二进制。

`sysbox-fs` 为每个 inner Pod 创建 FUSE server 时会执行 `fusermount3`。K3s 镜像没有动态加载器，因此 deploy 镜像编译静态 `fusermount3` 并放入 `generic` artifact，由 initContainer 一并复制。启动器会在启动 K3s 前检查它；缺失时应直接失败，而不是在 Pod pre-registration 阶段才报笼统的初始化错误。

## 218 已验证结果

| 项目 | 结果 |
| --- | --- |
| `/dev/fuse` 挂载 | 成功 |
| `sysbox-mgr` command 启动 | 成功 |
| `sysbox-fs` command 启动 | 成功 |
| inner K3s / containerd 启动 | 成功 |
| inner Sysbox RuntimeClass | 已注册 |
| inner K3s CNI | 成功（template 补齐 `bin_dir=/var/lib/rancher/k3s/data/cni` 与 `conf_dir` 后节点 Ready） |
| outer `/proc` opt-in | 成功：`rw,nosuid,nodev`，不含 `noexec` |
| inner Sysbox Pod | 成功：`nested-sysbox-hostusers` 在 218 的 `nested-poc` 节点为 `1/1 Running` |

## 复测脚本

`tests/sysbox-in-sysbox-218.sh` 在 218 的外层 PoC 已就绪后，创建 inner
`RuntimeClass`（`sysbox-runc-inner`）和 pause Pod，并输出
inner 节点、Pod 与事件。脚本先等待 inner Node Ready 和 `default` ServiceAccount，
避免刚重建 K3s 时创建 Pod 报 `serviceaccount "default" not found`。默认使用 `~/.kube/218.config`；可通过 `NAMESPACE`、
`DEPLOYMENT`、`KUBECONFIG_218` 覆盖。

## 测试镜像

| 镜像 | 启动方式 | 测试用途与结果 |
| --- | --- | --- |
| `docker.cnb.cool/i0358/zpk/nested-pause:20260810-1` | 镜像默认命令，`runtimeClassName: sysbox-runc-inner` | 最小 pause PoC；验证 inner Sysbox Pod Running、CNI、`kubectl exec` 以及 `free -h`、`top`、`ps -ef` 可读取真实 procfs 数据。 |
| `docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker:latest` | `/sbin/init`，`runtimeClassName: sysbox-runc-inner` | systemd/Docker 扩展测试；Pod `1/1 Running` 且 PID 1 为 systemd。专用 nested handler 下 systemd 为 `offline`，需以 `dockerd --iptables=false --storage-driver=vfs --bridge=none` 手动启动 daemon，并临时补齐 DNS 与 inner CNI SNAT；已成功拉取 `ccr.ccs.tencentyun.com/afan-public/nginx:latest`。 |

第二个镜像在 218 实测解析为
`sha256:8e22c97c910cdab7cf7ccfbe1118060fdd4d3de2c1761033a17f19d2657837b7`；
拉取的 nginx registry digest 为
`sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`。

## `/proc` 执行例外（仅 PoC）

inner CNI 创建 network namespace 时需执行 `/proc/self/exe`。外层 Sysbox 默认将 `/proc` 挂为 `noexec`，因此新增严格 opt-in 注解：

```yaml
metadata:
  annotations:
    sysbox/allow-proc-exec: "true"
```

仅小写字符串 `"true"` 生效；缺失、`false`、`TRUE` 都保留默认 `noexec,nosuid,nodev`。containerd 必须同时将该 key 配到 `pod_annotations` 与 `container_annotations`：前者保存 sandbox 元数据，后者才会传到实际 K3s 容器的 OCI spec。runtime 仅移除该 outer Pod `/proc` 的 `noexec`。它降低了容器隔离边界，仅限本 PoC，不能作为生产默认配置。

inner command-mode 使用专用 `sysbox-runc-inner` handler，而不是依赖 Pod 注解。wrapper 在 runc 父进程设置标志，`initConfig` 再可靠地传递到各 init helper。该模式跳过不可在 nested user namespace 创建的 proc/sys mount、Sysbox-FS emulated-source bind mount、sysctl 与只读挂载检查；因此仅适合最小 pause PoC，不能作为生产系统容器语义。

## 内层 procfs 修复

`sysbox-runc-inner` 恢复挂载 `/proc` 时，该 `mount("proc", "/proc", ...)` 会被**外层** `sysbox-fs` 的 seccomp notifier 捕获。旧路径通过外层的 nsenter helper 代理挂载；代理进入外层 user namespace 后，不再拥有内层 user namespace 所需的语义，内核返回 `EINVAL`，结果是 Pod 虽能运行但 `/proc` 为空，`free`、`top`、`ps` 均失败。

修复位于 `sysbox-fs/seccomp/mount.go`：当请求进程的 user-namespace inode 与外层容器 init 进程不同时，仍由 Sysbox-FS 代理 procfs mount，但 nsenter 改为进入目标的 mount/pid/network 等命名空间而**不进入** child user namespace。这样保留外层的挂载能力。直接让内核执行会因 child user namespace 返回 `EPERM`；进入全部 namespace 会返回 `EINVAL`。相同 user namespace 的常规 Sysbox procfs 挂载仍走原有 `AllNSs` 路径。

该请求还会在 tracer 的通用 `CAP_SYS_ADMIN` 检查前被识别：inner runc 在 child user namespace 中不具备该 capability，若照常拒绝则 nsenter helper 根本不会执行。例外仅限 `proc` 且 user-namespace inode 与外层 init 不同的请求；其他挂载仍必须通过原有 capability 检查。

218 复测时需使用 `Dockerfile.sysbox-inner-helper` 构建的新 helper；它以已验证的 `copyhelper:20260811-66` 为基础，只替换本次构建的 `sysbox-runc` 和 `sysbox-fs`，其余静态工具与脚本不变。

## 后续风险

1. 用 `hostNetwork: true` 绕过 CNI 不可行：Sysbox 必须使用 user namespace，拒绝与 host 共享 network/uts namespace。
2. 即使 CNI 成功，外层 user namespace 只有一个 65536-ID 映射；inner Sysbox 的 subuid 分配是否可用仍需单独验证。这是上游明确不支持 nesting 的核心风险。

因此，当前 PoC 已证明“无 systemd K3s 内可 command 启动 Sysbox daemon”和“inner K3s 的普通 CNI 可用”。应用此例外后继续验证 inner Sysbox Pod；若仍失败，优先检查 user namespace 映射。

## 2026-08-10 复测结论

218 上 outer Pod 加注解并重建后，`/proc` 挂载从 `rw,nosuid,nodev,noexec` 变为 `rw,nosuid,nodev`。containerd 的 `pod_annotations` 只会出现在 pause sandbox；补上 `container_annotations` 后，实际 K3s workload OCI spec 才收到注解。

## 2026-08-11 验收结论

218 已验证 `nested-sysbox-hostusers` 为 `1/1 Running`。专用 handler 采用
`1:65535` 子 ID 范围：外层映射仅有 `0..65535`，且 ID 0 不能再映射给内层
root。`sysbox-mgr` 与 `sysbox-runc` 必须同步使用此范围；同时专用模式跳过
隐式特殊目录卷，否则其 parent-namespace `chown` 会失败。该结论仍只覆盖
pause PoC，未覆盖生产系统容器语义。

## 2026-08-11 测试问题记录

| 现象 | 已确认原因 | 处理与结果 |
| --- | --- | --- |
| inner K3s 刚启动时创建 Pod 报 `serviceaccount "default" not found` | outer Pod Ready 不等于 inner API 初始化完成 | 测试脚本等待 inner Node Ready 与 default ServiceAccount 后再创建 Pod。 |
| helper 中 `sysbox-runc`、`sysbox-fs` 返回 `127` 或 `not found` | K3s 镜像没有动态加载器和 `libseccomp` | inner helper 必须使用 `make static` 产物；Dockerfile 只替换 runc/fs，保留已验证的静态工具。 |
| inner Pod 可 Running 但 `/proc` 为空，`free/top/ps` 失败 | 早期专用模式为绕过 mount 权限跳过 procfs | 已将 procfs 恢复为测试目标，不再以空 `/proc` 作为成功判据。 |
| inner runc 直接 `mount proc` 返回 `EPERM` | 第三层 user namespace 在 outer Sysbox 边界内无法完成 procfs mount；清空 inner RuntimeDefault seccomp 后仍复现 | 已排除 K3s seccomp 是根因；outer K3s 自身可 mount procfs。正在验证专用 handler 不创建第三层 user namespace 的路径。 |
| 以 `/proc/1/ns/user` 加入 outer user namespace时 runc 返回 `EOF` | nsexec 不能用该路径加入同一 user namespace | 改为专用模式不请求 `NEWUSER`，仍保留其他隔离 namespace；同时跳过 OCI UID/GID mappings 与 rootfs UID shifting。 |
| no-third-userns helper 启动时返回 `exit status 2` | 已确认 OCI UID/GID mappings 不能与缺失 `NEWUSER` 并存；首轮跳过 mappings 后仍有未记录的 runc 参数/验证错误 | 正在从 containerd task shim 日志读取准确错误；未将该路径记为成功。 |
| parent rootfs helper 挂 procfs 返回 `EPERM` | helper 保留 outer user namespace，仍不是 inner mount namespace owner | 不作为最终路径；保留该结果以说明为什么需要消除第三层 user namespace。 |

每次后续 218 复测均应在本节追加一行：记录 helper tag、命令、Pod phase、`kubectl exec` 输出或失败事件，避免将构建成功误记为验收成功。

### 测试记录规则

后续每次测试，无论通过或失败，均必须在本节记录：测试时间、helper 镜像 tag、执行命令、Pod phase、关键日志/事件、原因判断、采取的处理以及复测结论。网络、镜像拉取等环境问题也需记录，并与产品逻辑问题明确区分。

| 现象 | 已确认原因 | 处理与结果 |
| --- | --- | --- |
| helper `20260811-84` 的 inner `nested-sysbox-hostusers` 持续 `ContainerCreating`，且无容器启动事件 | containerd 已创建 shim，`sysbox-runc-inner` 已走完 `rootfsReadyAck`、`procReady`、`procRun`，随后卡在 exec FIFO；清理时移除了 O_RDWR FIFO writer 的短暂保留，`runc start` 因此无法读到握手字节。inner 基础组件同时有 Docker Hub 拉取超时，但测试 pause 镜像已成功拉取，不能将其误判为本 Pod 根因。 | 恢复专用模式在 FIFO 写入后的 2 秒保留；作为一次回归单独记录，待以新 helper 复测后再判定通过。 |
| 2026-08-11 14:33 HKT，helper `20260811-85`；`bash w7panel-doc/tests/sysbox-in-sysbox-218.sh`；`nested-sysbox-hostusers` `1/1 Running` | inner Node `nested-poc` Ready，default ServiceAccount 存在；事件为 Scheduled/Pulled/Created/Started，无失败事件；`free -h` 显示 Mem total 60.5G/free 46.8G，`top` 与 `ps -ef` 可见 PID 1 及 exec 进程 | 先恢复被环境误删的 outer `RuntimeClass/sysbox-runc`，再强制 rollout 使 deployment 实际使用 `20260811-85`；inner RuntimeClass 创建成功，pause Pod 及 procfs 工具验收通过。 |
| 2026-08-11 14:55 HKT，helper `20260811-85`；inner image `docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker`；PID 1 `/sbin/init`；执行 `docker pull ccr.ccs.tencentyun.com/afan-public/nginx:latest` | `nested-systemd-docker` 为 `1/1 Running`，实际 `runtimeClassName=sysbox-runc-inner`。专用 handler 下 systemd 为 `offline`，dockerd 不会自动启动；默认 dockerd 又因 overlay mount 与 iptables 权限失败。inner CoreDNS 无响应，且 CNI 使用 `ipMasq:false`，外网连接没有 SNAT。 | 以 `dockerd --iptables=false --storage-driver=vfs --bridge=none` 启动仅用于 pull 的 daemon；测试 Pod 临时写入 registry hosts 解析，outer K3s Pod 临时增加 `10.244.0.0/16` MASQUERADE。pull 成功，image ID `sha256:9a9a9fd723f1...c4c43`，registry digest `sha256:29cf9892ca11...159f`。这只证明受限配置下可 pull，不代表完整 systemd/Docker-in-Docker 语义已通过。 |

## 2026-08-11 最终验收（helper `20260811-83`）

专用 `sysbox-runc-inner` 不创建第三层 user namespace，并相应跳过 UID/GID mapping、rootfs UID shifting；inner Pod 仍有独立 pid/ipc/uts/mount/network/cgroup/time namespace，运行于 outer Sysbox 的 user namespace。此限制是当前内核/外层 Sysbox 边界下让 procfs 可用的必要条件。

218 证据：`k3k-console-164315/sysbox-inner-k3s-command-poc-f785bbf54-kjksq` 内的 inner Pod `default/nested-sysbox-hostusers` 为 `1/1 Running`。通过 outer → inner K3s 的 `kubectl exec` 实际执行：

```text
free -h: Mem total 60.5G, free 46.7G
top -b -n 1: 可见 PID 1、top、head
ps -ef: 可见 PID 1 与 exec 进程
```

这证明 `/proc/meminfo` 和进程目录均为真实可读数据，而非空目录。`tests/sysbox-in-sysbox-218.sh` 已将三条命令纳入成功条件后的输出，后续复测会同时覆盖 Pod Running 与 procfs 工具。

## 本次文件改动说明

| 文件 | 修改目的 |
| --- | --- |
| `sysbox-runc/libcontainer/configs/config.go` | 增加专用 nested command-mode 配置标记。 |
| `sysbox-runc/libcontainer/specconv/spec_linux.go` | 从 `sysbox-runc-inner` wrapper 环境生成该标记，避开 CRI 注解丢失。 |
| `sysbox-runc/libcontainer/{container_linux.go,standard_init_linux.go}` | 为无 `/proc` 的子进程保留 exec FIFO 启动握手，避免 `ContainerCreating` 卡住。 |
| `sysbox-runc/libcontainer/{init_linux.go,rootfs_init_linux.go,rootfs_linux.go}` | 专用模式跳过不适用的 sysfs 与 Sysbox-FS overmount；procfs 在复用 outer user namespace 后由 inner init 正常挂载，`init_linux.go` 同时保证 `kubectl exec` 可用。 |
| `sysbox-runc/libsysbox/syscont/spec.go` | 专用模式不创建第三层 user namespace，并跳过其 UID/GID mapping、rootfs UID shifting。 |
| `sysbox-runc/libcontainer/process_linux.go` | 无 UID/GID mapping 时以零值注册 Sysbox-FS，修复 registration panic。 |
| `sysbox-runc/libcontainer/process_linux.go` | 调整专用模式父子同步、FD 生命周期与诊断轨迹。 |
| `sysbox-runc/libsysbox/syscont/spec.go` | 专用模式跳过隐式特殊目录卷，并将 inner 映射降为 `1:65535`。 |
| `sysbox-runc/libsysbox/syscont/spec_test.go` | 覆盖特殊目录和映射相关的行为。 |
| `sysbox-mgr/utils.go` | 启动 mgr 时保留专用模式的 `1:65535` subuid/subgid，而不重写成默认 65536 块。 |
| `sysbox-mgr/subidAlloc/subidAllocSimple.go` | 允许专用模式分配 65535-ID 范围。 |
| `sysbox-fs/seccomp/mount.go` | 对 child user namespace 的内层 procfs mount 让内核原地执行，避免外层 nsenter 代理导致 `EINVAL`。 |
| `sysbox-fs/seccomp/tracer.go` | 仅放行 child user namespace 的 procfs 请求通过 capability 预检，让受控 nsenter 代理实际执行挂载。 |
| `sysbox-pkgr/k8s/Dockerfile.sysbox-k3s` | 向部署镜像加入 nested command-mode 所需静态工具。 |
| `sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh` | 支持部署命令模式的内层 Sysbox。 |
| `sysbox-pkgr/k8s/scripts/sysbox-inner-k3s.sh` | 新增：在无 systemd K3s 容器内直接启动 mgr、fs、containerd handler，并把 nested 环境传给 mgr。 |
| `w7panel-doc/tests/sysbox-in-sysbox-218.sh` | 新增：固定选择 Running outer Pod，等待 inner K3s 就绪后创建 RuntimeClass/pause Pod 并断言 Running。 |
| `w7panel-doc/Dockerfile.sysbox-inner-helper` | 从已验证 helper 最小替换 runc/fs，确保 218 PoC 使用当前嵌套修复二进制。 |
| `CHANGELOG.md`、`sysbox-runc/CHANGELOG.md`、`sysbox-mgr/CHANGELOG.md` | 记录改动、触发条件和预期效果。 |
