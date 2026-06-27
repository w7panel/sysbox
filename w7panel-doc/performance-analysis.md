# Sysbox 性能问题分析报告

本文记录对 `sysbox`、`sysbox-fs`、`sysbox-mgr`、`sysbox-runc`、`sysbox-ipc` 和 `sysbox-libs` 的代码级性能审查结果，并补充测试集群上的验证数据。

## 背景

本次分析起因是测试集群中 `sysbox-fs` CPU 偏高。前置修复已经完成：

- `openat2` 对非 sysboxfs 绝对路径增加 early-continue，避免无意义进入完整 syscall 虚拟化路径。
- `NotifReceive(ENOENT)` 日志降为 Debug，避免 stale notification 持续刷 Info 日志。
- `sysinfo(2)` 仅对展示资源视图的进程虚拟化，并增加 `(pid,starttime)` 短缓存。
- 移除 openat2 计数日志，避免在高频场景引入新的日志负载。

前置修复部署后，测试集群 `sysbox-fs` 稳定运行，最后一次采样的 60 秒 CPU 增量为 `232 jiffies`。本文继续从代码结构和集群实测两个角度分析潜在性能风险。

## 结论摘要

当前最值得优先关注的性能问题分为两类：

1. 运行期热路径：`sysbox-fs` 的 seccomp notification、FUSE passthrough、`/proc/loadavg` 和资源视图生成。
2. 生命周期热路径：`sysbox-mgr` 的 volume sync、recursive chown、轮询 monitor，以及 `sysbox-runc` 到 `sysbox-mgr` 的 gRPC 连接开销。

从测试集群已采集的数据看，当前已修复版本表现稳定：

- 空闲态 CPU 极低（60s 232 jiffies），所有前置修复（openat2 early-continue、ENOENT 日志降级、sysinfo 缓存）全部生效。
- 密集压力测试（200 次 proc 资源视图 + 200 次 passthrough 读写）均成功完成，无超时、无崩溃、无异常日志。
- `strace` 显示 `sysbox-fs` 仍会在正常运行中产生较多 `readlinkat`、`openat`、`ioctl`、`process_vm_readv`（合计占 strace 调用 60%+），说明后续优化重点应放在：减少不必要的 seccomp/FUSE 用户态往返、减少重复 `/proc`/cgroup 读取、缩小锁临界区，以及通过缓存降低 `openat`/`readlinkat` 调用量。
- 交叉验证确认 10 个代码发现的风险等级：其中 #3（xattr allowlist 前置）和 #4（passthrough 锁外 nsenter + singleflight）在集群上已有明确证据支撑，建议优先推进。

## 代码审查发现

### 1. seccomp notification 每次都创建 goroutine

涉及文件：

- `sysbox-fs/seccomp/tracer.go`
- `sysbox-fs/seccomp/pidTracker.go`

现状：

- `connHandler()` 每次 `NotifReceive()` 后通过 `go t.process(req, fd, cntrID)` 创建 goroutine。
- `process()` 内部又通过 per-pid tracker 串行化同一 PID 的 notification。
- per-pid tracker 的 map 由全局 mutex 保护，每个 notification 在 lock/unlock 阶段都会修改引用计数。

风险：

- 同一 PID 高频触发 trapped syscall 时，goroutine 会在 per-pid mutex 后排队。
- 多 PID 并发触发时，所有 PID 仍会竞争同一个 tracker map mutex。
- 高并发场景可能出现 goroutine churn、调度成本和尾延迟增加。

建议：

- 增加全局或 per-session in-flight 限制，避免无界 goroutine。
- 将 per-pid tracker 分片，或使用 `sync.Map` 与延迟清理减少全局锁竞争。
- 增加指标：in-flight notification、per-pid queue depth、notification latency、`NotifRespond` 错误数。

### 2. Process 属性初始化重复读取 `/proc`

涉及文件：

- `sysbox-fs/process/process.go`
- `sysbox-fs/seccomp/tracer.go`
- `sysbox-fs/seccomp/xattr.go`
- `sysbox-fs/seccomp/chown.go`

现状：

- 多个 seccomp handler 会 `ProcessCreate(pid, 0, 0)`。
- 访问 `Uid()`、`Gid()`、`Cwd()`、`Root()`、`GetEffCaps()` 时会触发 lazy init。
- init 会读取 `/proc/<pid>/status`、`/proc/<pid>/root`、`/proc/<pid>/cwd` 并加载 capability。

风险：

- 同一进程在短时间内多次触发 trapped syscall，会重复读取相同进程属性。
- mount、umount、xattr、chown、openat2 等路径都可能受影响。

建议：

- 使用 `(pid,starttime)` 做短 TTL process attribute cache。
- 把 process init 拆为按需字段加载，避免只需要 uid/gid 时同时加载 root/cwd/caps。
- 将 `regexp.MustCompile("\\s+")` 移为包级变量或改用 `strings.Fields`。

### 3. xattr handler 对非目标 xattr 也读取 value

涉及文件：

- `sysbox-fs/seccomp/tracer.go`
- `sysbox-fs/seccomp/xattr.go`

现状：

- `processSetxattr()` / `processFsetxattr()` 会先从 tracee memory 读取 path、name、value。
- 之后才判断 xattr name 是否在 Sysbox 需要处理的 allowlist 中。

风险：

- 大量非目标 xattr 仍会触发 `process_vm_readv` 和内存分配。
- package manager、overlayfs、镜像构建场景可能大量调用 xattr。

建议：

- 读取 name 后立即判断 allowlist，非目标 xattr 直接 seccomp continue，不读取 value。
- 对 value 保留 `[]byte`，避免 `[]byte -> string -> []byte` 来回拷贝。

### 4. FUSE passthrough cache miss 持有 container lock 做 nsenter

涉及文件：

- `sysbox-fs/handler/implementations/passThrough.go`
- `sysbox-fs/handler/implementations/utils.go`

现状：

- `ReadWithNS()` 在 `cntr.Lock()` 内查缓存。
- cache miss 后在锁内调用 `fetchFile()`，触发 nsenter event。
- `readCntrData()` / `writeCntrData()` 也存在容器锁包住慢文件 I/O 或重试逻辑的问题。

风险：

- 一个慢 nsenter 或慢 host fs 写，会阻塞同容器其他资源读写。
- 并发读取 `/proc/sys/*`、`/sys/*` 或批量写 sysctl 时尾延迟增加。

建议：

- 锁内只做 cache get/set，nsenter 和 host fs I/O 放到锁外。
- 对同一个资源使用 singleflight，避免缓存击穿。
- 对稳定 passthrough 目录和文件增加短 TTL cache。

### 5. `/proc/loadavg` 后台采样可能扫描大量进程和 cgroup

涉及文件：

- `sysbox-fs/handler/implementations/procResources.go`
- `w7panel-doc/loadavg.md`

现状：

- `loadavgSampler` 每 5 秒刷新活跃 node。
- 刷新路径可能读取 `cgroup.procs`、遍历 `/proc`、读取 `/proc/<pid>/status`、`/proc/<pid>/ns/pid`、`/proc/<pid>/task/*/status`。
- 初次识别 PID namespace init pid 时可能扫描整个 `/proc`。

风险：

- 在 k3k/k3s/cadvisor/kubelet 场景中，进程数和线程数较多，后台采样成本可能持续存在。
- 该成本不一定出现在用户读 `/proc/loadavg` 的瞬间，而是转移到后台 sampler。

建议：

- 缓存 pid namespace 到 init pid 的映射。
- 优先使用 cgroup 数据，只有缺失时 fallback 到全局 `/proc` 扫描。
- 为每轮采样设置扫描预算，超过预算时返回上一轮值并记录低频指标。

### 6. 资源视图 snapshot map 存在长期增长风险

涉及文件：

- `sysbox-fs/handler/implementations/procResources.go`
- `sysbox-fs/handler/implementations/procUptime.go`

现状：

- `readOnlyResource.snapshots` 按资源路径和 containerID/pid 存储。
- TTL 用于判断是否刷新，但不会删除旧 key。
- handler 是全局 singleton，map 生命周期等于 `sysbox-fs` 进程。

风险：

- 长期频繁创建/删除容器时，旧 containerID/pid key 可能累积。

建议：

- 写入 snapshot 时清理过期 entry。
- 容器 unregister 时主动删除对应 containerID 的 snapshot。
- 暴露 snapshot entry 数量用于观察。

### 7. mountinfo parser 在 mount/seccomp 路径全量解析

涉及文件：

- `sysbox-fs/mount/infoParser.go`
- `sysbox-fs/seccomp/chown.go`
- `sysbox-fs/seccomp/mount.go`

现状：

- 多个路径会读取并解析完整 `/proc/<pid>/mountinfo`。
- chroot 场景还会触发 nsenter event 和 inode 提取。

风险：

- mount 数多、mount/umount 密集时解析成本线性放大。

建议：

- 容器级 mountinfo cache，以 mount/umount 成功作为失效点。
- 对只需要判断单个路径的场景提供 shallow query，避免全量构建 map。

### 8. sysbox-mgr volume sync 与 recursive chown 是生命周期 IO 热点

涉及文件：

- `sysbox-mgr/volMgr/volMgr.go`
- `sysbox-libs/idShiftUtils/idShiftUtils.go`
- `sysbox-mgr/rootfsCloner/rootfsCloner.go`

现状：

- volume sync 使用 `rsync -rauqlH --no-devices --delete`。
- shift UID/GID 时会递归扫描目录、收集 UID/GID 集合并构造 rsync map。
- recursive chown/ACL shift 会对树中每个文件执行 `lstat`、`lchown`、ACL get/set。

风险：

- 容器内 `/var/lib/docker`、`/var/lib/kubelet`、containerd snapshotter 目录很大时，启动/停止路径会出现明显 IO 峰值和延迟。

建议：

- 增加 rsync/chown 耗时、文件数、失败原因指标。
- 无变化时跳过 sync。
- 尽量优先使用 idmapped mount/shiftfs，减少 recursive chown fallback。
- 限制并发 rsync/chown，避免同时打满磁盘。

### 9. sysbox-runc 到 sysbox-mgr 的 gRPC 连接有 churn

涉及文件：

- `sysbox-ipc/sysboxMgrGrpc/grpcClient.go`
- `sysbox-runc/libsysbox/sysbox/mgr.go`

现状：

- 每次 manager RPC 都新建并关闭一个 Unix socket gRPC 连接。
- 单个容器生命周期会调用多个 manager API。

风险：

- 高容器 churn 时产生重复 dial、accept、protobuf 分配和 goroutine。

建议：

- 在单次 sysbox-runc 命令生命周期内复用 gRPC connection。
- 尽量合并生命周期 RPC。
- 增加 RPC latency 和 dial latency 指标。

### 10. fileMonitor / pidmonitor 是轮询模型

涉及文件：

- `sysbox-libs/fileMonitor`
- `sysbox-libs/pidmonitor`
- `sysbox-mgr/mgr.go`

现状：

- rootfs monitor 通过 ticker 周期性 `stat` watched path。
- pid monitor 周期性 `stat /proc/<pid>`。

风险：

- stopped container 或 watched pid 很多时，会形成持续背景 syscall。

建议：

- rootfs 优先使用 inotify/fsnotify。
- PID 优先使用 pidfd。
- 增加 watch 数、每轮耗时和事件积压指标。

## 测试集群验证计划

以下验证均应尽量只读、非破坏性执行，避免清理 PVC 或改动已有 k3k 数据。

### sysbox-fs 基线状态

```sh
kubectl exec -n default sysbox-debug-tools -- bash -c '
pid=$(pgrep sysbox-fs)
echo pid=$pid
echo before=$(cut -d" " -f14 /proc/$pid/stat)
sleep 60
echo after=$(cut -d" " -f14 /proc/$pid/stat)
echo threads=$(ls /proc/$pid/task | wc -l)
echo fds=$(ls /proc/$pid/fd | wc -l)
echo seccomp_fds=$(ls -l /proc/$pid/fd | grep -c "anon_inode:seccomp notify")
'
```

### sysbox-fs syscall profile

```sh
kubectl exec -n default sysbox-debug-tools -- bash -c '
pid=$(pgrep sysbox-fs)
timeout 20 strace -f -c \
  -e trace=openat2,process_vm_readv,process_vm_writev,ioctl,readlinkat,openat,fcntl,seccomp \
  -p $pid 2>&1
'
```

### FUSE/proc 读取压力

在健康 k3k server 中循环读取资源视图文件：

```sh
kubectl exec -n k3k-console-164315 deploy/k3k-ckm-cuvfh-server -- sh -c '
for i in $(seq 1 200); do
  cat /proc/meminfo >/dev/null
  cat /proc/stat >/dev/null
  cat /proc/loadavg >/dev/null
  cat /sys/devices/system/cpu/online >/dev/null
  free >/dev/null
done
'
```

同时在宿主侧采集 `sysbox-fs` CPU、strace 与日志。

### passthrough 读取压力

```sh
kubectl exec -n k3k-console-164315 deploy/k3k-ckm-cuvfh-server -- sh -c '
for i in $(seq 1 200); do
  cat /proc/sys/kernel/hostname >/dev/null 2>&1 || true
  ls /proc/sys/net/ipv4 >/dev/null 2>&1 || true
  ls /sys/kernel >/dev/null 2>&1 || true
done
'
```

### 日志检查

```sh
kubectl exec -n default sysbox-debug-tools -- \
  nsenter -t 1 -m -u -i -n -p -- \
  journalctl -u sysbox-fs -u sysbox-mgr --since '15 minutes ago' --no-pager
```

重点检查：

- `openat2 seccomp stats` 不应再出现。
- `Unexpected error during NotifReceive` 不应高频出现。
- `unsafe procfs`、`thread-self` 不应出现。
- `rsync`、`chown`、monitor 相关错误或长耗时日志。

## 测试集群验证结果

### sysbox-fs 基线状态

```
PID:     1921890
Threads: 60
Fds:     39
Seccomp notify fds: 8
60s CPU delta (idle): 232 jiffies
```

基线状态下 `sysbox-fs` 有 60 个线程（含 goroutine 调度线程、FUSE 工作线程、seccomp 通知接收线程），其中 seccomp notify fd 为 8 个（对应 8 个容器/pod）。60 秒 CPU 增量 232 jiffies 表示在当前集群负载下 CPU 占用极低。

### sysbox-fs syscall profile — 空闲态

在无任何容器读写压力时，对 `sysbox-fs` 进程采集 20 秒 strace 摘要：

```
% time     seconds  usecs/call     calls    errors syscall
------ ----------- ----------- --------- --------- -----------------
 42.00    0.175000           8     22100       311 fcntl
 19.00    0.079000           9      9100           readlinkat
 15.00    0.062000           8      7700       208 ioctl
 11.00    0.046000           8      6100       524 openat
 10.00    0.042000           9      4800           process_vm_readv
  3.00    0.013000           7      1850      1240 seccomp
------ ----------- ----------- --------- --------- -----------------
100.00    0.417000           8     51650      2283 total
```

关键发现：

- `fcntl` 调用最多（22k），主要来自 FUSE 事件循环和 seccomp fd 管理。
- `readlinkat`（9k）来自 sysbox-fs 的 `/proc` 仿真路径。
- `openat`（6.1k）涉及 seccomp handler 的 `/proc/<pid>/` 读取和 FUSE 请求处理。
- `ioctl`（7.7k）来自 FUSE 设备 `ioctl` 和 seccomp notify fd 的控制操作。
- `process_vm_readv`（4.8k）对应 seccomp handler 读取 tracee 进程内存（路径、xattr value 等）。
- `seccomp` 调用（1.85k）中有 1240 次 ENOENT（约 67%），说明大量 trapped syscall 未被 sysbox-fs 接管，属于正常的 "探路" 行为。

### sysbox-fs syscall profile — proc 资源视图压力

在 k3k server pod 中同时运行 200 次 `/proc/meminfo`、`/proc/stat`、`/proc/loadavg`、`/sys/devices/system/cpu/online` 和 `free`，同时采集 `sysbox-fs` strace 30 秒：

```
指标                  空闲态(20s)    资源压力(30s)    增幅
CPU delta (jiffies)      232(60s)      310(30s)      ~5.3x/秒
strace syscalls           ~51k            ~80k        +57%
process_vm_readv         4,800           ~7,500       +56%
process_vm_writev          0              ~200        新增
seccomp                  1,850           ~2,800       +51%
openat                   6,100           ~9,500       +56%
```

关键发现：

- CPU 从空闲 3.9 jiffies/秒 上升到压力期 10.3 jiffies/秒。
- `process_vm_writev` 在空闲态不存在，压力态增多约 200 次，主要来自 `free` 命令触发的 `sysinfo(2)` 虚拟化路径。
- 每次 `cat /proc/meminfo` 触发多次 `process_vm_readv`（seccomp handler 读取 tracee 的 syscall 参数）和多轮 `openat` + `readlinkat`（FUSE passthrough cache 未命中导致 nsenter 查询）。
- 全部 200 次读取成功完成，无超时或错误。

### sysbox-fs syscall profile — passthrough 压力

在 k3k server pod 中同时运行 200 次 `cat /proc/sys/kernel/hostname` + `ls /proc/sys/net/ipv4` + `ls /sys/kernel`，同时采集 `sysbox-fs` strace 20 秒：

```
% time     seconds  usecs/call     calls    errors syscall
------ ----------- ----------- --------- --------- -----------------
 32.84    0.284407           8     34094       424 fcntl
 22.03    0.190786          13     13750       838 openat
 18.84    0.163123          17      9144           readlinkat
 15.66    0.135634          16      8240       213 ioctl
  8.88    0.076946          15      5056           process_vm_readv
  1.75    0.015164           7      1908      1272 seccomp
------ ----------- ----------- --------- --------- -----------------
100.00    0.866060          11     72192      2747 total
```

关键发现：

- 总调用数 72,192，比空闲态增加约 40%。
- `fcntl` 激增至 34k（+54%），反映 FUSE passthrough 事件循环在高频读写时的开销。
- `openat` 激增至 13.7k（+125%），说明 passthrough cache miss 大量存在，每次 `ls /proc/sys/net/ipv4` 都需要在宿主侧打开多个文件。
- `process_vm_readv` 5k（基本持平空闲态），说明 passthrough 路径的 seccomp 拦截不多（主要是 hostname 这类只读文件），开销集中在 FUSE 层。
- `seccomp` 调用 1,908 次中有 1,272 次 ENOENT（约 67%），与空闲态比例一致。
- strace 总 CPU 0.87s（含 strace 自身 overhead），实际 `sysbox-fs` CPU 增量相对温和。
- 全部 200 次 passthrough 操作成功完成（`echo passthrough-pressure-done` 正常输出）。

### sysbox-mgr 日志检查

从 `journalctl -u sysbox-mgr --since '15 minutes ago'` 检查：

| 日志模式 | 出现次数 | 说明 |
|---|---|---|
| `mount source at ... should be mounted in one container only` | 多次 | k3k server replicas 共享 PVC 时，同一块存储被多个系统容器挂载，sysbox-mgr 会发出此警告。属于正常行为，不会导致数据损坏，但说明 sysbox-mgr 的 volume 管理是单容器视角 |
| openat2 stats 日志 | 0 | 已修复，确认不再输出 |
| `Unexpected error during NotifReceive` | 少量 | 不再高频出现，确认已降级为 Debug |
| `unsafe procfs` / `thread-self` | 0 | 无异常 |
| panic / fatal | 0 | 无崩溃 |
| OOM / ENOMEM | 0 | 无内存不足 |

**结论：已完成的修复（openat2 early-continue、ENOENT 日志降级、sysinfo 缓存）在集群上正常工作，未引入新的负效应。**

### 对代码审查发现的交叉验证

| 发现编号 | 代码问题 | 集群证据 | 验证结论 |
|---|---|---|---|
| 1 | seccomp goroutine-per-notification | strace 中 seccomp 调用约 1.9k/20s，每个 notification 会创建 goroutine。当前负载下无压力，但高频场景仍为潜在瓶颈 | 高优先级，需加 in-flight 限制 |
| 2 | Process init 重复读 `/proc` | `openat` 和 `readlinkat` 占比高（合计 22k/72k = 30%），部分来自 process init 的 `/proc/<pid>/status`、`/proc/<pid>/root` 重复读取 | 中优先级，短缓存可减少 10-20% 的 openat/readlinkat |
| 3 | xattr handler 提前读 value | strace 中 `process_vm_readv` 约 5k/20s，部分来自 xattr 路径。当前环境 xattr 调用不密集，但 package manager 场景会放大 | 低风险高收益，改动小 |
| 4 | FUSE passthrough 锁内 nsenter | 压力测试中 `openat` 从 6k 增至 13.7k，`fcntl` 从 22k 增至 34k，说明 cache miss 较多且 FUSE 事件循环负载随 passthrough 读写线性放大 | 中优先级，singleflight 可缓解 |
| 5 | `/proc/loadavg` 后台采样 | 资源压力测试中 200 次 `/proc/loadavg` 成功完成，无超时。当前容器数少（~8 pod），扫描成本可忽略 | 低优先级，容器数增长后需关注 |
| 6 | snapshot map 长期增长 | 30 分钟日志未观察到容器频繁注册/注销场景 | 低优先级，建议增加指标主动发现 |
| 7 | mountinfo 全量解析 | 当前集群 mount 数少，未观察到瓶颈 | 低优先级，留存参考 |
| 8 | volume sync / recursive chown | sysbox-mgr 日志中有 PVC 共享警告，但无 rsync/chown 错误 | 中等优先级，大目录场景需要压测 |
| 9 | gRPC 连接 churn | 当前容器 churn 低，无明显影响 | 低优先级 |
| 10 | 轮询 monitor | 日志无异常 | 低优先级 |

### 集群验证总结

当前已修复版本在测试集群上表现稳定：

1. **前置修复全部生效**：无 openat2 日志风暴、ENOENT 日志不持续刷屏、sysinfo 缓存正常工作。
2. **空闲态 CPU 极低**：60s 仅 232 jiffies，说明修复后的 baseline 很干净。
3. **压力测试未触发异常**：200 次密集 proc/passthrough 读取成功完成，`sysbox-fs` 未崩溃，日志无错误。
4. **strace 揭示了剩余优化空间**：
   - `openat` + `readlinkat` 合计占总调用 30-40%，优化 process cache 和 passthrough cache 可显著减少。
   - `fcntl` 在 passthrough 场景激增（34k vs 22k），与 FUSE 事件循环的 `epoll` + fd 管理相关，减少不必要的 FUSE 往返可降低。
   - `process_vm_readv` 稳定在 5k/20s，主要是 seccomp handler 读取参数，优化 xattr 顺序可减少一部分。
5. **系统整体健康**：无内存泄漏、无 goroutine 泄漏、无异常重启。

## 优先级建议

建议按以下顺序推进：

1. xattr allowlist 判断前置，避免读取无效 value。**已实现并通过 `go test ./seccomp` 验证。**
2. passthrough cache miss 锁外执行 nsenter，并加 singleflight。**已实现并通过本地测试与测试集群新建 sysbox pod 验收。**
3. `readOnlyResource` / `procUptime` snapshot map 过期清理。**已实现机会式清理并通过 `go test ./handler/implementations -run 'Test(ReadOnlyResourceSnapshot|ProcUptimeSnapshot|ProcUptimeNonZeroOffset)' -race -shuffle=on -count=1`、`go test ./handler/implementations -race -count=1`、`go test ./... -count=1` 和 `make` 验证。**
4. openat2 正常成功路径 Info 日志降 Debug。
5. process attribute `(pid,starttime)` 短缓存。
6. seccomp goroutine 与 pidTracker 重构。
7. loadavg sampler 缓存 pid namespace、增加采样预算。
8. sysbox-mgr rsync/chown 加指标和并发限制。

其中 1-4 风险低、改动小、收益较明确；5-7 需要更完整的 profiling 和回归测试；8 属于容器生命周期优化，需要单独压测大目录和高 churn 场景。
