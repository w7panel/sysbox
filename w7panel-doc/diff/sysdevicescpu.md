# /sys/devices/system/cpu/{online,present}: LXCFS 与 Sysbox 实现差异

本文对比 LXCFS 与 Sysbox 对以下 sysfs CPU 文件的虚拟化实现：

- `/sys/devices/system/cpu/online`
- `/sys/devices/system/cpu/present`

重点关注 cpuset、CFS quota、CPU 编号策略、fallback 行为，以及和 `/proc/cpuinfo`、`/proc/stat` 的一致性。

## 代码位置

| 项目 | 入口 | 主要代码 |
| --- | --- | --- |
| LXCFS | `sys_devices_system_cpu_online_read()` | `lxcfs/src/sysfs_fuse.c`、`lxcfs/src/cgroups/cgfsng.c`、`lxcfs/src/proc_cpuview.c` |
| Sysbox | `readCPUOnline()`、`readCPUPresent()` | `sysbox-fs/handler/implementations/procResources.go` |

## 差异总览

| 对比项 | LXCFS 实现 | Sysbox 当前实现 | 影响 |
| --- | --- | --- | --- |
| `/sys/devices/system/cpu/online` | 专门虚拟化 | 专门虚拟化 | 都会隐藏宿主完整 online CPU |
| `/sys/devices/system/cpu/present` | 没看到专门虚拟化逻辑；新 sysfs 逻辑中更像 CPU 子文件 passthrough | 专门虚拟化，输出与 `online` 一致 | Sysbox 更主动隐藏宿主 present CPU |
| cgroup 定位 | 通过容器 init pid 找 `cpuset` 和 `cpu` cgroup | 通过 `cgroupForReq(req)`，并 prune `/init.scope` | Sysbox 更偏请求进程；LXCFS 更偏容器 init cgroup |
| cpuset 来源 | `get_cpuset()`，读取 `cpuset.cpus` / `cpuset.cpus.effective`，必要时查祖先 | `readCPUSetFromCgroup()`，读取 v2/v1 cpuset，并查有效祖先 | 基本一致 |
| CPU quota | 启用 CFS cpuview 时用 `max_cpu_count()` 结合 quota | `effectiveCPUCount()` 结合 cpuset count 和 `cpu.max` / v1 quota | 目标一致 |
| CPU 编号策略 | 启用 CFS cpuview 时输出连续 `0-(N-1)`；未启用时输出 cpuset 原始范围 | 始终输出连续 `0-(N-1)` | Sysbox 不保留宿主 cpuset 原始 CPU ID |
| 非连续 cpuset | 未启用 cpuview 时可输出如 `2,4,6` | 输出 `0-2` | 明显差异 |
| `online` 与 `present` 一致性 | `online` 虚拟化；`present` 可能透传宿主 | `online` 和 `present` 始终一致 | Sysbox 容器内 CPU 视图更统一 |
| fallback | 找不到 cpuset/cpu cgroup 时回退宿主 `online` | `effectiveCPUCount <= 0` 时分别回退宿主 `online` / `present` | 基本一致，但 present 语义不同 |
| 与 `/proc/cpuinfo` | LXCFS 更倾向保留 cpuset CPU ID / cpuview 映射 | Sysbox 与 cpuinfo/stat 一起使用连续 CPU 编号 | Sysbox 内部一致性更强 |
| 与 LXCFS 对齐程度 | 标准参考 | `online` 部分接近，`present` 不完全一致 | 差异集中在 CPU ID 策略和 present 是否虚拟化 |

## LXCFS online 实现说明

LXCFS 的 `/sys/devices/system/cpu/online` 由 `sys_devices_system_cpu_online_read()` 处理。

处理流程：

1. 通过 FUSE 请求 pid 找容器 init pid。
2. 读取 init pid 的 `cpuset` cgroup。
3. 读取 init pid 的 `cpu` cgroup。
4. 调用 `do_cpuset_read()`。
5. `do_cpuset_read()` 读取 cpuset。
6. 如果启用 CFS cpuview，则通过 `max_cpu_count(cpuset_cg, cpu_cg)` 计算可见 CPU 数，并输出连续范围：

```text
0
0-1
0-3
```

7. 如果未启用 CFS cpuview，则直接输出 cpuset 原始内容：

```text
2,4,6
0,2-3
```

找不到 cgroup 时，LXCFS 回退宿主 `/sys/devices/system/cpu/online`。

## LXCFS present 实现说明

从当前代码看，LXCFS 没有像 `online` 一样对 `/sys/devices/system/cpu/present` 做专门虚拟化。

LXCFS 新 sysfs 逻辑会处理 `/sys/devices/system/cpu/<subfile>` 这类 CPU 子文件路径。对于非 `online` 的子文件，读取路径更接近 passthrough。

因此 `present` 在 LXCFS 中通常不是和 `online` 同一套显式虚拟化逻辑。

## Sysbox 实现说明

Sysbox 分别注册：

- `SysDevicesSystemCpuOnline_Handler`
- `SysDevicesSystemCpuPresent_Handler`

两者核心逻辑相同：

```text
count = effectiveCPUCount(req)
if count > 0:
    return cpuRangeForCount(count)
else:
    return host file
```

`effectiveCPUCount()` 会：

1. 读取请求进程对应 cgroup 的 cpuset。
2. 统计 cpuset 中 CPU 数量。
3. 读取 CPU quota。
4. 如果 quota 限制更小，则使用 quota 限制。
5. 返回最终可见 CPU 数量。

然后统一输出连续 CPU 范围：

```text
1 CPU  -> 0
2 CPU  -> 0-1
4 CPU  -> 0-3
```

Sysbox 不把 cpuset 原始 CPU ID 暴露给容器。

## CPU 编号策略差异

这是两个实现最核心的差异。

假设宿主 cgroup cpuset 是：

```text
2,4,6
```

LXCFS 在未启用 CFS cpuview 时可能输出：

```text
2,4,6
```

Sysbox 会输出：

```text
0-2
```

Sysbox 这样做的原因是保持容器内部 CPU 编号连续，并与以下文件一致：

- `/proc/cpuinfo`
- `/proc/stat`
- `/sys/devices/system/cpu/online`
- `/sys/devices/system/cpu/present`

代价是容器内看不到宿主 cpuset 的真实 CPU ID。

## online 与 present 差异

### LXCFS

`online` 是明确虚拟化路径。

`present` 没有看到同级别的专门虚拟化逻辑，通常更接近 sysfs 子文件 passthrough。

这意味着容器内可能出现：

```text
online  = 容器可见 CPU
present = 宿主 present CPU
```

具体取决于 LXCFS 版本和 sysfs 挂载方式。

### Sysbox

Sysbox 明确让 `present` 与 `online` 使用同一套逻辑。

这意味着容器内：

```text
online  = 0-(N-1)
present = 0-(N-1)
```

这种行为更利于容器内部工具得到一致 CPU 视图。

## fallback 差异

LXCFS 的 `online` fallback：

- 找不到 cpuset cgroup，回退宿主 `online`。
- 找不到 cpu cgroup，回退宿主 `online`。

Sysbox 的 fallback：

- `effectiveCPUCount(req) <= 0` 时回退宿主文件。
- `online` 回退宿主 `/sys/devices/system/cpu/online`。
- `present` 回退宿主 `/sys/devices/system/cpu/present`。

Sysbox 的 `effectiveCPUCount()` 如果 cpuset 读不到，会尝试从宿主 `online` 推导 CPU 数，因此实际更倾向继续输出连续范围，而不是直接回退宿主原始格式。

## 是否已经对齐

当前 Sysbox 与 LXCFS 在 `/sys/devices/system/cpu/online` 上部分对齐。

已经接近的部分：

- 都基于 cpuset 和 CPU quota 计算容器可见 CPU。
- 启用 cpuview/CFS 场景下，LXCFS 也可能输出连续 `0-(N-1)`，这和 Sysbox 一致。
- 都支持 fallback 宿主文件。

仍存在差异的部分：

- Sysbox 始终输出连续 CPU 编号。
- LXCFS 在未启用 cpuview 时可能输出 cpuset 原始 CPU ID。
- Sysbox 显式虚拟化 `present`，LXCFS 对 `present` 不一定专门虚拟化。
- Sysbox 的 `online/present/cpuinfo/stat` 是一套统一连续 CPU 视图；LXCFS 更接近 cpuset/cpuview 的原始语义。

## 强制对齐建议

如果目标是严格对齐 LXCFS，需要先决定 CPU ID 策略。

### 方案一：保留 Sysbox 当前策略

继续让所有 CPU 视图输出连续编号：

- `/proc/cpuinfo`: processor `0..N-1`
- `/proc/stat`: `cpu0..cpuN`
- `/sys/devices/system/cpu/online`: `0-(N-1)`
- `/sys/devices/system/cpu/present`: `0-(N-1)`

优点：

- 容器内视图一致。
- 用户态工具兼容性好。
- 不泄露宿主 cpuset 原始 CPU ID。

缺点：

- 非连续 cpuset 场景不严格等同 LXCFS。

### 方案二：对齐 LXCFS cpuset 原始 ID

让 `online` 输出 cpuset 原始范围，例如：

```text
2,4,6
```

同时需要同步调整：

- `/proc/cpuinfo`
- `/proc/stat`
- CPU per-cpu 统计映射
- `present` 是否透传或保留宿主语义

优点：

- 更贴近 LXCFS cpuset 原始语义。

缺点：

- 改动面大。
- 容器内 CPU 编号可能非连续。
- 需要重新验证 `top`、`nproc`、JVM、Go runtime、监控 agent 等工具行为。

## 当前结论

Sysbox 当前选择的是“容器内连续 CPU 编号”策略。

这与 LXCFS 在启用 CFS cpuview 时的 `online` 输出接近，但与 LXCFS 未启用 cpuview 时保留 cpuset 原始 CPU ID 的行为不同。

`present` 方面，Sysbox 比 LXCFS 更主动虚拟化，确保它和 `online` 一致，避免容器看到宿主全部 present CPU。

如果目标是容器内 CPU 视图一致性，当前 Sysbox 策略合理。

如果目标是逐字段复刻 LXCFS，需要整体重构 CPU ID 映射，而不能只改 `online` 或 `present`。
