# 1016310：sysbox-fs FUSE 会话恢复（当前方案）

> 2026-11-19 更新。TAPD 1016310 原正文为废弃设计稿；以本文和实际发布验收记录为准。

## 行为与边界

- 同一宿主启动周期内，正常停止的 sysbox-fs 排空请求并保存登记状态；systemd FD store 保留 seccomp listener。新进程恢复登记视图和挂载。CO-RE eBPF 识别旧 FUSE 设备上的 `EIO`/`ENOTCONN`，按 namespace/设备去重后触发修复。
- 保留每分钟一次的静默故障兜底：只检查登记容器及其 cgroup 子树中的 mount namespace。开销随登记容器和相关进程数变化，不能宣称“零轮询”或“容器数量无关”。
- 新 `open` 在修复后可读；业务已持有的旧 FD 仍指向断开的会话，须由业务关闭并重新打开。不会以宿主 `/proc` 透传作为降级，因为这会破坏隔离。
- 脏 checkpoint、PID 复用、未知旧挂载、登记不完整、目标父挂载为 shared、子挂载传播不受支持等情况安全拒绝。目标挂载自身为 shared 不一概拒绝。
- 这是同机正常停止后的恢复，不承诺进程崩溃、宿主重启或首次从旧 unit 直接保留旧 Pod 的恢复。

## 安装与验收

首次从旧版升级时，必须完整安装匹配的 Chart/systemd unit，确认 `--session-recovery`、`FileDescriptorStoreMax` 与 `FileDescriptorStorePreserve=yes` 生效，并迁移旧 Pod。旧 unit 创建的会话没有可接管的 checkpoint/listener；仅换新二进制重启会使存量虚拟 `/proc` 断连。后续重启验收须确认 Pod UID、容器身份不变，检查新 `open`、旧 FD 行为、分钟兜底、服务日志和恢复指标。

源码有本机恢复指标 socket；218 已通过 VMAlert、Alertmanager 和企业微信机器人转发器接通通知链路；Webhook 保存在 Kubernetes Secret，受控测试告警投递成功且用户确认收到。Chart 源码已推送 w7panel-metrics/dev-v1 的 a0d5abb，本次仅部署新增告警资源，完整 Chart Release 尚未发布。218 在 2026-11-18 的二进制单换测试触发断连；随后完整安装 rc2 源码候选，先人工迁移陈旧 checkpoint 和旧挂载，由 installer 重建旧会话 Pod。新会话连续两次重启恢复通过。`v0.7.1-42` 正式版完成 L0/L2 与一次 FS 重启验收。后续源码修复首次并发故障重复上报，隔离 mount namespace 的真实 FUSE/eBPF 测试中，两个独立故障设备的批量修复、64 路首次并发与冷却到期去重在本地 race 模式和 218 目标内核上各连续三次通过。包含该修复的正式 `v0.7.1-44` 已在 218 安装：L0/L2 功能回归通过，FS 重启后所选 CKM 的 Pod UID/容器 ID 不变，虚拟 `/proc` 可读，恢复 successes=22、failures=0。旧 unit 首次升级的自动重建在隔离 VM 已通过，但这必然更换旧 Pod，不能视为原 Pod 无重启；8 副本持续补建时，v47 installer 曾因残留 FUSE 挂载循环重试，缩容和隔离 VM 重启后才完成升级。v47 隔离 VM 的一次聚合 Sysbox 服务重启后，分钟巡检扫描 3 个候选并修复 3 个挂载，原双容器 Pod 无重启且虚拟 `/proc` 可读；同轮有 1 个 eBPF 故障事件，因此完全无事件的静默故障尚未独立证明。218 业务多容器高密度故障注入仍未验收。详情见 [KNOWN-ISSUES.md](KNOWN-ISSUES.md) 和 [恢复实现说明](../../sysbox-fs/recovery/README.md)。

隔离 VM 另发现：对已恢复的新 FUSE 连接执行 `abort` 后，测试 Pod 注销时宿主挂载卸载返回 `Invalid argument`；下一次服务重启因脏 checkpoint 被安全拒绝。源码候选增加断连挂载的卸载清理；本机 race 测试及隔离 VM 的新建连接故障、Pod 注销、后续服务重启通过。2026-11-19 又用当前源码构建的 FS 在隔离 VM 复测已恢复连接再次断连：双容器 Pod 的 3 个挂载恢复成功后，仅中止容器 `a` 的新 FUSE 连接；Pod 注销完成，目标挂载目录消失，下一次 Sysbox 聚合服务重启正常，无卸载或脏 checkpoint 错误。本机相关包 race 测试通过。测试后恢复原 v47 FS，Deployment 和 FUSE 挂载均为 0，四项服务 active、K3s Ready；正式工件仍未验收。该新连接不属于旧 checkpoint 设备，不能拿它判定分钟兜底失效。另在隔离 VM 的 v47 FS 上，正常重启后的首次巡检修复 3 个挂载，但仍有 1 个 eBPF 事件；再次重启后，仅临时清空该 FS 进程的 eBPF `allowed` map，分钟巡检在 `fault_events=0` 时独立修复 3 个挂载、失败数为 0。原双容器 Pod 的 UID、两个容器 ID 和重启计数不变，两个容器的 `/proc/meminfo`、`/proc/stat` 可读。测试后 Deployment 缩至 0，重启 Sysbox 恢复正常 eBPF map，FUSE 挂载为 0，服务 active、K3s 节点 Ready。本项是隔离 VM 的受控静默故障验证，不等同于 218 业务高密度或正式工件验收。

同日以隔离 VM 的原 v47 FS 对 8 Pod/16 容器执行聚合 `sysbox.service` 重启：重启前后 Pod UID、容器 ID 和 restartCount 全部不变，16/16 容器新打开 `/proc/meminfo` 成功；分钟巡检统计 24 个候选，attempts=31、successes=24、failures=0，期间 old_fd_events=7、fault_events=9、dropped=0，FD store 为 24→24。测试后 Deployment/Pod/FUSE 挂载均为 0，FD store=0，四项服务 active、VM 节点 Ready，重启后服务无 error 日志。该结果验证受控 VM 的高密度正常重启恢复；未测试正式工件、业务节点高密度故障注入、旧 FD 的长期持有行为或旧版首次升级自动迁移。
