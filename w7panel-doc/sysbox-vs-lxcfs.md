# Sysbox-FS 与 LXCFS 资源视图实现差异

本文对比当前 Sysbox-FS 与 LXCFS 在 procfs/sysfs 资源视图上的实现差异，并说明当前没有完全按 LXCFS 对齐的原因。

对比范围：

- /proc/cpuinfo
- /proc/diskstats
- /proc/meminfo
- /proc/stat
- /proc/swaps
- /proc/uptime
- /proc/slabinfo
- /proc/pressure/io
- /proc/pressure/cpu
- /proc/pressure/memory
- /sys/devices/system/cpu/online
- /proc/loadavg
- /sys/devices/system/cpu/present

说明：/sys/devices/system/cpu/online 在需求中重复出现一次，本文只列一次。

## 总体差异

LXCFS 的实现目标是长期维护一套接近内核 procfs/sysfs 格式的容器视图。它通常会：

- 以访问进程所在容器的 init/reaper PID 为入口定位 cgroup。
- 对每个打开文件维护 FUSE file handle 缓存，offset=0 生成内容，offset>0 从同一份缓存读取。
- 从 cgroup 原始统计中重建 proc 文件，必要时维护历史数据或后台采样。
- 在无法生成容器视图时，部分路径会回退宿主文件。

当前 Sysbox-FS 的实现目标更保守：

- 优先保证容器内常用工具可解析、数据自洽、不暴露宿主敏感细节。
- 使用 Go handler 按请求动态生成内容，近期已加入短 TTL snapshot，避免分块读取时内容不一致。
- 主要面向 cgroup v2，同时保留部分 cgroup v1 兼容读取。
- 对无法可靠从 cgroup 构造的数据，倾向输出空视图或保守模拟，而不是回退宿主完整信息。

## 路径对比

| 路径 | LXCFS 实现 | 当前 Sysbox-FS 实现 | 未完全对齐原因 |
|---|---|---|---|
| /proc/cpuinfo | 在 src/proc_cpuview.c 的 proc_cpuinfo_read() 中处理。结合 cpuset、cpu quota、架构/personality 过滤真实 CPU block，并重新编号。 | 在 procResources.go 的 readCPUInfo() 中读取宿主 /proc/cpuinfo，然后按 effectiveCPUCount() 截取前 N 个 CPU block 并重写 processor 编号。 | LXCFS 会按具体 cpuset CPU ID 过滤，例如 2,4,6；Sysbox 当前输出连续 0..N-1。这样能保证工具看到连续 CPU 视图，但没有完整保留 cpuset 物理 CPU 映射和架构特殊格式。 |
| /proc/diskstats | 在 proc_fuse.c 的 proc_diskstats_read() 中读取 blkio/io cgroup 统计，并映射为 diskstats 字段；失败时可能回退宿主 /proc/diskstats。 | 当前优先读取 cgroup v2 io.stat；cgroup v1 下读取 blkio.throttle/io_*、blkio.io_* 并生成主要 diskstats 字段；无可靠 cgroup 数据时输出空内容。 | 已对齐 LXCFS 的 v1/v2 cgroup 优先读取方向，但不回退宿主完整 diskstats，避免暴露宿主磁盘设备和 I/O 细节。 |
| /proc/meminfo | 在 proc_fuse.c 的 proc_meminfo_read() 中基于 memory cgroup、memory.stat、swap、zswap 生成大量字段。 | 当前基于 memory.max/current 或 v1 limit/usage 生成核心字段；同时读取 cgroup v2/v1 memory.stat 映射 active/inactive、cache、slab、dirty、writeback、anon、mapped、shmem、hugepage 等字段。 | 已比早期实现更接近 LXCFS，但 zswap、部分 THP/内核字段仍受 cgroup v2 可用字段限制，无法完整复刻 LXCFS 所有计算。 |
| /proc/stat | 在 proc_fuse.c 的 proc_stat_read() 和 proc_cpuview.c 的 cpuview_proc_stat() 中处理。它会读取 cpuacct/cpuset，维护历史增量，生成更真实的 per-cpu 统计。 | 当前基于容器 uptime、可见 CPU 数、cgroup CPU usage 生成 cpu/cpuN 行，保留非 CPU 行并把 btime 改成容器创建时间。 | LXCFS 的 CPU 统计需要历史缓存和更复杂的 cpuacct usage_all 逻辑。Sysbox 当前选择生成自洽视图，保证 /proc/uptime、/proc/stat、top 之间不冲突，但精度不等同 LXCFS。 |
| /proc/swaps | 在 proc_fuse.c 的 proc_swaps_read() 中结合 swap 配置、memory+swap limit、swappiness 输出虚拟 swap 行。 | 当前输出 header；如果能从 cgroup 读到明确 swap limit，则输出一条 virtual swap。 | Sysbox 还没有完整接管 swapon/swapoff，也没有 LXCFS 那套 swap 配置开关与 swappiness 处理，因此只做可确定场景。 |
| /proc/uptime | 在 proc_fuse.c 的 proc_uptime_read() 中使用 get_reaper_age() 作为 uptime，用 get_reaper_busy() 推算 idle。 | 当前使用容器创建时间计算 uptime，结合可见 CPU 数与 cgroup CPU usage 计算 idle，并对 offset 分块读取做 snapshot。 | LXCFS 以 reaper/PID1 年龄为核心。Sysbox 还配合 sysbox-runc time namespace offset 修正 /proc/[pid]/stat starttime，整体以容器创建时间为时间基准。 |
| /proc/slabinfo | 在 proc_fuse.c 的 proc_slabinfo_read() 中优先读取 cgroup memory.kmem.slabinfo，失败时可能回退宿主 slabinfo。 | 当前 cgroup v1 memory.kmem.slabinfo 可用时读取；否则输出静态 header。 | cgroup v2 通常没有等价 slabinfo 文件。Sysbox 不回退宿主完整 slabinfo，避免暴露宿主内核 slab 细节。 |
| /proc/pressure/io | 在 proc_fuse.c 的 proc_pressure_read() 中读取 cgroup pressure 文件，并支持 pressure write/poll 相关逻辑。 | 当前读取 cgroup v2 io.pressure；失败时回退 /proc/pressure/io；补齐了换行和 snapshot 读取。 | Sysbox 目前只实现 read 视图，没有实现 LXCFS 的 PSI trigger write/poll 行为。 |
| /proc/pressure/cpu | 同 /proc/pressure/io，读取 cpu.pressure，并支持 write/poll。 | 当前读取 cgroup v2 cpu.pressure；失败时回退 /proc/pressure/cpu。 | 差异同上：只实现读取，没有实现 PSI 事件触发器写入和 poll。 |
| /proc/pressure/memory | 同 /proc/pressure/io，读取 memory.pressure，并支持 write/poll。 | 当前读取 cgroup v2 memory.pressure；失败时回退 /proc/pressure/memory。 | 差异同上：只实现读取，没有实现 PSI 事件触发器写入和 poll。 |
| /sys/devices/system/cpu/online | 在 sysfs_fuse.c 的 sys_devices_system_cpu_online_read() 中基于 cpuset 与 CPU quota 输出可见 CPU。 | 当前通过 effectiveCPUCount() 输出连续 CPU 范围 0..N-1。 | LXCFS 更贴近 cpuset 实际 CPU ID。Sysbox 输出连续范围，和 /proc/cpuinfo、/proc/stat 的连续 CPU 编号保持一致，避免用户态工具看到非连续 CPU 时产生额外问题。 |
| /proc/loadavg | 在 proc_loadavg.c 的 proc_loadavg_read() 中使用 loadavg 后台结构与 cgroup hash 维护负载移动平均。 | 当前已实现 lazy-start 后台采样器：第一次读取时按访问进程 PID namespace/cgroup 注册 load node，后台每 5 秒刷新 running/total/last pid，并按 LXCFS 固定点 EWMA 算法生成 1/5/15 分钟 loadavg。 | 已对齐 LXCFS 的核心采样模型和计算公式。差异是 Sysbox 以 Go 全局 sampler 管理节点，没有复刻 LXCFS 的 pthread hash bucket 锁结构；节点回收使用 lastSeen TTL。 |
| /sys/devices/system/cpu/present | LXCFS 新 sysfs 逻辑可把 /sys/devices/system/cpu 下普通文件作为 SUBFILE 透传；online 有专门虚拟化。 | 当前新增 handler，输出与 online 一致的连续 CPU 范围 0..N-1。 | LXCFS 对 present 主要是 sysfs 子文件路径支持，不一定专门虚拟化为容器 CPU present。Sysbox 选择让 present 与 online/cpuinfo/stat 一致，避免容器看到宿主全部 CPU。 |

## 当前没有完全对齐 LXCFS 的主要原因

### 1. 架构不同

LXCFS 是一个独立 FUSE 文件系统，很多逻辑围绕 FUSE file handle、per-open 缓存、后台线程、cgroup 历史状态展开。

Sysbox-FS 当前是 Sysbox 系统容器运行时的一部分，handler 更偏请求式动态生成。直接移植 LXCFS 的后台状态机会显著增加复杂度，也会影响已有 sysbox-fs handler 模型。

### 2. 安全策略不同

LXCFS 在一些失败路径会回退宿主文件。例如 diskstats、slabinfo、pressure 在无法生成 cgroup 视图时可能直接读宿主文件。

Sysbox 当前更倾向隐藏宿主细节：

- /proc/diskstats 无可靠 cgroup 数据时输出空内容。
- /proc/slabinfo 无 cgroup slabinfo 时只输出 header。
- /proc/loadavg 不回退宿主 loadavg。

这样牺牲了信息完整度，但降低了宿主资源信息泄露风险。

### 3. cgroup v2 与内核能力限制

LXCFS 历史上对 cgroup v1 的 blkio、cpuacct、memory.kmem.slabinfo 等文件支持较多。

当前环境主要是 cgroup v2。cgroup v2 下：

- 没有完全等价的 memory.kmem.slabinfo。
- io.stat 字段和 /proc/diskstats 字段不是一一对应。
- cpu.stat 没有 LXCFS cpuacct.usage_all 那样直接的 per-cpu 历史视图。

因此 Sysbox 只能生成近似且自洽的资源视图，而不是完整复刻 LXCFS。

### 4. /proc/loadavg 后台采样差异

LXCFS 的 /proc/loadavg 不是简单读一个 cgroup 文件，而是维护 cgroup 级 loadavg 节点和历史移动平均。当前 Sysbox-FS 已实现后台采样器，采样周期和固定点 EWMA 公式与 LXCFS 对齐。

仍未完全逐行复刻 LXCFS 的地方：

- LXCFS 使用 pthread + hash bucket 多级锁；Sysbox 使用 Go goroutine + mutex。
- LXCFS 以 cgroup 目录递归深度扫描 cgroup.procs；Sysbox 优先按访问进程 PID namespace 扫描宿主 /proc，失败时再用容器 root /proc、进程树或 cgroup 估算。
- LXCFS 在 cgroup 消失时删除 node；Sysbox 使用 lastSeen TTL 回收长时间不再读取的 node。

### 5. CPU ID 语义取舍

LXCFS 更接近 cpuset 的真实 CPU ID。Sysbox 当前更偏“容器内连续 CPU 编号”：

- /proc/cpuinfo: processor 从 0 连续编号。
- /proc/stat: cpu0..cpuN 连续生成。
- /sys/devices/system/cpu/online: 输出 0..N-1。
- /sys/devices/system/cpu/present: 输出 0..N-1。

这样可以保证容器内工具看到的 CPU 视图一致，但没有表达宿主 cpuset 的真实 CPU 编号。

## 当前实现的验收状态

当前实现已经完成以下目标：

- 目标路径均可挂载到 sysboxfs。
- /proc/uptime 支持分块读取，不再导致 top 报 bad data in /proc/uptime。
- /proc/uptime、/proc/stat、/sys/devices/system/cpu/online、/sys/devices/system/cpu/present 使用同一可见 CPU/time 视图。
- /proc/loadavg 格式可解析，top/uptime 可正常运行。
- /proc/meminfo/free 能反映容器 memory limit。
- pressure 文件优先读取 cgroup v2 pressure。

当前仍未完全等同 LXCFS 的部分：

- /proc/loadavg 已实现后台采样和 EWMA 移动平均，但采样节点管理方式与 LXCFS 不同。
- /proc/stat 没有 LXCFS 的 per-cpu 历史增量精度。
- /proc/diskstats 已支持 cgroup v2 io.stat 与 cgroup v1 blkio 主要字段，但仍不回退宿主完整文件。
- /proc/slabinfo 在 cgroup v2 下仍是保守 header。
- pressure write/poll 未实现。
- CPU present/online 使用连续容器 CPU 编号，而不是保留宿主 cpuset CPU ID。

## 后续如果要继续向 LXCFS 对齐

建议按优先级推进：

1. 为 /proc/stat 增加 per-container CPU 历史增量，减少每次读取时的估算。
2. 继续扩展 /proc/diskstats 中 discard、queue time 等字段在不同内核/cgroup 文件上的兼容映射。
3. 评估是否需要 pressure write/poll；如果业务不依赖 PSI trigger，可继续只读。
4. 决定 CPU ID 策略：继续使用连续 CPU 编号，或改为保留 cpuset 原始 CPU ID。这个选择会同时影响 cpuinfo、stat、online、present。
