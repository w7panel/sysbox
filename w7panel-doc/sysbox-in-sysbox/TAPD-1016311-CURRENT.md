# 1016311：sysbox-fs 调度与缓存（当前方案）

> 2026-11-19 更新。TAPD 1016311 原正文及伪代码为废弃设计稿；以本文和实际发布验收记录为准。

## 已实现

- `/proc/loadavg` 按视图维持独立的 5 秒 EWMA 采样状态，所有者退出后停止并清理。瞬时资源文件按请求视图使用短 TTL 快照，并合并同键并发未命中；容器注销时清理所属状态。
- 全局原始数据可有限缓存；最终输出仍必须按每次请求的 cgroup 和 PID namespace 实时确定视图。runc 登记的身份仅作为种子，不能用 L1 init 快照直接替代内层请求者解析。
- `/proc/swaps` 最终输出含请求者 cgroup 的 swap 用量，不能作为全局静态输出缓存。已采用按请求视图的短 TTL 快照，保证分段读取一致。
- cgroup v2 请求路径和视图键已减少无用解析与分配。键生成本地微基准约 32→21 ns/次；该数字不代表整机 sysbox-fs CPU 已下降。

## 不采用与未完成

- 不采用容器 TTL 注解；不把所有 handler 改为统一 `procCache`/`RWMutex`，也不把 cpuinfo、swaps 等最终输出一概做全局长 TTL 缓存。缓存边界按原始数据和请求视图区分。
- 不采用旧稿中的 runc 创建期完整 uptime/loadavg/cpuinfo 最终输出种子及 runc 下发透传/模拟文件名单。短 TTL 种子可能首读前过期，外层 init 种子不能代表不同 cgroup/PID namespace 的请求者；名单会与现有 FS handler 分流重复，还需扩大跨组件协议。现有 handler 注册和实时请求者解析继续负责分流。
- L2 `sysbox-runc-lite` 不启动、登记或依赖独立 sysbox-fs/mgr；其 `/proc` 走既有挂载与默认运行时行为，部分读取可碰到继承的 L1 FUSE 挂载，不增加 L2 专用虚拟化逻辑。

## 验收状态

L0/L2 功能回归已通过，但 CPU 目标未达成。在 218/server1 的隔离 QEMU VM 中，同一组 8 Pod、16 容器各重新打开四个 `/proc` 文件 250 次，共 16,000 次成功读取：v40 的 FS CPU 为 26.57 秒，v47 源码候选为 56.28/59.18 秒，回退 v40 后为 26.32 秒。分文件各 4,000 次成功读取时，v40→v47 的 FS CPU 秒分别为 meminfo 7.63→16.46、stat 8.04→15.45、uptime 3.91→6.92、loadavg 5.80→11.90；回退分布于所有文件。当前每次请求实时读取 cgroup/PID namespace，保住内层视图正确性，但带来明显开销；不能为追求旧版 CPU 数字而复用外层 init 快照。此为隔离 VM 源码候选结果，不代表 218 业务负载或正式 Release。详情见 [KNOWN-ISSUES.md](KNOWN-ISSUES.md)。
