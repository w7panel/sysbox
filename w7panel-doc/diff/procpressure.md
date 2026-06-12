# /proc/pressure/{io,cpu,memory}: LXCFS 与 Sysbox 实现差异

本文对比 LXCFS 与 Sysbox 对 PSI pressure 文件的虚拟化实现，覆盖：

- `/proc/pressure/io`
- `/proc/pressure/cpu`
- `/proc/pressure/memory`

重点关注 cgroup pressure 数据来源、fallback 行为、父目录处理、只读/写入能力，以及 LXCFS PSI trigger write/poll 与 Sysbox 当前实现的差异。

## 代码位置

| 项目 | 入口 | 主要代码 |
| --- | --- | --- |
| LXCFS | `proc_pressure_read()`、`proc_psi_trigger_write()`、`proc_psi_trigger_poll()` | `lxcfs/src/proc_fuse.c`、`lxcfs/src/cgroups/cgfsng.c` |
| Sysbox | `readPressure()`、`ProcPressure_Handler` | `sysbox-fs/handler/implementations/procResources.go`、`sysbox-fs/handler/implementations/procPressure.go` |

## 文件映射

| proc 文件 | LXCFS controller | LXCFS cgroup 文件 | Sysbox controller | Sysbox cgroup 文件 |
| --- | --- | --- | --- | --- |
| `/proc/pressure/io` | `blkio` | `io.pressure` | `blkio` | `io.pressure` |
| `/proc/pressure/cpu` | `cpu` | `cpu.pressure` | `cpu` | `cpu.pressure` |
| `/proc/pressure/memory` | `memory` | `memory.pressure` | `memory` | `memory.pressure` |

## 差异总览

| 对比项 | LXCFS 实现 | Sysbox 当前实现 | 影响 |
| --- | --- | --- | --- |
| 入口 | 三个 pressure 文件共用 `proc_pressure_read()` | 三个 handler 共用 `readPressure()` | 结构类似 |
| 父目录 `/proc/pressure` | 支持目录 lookup/readdir | 新增 `ProcPressure_Handler` 只读目录 | 都能稳定挂载/访问父目录 |
| cgroup 定位 | 通过容器 init pid 找对应 controller cgroup | 通过 `cgroupForReq(req)`，再 `pruneInitScopeCgroup()` | Sysbox 更偏请求进程，并避免误读 `/init.scope` 叶子 cgroup |
| 读取数据源 | 打开 cgroup 的 `*.pressure` 文件逐行复制 | 读取 cgroup 的 `*.pressure` 内容 | 读视图基本一致 |
| cgroup v2 | 读取 unified hierarchy 中对应 pressure 文件 | 优先读取 cgroup v2 `io.pressure/cpu.pressure/memory.pressure` | 基本一致 |
| cgroup v1 | 通过 controller hierarchy 查找 pressure 文件 | fallback 读取对应 v1 controller 下的 pressure 文件 | 基本一致，实际依赖内核是否提供 |
| fallback | 找不到 cgroup 或 pressure fd 时回退宿主 `/proc/pressure/<name>` | 读不到 cgroup pressure 时回退宿主 `/proc/pressure/<name>` | 都可能暴露宿主 PSI |
| 输出改写 | 不改写内容，直接复制 cgroup 或宿主文件 | 不改写内容，只补 trailing newline | 内容基本一致 |
| offset 读取 | 使用 FUSE file handle cache | 通用 read-only resource 读取路径 | 都支持普通分块读取 |
| 文件权限 | pressure fd 以 `O_RDWR` 打开，以支持 trigger write/poll | handler 文件 mode 是 `0444` | Sysbox 当前只读 |
| PSI trigger write | 支持，受 `--enable-psi-poll` / `LXCFS_PSI_POLL_ON` 控制 | 不支持 | 依赖 PSI trigger 的程序在 Sysbox 内不能按 LXCFS 行为工作 |
| PSI trigger poll | 支持 `poll()` / `POLLPRI` 通知 | 不支持 | Sysbox 只满足读取 PSI 指标，不满足事件触发器 |
| 与 LXCFS 对齐程度 | 标准参考 | read 视图基本对齐；write/poll 未对齐 | 差异集中在 PSI trigger 能力 |

## LXCFS 实现说明

LXCFS 的三个 pressure 文件共用 `proc_pressure_read()`。

读取流程：

1. 根据文件类型选择 fallback path、controller 和 `get_pressure_fd()`。
2. 通过 FUSE 请求 pid 找容器 init pid。
3. 获取 init pid 对应 controller 的 cgroup 路径。
4. 打开 cgroup 下对应 pressure 文件：
   - `io.pressure`
   - `cpu.pressure`
   - `memory.pressure`
5. 打开成功后逐行复制输出。
6. 如果找不到 cgroup、找不到 pressure 文件或打开失败，则回退宿主 `/proc/pressure/<name>`。

LXCFS 在 cgroup 层打开 pressure 文件时使用 `O_RDWR`，因为它还支持 PSI trigger 的写入和 poll。

## Sysbox 实现说明

Sysbox 的三个 pressure 文件通过 `readPressure(name, controller, cgroupFile)` 注册：

- `/proc/pressure/io` -> `readPressure("io", "blkio", "io.pressure")`
- `/proc/pressure/cpu` -> `readPressure("cpu", "cpu", "cpu.pressure")`
- `/proc/pressure/memory` -> `readPressure("memory", "memory", "memory.pressure")`

读取流程：

1. 通过请求上下文获取 cgroup。
2. 调用 `pruneInitScopeCgroup()`，避免读取 systemd `/init.scope` 这类叶子 cgroup。
3. 优先读取 cgroup v2 下的 pressure 文件。
4. 如果 v2 读取失败，尝试读取 v1 controller 下的 pressure 文件。
5. 如果仍失败，回退宿主 `/proc/pressure/<name>`。
6. 返回内容时确保有 trailing newline。

Sysbox 还单独实现了 `/proc/pressure` 父目录 handler，目录下暴露 `io`、`cpu`、`memory` 三个只读文件。

## PSI trigger write/poll 差异

LXCFS 不只支持读取 PSI 指标，还支持 PSI trigger。

典型用法是向 pressure 文件写入触发器：

```text
some <threshold_us> <window_us>
full <threshold_us> <window_us>
```

然后通过 `poll()` 等待压力事件。

LXCFS 的实现行为：

- 只有启用 `--enable-psi-poll` 时才允许。
- 会解析并校验写入参数。
- 会把 trigger 写到底层 cgroup pressure fd。
- 会保存 trigger 状态到 FUSE file handle。
- `poll()` 时通过 `POLLPRI` / FUSE notify 机制通知事件。

Sysbox 当前没有实现这部分：

- pressure 文件是只读 `0444`。
- 没有 write handler。
- 没有 poll handler。
- 不支持 PSI trigger 生命周期。

因此，Sysbox 当前只对“读取 pressure 指标”的场景对齐 LXCFS；对“写 trigger 并 poll 事件”的场景没有对齐。

## 三个文件的输出差异

### /proc/pressure/io

两边都优先读取 cgroup 的 `io.pressure`。

正常输出类似：

```text
some avg10=0.00 avg60=0.00 avg300=0.00 total=0
full avg10=0.00 avg60=0.00 avg300=0.00 total=0
```

如果 cgroup 文件不可用，两边都会回退宿主 `/proc/pressure/io`。

### /proc/pressure/cpu

两边都优先读取 cgroup 的 `cpu.pressure`。

需要注意：不同内核和 cgroup 配置下，CPU pressure 可能只有 `some` 行，没有 `full` 行。这属于内核 PSI 文件本身的格式差异，不是 Sysbox 或 LXCFS 单独生成。

如果 cgroup 文件不可用，两边都会回退宿主 `/proc/pressure/cpu`。

### /proc/pressure/memory

两边都优先读取 cgroup 的 `memory.pressure`。

正常输出通常有 `some` 和 `full` 两行。

如果 cgroup 文件不可用，两边都会回退宿主 `/proc/pressure/memory`。

## fallback 与隔离性

LXCFS 和 Sysbox 都会在无法读取 cgroup pressure 文件时回退宿主 `/proc/pressure/*`。

优点：

- 保证文件可读。
- 兼容只检查 PSI 文件存在和格式的工具。
- 避免因为内核/cgroup 不支持 PSI 导致应用读取失败。

缺点：

- 容器内可能看到宿主级 PSI。
- 在压力监控场景下，回退宿主会让数据不再代表容器自身。

因此排查时应确认容器内文件是否真的来自 cgroup：

```bash
cat /proc/pressure/io
cat /proc/pressure/cpu
cat /proc/pressure/memory

cat /sys/fs/cgroup/io.pressure
cat /sys/fs/cgroup/cpu.pressure
cat /sys/fs/cgroup/memory.pressure
```

如果两者一致，说明使用的是 cgroup 视图；如果与宿主 `/proc/pressure/*` 一致，则可能发生了 fallback。

## 是否已经对齐

当前 Sysbox 与 LXCFS 在 pressure 读取视图上基本对齐。

已经对齐的部分：

- 三个文件都优先读取 cgroup pressure。
- 读取失败时回退宿主 `/proc/pressure/*`。
- 不重写 pressure 内容。
- 支持 `/proc/pressure` 父目录。
- 支持 io/cpu/memory 三类 PSI 文件。

仍存在差异的部分：

- Sysbox 使用请求进程 cgroup，并额外 prune `/init.scope`；LXCFS 使用容器 init pid cgroup。
- Sysbox 只读；LXCFS 支持 PSI trigger write/poll。
- Sysbox 没有 `--enable-psi-poll` 等价开关。
- Sysbox 返回内容会补 trailing newline。

## 强制对齐建议

如果目标是只对齐 PSI 读取视图，当前 Sysbox 基本不需要调整。

如果目标是完整对齐 LXCFS，需要评估：

1. 是否实现 pressure 文件 write handler。
2. 是否实现 PSI trigger 参数解析和校验。
3. 是否把 trigger 写到底层 cgroup pressure fd。
4. 是否实现 poll / notify 机制。
5. 是否增加类似 LXCFS `--enable-psi-poll` 的开关。

这部分实现复杂度明显高于只读视图。若业务只是 `cat /proc/pressure/*` 或监控采集 `avg10/avg60/avg300/total`，当前只读实现已经足够。

## 当前结论

Sysbox 当前 `/proc/pressure/{io,cpu,memory}` 的读视图与 LXCFS 基本一致。

主要差异是 LXCFS 额外支持 PSI trigger 的 write/poll，而 Sysbox 当前不支持。

如果只关注容器内 PSI 指标读取，Sysbox 当前实现可用。

如果应用依赖向 `/proc/pressure/*` 写 trigger 并通过 `poll()` 等待压力事件，Sysbox 与 LXCFS 尚未对齐。
