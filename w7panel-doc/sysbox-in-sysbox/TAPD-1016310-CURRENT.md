# 1016310：sysbox-fs FUSE 会话恢复（当前方案）

> 2026-11-18 更新。TAPD 1016310 原正文为废弃设计稿；以本文和实际发布验收记录为准。

## 行为与边界

- 同一宿主启动周期内，正常停止的 sysbox-fs 排空请求并保存登记状态；systemd FD store 保留 seccomp listener。新进程恢复登记视图和挂载。CO-RE eBPF 识别旧 FUSE 设备上的 `EIO`/`ENOTCONN`，按 namespace/设备去重后触发修复。
- 保留每分钟一次的静默故障兜底：只检查登记容器及其 cgroup 子树中的 mount namespace。开销随登记容器和相关进程数变化，不能宣称“零轮询”或“容器数量无关”。
- 新 `open` 在修复后可读；业务已持有的旧 FD 仍指向断开的会话，须由业务关闭并重新打开。不会以宿主 `/proc` 透传作为降级，因为这会破坏隔离。
- 脏 checkpoint、PID 复用、未知旧挂载、登记不完整、目标父挂载为 shared、子挂载传播不受支持等情况安全拒绝。目标挂载自身为 shared 不一概拒绝。
- 这是同机正常停止后的恢复，不承诺进程崩溃、宿主重启或首次从旧 unit 直接保留旧 Pod 的恢复。

## 安装与验收

首次从旧版升级时，必须完整安装匹配的 Chart/systemd unit，确认 `--session-recovery`、`FileDescriptorStoreMax` 与 `FileDescriptorStorePreserve=yes` 生效，并迁移旧 Pod。旧 unit 创建的会话没有可接管的 checkpoint/listener；仅换新二进制重启会使存量虚拟 `/proc` 断连。后续重启验收须确认 Pod UID、容器身份不变，检查新 `open`、旧 FD 行为、分钟兜底、服务日志和恢复指标。

源码有本机恢复指标 socket；正式 Alertmanager 接收地址尚未提供，通知链路未验收。218 在 2026-11-18 的二进制单换测试触发断连，已回滚并重建 19 个完整运行时 Pod，六个 CKM Ready；这不是新版正式发布验收。详情见 [KNOWN-ISSUES.md](KNOWN-ISSUES.md) 和 [恢复实现说明](../../sysbox-fs/recovery/README.md)。
