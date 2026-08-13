# CKM 内层 K3s 运行 Sysbox（实验性 PoC）

> 2026-08-11 更新：`sysbox-runc-inner` 与 inner `sysbox-mgr` 已实现 `--mapping-mode nested-identity`、独立 child userns 和 `0 0 65536`。真实 L1 Docker 验证随后确认，继承 L0 Sysbox seccomp user-notify listener 的进程树无法再创建 L1 listener（内核返回 `EBUSY`），因此完整 L2 systemd/Docker/K3s 尚未通过。原 `SYSBOX_SKIP_SPECIAL_MOUNTS=true`、共享 outer userns 和 `1:65535` allocator 路径仅保留为历史 PoC。

> 上游 Sysbox 不支持 Sysbox nesting。本方案仅用于 218 验证，不可作为生产功能启用。

## 目标与边界

外层 CKM server Pod 使用 `runtimeClassName: sysbox-runc` 和 `hostUsers: false`；其内运行的 K3s 不含 systemd。当前目标只验证：目标 Pod 创建容器时同时满足以下两个条件：

1. 使用 `sysbox-runc` runtime；
2. 使用独立 user namespace（Kubernetes 配置为 `hostUsers: false`，本文简称 `use-namespace`）。

不再把“目标 Pod 内启动 systemd，再由 systemd 启动 Docker”作为测试路径。目标是在不维护派生 K3s 镜像的前提下，在该 K3s 内启动 Sysbox runtime，并由目标 Pod 直接承载普通容器工作负载。

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

### 当前验收条件

目标 Pod 必须显式写出两个条件，缺一不可：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: sysbox-use-namespace
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  containers:
    - name: workload
      image: docker.cnb.cool/i0358/zpk/nested-pause:20260810-1
      command: ["/bin/sh", "-c", "sleep 3600"]
```

这里 `runtimeClassName: sysbox-runc` 决定 containerd 使用 Sysbox runtime；`hostUsers: false` 才会让 Pod 使用独立 user namespace。Kubernetes 没有名为 `use-namespace` 的标准 Pod 字段，因此 `use-namespace` 在本文中是验收条件名称，不是额外 YAML key。不得只检查 Pod 的 `Running` 状态，必须同时检查实际 Pod 配置和容器内的 user namespace：

```sh
kubectl get pod sysbox-use-namespace -o jsonpath='{.spec.runtimeClassName}{"\n"}{.spec.hostUsers}{"\n"}'
kubectl exec sysbox-use-namespace -- cat /proc/self/uid_map
kubectl exec sysbox-use-namespace -- readlink /proc/self/ns/user
```

预期结果为 `sysbox-runc`、`false`，以及非宿主初始 user namespace 的映射；若任一条件不满足，本次测试失败。

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
| 普通非 systemd 工作负载 | 直接启动业务进程，`runtimeClassName: sysbox-runc-inner` | 当前目标；只验证 Sysbox runtime 与独立 user namespace 两个条件，不启动 systemd 或 Docker daemon。 |

历史 systemd/Docker 镜像测试结果不再作为当前验收依据；此前的镜像 digest 和手动 `dockerd` 拉取记录仅用于追溯，不要求复测。

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
| 2026-08-12；临时 L1 `sysbox-nested-l1-v3`，当前构建 `sysbox-runc/sysbox-fs/sysbox-mgr`；L2 `nested-l2-v3` | L2 未进入容器进程：`failed to pre-register with sysbox-fs`。L1 `sysbox-fs` 日志先报缺少 `fusermount3`，补齐后又报 `fuse device not found`；L2 保持 `Created`，无 procfs、systemd 或 Docker 验收结果。 | 已保存 L1 日志与 inspect 到 `/tmp/sysbox-nested-handoff-20260812`，停止临时容器，并恢复宿主 `/usr/bin/sysbox-fs` 为 `sysbox-fs.host-original`。该次为 FUSE 设备/工具环境失败，不作为 seccomp listener 或 namespace 逻辑结论。 |
| 2026-08-12；`bash w7panel-doc/tests/sysbox-in-sysbox-218.sh`；目标 Pod 使用 `sysbox-runc-inner`、`hostUsers: false` | Pod 持续 `ContainerCreating`；sandbox 事件为 `failed to create network namespace`，原因是 `fork/exec /proc/self/exe: operation not permitted`。检查 outer K3s 容器确认 `/proc` 仍为 `rw,nosuid,nodev,noexec`。 | 未进入目标容器，因此没有 `uid_map` 或 procfs 验收结果；判定为 outer Pod 的 `sysbox/allow-proc-exec` 未传递到实际 K3s 容器 OCI spec。inner K3s 模板已补充 `sysbox-runc-inner` 的 `pod_annotations` 与 `container_annotations`，但需要用包含该模板的新版 helper/重建 outer Pod 后复测。 |

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

## 交接说明（2026-08-12）

### 当前代码状态

本轮工作仍未提交、未推送；根仓库及多个子模块均存在用户已有的未提交改动，接手时不得使用 `git reset --hard`、`git checkout --` 或批量清理。当前重点改动包括：

- `sysbox-runc/libcontainer/nsenter/nsexec.c`：加入 namespace 的顺序调整。加入 `pid/mnt/net/...` 后最后才进入目标 user namespace，避免进入 L2 userns 后再 `setns(pid)` 返回 `EPERM`；同时保留 nsexec log pipe，以便 EOF 时输出实际失败点。
- `sysbox-fs/nsenter/event.go`：将 `_LIBCONTAINER_LOGPIPE` 接入 nsenter 子进程，bootstrap 失败会带出 `setns`/同步错误，而不是只报告 `Error receiving first-child pid: EOF`。
- `sysbox-fs/seccomp/{syscall.go,mount.go,umount.go}`：为 nested 请求选择目标 namespace 路径，并记录 proc/sysfs helper 错误。nested 挂载必须进入 L2 的 mount/pid namespace；不能把 L1 的 `/proc` bind 到 L2。
- `sysbox-runc/libcontainer/rootfs_linux.go`：nested 模式在 rootfs early 阶段跳过 proc/sysfs 直接 mount，延迟到 init 进程发出 mount syscall 后由 seccomp-notify 处理。
- `sysbox-runc/utils_linux.go` 与 `libsysbox/syscont/spec.go`：nested 仍跳过 L1 的 Sysbox-FS 特殊 FUSE 注入和 L1 mgr `FsState`，但必须生成 mount/umount 的 seccomp-notify 规则。
- `sysbox-runc/libcontainer/init_linux.go`：nested 不允许安装第二个 seccomp-notify listener；内核会返回 `EBUSY`，因此尝试复用继承的 listener。
- `sysbox-runc/libsysbox/syscont/spec_test.go`：专用 wrapper 不再删除 user namespace；nested-identity 必须始终创建新的 child userns。

### 最新测试结论

记录交接时宿主上的 `sysbox-mgr`、`sysbox-fs`、Docker 均为 `active`，但 `/usr/bin/sysbox-fs` 仍是本轮临时测试构建（其 SHA-256 与 `/tmp/sysbox-nested-bin/sysbox-fs` 相同），尚未恢复 `/tmp/sysbox-nested-bin/sysbox-fs.host-original`。接手后已保存日志、停止临时容器，并恢复宿主二进制；使用当前构建的静态 `sysbox-runc`、`sysbox-fs`、`sysbox-mgr` 在临时 L1 中测试：

1. L1 使用 `nestybox-ubuntu-bionic-systemd-docker` 和 `/sbin/init` 启动成功，L1 映射为 `0 165536 65536`。
2. L1 内 `unshare -Urnm` 的独立 `sysfs` 与 `proc` 挂载均已成功，证明 L0 seccomp listener、nsenter 和 mount helper 的 namespace 顺序修复有效。
3. L2 首次启动曾在 `rootfs_linux.go` 直接挂载 sysfs 处失败：`mount through procfd: operation not permitted`；已改为延迟 mount。
4. 恢复 L2 mount/umount seccomp-notify 规则后，L2 尝试自行安装第二个 listener，内核明确返回：`error loading seccomp filter: device or resource busy`。这证明“L2 独立安装第二个 listener”不可行；最终实现必须让 L1/L2 共享或由 L0 统一管理 listener，并且还要确认 seccomp-notify fd 是否能跨 clone/userns 正确传递。
5. `sysbox-nested-l1-v3` 内的 `nested-l2-v3` 已进入 `running`，但 `/proc` 是空目录，`/proc/self/uid_map` 与 `/proc/1/comm` 均不存在，`systemctl is-system-running` 为 `offline`。这只证明 create/start 握手和容器进程存活，不能记作 systemd、Docker 或独立 procfs 验收通过。

### 接手后的首要任务

优先检查 `sysbox-runc` 的 seccomp-notify fd 生命周期：`process_linux.go` 中 L2 init 是否继承 L1 已安装的 listener fd，`standard_init_linux.go` 是否在 nested 模式错误地把 `SeccompNotif` 清空，父进程是否仍把 listener fd 注册到正确的 L0/L1 `sysbox-fs` tracer。目标是让 L2 的 `mount("proc")`、`mount("sysfs")`、`umount2` 被已有 listener 捕获，并由 L0 `sysbox-fs` 在 L2 mount/pid/user namespace 坐标中完成。

可重复的最小验证命令（在 L1 内）：

```sh
mkdir -p /tmp/sys /tmp/proc
unshare -Urnm sh -c 'mount -t sysfs sysfs /tmp/sys'
unshare -Urnm sh -c 'mount -t proc proc /tmp/proc'
```

完整 L2 验收必须同时检查：

```sh
cat /proc/self/uid_map          # nested-identity 目标为 0 0 65536
cat /proc/1/comm                # 应为 systemd，而不是 L1 的 PID 1
mount | grep -E ' on /(proc|sys) '
systemctl is-system-running
systemctl is-active docker
docker pull ccr.ccs.tencentyun.com/afan-public/nginx:latest
```

### 构建、替换与恢复

Go 缓存应放在 `/tmp`，避免写入只读的 `/root/go`。静态产物输出到 `/tmp/sysbox-nested-bin`，再挂入测试 L1。若临时替换宿主 `/usr/bin/sysbox-fs`，必须先备份并在测试结束恢复：

```sh
install -m 0755 /usr/bin/sysbox-fs /tmp/sysbox-nested-bin/sysbox-fs.host-original
install -m 0755 /tmp/sysbox-nested-bin/sysbox-fs /usr/bin/sysbox-fs
systemctl restart sysbox-fs

# 测试结束
install -m 0755 /tmp/sysbox-nested-bin/sysbox-fs.host-original /usr/bin/sysbox-fs
systemctl restart sysbox-fs
systemctl is-active sysbox-mgr sysbox-fs docker
```

临时容器命名通常使用 `sysbox-nested-l1-v3` 和 `nested-l2-v3`；清理前先确认状态和日志，避免在 sysbox-fs 正在处理 FUSE 请求时强制删除导致宿主服务卡住。所有测试记录需注明时间、helper 构建版本、L1 是否使用 `seccomp=unconfined`、L2 Pod/容器状态、关键错误和恢复动作。

## 交接补充（2026-08-12 后续测试）

### 本轮代码变更

为解决 L2 rootfs 初始化阶段直接挂载 proc/sysfs 返回 `EPERM`，`sysbox-runc/libcontainer/standard_init_linux.go` 新增 `mountNestedSpecialFilesystems()`：在 L2 完成 rootfs 初始化并向父 runc 发送 `rootfsReady`、完成 sysbox-fs 注册后，按 OCI mount 配置主动发出 procfs/sysfs mount syscall。设计意图是让已有的 seccomp-notify listener 捕获这些 syscall，再由 sysbox-fs 在 L2 的 mount/pid namespace 中处理。

同时保留以下约束：

- nested rootfs early 阶段不直接 mount proc/sysfs；
- nested 不注入 L1 的 Sysbox-FS 特殊 FUSE mount，也不读取 L1 mgr `FsState`；
- nested 不安装第二个 seccomp-notify listener。内核实测第二次安装返回 `EBUSY`（`device or resource busy`）；
- nested 必须创建 child user namespace，映射目标为 `0 0 65536`；
- nsexec 加入 namespace 时最后进入 user namespace，避免进入 L2 userns 后再加入 PID/mount namespace 导致 `EPERM`。

相关构建测试通过：

```sh
GOPATH=/tmp/sysbox-go-path \
GOCACHE=/tmp/sysbox-go-build \
GOMODCACHE=/tmp/sysbox-go-mod \
go test -vet=off ./libcontainer/specconv ./libsysbox/syscont \
  ./libcontainer/cgroups/fs2 ./libcontainer/cgroups/systemd
```

### 最新临时 L1/L2 测试结果

当前临时容器为 `sysbox-nested-l1-v3`，其 L2 测试容器为 `nested-l2-v3`。L1 使用 `/sbin/init` 启动，inner `sysbox-mgr` 已能在安装 `rsync` 后创建 `/run/sysbox/sysmgr.sock`。但 L2 尚未进入 runc 挂载验证，最新失败发生在 inner sysbox-fs pre-registration：

```text
fusermount3: fuse device not found, try 'modprobe fuse' first
Container pre-registration error: unable to initialize fuseServer
```

此前还出现过：

```text
fusermount: exec: "fusermount3": executable file not found in $PATH
```

因此这次失败属于测试镜像/运行环境依赖问题，不是 proc/sysfs 延迟挂载逻辑本身。L1 必须同时满足：

```sh
test -c /dev/fuse
command -v fusermount3
rsync --version
```

旧版 Ubuntu 镜像通常只有 `/bin/fusermount`；可在测试环境建立 `fusermount3 -> /bin/fusermount`，但仍必须确认 `/dev/fuse` 在 inner L1 内可用，并且 FUSE 设备/模块权限没有被外层 seccomp 或设备策略拦截。若 `fusermount3` 报 `fuse device not found`，应先检查：

```sh
ls -l /dev/fuse
test -e /sys/module/fuse || modprobe fuse
```

不能把此状态记录为 L2 启动成功。

### 继续测试顺序

1. 在全新 L1 中确认 `/dev/fuse`、`fusermount3`、`rsync`，再启动 inner mgr/fs；不要在 Docker daemon 重启后假定手工启动的 inner 服务仍存在。
2. 确认 `/run/sysbox/sysmgr.sock`、`/run/sysbox/sysfs.sock` 后，重新注册 `sysbox-runc-inner` runtime，再创建 L2。
3. L2 成功启动后检查：

   ```sh
   ls -ld /proc /sys
   cat /proc/self/uid_map
   cat /proc/1/comm
   mount | grep -E ' on /(proc|sys) '
   systemctl is-system-running
   ```

4. 每次失败都记录 helper 构建时间、L1 是否使用 `seccomp=unconfined`、inner mgr/fs 日志和容器状态；特别区分 `fusermount3`/`/dev/fuse` 环境错误与 seccomp listener、namespace、rootfs mount 逻辑错误。

### 当前宿主恢复提示

本轮测试曾临时替换宿主 `/usr/bin/sysbox-fs`。结束测试前必须恢复备份并重启服务：

```sh
install -m 0755 /tmp/sysbox-nested-bin/sysbox-fs.host-original /usr/bin/sysbox-fs
systemctl restart sysbox-fs
systemctl is-active sysbox-mgr sysbox-fs docker
```

临时 L1 已清理；后续如重启测试，清理前先保存 `/var/log/sysbox-inner/{mgr,fs,runc}.log`，避免直接强制删除导致 FUSE 请求残留。文档和代码均未提交、未推送。
