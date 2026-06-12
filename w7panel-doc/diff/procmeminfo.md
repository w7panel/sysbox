# /proc/meminfo: LXCFS 与 Sysbox 实现差异

本文对比 LXCFS 与 Sysbox 对 `/proc/meminfo` 的虚拟化实现，重点关注内存 limit、usage、`memory.stat` 字段、swap、zswap、fallback 以及字段完整性。

## 代码位置

| 项目 | 入口 | 主要代码 |
| --- | --- | --- |
| LXCFS | `proc_meminfo_read()` | `lxcfs/src/proc_fuse.c` |
| Sysbox | `readMemInfo()` | `sysbox-fs/handler/implementations/procResources.go` |

## 差异总览

| 对比项 | LXCFS 实现 | Sysbox 当前实现 | 影响 |
| --- | --- | --- | --- |
| 入口 | `proc_meminfo_read()` | `readMemInfo()` | 都接管 `/proc/meminfo` |
| cgroup 定位 | 通过 `lookup_initpid_in_store(fc->pid)` 找容器 init pid，再取 memory cgroup | 通过 `cgroupForReq(req)` 取请求进程对应 cgroup | Sysbox 更偏向当前读取进程；LXCFS 更偏向容器 init cgroup |
| 输出方式 | 读取宿主 `/proc/meminfo`，逐行替换指定字段，未识别字段原样保留 | 自己生成固定字段列表 | Sysbox 会缺少宿主 meminfo 后续字段，如 `HugePages_*`、`DirectMap*` 等 |
| `MemTotal` | cgroup memory limit，并 cap 到宿主 `MemTotal` | cgroup memory limit；如果无 limit、0、或大于宿主则回退宿主 | 基本一致 |
| `MemFree` | `memlimit - memusage` | `limit - usage` | 一致 |
| `MemAvailable` | `limit - usage + active_file + inactive_file + slab_reclaimable` | 同公式，但额外 clamp 到 `MemTotal` | Sysbox 更保守；严格对齐 LXCFS 需去掉 clamp |
| `Buffers` | 固定 0 | 固定 0 | 一致 |
| `Cached` | `memory.stat total_cache/file` | `memory.stat file`，缺失时用宿主值兜底 | 基本一致，但 Sysbox 有 host fallback |
| `Active/Inactive` | 来自 anon/file active/inactive 汇总 | 来自 anon/file active/inactive 汇总，缺失时用宿主兜底 | 基本一致 |
| `AnonPages` | `active_anon + inactive_anon - shmem` | 直接使用 `memory.stat anon` | 明显差异 |
| `Mapped` | `mapped_file/file_mapped` | `file_mapped`，缺失时宿主兜底 | 基本一致 |
| `Slab/SReclaimable/SUnreclaim` | 来自 `memory.stat` | 来自 `memory.stat`，缺失时宿主兜底 | 基本一致 |
| `KReclaimable` | 未专门重写，通常随宿主原样保留 | 输出宿主 `KReclaimable`，并按 usedKB 限制 | Sysbox 会裁剪该字段，LXCFS 多数情况保留宿主 |
| `SwapTotal/SwapFree` | 受 `LXCFS_SWAP_ON` 开关控制；未开启则 0 | 根据 cgroup `memory.swap.max` 或 v1 `memory.memsw.*` 推导 | Sysbox 依赖 pod/cgroup 是否配置 swap limit |
| cgroup v1 swap | 对 v1 会把 `swtotal += memlimit`，再按宿主 swap cap | 用 `memsw.limit - memory.limit` | v1 swap 语义存在差异 |
| swappiness=0 | `SwapTotal = swusage` | `SwapTotal = usedKB` | 基本一致 |
| `Zswap/Zswapped` | 支持，受 `LXCFS_ZSWAP_ON` 控制 | 当前不输出 | Sysbox 缺少 zswap 字段 |
| fallback 策略 | 任一关键步骤失败直接回退宿主 `/proc/meminfo` | 无有效 memory limit 时回退宿主；`memory.stat` 缺失时部分字段用宿主兜底 | Sysbox fallback 更细粒度 |
| 字段完整性 | 跟随宿主内核 `/proc/meminfo` 字段变化 | 固定字段集合 | LXCFS 对新内核字段兼容更好 |

## LXCFS 实现说明

LXCFS 的 `/proc/meminfo` 入口是 `proc_meminfo_read()`。

处理流程：

1. 通过 FUSE 请求 pid 找到容器 init pid。
2. 读取 init pid 对应的 memory cgroup。
3. 读取 memory current / usage。
4. 解析 `memory.stat`。
5. 读取 cgroup memory limit，并取有效最小 limit。
6. 如果启用了 swap 虚拟化，读取 cgroup swap 信息。
7. 打开宿主 `/proc/meminfo`。
8. 对特定字段逐行替换。
9. 未识别字段原样透传。

LXCFS 的关键特点是“宿主文件模板 + 指定字段替换”。因此宿主内核新增的 `/proc/meminfo` 字段通常会自然保留。

## Sysbox 实现说明

Sysbox 的 `/proc/meminfo` 入口是 `readMemInfo()`。

处理流程：

1. 解析宿主 `/proc/meminfo`，得到 host 字段 map。
2. 通过请求上下文找到 memory cgroup。
3. 读取 memory limit 和 usage。
4. 如果没有有效 memory limit，或者 limit 大于宿主 `MemTotal`，回退宿主 `/proc/meminfo`。
5. 读取 `memory.stat`。
6. 根据固定字段列表生成新的 `/proc/meminfo` 内容。
7. 部分字段如果 cgroup 统计缺失，则用宿主字段并按容器 limit/usage 做裁剪。

Sysbox 的关键特点是“固定字段生成”。字段更可控，但不会自然继承宿主 `/proc/meminfo` 的后续字段。

## 字段生成差异

### MemTotal

LXCFS 和 Sysbox 都以 cgroup memory limit 作为 `MemTotal`。

LXCFS 会在输出时把 `memlimit` cap 到宿主 `MemTotal`。

Sysbox 如果发现 limit 无效、为 0、或者超过宿主 `MemTotal`，直接返回宿主 `/proc/meminfo`。

### MemFree

两边公式一致：

```text
MemFree = memory_limit - memory_usage
```

### MemAvailable

两边核心公式一致：

```text
MemAvailable = memory_limit - memory_usage + active_file + inactive_file + slab_reclaimable
```

差异是 Sysbox 会把结果 clamp 到 `MemTotal`，LXCFS 当前代码没有看到同样的 clamp。

如果目标是逐字段严格对齐 LXCFS，需要移除 Sysbox 的 `MemAvailable <= MemTotal` 限制。

### AnonPages

LXCFS 使用：

```text
AnonPages = active_anon + inactive_anon - shmem
```

Sysbox 当前使用：

```text
AnonPages = memory.stat anon
```

这是当前 `/proc/meminfo` 中比较明确的字段差异。

### Cached

LXCFS 使用 memory stat 中的 cache/file 统计。

Sysbox 使用 `memory.stat file`，如果不存在则用宿主 `Cached` 兜底并做上限裁剪。

### KReclaimable

LXCFS 没有在当前逻辑中特别重写 `KReclaimable`，通常会随宿主 `/proc/meminfo` 原样保留。

Sysbox 会输出 `KReclaimable`，值来自宿主字段，并按 usedKB 做限制。

这会导致该字段两边不完全一致。

## Swap 差异

### LXCFS

LXCFS 的 swap 由全局选项 `LXCFS_SWAP_ON` 控制。

如果未启用 swap 虚拟化，`SwapTotal` 和 `SwapFree` 输出为 0。

如果启用 swap 虚拟化，LXCFS 会读取 cgroup swap 信息，并按宿主 `SwapTotal` 做 cap。

cgroup v1 下，LXCFS 对 `SwapTotal` 有特殊处理：

```text
swtotal += memlimit
swtotal = min(swtotal, host SwapTotal)
```

### Sysbox

Sysbox 没有类似 `LXCFS_SWAP_ON` 的全局开关。

Sysbox 直接根据 cgroup swap 配置推导：

- cgroup v2: `memory.swap.max` 和 `memory.swap.current`
- cgroup v1: `memory.memsw.limit_in_bytes - memory.limit_in_bytes`

如果 cgroup 没有明确 swap limit，Sysbox 会显示 0。

这也是 Kubernetes 中 pod `free -h` swap 为 0 的常见原因：pod 的 request/limit 或 kubelet swap 策略没有产生有效 `memory.swap.max`。

## Zswap 差异

LXCFS 支持：

- `Zswap`
- `Zswapped`

并受 `LXCFS_ZSWAP_ON` 控制。

Sysbox 当前没有输出这两个字段。

如果宿主 `/proc/meminfo` 包含这两个字段，LXCFS 会根据配置输出虚拟值或 0；Sysbox 当前固定字段列表里没有它们。

## fallback 差异

LXCFS 的 fallback 更整体：

- 找不到 memory cgroup，回退宿主。
- 读取 memory current 失败，回退宿主。
- 解析 `memory.stat` 失败，回退宿主。
- 读取 memory limit 失败，回退宿主。

Sysbox 的 fallback 更细粒度：

- 解析宿主 `/proc/meminfo` 失败，回退宿主。
- 没有有效 memory limit，回退宿主。
- limit 为 0，回退宿主。
- limit 大于宿主 `MemTotal`，回退宿主。
- `memory.stat` 缺失时，不整体回退，而是部分字段使用宿主值兜底。

## 是否已经对齐

当前 Sysbox 与 LXCFS 在 `/proc/meminfo` 上是“核心字段基本对齐，但输出模型未完全对齐”。

已经接近的部分：

- `MemTotal` 基于 cgroup memory limit。
- `MemFree` 基于 `limit - usage`。
- `MemAvailable` 使用相同核心公式。
- `Buffers` 和 `SwapCached` 输出 0。
- `Cached`、`Active`、`Inactive`、`Slab` 等主要来自 `memory.stat`。
- swap 会基于 cgroup swap 限制生成。

仍存在差异的部分：

- Sysbox 固定字段生成，LXCFS 逐行替换并保留宿主未知字段。
- `AnonPages` 公式不同。
- Sysbox 对 `MemAvailable` 做了额外 clamp。
- `KReclaimable` 处理不同。
- Sysbox 没有 `Zswap` / `Zswapped`。
- cgroup v1 swap 计算语义不同。
- fallback 策略不同。

## 强制对齐建议

如果目标是尽量和 LXCFS 行为一致，建议按优先级处理：

1. 将 Sysbox 改为读取宿主 `/proc/meminfo`，逐行替换指定字段，未识别字段原样保留。
2. 将 `AnonPages` 改为 `active_anon + inactive_anon - shmem`。
3. 评估是否去掉 `MemAvailable` 的 `<= MemTotal` clamp，以严格匹配 LXCFS。
4. 增加 `Zswap` / `Zswapped` 字段输出，默认行为按 LXCFS 配置模型决定。
5. 评估 cgroup v1 swap 是否需要改成 LXCFS 的 `swtotal += memlimit` 语义。
6. 明确是否保留 Sysbox 当前“请求 pid 优先”的 cgroup 定位逻辑。该逻辑对 Kubernetes pod 内多进程读取更准确，但与 LXCFS init pid 模型不同。

## 当前结论

Sysbox 当前 `/proc/meminfo` 已经覆盖容器内最常用的内存视图字段，`free`、基础监控和常规应用读取的核心值基本可用。

如果目标是与 LXCFS 逐字段严格一致，主要需要调整输出模型、`AnonPages`、`MemAvailable`、zswap 字段和 v1 swap 语义。
