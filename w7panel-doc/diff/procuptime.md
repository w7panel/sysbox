# /proc/uptime: LXCFS 与 Sysbox 实现差异

本文对比 LXCFS 与 Sysbox 对 `/proc/uptime` 的虚拟化实现，重点关注 uptime 第一列、idle 第二列、时间来源、CPU 使用统计来源以及与 `/proc/stat` 的一致性。

## 代码位置

| 项目 | 入口 | 主要代码 |
| --- | --- | --- |
| LXCFS | `proc_uptime_read()` | `lxcfs/src/proc_fuse.c` |
| Sysbox | `ProcUptime.readUptime()` | `sysbox-fs/handler/implementations/procUptime.go` |

## 差异总览

| 对比项 | LXCFS 实现 | Sysbox 当前实现 | 影响 |
| --- | --- | --- | --- |
| 入口 | `proc_uptime_read()` | `ProcUptime.readUptime()` | 都接管 `/proc/uptime` |
| 第一列 uptime 来源 | `get_reaper_age(fc->pid)`，即容器 reaper / PID1 的年龄 | `containerUptime(req.Container.Ctime(), now)`，即 Sysbox 记录的容器创建时间到当前时间 | 两者通常接近，但不是同一个时间源 |
| 第一列公式 | `CLOCK_BOOTTIME - /proc/<reaper>/stat starttime` | `now - container.Ctime()` | LXCFS 更贴近容器 PID1 生命周期；Sysbox 更贴近 runtime/container 元数据生命周期 |
| 第二列 idle 来源 | `reaperage - get_reaper_busy(fc->pid)` | `uptime * effectiveCPUCount - cgroup CPU usage` | 明显差异 |
| 第二列 CPU 数量 | 不直接按可见 CPU 数扩大，默认从 `reaperage` 起算 | 会乘以容器可见 CPU 数 | 多 CPU 容器里 Sysbox 第二列可能明显大于第一列 |
| busy 计算 | `get_reaper_busy()`，围绕 reaper/PID namespace 进程 busy 时间推算 | `cpuUsageFromCgroup(cgroupForReq(req)).UsageSeconds` | LXCFS 偏 reaper/容器进程视角；Sysbox 偏 cgroup 总 CPU 使用视角 |
| cgroup 依赖 | 第一列主要依赖 reaper starttime；busy 逻辑依赖进程统计 | idle 依赖 cgroup CPU 统计和有效 CPU 数 | Sysbox 与 `/proc/stat` 的 CPU 容量模型更一致 |
| offset 读取 | 使用 `d->cached` 支持非 0 offset 分块读取 | 使用 snapshot cache 支持非 0 offset 分块读取 | 两边都处理了 `top` 等程序的分块读取 |
| 输出格式 | `%.2lf %.2lf\n` | `%.2f %.2f\n` | 格式一致，都是两位小数 |
| fallback | `snprintf` 失败或 buffer 不足时回退宿主 `/proc/uptime` | 正常路径不回退宿主；offset 超出返回 EOF | fallback 策略不同 |
| 与 `/proc/stat` 的关系 | uptime 独立使用 reaper age/busy；`/proc/stat` cpuview 使用 cpuacct/cpuset | uptime idle 和 `/proc/stat` 一样使用 cgroup CPU usage + visible CPU capacity 模型 | Sysbox 内部 `/proc/uptime` 与 `/proc/stat` 时间模型更统一 |
| 与 LXCFS 对齐程度 | 标准参考 | 当前未严格对齐 | 主要差异是 uptime 时间源和 idle 第二列公式 |

## LXCFS 实现说明

LXCFS 的 `/proc/uptime` 入口是 `proc_uptime_read()`。

处理流程：

1. 通过 FUSE 请求上下文拿到调用进程 pid。
2. 通过 reaper/PID1 的 `/proc/<pid>/stat` 第 22 字段 `starttime` 获取进程启动时间。
3. 使用 `CLOCK_BOOTTIME` 获取宿主启动后的当前时间。
4. 计算 reaper 年龄作为第一列 uptime。
5. 调用 `get_reaper_busy()` 获取容器 reaper/PID namespace 相关的 busy 时间。
6. 第二列 idle 初始为 reaperage，如果 `reaperage >= busytime`，则输出 `reaperage - busytime`。
7. 输出两位小数格式。

LXCFS 的核心语义是：`/proc/uptime` 第一列绑定到容器 reaper/PID1 的年龄，而不是宿主 uptime。

## Sysbox 实现说明

Sysbox 的 `/proc/uptime` 入口是 `ProcUptime.readUptime()`。

处理流程：

1. 从请求上下文获取 Sysbox 容器对象。
2. 读取容器 `Ctime()`。
3. 使用 `now - Ctime()` 计算第一列 uptime。
4. 通过 `effectiveCPUCount(req)` 获取容器可见 CPU 数。
5. 通过 `cpuUsageFromCgroup(cgroupForReq(req))` 获取 cgroup CPU 使用量。
6. 使用 `uptime * visible_cpu_count - cgroup_cpu_usage` 计算第二列 idle。
7. 对结果做下限 0 和上限 capacity clamp。
8. 使用 snapshot 缓存支持非 0 offset 分块读取。

Sysbox 的核心语义是：`/proc/uptime` 与 `/proc/stat` 使用同一套容器容量模型。

## 关键公式

### LXCFS

```text
uptime = CLOCK_BOOTTIME - reaper_starttime

idle = uptime
if uptime >= reaper_busy:
    idle = uptime - reaper_busy
```

### Sysbox

```text
uptime = now - container_ctime

capacity = uptime * visible_cpu_count
idle = capacity - cgroup_cpu_usage
idle = clamp(idle, 0, capacity)
```

## 主要差异

### 1. 第一列时间源不同

LXCFS 使用容器 reaper/PID1 的进程 starttime。

Sysbox 使用容器状态里的创建时间 `Ctime()`。

这两个时间通常接近，但不保证完全一致。比如 runtime 创建容器对象、容器 init 进程启动、sysbox-fs 记录状态之间可能有微小时间差。

### 2. 第二列 idle 语义不同

Linux 宿主 `/proc/uptime` 第二列表示所有 CPU 的累计 idle 时间，因此多 CPU 宿主上第二列通常可以大于第一列。

Sysbox 当前按这个语义实现：

```text
idle = uptime * visible_cpu_count - cgroup_cpu_usage
```

LXCFS 当前实现没有直接乘以可见 CPU 数，而是从 reaperage 减去 reaper busy 时间。因此在多 CPU 容器里，LXCFS 与 Sysbox 的第二列可能差距明显。

### 3. CPU 使用统计范围不同

LXCFS 使用 `get_reaper_busy()` 推算 busy 时间，语义更接近容器 reaper/PID namespace 视角。

Sysbox 使用 cgroup CPU usage，语义更接近整个容器 cgroup 的总 CPU 使用量。

如果容器内存在多个进程，Sysbox 会更自然地反映整个 cgroup 的 CPU 消耗。

### 4. 与 `/proc/stat` 的一致性不同

Sysbox 的 `/proc/uptime` 和 `/proc/stat` 都基于：

- 容器 uptime
- 可见 CPU 数
- cgroup CPU usage

因此两者内部模型更统一。

LXCFS 的 `/proc/uptime` 使用 reaper age/busy，而 `/proc/stat` 使用 cpuacct/cpuset/cpuview，两者不是完全同一套模型。

### 5. 分块读取处理

两边都处理了非 0 offset 读取。

LXCFS 使用 FUSE file handle 的 `d->cached` 缓存。

Sysbox 使用 snapshot cache，避免同一次分块读取过程中重新计算 uptime，导致返回内容不一致。

这对 `top` 等程序很重要，否则可能出现 `/proc/uptime` 解析异常。

## 是否已经对齐

当前 Sysbox 与 LXCFS 在 `/proc/uptime` 上没有严格对齐。

已经接近的部分：

- 都输出两列浮点数。
- 都保留两位小数。
- 第一列都表示容器生命周期级别的 uptime，而不是宿主 uptime。
- 都支持 offset 分块读取。

仍存在差异的部分：

- 第一列时间源不同：LXCFS 使用 reaper/PID1 starttime，Sysbox 使用容器 `Ctime()`。
- 第二列 idle 公式不同。
- LXCFS 不直接按可见 CPU 数扩大 idle，Sysbox 会按可见 CPU 总容量计算。
- busy 统计来源不同：LXCFS 是 reaper busy，Sysbox 是 cgroup CPU usage。
- fallback 策略不同。

## 强制对齐建议

如果目标是尽量和 LXCFS 行为一致，建议按优先级处理：

1. 第一列改为基于容器 init/reaper 的 `/proc/<pid>/stat` starttime 计算，而不是容器 `Ctime()`。
2. 实现类似 LXCFS `get_reaper_busy()` 的 busy 时间计算。
3. 第二列改为 `reaperage - reaper_busy`，不再使用 `uptime * visible_cpu_count - cgroup_cpu_usage`。
4. 保留当前 snapshot/offset 机制，避免破坏 `top` 的分块读取兼容性。
5. 调整后需要重新检查 `/proc/stat`、`btime`、`/proc/[pid]/stat starttime` 的时间语义是否仍一致。

## 当前结论

Sysbox 当前 `/proc/uptime` 采用的是容器容量模型，和 Sysbox 自己的 `/proc/stat` 实现更一致。

LXCFS 采用的是 reaper/PID1 年龄模型，输出更贴近 LXCFS 原始语义。

如果只关注容器内 `top`、`uptime` 等工具能稳定解析并看到容器级 uptime，当前 Sysbox 实现可用。

如果目标是与 LXCFS 逐字段严格一致，需要改第一列时间源和第二列 idle 公式。
