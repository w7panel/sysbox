# sysinfo(2) 本地虚拟化说明

本文记录 Sysbox 对 `sysinfo(2)` 的虚拟化实现、本地验证方式，以及为什么 `free -h` 会从宿主内存变为容器 cgroup 限制视图。

## 背景

`free` 不只读取 `/proc/meminfo`。BusyBox `free`、procps `free` 这类工具通常会直接调用 `sysinfo(2)` 获取内存和 swap 信息。

`sysinfo(2)` 是内核 syscall，不是读取某个固定文件。因此只虚拟化 `/proc/meminfo` 时会出现：

```text
cat /proc/meminfo 看到容器限制
free -h 仍看到宿主内存和宿主 swap
```

要让 `free -h` 也显示容器视图，需要在 syscall 层拦截 `sysinfo(2)`，并把返回给进程的 `struct sysinfo` 改成 cgroup 视图。

## 实现链路

当前实现分两部分：

1. `sysbox-runc` 给系统容器配置 seccomp notify，把 `sysinfo` 加入 syscall trap list。
2. `sysbox-fs` 收到 seccomp notification 后，在 `processSysinfo()` 中生成虚拟化后的 `unix.Sysinfo_t`，再通过 `process_vm_writev` 写回触发 syscall 的进程地址空间。

相关代码：

```text
sysbox-runc/libsysbox/syscont/syscalls.go
sysbox-fs/seccomp/sysinfo.go
sysbox-fs/handler/implementations/procResources.go
```

`sysinfo(2)` 的返回值来自 `SysinfoMemoryForPid(pid)`：

- `TotalRAM`：cgroup memory limit。
- `FreeRAM`：cgroup memory limit - cgroup memory usage。
- `SharedRAM`：优先来自 cgroup memory.stat 的 shmem。
- `TotalSwap`：cgroup swap limit。
- `FreeSwap`：cgroup swap limit - cgroup swap current。

cgroup v2 下 swap 读取规则：

```text
memory.swap.max     -> SwapTotal
memory.swap.current -> SwapUsed
```

如果 `memory.swap.max=0`，容器内 `free -h` 显示 `Swap: 0` 是正确结果，表示 kubelet 给该 Pod 写入的 swap 限制就是 0。

## 选择性拦截

最初全量拦截 `sysinfo(2)` 后，K3s 进程也会进入 sysbox-fs handler。由于 handler 会向触发进程的用户态地址写入 `struct sysinfo`，对长期运行的 Go/K3s 进程风险较高；之前远端 pod 曾看到 Go runtime 崩溃栈，真正异常是 `unexpected fault address 0x0` / `fatal error: fault`，不是 `runtime.gopark proc.go:460` 本身。

当前策略是只虚拟化用于展示资源视图的命令：

- `/proc/<pid>/exe` basename 是 `busybox`：虚拟化。
- `argv0` basename 是 `free`：虚拟化。
- `/proc/<pid>/comm` 是 `free`：虚拟化。
- `/proc/<pid>/exe` basename 是 `k3s`：明确跳过，交回内核真实 `sysinfo(2)`。
- 其他进程默认跳过，返回 seccomp continue。

这样 `free -h` 能看到容器限制，而 K3s 这类业务进程继续使用内核真实 syscall，降低崩溃风险。

## 本地验证记录

本地测试使用 `default/rancherk3s-pgaytddm` deployment：

```text
runtimeClassName: sysbox-runc
memory limit: 256Mi
```

安装新 `sysbox-fs` 后，如果未同步安装包含 `sysinfo` trap 的 `sysbox-runc`，现象是：

```text
/proc/meminfo 显示 262144 kB
free -h 仍显示宿主 7.8G / 4.0G swap
```

原因是 syscall 没有进入 sysbox-fs handler。需要确认 `/usr/bin/sysbox-runc` 已是包含 `Trap sysinfo in system containers` 的构建，并重建 Pod：

```sh
install -m 0755 /root/workspace/sysbox/sysbox-runc/build/amd64/sysbox-runc /usr/bin/sysbox-runc
k3s kubectl delete pod -n default -l app=rancherk3s-pgaytddm
```

同时安装 sysbox-fs：

```sh
make -C /root/workspace/sysbox/sysbox-fs
install -m 0755 /root/workspace/sysbox/sysbox-fs/build/sysbox-fs /usr/bin/sysbox-fs
systemctl restart sysbox-fs
k3s kubectl delete pod -n default -l app=rancherk3s-pgaytddm
```

最终本地验证结果：

```text
free -h:
Mem:   256.0M  359.0K  255.5M  0  189.0K  255.6M
Swap:       0       0       0

/proc/meminfo:
MemTotal: 262144 kB

/sys/fs/cgroup/memory.max:
268435456

/sys/fs/cgroup/memory.swap.max:
0

pod restartCount:
0
```

这里 `Swap: 0` 是因为该 Pod 的 `memory.swap.max=0`，不是 sysinfo 拦截逻辑把 swap 清零。

## 常用排查命令

确认容器内 `free` 实际二进制：

```sh
pod=$(k3s kubectl get pod -n default -l app=rancherk3s-pgaytddm \
  --field-selector=status.phase=Running \
  -o jsonpath='{.items[-1:].metadata.name}')

k3s kubectl exec -n default "$pod" -- sh -lc '
type free
command -v free
ls -l $(command -v free)
readlink -f $(command -v free)
'
```

确认 seccomp 已启用：

```sh
k3s kubectl exec -n default "$pod" -- sh -lc '
grep -E "Seccomp|NoNewPrivs" /proc/self/status
'
```

确认资源视图：

```sh
k3s kubectl exec -n default "$pod" -- sh -lc '
free -h
echo ---
head -5 /proc/meminfo
echo ---
cat /sys/fs/cgroup/memory.max
cat /sys/fs/cgroup/memory.current
cat /sys/fs/cgroup/memory.swap.max
cat /sys/fs/cgroup/memory.swap.current
'
```

确认 pod 没有因为 sysinfo 拦截重启：

```sh
k3s kubectl get pod -n default "$pod" -o jsonpath='
{range .status.containerStatuses[*]}name={.name} restartCount={.restartCount} ready={.ready}{"\n"}{end}
'
```

查看 sysbox-fs 日志：

```sh
journalctl -u sysbox-fs --no-pager --since '5 minutes ago' |
  rg 'Handled sysinfo|unknown container|warning|error'
```

`unknown container` 通常表示 sysbox-fs 重启后旧容器还在发 seccomp notification，但 sysbox-fs 内存中的注册状态已丢失。重建对应 Pod 后会恢复。若日志里的 container id 是旧 k3k server 这类无关容器，不影响新建测试 Pod。

## 注意事项

- `sysinfo(2)` 不读取 `/proc/meminfo`，所以改 `/proc/meminfo` 不会自动影响 `free -h`。
- 更新 sysbox-fs 后，需要重启 sysbox-fs 并重建 Pod，否则旧容器可能仍处于旧注册状态。
- 更新 sysbox-runc 后，必须重建 Pod，新进程才会带上新的 seccomp notify 规则。
- 如果想让 Pod 显示非 0 swap，需要先让 kubelet 给该 Pod 写入非 0 的 `memory.swap.max`；Sysbox 只展示 cgroup 限制，不负责给 Pod 分配 swap。
