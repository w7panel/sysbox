# /proc/stat: LXCFS 与 Sysbox 实现差异

本文对比 LXCFS 与 Sysbox 对 `/proc/stat` 的虚拟化实现，重点关注 CPU 行、非 CPU 行、cgroup 统计来源、时间模型以及对齐风险。

## 代码位置

| 项目 | 入口 | 主要代码 |
| --- | --- | --- |
| LXCFS | `proc_stat_read()` | `lxcfs/src/proc_fuse.c`、`lxcfs/src/proc_cpuview.c` |
| Sysbox | `readProcStat()` | `sysbox-fs/handler/implementations/procResources.go` |

## 差异总览

| 对比项 | LXCFS 实现 | Sysbox 当前实现 | 影响 |
| --- | --- | --- | --- |
| 入口 | `proc_stat_read()` | `readProcStat()` | 都接管 `/proc/stat` |
| cgroup 定位 | 通过容器 init pid 找 `cpuset`、`cpu`、`cpuacct` cgroup | 优先使用请求 pid 的 cgroup，再 fallback 到容器 cgroup | Sysbox 对实际读取进程更敏感；LXCFS 更偏容器 init cgroup |
| host pid namespace | `initpid == 1` 时直接返回宿主 `/proc/stat` | 没有同样的显式判断，依赖 CPU limit 和 cgroup 逻辑 | host pid namespace 场景语义不完全一致 |
| CPU 数量来源 | `cpuset` 加 `cpu.cfs_quota` / `cpu.cfs_period`，通过 `max_cpu_count()` / `exact_cpu_count()` 计算 | `effectiveCPUCount(req)` | 目标一致，都是限制可见 CPU 行数；计算路径不同 |
| CPU 行输出 | 重写 `cpu` 和 `cpuN` 行 | 重写 `cpu` 和 `cpuN` 行 | 核心行为一致 |
| 非 CPU 行 | 基本保留宿主 `/proc/stat` 后续行 | 保留宿主非 CPU 行，但特殊重写 `btime` | `intr`、`ctxt`、`processes`、`procs_running` 等基本仍是宿主值 |
| `btime` | 保留宿主 `btime` | 如果有容器对象，改成容器创建时间 | 明显差异；Sysbox 更像容器视图，LXCFS 更像宿主 passthrough |
| CPU 使用来源 | 优先 `cpuacct.usage_all`，失败 fallback 到 `cpuacct.usage_percpu` 或宿主 `/proc/stat` | cgroup v2 用 `cpu.stat`，cgroup v1 用 `cpuacct.stat` / `cpuacct.usage` / `cpuacct.usage_percpu` | 统计源不同；v2 上 Sysbox 更直接使用 unified cgroup 的 `cpu.stat` |
| idle 计算 | `idle + (host all_used - cgroup used)`，把容器外 CPU 时间折算成 idle | 使用容器 CPU usage delta 和 `elapsed * visible_cpu_count` 计算 idle | LXCFS 更贴近宿主 `/proc/stat` 差值模型；Sysbox 更贴近容器容量模型 |
| 历史状态 | `proc_stat_history`，按 cgroup 保存历史 usage/view | `procStatStates`，按 cgroup/container/pid key 保存历史 raw/view | 两边都维护历史，避免 `/proc/stat` 回退或跳变 |
| 首次读取 | 基于当前 `cpuacct` 和宿主 `/proc/stat` 初始化视图 | 基于容器 uptime 和当前 cgroup CPU 用量生成初始 ticks | 首次读数可能明显不同 |
| 增量计算 | 计算 `cpuacct` 前后差值，再分摊到可见 CPU | 计算 cgroup CPU usage delta，并按 percpu 或 host busy 权重分摊 | 长期趋势接近，逐次采样值不保证一致 |
| CFS 小数 CPU | `exact_cpu_count()` 会对 partial CPU 调整 idle | `effectiveCPUCount()` 输出整数 CPU 行，delta capacity 按可见 CPU 数计算 | 小数 CPU quota 下可能不完全一致 |
| 超出可见 CPU 的用量 | 将 surplus user/system 尝试塞回可见 CPU | 使用权重把 delta 拆分到可见 CPU | CPU 时间分摊策略不同 |
| CPU 字段列数 | cpuview 路径固定输出 10 列 | 跟随宿主 `cpu` 行字段数量 `fieldCount` | Sysbox 对新内核 CPU 字段兼容更好；LXCFS cpuview 输出更固定 |
| `nice/iowait/irq/softirq/steal/guest` | cpuview 路径基本输出 0，只保留 user/system/idle | 增量只更新 user/system/idle，其他列通常为 0 | 基本一致 |
| cpuset 过滤 | 只输出 cpuset 内 CPU，并重新编号 | 输出 `effectiveCPUCount` 个 `cpu0..cpuN` | 都会隐藏不可见 CPU，但 CPU 映射方式不同 |
| cpuacct 失败 | fallback 到宿主，或按 cpuset 过滤宿主 CPU 行 | cgroup CPU usage 缺失时仍可生成基于 uptime/capacity 的视图 | Sysbox 更可能继续输出虚拟 CPU 行；LXCFS 更可能回退宿主 |
| 总体风格 | 在宿主 `/proc/stat` 基础上替换 CPU 行，其余 passthrough | 生成虚拟 CPU 行，再追加宿主非 CPU 行，并重写 `btime` | 差异集中在 `btime` 和 CPU 时间模型 |

## LXCFS 实现说明

LXCFS 的 `/proc/stat` 入口在 `proc_stat_read()`。

处理流程：

1. 通过 FUSE 请求 pid 找到容器 init pid。
2. 如果是 host pid namespace 或 init pid 为 1，直接返回宿主 `/proc/stat`。
3. 读取 init pid 对应的 `cpuset`、`cpu` cgroup。
4. 读取 cpuset，确定哪些物理 CPU 可见。
5. 打开宿主 `/proc/stat`，跳过第一行总计 CPU 行。
6. 优先读取 `cpuacct.usage_all`，失败时 fallback 到 `cpuacct.usage_percpu` 或宿主数据。
7. 如果支持 cpuview 且启用 CFS 视图，进入 `cpuview_proc_stat()`。
8. 输出虚拟 `cpu` 和 `cpuN` 行。
9. 其余非 CPU 行从宿主 `/proc/stat` 原样透传。

LXCFS 的 cpuview 会维护每个 cgroup 的历史 CPU 视图。它用前后两次 `cpuacct` 差值计算增量，再结合 CFS quota、cpuset 和历史视图生成容器看到的 CPU 时间。

LXCFS 的 idle 计算核心思想是：容器没有使用到的宿主 CPU 时间，被折算成容器视角下的 idle。

## Sysbox 实现说明

Sysbox 的 `/proc/stat` 入口在 `readProcStat()`。

处理流程：

1. 读取宿主 `/proc/stat`。
2. 通过 `effectiveCPUCount(req)` 计算容器可见 CPU 数量。
3. 如果 CPU 数量无效，直接返回宿主 `/proc/stat`。
4. 解析宿主 CPU 行字段数量和 host CPU 当前值。
5. 通过 `procStatCPUView()` 生成虚拟 `cpu` 和 `cpuN` 行。
6. 追加宿主非 CPU 行。
7. 如果遇到 `btime` 且存在容器对象，改写为容器创建时间。

Sysbox 会按 cgroup/container/pid key 维护 `/proc/stat` 历史状态。增量来自 cgroup CPU 统计，idle 来自 `elapsed * visible_cpu_count - used_delta`。

Sysbox 当前的模型更接近容器容量模型：在一个采样周期内，容器最多拥有 `可见 CPU 数 * 采样时间` 的 CPU 时间；实际使用之外的时间计为 idle。

## 关键行为差异

### 1. `btime`

LXCFS 保留宿主 `btime`。Sysbox 会把 `btime` 改成容器创建时间。

这会导致容器内读取 `/proc/stat` 时，Sysbox 更符合“容器启动时间”的直觉，但不与 LXCFS 对齐。

如果要求严格对齐 LXCFS，需要取消 Sysbox 对 `btime` 的重写。

### 2. CPU idle 时间

LXCFS 基于宿主 `/proc/stat` 的 CPU 时间和 cgroup CPU 使用量推导 idle。

Sysbox 基于容器视角的容量推导 idle：

```text
capacity = elapsed_seconds * visible_cpu_count
idle = capacity - cgroup_used_delta
```

因此，即使 user/system 长期趋势接近，两边每次采样的 idle 和总 tick 也可能不同。

### 3. 小数 CPU quota

LXCFS 使用 `exact_cpu_count()` 对 partial CPU 做 idle 修正。

Sysbox 当前输出整数个 CPU 行，增量容量按可见 CPU 数计算。小数 CPU quota 场景下，CPU 总时间可能不完全贴近 LXCFS。

### 4. CPU 字段完整性

LXCFS cpuview 路径输出固定 10 个 CPU 字段：

```text
user nice system idle iowait irq softirq steal guest guest_nice
```

Sysbox 会解析宿主 `/proc/stat` 的 CPU 字段数量，并按相同数量输出。

这使 Sysbox 对新内核字段更兼容，但与 LXCFS 固定格式不完全一致。

## 是否已经对齐

当前 Sysbox 与 LXCFS 在 `/proc/stat` 上是“功能方向一致，但实现模型未完全对齐”。

已经接近的部分：

- 都隐藏不可见 CPU。
- 都重写 `cpu` 和 `cpuN` 行。
- 都维护历史状态，避免 CPU tick 回退。
- 都主要只保留 user/system/idle，其他 CPU 子字段通常为 0。
- 非 CPU 行大部分都透传宿主。

仍存在差异的部分：

- Sysbox 重写 `btime`，LXCFS 不重写。
- Sysbox idle 计算基于容器容量，LXCFS idle 计算基于宿主 CPU 时间差。
- 小数 CPU quota 的修正策略不同。
- CPU 字段列数策略不同。
- cgroup 定位策略不同。

## 强制对齐建议

如果目标是尽量和 LXCFS 行为一致，建议按优先级处理：

1. 取消 Sysbox 对 `btime` 的容器创建时间重写，保持宿主 `btime`。
2. 将 idle 计算改成 LXCFS 风格：基于宿主 CPU 行的 busy 时间和 cgroup used 时间推导 idle。
3. 补齐小数 CPU quota 的 idle 修正逻辑，对齐 LXCFS `exact_cpu_count()` 行为。
4. 评估是否将 cpuview 输出固定为 10 列，或者保留 Sysbox 当前的宿主字段数量兼容策略。
5. 明确 cgroup 定位是否继续优先使用请求 pid。这个行为对 Kubernetes pod 内多进程读取更准确，但和 LXCFS init pid 模型不同。

## 当前结论

Sysbox 当前 `/proc/stat` 不是严格复刻 LXCFS。它已经实现了容器 CPU 视图，但采用的是独立的容量模型和历史增量模型。

如果只是让容器内 `top`、`uptime`、基础监控看到受限 CPU 视图，当前实现方向可用。

如果目标是与 LXCFS 输出逐字段对齐，优先需要调整 `btime`、idle 计算和小数 CPU quota 处理。
