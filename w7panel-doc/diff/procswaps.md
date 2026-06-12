# /proc/swaps: LXCFS 与 Sysbox 实现差异

本文对比 LXCFS 与 Sysbox 对 `/proc/swaps` 的虚拟化实现，重点关注 swap 开关、cgroup v1/v2 swap 语义、宿主 SwapTotal 封顶、无有效限制时的输出策略，以及与 `/proc/meminfo` 中 `SwapTotal` / `SwapFree` 的一致性。

## 代码位置

| 项目 | 入口 | 主要代码 |
| --- | --- | --- |
| LXCFS | `proc_swaps_read()` | `lxcfs/src/proc_fuse.c` |
| Sysbox | `ProcSwaps.readSwaps()` | `sysbox-fs/handler/implementations/procSwaps.go`、`sysbox-fs/handler/implementations/procResources.go` |

## 差异总览

| 对比项 | LXCFS 实现 | Sysbox 当前实现 | 影响 |
| --- | --- | --- | --- |
| 入口 | `proc_swaps_read()` | `ProcSwaps.readSwaps()` | 都接管 `/proc/swaps` |
| cgroup 定位 | 通过容器 init pid 找 memory cgroup | 通过 `cgroupForReq(req)` 找请求对应 cgroup | Sysbox 更偏当前读取进程；LXCFS 更偏容器 init cgroup |
| 输出 header | 固定输出 `Filename Type Size Used Priority` | 固定输出 `Filename Type Size Used Priority` | 一致 |
| swap 行名称 | `none ... virtual` | `none ... virtual` | 基本一致 |
| 全局开关 | 受 `LXCFS_SWAP_ON` 控制 | 没有全局 swap 开关 | Sysbox 只看 cgroup 是否有有效 swap limit |
| 无有效 swap | 未启用 `LXCFS_SWAP_ON` 或 swtotal 为 0 时只输出 header | cgroup 没有有效 swap limit 时只输出 header | 输出结果通常一致，但触发条件不同 |
| cgroup v2 | 通过 `get_swap_info()` 读取 cgroup v2 swap 信息 | 读取有效 `memory.swap.max` 和 `memory.swap.current` | 目标一致 |
| cgroup v2 `max` | LXCFS 依赖 `get_swap_info()` 内部语义 | Sysbox 对 `memory.swap.max=max` 不直接显示，会向父层查找有效数值限制 | Sysbox 避免把无限制 swap 暴露为宿主 swap |
| cgroup v1 total | LXCFS 先取 swap，再执行 `swtotal = memlimit + swtotal` | Sysbox 使用 `memory.memsw.limit - memory.limit` | v1 语义存在差异 |
| cgroup v1 used | 来自 `get_swap_info()` 的 `swusage` | `memory.memsw.usage - memory.usage` | 方向一致 |
| 宿主 SwapTotal cap | 直接 `swtotal = min(swtotal, host SwapTotal)` | 只有 `hostSwapTotalKB > 0` 时才 cap | 宿主无 swap 时语义有差异 |
| swappiness=0 | `swtotal = swusage` | `totalKB = usedKB` | 基本一致 |
| used > total | 没有明显额外 clamp，依赖前面计算 | 如果 used 大于 total，Sysbox clamp 到 total | Sysbox 更保守 |
| fallback | 找不到 memory cgroup 时回退宿主 `/proc/swaps`；部分失败路径返回空 | 不回退宿主 swap 行；没有有效 cgroup swap 时只输出 header | Sysbox 更保守，避免泄露宿主 swap |
| 与 `/proc/meminfo` | `/proc/swaps` 和 `/proc/meminfo` 使用相近 swap 逻辑 | `/proc/swaps` 和 `/proc/meminfo` 共用 `swapInfoForCgroup()` | Sysbox 内部一致性较好 |
| swapon/swapoff | LXCFS 有相关虚拟化语义和选项 | Sysbox 不接管容器内 swapon/swapoff 对宿主的真实影响 | 容器内 swapon 不应改变 Sysbox 虚拟 swap 视图 |
| 与 LXCFS 对齐程度 | 标准参考 | 主要语义接近，但策略更保守 | 差异集中在全局开关、fallback、v1 total 和 host cap |

## LXCFS 实现说明

LXCFS 的 `/proc/swaps` 入口是 `proc_swaps_read()`。

处理流程：

1. 通过 FUSE 请求 pid 找到容器 init pid。
2. 获取 init pid 对应的 memory cgroup。
3. 读取 cgroup memory limit 和 current usage。
4. 如果启用了 `LXCFS_SWAP_ON`，通过 `get_swap_info()` 获取 swap total、swap usage、swappiness。
5. 固定输出 `/proc/swaps` header。
6. 读取宿主 `/proc/meminfo` 的 `SwapTotal` 和 `SwapFree`。
7. cgroup v1 下将 `memlimit` 加入 `swtotal`。
8. 使用宿主 `SwapTotal` 对 `swtotal` 封顶。
9. 如果 swappiness 为 0，则把 `swtotal` 改成 `swusage`。
10. 如果 `swtotal > 0`，输出一条 `none virtual` swap 行。

LXCFS 的关键特点是有全局 swap 开关，并且会按照宿主 `SwapTotal` 做封顶。

## Sysbox 实现说明

Sysbox 的 `/proc/swaps` 入口是 `ProcSwaps.readSwaps()`。

处理流程：

1. 固定输出 `/proc/swaps` header。
2. 通过请求上下文获取 cgroup。
3. 调用 `swapInfoForCgroup()` 获取 swap total 和 used。
4. cgroup v2 优先读取有效 `memory.swap.max`，并读取 `memory.swap.current`。
5. cgroup v1 fallback 到 `memory.memsw.limit_in_bytes - memory.limit_in_bytes`。
6. 根据宿主 `SwapTotal` 做可选封顶。
7. swappiness 为 0 时，将 total 设为 used。
8. 如果 total 为 0，只输出 header。
9. 如果 total 大于 0，输出一条 `none virtual` swap 行。

Sysbox 的关键特点是没有全局开关，完全由 cgroup 是否存在有效 swap limit 决定是否显示 swap。

## 输出格式

两边都会输出类似格式：

```text
Filename                                Type            Size    Used    Priority
none                                    virtual         <total> <used>  0
```

如果没有可见 swap，则只输出 header。

## cgroup v2 差异

Sysbox 当前明确处理 cgroup v2：

- `memory.swap.max` 为数值时，作为 swap total。
- `memory.swap.current` 作为 used。
- 如果当前 cgroup 的 `memory.swap.max=max`，会向父层查找有效数值限制。
- 如果没有有效数值限制，则隐藏 swap，只输出 header。

这个策略避免把无限制或未配置状态误显示成宿主 swap。

LXCFS 的 v2 语义封装在 `get_swap_info()` 中，最终在 `/proc/swaps` 输出前仍会按宿主 `SwapTotal` cap。

## cgroup v1 差异

LXCFS cgroup v1 下会执行：

```text
swtotal = memlimit + swtotal
swtotal = min(swtotal, host SwapTotal)
```

Sysbox cgroup v1 下使用：

```text
total = memory.memsw.limit_in_bytes - memory.limit_in_bytes
used = memory.memsw.usage_in_bytes - memory.usage_in_bytes
```

因此 v1 下两边对 `Size` 的解释不完全一致。

如果目标是严格对齐 LXCFS，需要重新评估 v1 total 语义。

## 宿主 SwapTotal 封顶差异

LXCFS 直接使用宿主 `SwapTotal` 封顶：

```text
if hostswtotal < swtotal:
    swtotal = hostswtotal
```

Sysbox 只有宿主 `SwapTotal > 0` 时才封顶：

```text
if hostSwapTotalKB > 0 && totalKB > hostSwapTotalKB:
    totalKB = hostSwapTotalKB
```

这意味着宿主没有 swap 时，两边可能不同：

- LXCFS 更可能把 total 压到 0。
- Sysbox 在 cgroup 有数值型 swap limit 时可能保留该 limit。

这个差异之前也体现在 `/proc/meminfo` 的 `SwapTotal` 逻辑中。

## fallback 差异

LXCFS 如果找不到 memory cgroup，会回退宿主 `/proc/swaps`。

Sysbox 不回退宿主 swap 行。只要没有有效 cgroup swap info，就只输出 header。

这是 Sysbox 有意更保守的地方：避免容器在没有明确 swap limit 的情况下看到宿主 swap 设备和容量。

## 与 /proc/meminfo 的一致性

Sysbox 的 `/proc/swaps` 和 `/proc/meminfo` 共用 `swapInfoForCgroup()`。

因此正常情况下：

```text
/proc/swaps 第二行 Size == /proc/meminfo SwapTotal
/proc/swaps 第二行 Used == SwapTotal - SwapFree
```

LXCFS 的 `/proc/swaps` 和 `/proc/meminfo` 也使用相近的 swap 计算逻辑，但实现分散在两个读取函数里。

## Kubernetes LimitedSwap 影响

在 Kubernetes / K3s 中，Sysbox 是否显示 swap 取决于 kubelet 是否给 pod cgroup 写入有效 `memory.swap.max`。

常见情况：

| Pod resources | QoS | 常见 `memory.swap.max` | Sysbox `/proc/swaps` |
| --- | --- | --- | --- |
| `requests.memory < limits.memory` | Burstable | 数值型 | 显示一条 virtual swap |
| `requests.memory == limits.memory` | Guaranteed | `0` | 只显示 header |
| 只写 `limits.memory` | 通常 Guaranteed | `0` | 只显示 header |
| 未配置有效 swap | BestEffort/Burstable，取决于 kubelet | `max` 或无有效限制 | 只显示 header |

因此看到 `/proc/swaps` 只有 header 时，优先检查：

```bash
cat /sys/fs/cgroup/memory.swap.max
cat /sys/fs/cgroup/memory.swap.current
```

## 是否已经对齐

当前 Sysbox 与 LXCFS 在 `/proc/swaps` 上是“主要语义接近，但 Sysbox 更保守”。

已经接近的部分：

- 都输出固定 header。
- 有有效 swap 时都输出一条 `none virtual` 行。
- 都处理 swappiness 为 0 的情况。
- 都会用宿主 `SwapTotal` 参与封顶。
- `/proc/swaps` 和 `/proc/meminfo` 的 swap 视图保持一致。

仍存在差异的部分：

- Sysbox 没有 `LXCFS_SWAP_ON` 全局开关。
- Sysbox 不回退宿主 `/proc/swaps` swap 行。
- cgroup v1 total 计算语义不同。
- 宿主 `SwapTotal=0` 时 cap 语义不同。
- Sysbox 会隐藏 `memory.swap.max=max` 这类无限制/默认状态。
- Sysbox 不接管 swapon/swapoff 虚拟化。

## 强制对齐建议

如果目标是尽量和 LXCFS 行为一致，建议按优先级处理：

1. 评估是否增加类似 `LXCFS_SWAP_ON` 的全局开关。
2. cgroup v1 下评估是否改为 LXCFS 的 `swtotal = memlimit + swtotal` 语义。
3. 宿主 `SwapTotal=0` 时，评估是否也强制 cap 到 0。
4. 评估找不到 memory cgroup 时是否回退宿主 `/proc/swaps`。这会更接近 LXCFS，但会暴露宿主 swap。
5. 保留当前 `memory.swap.max=max` 隐藏策略，除非明确要求完全复刻 LXCFS。

## 当前结论

Sysbox 当前 `/proc/swaps` 已经实现容器级 swap 视图，且和 `/proc/meminfo` 的 swap 字段共用计算逻辑。

相比 LXCFS，Sysbox 更保守：没有有效 cgroup swap limit 就隐藏 swap，不展示宿主 swap。

如果目标是 Kubernetes LimitedSwap 场景下的容器隔离视图，当前 Sysbox 策略更安全。

如果目标是与 LXCFS 逐行为严格一致，需要重点调整全局开关、fallback、cgroup v1 total 和宿主 SwapTotal cap 语义。
