# Sysbox-FS 与 LXCFS 资源视图实现差异

本文对比当前 Sysbox-FS 与 LXCFS 在 procfs/sysfs 资源视图上的实现差异，并说明当前没有完全按 LXCFS 对齐的原因。LXCFS 源码核对基准为 /root/workspace/lxcfs。

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

当前 Sysbox-FS 的实现目标更保守，但不同路径的失败策略并不完全一致：

- 优先保证容器内常用工具可解析、数据自洽、不暴露宿主敏感细节。
- 使用 Go handler 按请求动态生成内容，近期已加入短 TTL snapshot，避免分块读取时内容不一致。
- 主要面向 cgroup v2，同时保留部分 cgroup v1 兼容读取。
- 对 loadavg、swaps 等路径倾向输出容器视图或空视图；对 diskstats、slabinfo、pressure、meminfo 等路径，在无法生成有效 cgroup 视图时仍可能回退宿主文件，以保持与传统 procfs/sysfs 可读性兼容。

## 路径对比

| 路径 | LXCFS 实现 | 当前 Sysbox-FS 实现 | 未完全对齐原因 |
|---|---|---|---|
| /proc/cpuinfo | 在 src/proc_cpuview.c 的 proc_cpuinfo_read() 中处理。结合 cpuset、cpu quota、架构/personality 过滤真实 CPU block，并重新编号。 | 在 procResources.go 的 readCPUInfo() 中读取宿主 /proc/cpuinfo，按 cpuset 过滤真实 CPU block，再按 effectiveCPUCount() 限制数量并把 processor 连续重写为 0..N-1。effectiveCPUCount() 同时考虑 cpuset 和 cpu quota。 | Sysbox 已按 cpuset 过滤具体 CPU block，但输出仍是连续 0..N-1，不保留 cpuset 原始 CPU ID；架构/personality 处理也没有完整复刻 LXCFS。 |
| /proc/diskstats | 在 proc_fuse.c 的 proc_diskstats_read() 中读取 blkio/io cgroup 统计，并映射为 diskstats 字段；失败时可能回退宿主 /proc/diskstats。 | 当前优先读取 cgroup v2 io.stat；cgroup v1 下读取 blkio.throttle/io_*、blkio.io_* 并生成主要 diskstats 字段；生成时只遍历宿主 /proc/diskstats 中已有设备，不再为 cgroup 中的未知设备合成 devX_Y 行；无可靠 cgroup 数据或无法生成有效行时回退宿主 /proc/diskstats。 | 已对齐 LXCFS 的 v1/v2 cgroup 优先读取方向、宿主设备列表遍历方式和失败回退方向。差异是 Sysbox 的字段映射更偏主要计数器近似，v2 io.stat 与 diskstats 字段不是一一对应。 |
| /proc/meminfo | 在 proc_fuse.c 的 proc_meminfo_read() 中基于 memory cgroup、memory.stat、swap、zswap 生成大量字段。 | 当前基于 memory.max/current 或 v1 limit/usage 生成核心字段；同时读取 cgroup v2/v1 memory.stat 映射 active/inactive、cache、slab、dirty、writeback、anon、mapped、shmem、hugepage 等字段。 | 已比早期实现更接近 LXCFS，但 zswap、部分 THP/内核字段仍受 cgroup v2 可用字段限制，无法完整复刻 LXCFS 所有计算。 |
| /proc/stat | 在 proc_fuse.c 的 proc_stat_read() 和 proc_cpuview.c 的 cpuview_proc_stat() 中处理。它会读取 cpuacct/cpuset，维护 cgroup 级历史增量，生成更真实的 per-cpu 统计。 | 当前基于容器 uptime、可见 CPU 数、cgroup CPU usage 初始化 cpu/cpuN 行；后续按 cgroup CPU usage delta、宿主 per-cpu 权重和 per-container 状态缓存累计增量，原样保留宿主非 CPU 行（包括 `btime`）。 | 已对齐“需要历史状态避免每次静态估算”的方向，但 LXCFS 依赖 cpuacct.usage_all/cpuview、cpuset 物理 CPU 映射和 CFS quota 逻辑；Sysbox 使用 Go 全局状态与 cgroup v2 cpu.stat/v1 cpuacct 数据近似分摊，精度和 CPU ID 语义仍不完全等同 LXCFS。 |
| /proc/swaps | 在 proc_fuse.c 的 proc_swaps_read() 中结合 swap 配置、memory+swap limit、swappiness 输出虚拟 swap 行，并按宿主 SwapTotal 封顶。 | 当前输出 header；只有从 cgroup 读到明确 swap limit/current 时才输出一条 virtual swap。cgroup v2 使用 memory.swap.max/current，若当前层是 max 则向父层查找有效限制；cgroup v1 使用 memory.memsw.limit_in_bytes-memory.limit_in_bytes 作为 swap 总量，并用 memory.memsw.usage-memory.usage 作为已用量；无有效限制时不展示宿主 SwapTotal。 | 已更接近 LXCFS 的主要输出语义。差异是 Sysbox 没有 LXCFS 的全局 LXCFS_SWAP_ON 配置开关，也不接管 swapon/swapoff；另外 Sysbox 只有宿主 SwapTotal > 0 时才按宿主值封顶，LXCFS 会直接按宿主 SwapTotal 封顶。 |
| /proc/uptime | 在 proc_fuse.c 的 proc_uptime_read() 中使用 get_reaper_age() 作为 uptime，用 get_reaper_busy() 推算 idle。 | 当前使用容器创建时间计算 uptime，结合可见 CPU 数与 cgroup CPU usage 计算 idle，并对 offset 分块读取做 snapshot。 | LXCFS 以 reaper/PID1 年龄为核心。Sysbox 还配合 sysbox-runc time namespace offset 修正 /proc/[pid]/stat starttime，整体以容器创建时间为时间基准。 |
| /proc/slabinfo | 在 proc_fuse.c 的 proc_slabinfo_read() 中优先读取 cgroup memory.kmem.slabinfo，失败时可能回退宿主 slabinfo。 | 当前 cgroup v1 memory.kmem.slabinfo 可用时读取；否则回退宿主 /proc/slabinfo。 | cgroup v2 通常没有等价 slabinfo 文件，因此 cgroup v2 下实际会暴露宿主 slabinfo；这一点与“保守隐藏宿主细节”的目标不完全一致，但与 LXCFS 的失败回退策略更接近。 |
| /proc/pressure/io | 在 proc_fuse.c 的 proc_pressure_read() 中读取 cgroup pressure 文件；proc_write/proc_poll 还支持 PSI trigger write/poll，其中 poll 受 LXCFS 启动选项控制。 | 当前读取容器根 cgroup 的 cgroup v2 io.pressure；失败时回退 /proc/pressure/io；补齐了换行和 snapshot 读取。 | Sysbox 目前只实现 read 视图，没有实现 LXCFS 的 PSI trigger write/poll 行为。 |
| /proc/pressure/cpu | 同 /proc/pressure/io，读取 cpu.pressure，并支持 PSI trigger write/poll。 | 当前读取容器根 cgroup 的 cgroup v2 cpu.pressure；失败时回退 /proc/pressure/cpu。 | 差异同上：只实现读取，没有实现 PSI 事件触发器写入和 poll。 |
| /proc/pressure/memory | 同 /proc/pressure/io，读取 memory.pressure，并支持 PSI trigger write/poll。 | 当前读取容器根 cgroup 的 cgroup v2 memory.pressure；失败时回退 /proc/pressure/memory。 | 差异同上：只实现读取，没有实现 PSI 事件触发器写入和 poll。 |
| /sys/devices/system/cpu/online | 在 sysfs_fuse.c 的 sys_devices_system_cpu_online_read() 中基于 cpuset 与 CPU quota 输出可见 CPU。 | 当前通过 effectiveCPUCount() 输出连续 CPU 范围 0..N-1。 | LXCFS 更贴近 cpuset 实际 CPU ID。Sysbox 输出连续范围，和 /proc/cpuinfo、/proc/stat 的连续 CPU 编号保持一致，避免用户态工具看到非连续 CPU 时产生额外问题。 |
| /proc/loadavg | 在 proc_loadavg.c 的 proc_loadavg_read() 中使用 loadavg 后台结构与 cgroup hash 维护负载移动平均。首次读取创建 load node，后续由 5 秒刷新线程更新。 | 当前已实现 lazy-start 后台采样器：第一次读取时按访问进程 PID namespace 反查 namespace PID 1 作为采样 PID，注册 load node 但不立即刷新；后台每 5 秒刷新 running/total/last pid，并按 LXCFS 固定点 EWMA 算法生成 1/5/15 分钟 loadavg。 | 已对齐 LXCFS 的核心采样模型、初始 0/1 行为和计算公式。差异是 Sysbox 以 Go 全局 sampler 管理节点，没有复刻 LXCFS 的 pthread hash bucket 锁结构；节点回收使用 lastSeen TTL。 |
| /sys/devices/system/cpu/present | LXCFS 新 sysfs 逻辑可把 /sys/devices/system/cpu 下普通文件作为 SUBFILE 透传；online 有专门虚拟化。 | 当前新增 handler，输出与 online 一致的连续 CPU 范围 0..N-1。 | LXCFS 对 present 主要是 sysfs 子文件路径支持，不一定专门虚拟化为容器 CPU present。Sysbox 选择让 present 与 online/cpuinfo/stat 一致，避免容器看到宿主全部 CPU。 |

## 逐项差异说明

### /proc/cpuinfo

差异：

- LXCFS 会按 cpuset、CFS quota、personality 和架构格式生成 CPU 视图。
- Sysbox-FS 当前先按 cpuset 过滤宿主 CPU block，再按 quota 限制数量，最后把 processor 编号连续重写为 0..N-1。
- 如果 cpuset 是 2,4,6，LXCFS 更接近保留这些物理 CPU 的语义；Sysbox-FS 会输出 processor 0,1,2。

影响：

- 容器内工具看到的 CPU 数量是正确或近似正确的。
- CPU ID 不再表达宿主 cpuset 的真实 CPU ID，依赖物理 CPU 编号的程序看到的语义与 LXCFS 不同。

不能完全对齐 LXCFS 的原因：

- Sysbox-FS 当前选择让 /proc/cpuinfo、/proc/stat、online、present 使用同一套连续 CPU 编号，避免用户态工具处理非连续 CPU ID 时出现兼容问题。
- 完整复刻 LXCFS 需要引入更复杂的 cpuset CPU ID 映射，并同步改造 stat、online、present，属于全局 CPU 视图策略变更。
- LXCFS 的架构/personality 分支较多，直接移植会增加维护成本；当前 Sysbox-FS 只覆盖主流工具需要的可解析格式。

### /proc/diskstats

差异：

- LXCFS 从 blkio/io cgroup 统计生成 diskstats，失败时可能回退宿主 /proc/diskstats。
- Sysbox-FS 优先使用 cgroup v2 io.stat，其次使用 cgroup v1 blkio.throttle/io_* 和 blkio.io_*，无法生成有效行时回退宿主 /proc/diskstats。
- Sysbox-FS 生成行时只使用宿主 /proc/diskstats 中已有设备名和顺序；如果 cgroup 统计里出现宿主 diskstats 没有的设备号，会跳过而不是合成 devX_Y 设备名。
- cgroup v2 io.stat 只有 rios、rbytes、wios、wbytes、dios、dbytes 等聚合字段，无法完整对应 diskstats 的所有字段。

影响：

- 有 cgroup I/O 统计时，容器内能看到按 cgroup 近似生成的磁盘统计。
- 缺少 cgroup 数据时会暴露宿主 diskstats，这与隔离目标有张力，但能保证工具读取该文件不失败。
- 某些字段如队列时间、服务时间、discard 细节在不同内核和 cgroup 版本下精度不一致。

不能完全对齐 LXCFS 的原因：

- cgroup v2 的 io.stat 数据模型与 /proc/diskstats 不是一一映射，缺少内核 diskstats 中部分时序和队列字段。
- cgroup v1 blkio 不同文件在不同内核上可用性不同，Sysbox-FS 只能按可用文件组合生成近似值。
- 要完全接近 LXCFS，需要维护更多内核版本兼容分支和字段映射，且仍无法弥补 cgroup v2 原始数据缺口。

### /proc/meminfo

差异：

- LXCFS 对 memory cgroup、memory.stat、swap、zswap、THP、kernel memory 等字段做大量计算。
- Sysbox-FS 使用 memory.max/current 或 v1 limit/usage 作为 MemTotal/MemFree 基础，再用 memory.stat 映射 active/inactive、cache、slab、dirty、writeback、anon、mapped、shmem、hugepage 等字段。
- 当没有有效 memory limit、limit 为 0、或 limit 大于宿主 MemTotal 时，Sysbox-FS 回退宿主 /proc/meminfo。

影响：

- 常用工具如 free/top 能看到容器 memory limit。
- 部分低频字段与宿主或 LXCFS 的精确计算不完全一致。
- 在没有有效 limit 的场景会退回宿主视图。

不能完全对齐 LXCFS 的原因：

- cgroup v2 不再提供 cgroup v1 时代的部分 kernel memory、kmem、zswap 或 THP 细粒度统计。
- LXCFS 长期积累了大量字段级兼容逻辑，Sysbox-FS 当前只实现对常用工具影响最大的字段。
- 如果强行补齐所有字段，很多值只能用宿主字段或估算值填充，反而可能制造“看起来精确但实际不可靠”的视图。

### /proc/stat

差异：

- LXCFS 依赖 cpuacct、cpuset 和 cpuview 逻辑，维护 cgroup 级 CPU 历史增量，生成更接近真实 per-cpu 的统计。
- Sysbox-FS 用容器 uptime、可见 CPU 数和 cgroup CPU usage 初始化 CPU 行，再用 cgroup usage delta、宿主 per-cpu 权重和 Go 全局状态缓存持续累计。
- Sysbox-FS 原样保留非 CPU 行（包括宿主 `btime`），以维持 UserNS 下 kubelet 所需的 boot-time 语义。

影响：

- top、uptime 等工具能读取到随时间增长的 CPU 统计，不再是每次静态估算。
- per-cpu 分摊是近似值，CPU ID 仍是连续 0..N-1。
- 在 cgroup v2 下缺少 cpuacct.usage_all 一类直接 per-cpu cgroup 数据，精度低于 LXCFS 的 cgroup v1 路径。

不能完全对齐 LXCFS 的原因：

- cgroup v2 cpu.stat 主要提供 usage_usec、user_usec、system_usec 等总量字段，不提供与 LXCFS cpuacct.usage_all 等价的 per-cpu 历史视图。
- Sysbox-FS handler 是请求式 Go 实现，直接复刻 LXCFS cpuview 的全量状态机、锁模型和 per-open 缓存会显著增加复杂度。
- 继续保留连续 CPU 编号意味着即使 per-cpu 统计更精确，也不会完全等同 LXCFS 的 cpuset 原始 CPU ID 语义。

### /proc/swaps

差异：

- LXCFS 结合 swap 配置、memory+swap limit、swappiness 输出虚拟 swap 行，并按宿主 SwapTotal 封顶。
- Sysbox-FS 只有读到明确的 cgroup swap limit/current 时才输出一条 virtual swap；否则只输出 header。
- cgroup v2 使用 memory.swap.max/current，并在当前层为 max 时向父层查找有效限制；cgroup v1 使用 memory.memsw.limit_in_bytes-memory.limit_in_bytes 作为 swap 总量，并使用 memory.memsw.usage_in_bytes-memory.usage_in_bytes 作为已用量。

影响：

- 有明确 swap 限制时，容器能看到与自身限制相关的 swap 视图。
- 没有限制时不会展示宿主 SwapTotal，避免把宿主 swap 当成容器可用资源暴露。
- 没有实现 swapon/swapoff 虚拟化，也没有 LXCFS_SWAP_ON 开关。

不能完全对齐 LXCFS 的原因：

- Sysbox-FS 目前只负责只读资源视图，不接管 swapon/swapoff 行为。
- cgroup v2 的 swap 语义与 v1 memsw 不同，尤其 max、父层限制和宿主 SwapTotal 的组合需要取舍。
- 直接按 LXCFS 方式展示宿主 SwapTotal 会在无限制或未配置场景暴露宿主资源信息，和当前更保守的 swaps 策略不一致。

### /proc/uptime

差异：

- LXCFS 以容器 reaper/PID1 年龄作为 uptime，并用 reaper busy 时间推算 idle。
- Sysbox-FS 使用容器创建时间计算 uptime，结合可见 CPU 数和 cgroup CPU usage 计算 idle。
- Sysbox-FS 对 offset 分块读取使用 snapshot，避免同一次读取中两段数据不一致。

影响：

- uptime 与 Sysbox 容器生命周期一致，top 等工具可正常解析。
- 与 LXCFS 以 reaper 年龄为核心的时间基准不完全相同。
- idle 是基于 cgroup usage 和 CPU capacity 推算，不是 LXCFS 的 reaper busy 逻辑。

不能完全对齐 LXCFS 的原因：

- Sysbox 还配合 sysbox-runc time namespace offset 修正 /proc/[pid]/stat starttime，整体时间视图以容器创建时间为基准。
- 切换到 reaper 年龄需要重新统一 uptime、/proc/stat btime、/proc/[pid]/stat starttime 等多处时间语义。
- 当前实现已经解决分块读取一致性和常用工具解析问题，继续追求 LXCFS 的 reaper busy 模型收益有限。

### /proc/slabinfo

差异：

- LXCFS 优先读取 cgroup v1 memory.kmem.slabinfo，失败时可能回退宿主 slabinfo。
- Sysbox-FS 当前 cgroup v1 memory.kmem.slabinfo 可用时读取，否则回退宿主 /proc/slabinfo。
- cgroup v2 通常没有等价的 memory.kmem.slabinfo。

影响：

- cgroup v1 下可以得到 cgroup slabinfo。
- cgroup v2 下会看到宿主 slabinfo，隔离性不如理想容器视图。

不能完全对齐 LXCFS 的原因：

- cgroup v2 缺少等价内核接口，Sysbox-FS 无法从 cgroup 原始数据重建完整 slabinfo。
- 如果不回退宿主，只能输出 header 或空视图，会影响依赖 slabinfo 存在的诊断工具。
- 当前行为更接近 LXCFS 的回退策略，但不是严格隔离视图。

### /proc/pressure/*

差异：

- LXCFS 读取 cgroup pressure 文件，并支持 PSI trigger write/poll。
- Sysbox-FS 读取容器根 cgroup 的 cgroup v2 io.pressure、cpu.pressure、memory.pressure，避免误读 /init.scope 叶子 cgroup；失败时回退宿主 /proc/pressure/*。
- Sysbox-FS 当前只实现 read，没有实现 PSI trigger 写入和 poll。

影响：

- 只读 PSI 数据可用，常规 cat/监控读取可以工作。
- 依赖 PSI trigger 的程序无法通过 Sysbox-FS 获得与 LXCFS 相同的事件通知行为。
- 回退宿主 pressure 时会暴露宿主压力信息。

不能完全对齐 LXCFS 的原因：

- PSI trigger 需要实现 write/poll 语义，不只是生成文件内容；这会扩展当前 read-only resource handler 模型。
- 需要处理 trigger 生命周期、poll 等待、并发和关闭清理，复杂度明显高于普通 proc 文件读取。
- 如果业务只需要读取 pressure 文件，实现 trigger 的收益有限。

### /proc/loadavg

差异：

- LXCFS 使用后台 loadavg 结构、cgroup hash 和固定点 EWMA 维护 1/5/15 分钟负载。
- Sysbox-FS 已实现 lazy-start 后台 sampler，采样周期和固定点 EWMA 公式与 LXCFS 对齐。
- Sysbox-FS 首次读取只创建节点并返回初始值，和 LXCFS 一样等待后台 5 秒采样刷新，避免 `top` 在空闲容器里长期看到宿主 load。
- Sysbox-FS 优先从访问 `/proc/loadavg` 的进程 PID namespace 反查 namespace PID 1 作为采样对象；这避免 Kubernetes/CRI 场景中误用 sandbox/pause 容器 init pid，导致业务容器一直显示 `0/1 1`。
- Sysbox-FS 的节点管理使用 Go goroutine + mutex，节点回收靠 lastSeen TTL；LXCFS 使用 pthread、hash bucket 锁和 cgroup 消失删除。

影响：

- loadavg 的前三列和 LXCFS 计算方向基本一致，top/uptime 可正常使用。
- running/total/last pid 的节点生命周期与 LXCFS 不完全一致，边界场景可能有差异；采样来源已尽量和 LXCFS 一样绑定到读取进程所在容器/PID namespace。

不能完全对齐 LXCFS 的原因：

- Sysbox-FS 的进程发现优先使用访问进程 PID namespace/cgroup，再 fallback 到容器 root /proc 或 cgroup；LXCFS 的扫描路径和数据结构不同。
- 复刻 LXCFS 的 hash bucket、多级锁和节点删除模型不符合当前 Go handler 结构。
- 当前 sampler 已满足主要工具解析和移动平均语义，继续逐行复刻会增加复杂度但收益有限。

### /sys/devices/system/cpu/online 和 /sys/devices/system/cpu/present

差异：

- LXCFS online 专门虚拟化，倾向保留 cpuset 和 quota 共同决定的可见 CPU 语义；present 更多来自 sysfs 子文件透传逻辑。
- Sysbox-FS online 与 present 都使用 effectiveCPUCount() 输出连续范围 0..N-1。
- present 在 Sysbox-FS 中被显式虚拟化为与 online 一致，而不是透传宿主 present。

影响：

- 容器内 CPU 视图在 cpuinfo、stat、online、present 之间保持一致。
- 宿主 cpuset 原始 CPU ID 被隐藏，不能表达 2,4,6 这类非连续 CPU 集合。

不能完全对齐 LXCFS 的原因：

- Sysbox-FS 当前选择“连续容器 CPU 编号”作为统一策略，改变 online/present 会连带影响 cpuinfo 和 stat。
- 透传 present 可能让容器看到宿主全部 CPU，与 cpuinfo/stat 中的可见 CPU 数不一致。
- 保留 cpuset 原始 CPU ID 需要整体改造 CPU 视图语义，并重新验证容器内工具兼容性。

## 当前没有完全对齐 LXCFS 的主要原因

### 1. 架构不同

LXCFS 是一个独立 FUSE 文件系统，很多逻辑围绕 FUSE file handle、per-open 缓存、后台线程、cgroup 历史状态展开。

Sysbox-FS 当前是 Sysbox 系统容器运行时的一部分，handler 更偏请求式动态生成。直接移植 LXCFS 的后台状态机会显著增加复杂度，也会影响已有 sysbox-fs handler 模型。

### 2. 失败回退策略不同

LXCFS 在一些失败路径会回退宿主文件。例如 diskstats、slabinfo、pressure 在无法生成 cgroup 视图时可能直接读宿主文件。

当前 Sysbox-FS 的策略是按路径区分：

- /proc/diskstats 无可靠 cgroup 数据或无法生成有效行时回退宿主 /proc/diskstats。
- /proc/slabinfo 无 cgroup v1 memory.kmem.slabinfo 时回退宿主 /proc/slabinfo。
- pressure 文件无 cgroup pressure 文件时回退宿主 /proc/pressure/*。
- /proc/meminfo 在没有有效 memory limit、limit 为 0、或 limit 大于宿主 MemTotal 时回退宿主 /proc/meminfo。
- /proc/loadavg 不回退宿主 loadavg。
- /proc/swaps 无可靠 cgroup swap limit/current 时只输出 header。

因此，Sysbox 对 loadavg/swaps 仍偏保守；对 diskstats/slabinfo/pressure/meminfo 则更偏可读性和兼容性，存在暴露宿主资源细节的可能。

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
- LXCFS 以 cgroup 目录递归深度扫描 cgroup.procs；Sysbox 优先按访问进程 PID namespace 扫描宿主 /proc，失败时再用容器 root /proc 或 cgroup 估算。
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
- top 读取到的 load average 与 /proc/loadavg 前三列一致，测试脚本允许 0.05 的采样误差。
- /proc/meminfo/free 能反映容器 memory limit。
- pressure 文件优先读取容器根 cgroup 的 cgroup v2 pressure。

当前仍未完全等同 LXCFS 的部分：

- /proc/loadavg 已实现后台采样和 EWMA 移动平均，但采样节点管理方式与 LXCFS 不同。
- /proc/stat 已有 per-container 历史增量缓存，但没有 LXCFS 基于 cpuacct.usage_all/cpuview 的 per-cpu 精度和 cpuset 物理 CPU 映射。
- /proc/diskstats 已支持 cgroup v2 io.stat 与 cgroup v1 blkio 主要字段，按宿主 diskstats 设备列表输出，失败时会回退宿主完整文件；字段精度和 LXCFS 仍不完全一致。
- /proc/swaps 已支持明确 cgroup swap limit/current、v2 max 向父层查找有效限制、v1 memsw-memory swap 总量口径和 v1 swappiness=0 处理；无有效限制时不展示宿主 SwapTotal，且没有 LXCFS_SWAP_ON 开关和 swapon/swapoff 虚拟化。宿主 SwapTotal 为 0 时的封顶语义与 LXCFS 仍有差异。
- /proc/slabinfo 在 cgroup v2 下没有 cgroup 级 slabinfo，当前回退宿主 /proc/slabinfo。
- pressure write/poll 未实现。
- CPU present/online 使用连续容器 CPU 编号，而不是保留宿主 cpuset CPU ID。

## 后续如果要继续向 LXCFS 对齐

建议按优先级推进：

1. 继续提升 /proc/stat 的 per-cpu 分摊精度，评估是否需要更接近 LXCFS cpuacct.usage_all/cpuview 的状态模型。
2. 继续扩展 /proc/diskstats 中 discard、queue time 等字段在不同内核/cgroup 文件上的兼容映射。
3. 评估是否需要 pressure write/poll；如果业务不依赖 PSI trigger，可继续只读。
4. 决定 CPU ID 策略：继续使用连续 CPU 编号，或改为保留 cpuset 原始 CPU ID。这个选择会同时影响 cpuinfo、stat、online、present。
