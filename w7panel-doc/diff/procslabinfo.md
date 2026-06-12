# /proc/slabinfo: LXCFS 与 Sysbox 实现差异

本文对比 LXCFS 与 Sysbox 对 `/proc/slabinfo` 的虚拟化实现，重点关注 cgroup v1 `memory.kmem.slabinfo`、cgroup v2 限制、fallback 行为以及隔离性影响。

## 代码位置

| 项目 | 入口 | 主要代码 |
| --- | --- | --- |
| LXCFS | `proc_slabinfo_read()` | `lxcfs/src/proc_fuse.c`、`lxcfs/src/cgroups/cgfsng.c` |
| Sysbox | `readSlabinfo()` | `sysbox-fs/handler/implementations/procResources.go` |

## 差异总览

| 对比项 | LXCFS 实现 | Sysbox 当前实现 | 影响 |
| --- | --- | --- | --- |
| 入口 | `proc_slabinfo_read()` | `readSlabinfo()` | 都接管 `/proc/slabinfo` |
| cgroup 定位 | 通过容器 init pid 找 memory cgroup | 通过 `cgroupForReq(req)` 找请求对应 cgroup | Sysbox 更偏当前读取进程；LXCFS 更偏容器 init cgroup |
| cgroup v1 数据源 | `memory.kmem.slabinfo` | `memory.kmem.slabinfo` | 核心数据源一致 |
| cgroup v2 数据源 | 通常没有等价 `memory.kmem.slabinfo` | 通常没有等价 `memory.kmem.slabinfo` | cgroup v2 下无法生成真实容器级 slabinfo |
| 成功路径 | 打开 cgroup 的 `memory.kmem.slabinfo`，逐行复制输出 | 读取 cgroup 的 `memory.kmem.slabinfo`，确保结尾换行后输出 | 输出内容基本一致 |
| fallback | 找不到 memory cgroup、找不到 slabinfo fd、打开失败时回退宿主 `/proc/slabinfo` | 读不到 v1 `memory.kmem.slabinfo` 或内容为空时回退宿主 `/proc/slabinfo` | 两边都会在失败时暴露宿主 slabinfo |
| offset 读取 | 使用 FUSE file handle cache 支持 offset 读取 | 通用 read-only resource 路径处理读取 | 都可被普通工具读取 |
| 输出改写 | 不改写 slab 行，直接复制 cgroup 或宿主文件 | 不改写 slab 行，直接返回 cgroup 或宿主文件 | 没有字段级虚拟化 |
| 隔离性 | cgroup v1 下较好；cgroup v2/fallback 时暴露宿主细节 | cgroup v1 下较好；cgroup v2/fallback 时暴露宿主细节 | cgroup v2 下隔离性不理想 |
| 与 LXCFS 对齐程度 | 标准参考 | 基本对齐 | 当前 Sysbox 行为和 LXCFS 非常接近 |

## LXCFS 实现说明

LXCFS 的 `/proc/slabinfo` 入口是 `proc_slabinfo_read()`。

处理流程：

1. 通过 FUSE 请求 pid 找到容器 init pid。
2. 如果 init pid 无效或共享 pid namespace，则使用请求 pid。
3. 获取该 pid 的 memory cgroup。
4. 调用 `get_memory_slabinfo_fd()` 打开 cgroup 对应的 `memory.kmem.slabinfo`。
5. 如果打开成功，逐行复制该文件内容作为 `/proc/slabinfo` 输出。
6. 如果任一步失败，回退读取宿主 `/proc/slabinfo`。

LXCFS 获取 slabinfo fd 的逻辑会检查 memory hierarchy 中是否存在 `memory.kmem.slabinfo`，然后按 cgroup 路径打开对应文件。

## Sysbox 实现说明

Sysbox 的 `/proc/slabinfo` 入口是 `readSlabinfo()`。

处理流程：

1. 通过请求上下文获取 cgroup。
2. 尝试读取 cgroup v1 memory 控制器下的 `memory.kmem.slabinfo`。
3. 如果读取成功且内容非空，直接返回该内容，并确保结尾有换行。
4. 如果读取失败或内容为空，回退读取宿主 `/proc/slabinfo`。

Sysbox 当前没有尝试从 cgroup v2 的 `memory.stat` 重建 slabinfo，因为 cgroup v2 没有完整等价的 slab cache 明细接口。

## 关键行为

### cgroup v1

cgroup v1 下，如果内核启用了 kmem accounting 且存在：

```text
memory.kmem.slabinfo
```

那么 LXCFS 和 Sysbox 都会输出该 cgroup 的 slabinfo。

这种情况下两边基本一致。

### cgroup v2

cgroup v2 通常只有 `memory.stat` 中的 slab 汇总项，例如：

```text
slab
slab_reclaimable
slab_unreclaimable
```

这些字段只能表示汇总值，不能还原 `/proc/slabinfo` 中每个 slab cache 的完整明细。

因此 cgroup v2 下，LXCFS 和 Sysbox 都很难生成真实的容器级 `/proc/slabinfo`。

当前 Sysbox 会回退宿主 `/proc/slabinfo`。

### fallback 到宿主

两边都会 fallback 到宿主 `/proc/slabinfo`。

这保证了工具兼容性，例如只检查 header 或需要 slabinfo 文件存在的诊断工具不会失败。

但代价是会暴露宿主 slab cache 明细，隔离性不如真实容器视图。

## 是否已经对齐

当前 Sysbox 与 LXCFS 在 `/proc/slabinfo` 上基本对齐。

已经对齐的部分：

- cgroup v1 优先读取 `memory.kmem.slabinfo`。
- 不做字段级重写。
- cgroup 数据不可用时回退宿主 `/proc/slabinfo`。
- cgroup v2 下没有真实 slabinfo 明细来源。

仍存在轻微差异的部分：

- LXCFS 使用容器 init pid 定位 cgroup；Sysbox 使用请求上下文定位 cgroup。
- LXCFS 通过 fd 逐行复制；Sysbox 一次性读取文件内容。
- Sysbox 会确保返回内容有 trailing newline。

这些差异通常不会影响 `/proc/slabinfo` 的最终内容。

## 强制对齐建议

如果目标是继续保持与 LXCFS 行为一致，当前 Sysbox 不需要做大改。

可以考虑的细节：

1. 保持 cgroup v1 `memory.kmem.slabinfo` 优先。
2. 保持失败时回退宿主 `/proc/slabinfo`，以匹配 LXCFS 兼容性。
3. 如果未来要提升隔离性，可以在 cgroup v2 下选择输出只有 header 的空 slabinfo，但这会偏离 LXCFS fallback 行为。
4. 如果要更贴近 LXCFS 的 cgroup 定位语义，可评估是否改为容器 init pid memory cgroup；但 Sysbox 当前请求 pid 优先对 Kubernetes 多进程场景更直接。

## 当前结论

Sysbox 当前 `/proc/slabinfo` 与 LXCFS 基本一致。

cgroup v1 下，两边都优先使用 `memory.kmem.slabinfo`。

cgroup v2 下，由于内核没有等价的 per-slab cache 明细接口，两边都无法生成完整容器级 slabinfo，当前回退宿主 `/proc/slabinfo` 是兼容性优先的选择，但会暴露宿主 slab cache 细节。
