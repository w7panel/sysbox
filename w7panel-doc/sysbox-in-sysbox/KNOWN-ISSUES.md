# Sysbox-in-Sysbox 已知问题与能力边界

本文记录当前限制、待验证事项以及已经定位的问题根因。执行步骤以
[README.md](./README.md) 为准；旧 CKM、旧镜像和 L3 实验过程见
[HISTORY.md](./HISTORY.md)。

## 2026-11-20：按文件跳过无用 PID namespace 查询的 CPU 对照

- `/proc/loadavg` 的进程计数和 lastPID 仍按每个请求者的 cgroup/PID namespace 解析；`meminfo`、`stat`、`uptime`、`swaps`、`cpuinfo` 等输出只使用请求者实时 cgroup，快照键不再为这些文件读取无用的 PID namespace。缺失请求者 cgroup 时仍拒绝用外层 init/宿主视图代替。相关 domain、handler、fuse、cmd race 测试和 cgroup/PIDns 键隔离用例通过。
- 218/server1 隔离 VM 仅换 FS 候选 SHA256 `88852d659c1946f57f0890bfb9e8013421d32a6342c0ed85f35e2349e164edfc`，每轮 8 Pod/16 容器共 16,000 次完整缓冲 `/proc/meminfo` 读取全部成功。同源原版→候选→原版的 FS CPU 秒依次为 6.54/6.83→5.91/5.68→6.78/6.27。每文件 4,000 次的六文件原版/候选 CPU 秒：swaps 1.78/1.92，meminfo 1.74/1.50，stat 1.80/1.37，loadavg 1.68/1.59，cpuinfo 1.71/1.67，uptime 1.66/1.47；六项合计 10.37/9.52，但测量时宿主负载波动，且 swaps 变差，不能据此宣称整体 CPU 降耗达标。候选 8 Pod/16 容器均 Ready、重启 0，FS 当日无 error 日志；收尾恢复原 FS SHA256 `33479bb0a11ecb0a9471ef38273bb228dabaad7f43ab44d07cc3db4b59ba9d65`，测试 Pod/FUSE 挂载/FD store 均为 0，节点 Ready。
- 最终源码仅把 `loadavg` 分支改为避免额外构造无用的 cgroup-only 键；最终 FS SHA256 `8da2a802092115758beb89c8eb7c2b20d7bc1512213a93d0372816269fe3ffb2` 在相同 VM 新建 8 Pod/16 容器后，六文件各 4,000 次完整读取全部成功，FS CPU 秒依次为 swaps 1.54、meminfo 1.51、stat 1.50、loadavg 1.49、cpuinfo 1.57、uptime 1.58（合计 9.19）；另 16,000 次 meminfo 为 5.57 秒。两次候选的 swaps 差异显示单轮逐文件排序受环境影响；这仍是隔离 VM 微负载，不能代替业务混合负载或正式发布验收。测试后再度恢复原 FS，Pod/FUSE 挂载/FD store 为 0，四项服务 active、节点 Ready，FS 当日无 error 日志。

## 2026-11-20：binfmt_misc 父目录查找依赖移除

- 源码候选使固定 `/proc/sys/fs` 父目录和已有的 `binfmt_misc` 挂载点都由 FS handler 直接解析，恢复助手拼接新 FUSE 树时不再为父目录启动 namespace 代理；子挂载保留、ID 校验及原子替换逻辑不变。`handler/implementations`、`recovery`、`fuse` 的 race 测试通过。
- 218/server1 隔离 VM 安装仅替换 FS 的候选 SHA256 `007080fc6cb668516f2ce1f9532aa5c117033bde2689bc76deb73066d930b5cc`，8 个双容器测试 Pod 两次重启 FS 后各进行 64 路并发首读。两轮首读分别为 54/64、38/64 成功；异步恢复后 16/16 容器都能重新读取，Pod UID、容器 ID、restartCount 不变。两轮恢复均为 attempts=16、successes=16、failures=0，fault_events=16/17、dropped=0；容器内 `binfmt_misc` 子挂载仍在，FS 无 error 级日志。首读失败仍符合故障事件触发后异步恢复的现有时序。测试后 Deployment/Pod、FUSE 挂载和 FD store 均为 0，原 FS SHA256 `33479bb0a11ecb0a9471ef38273bb228dabaad7f43ab44d07cc3db4b59ba9d65` 已恢复，四项服务 active、K3s 节点 Ready。此为源码候选的隔离 VM 回归，不证明正式发布或业务节点零失败。
- 复核发现第一候选把真实权限为 `0555` 的 `/proc/sys/fs` 合成为 `0755`。修正版仅匹配固定父路径并保留 `0555`，相关包 race 测试通过。修正版 SHA256 `06f91e64a2f0aa787e279260287a4ea614ba81c82a6b2dabf370591105c3659b` 在相同 VM 新建 8 Pod/16 容器后再跑两轮 64 路故障首读，分别 42/64、49/64 成功；两轮异步恢复均 16/16 成功、failures=0、dropped=0，身份不变，容器内父目录权限实测 `0555`。最终原 FS 已恢复，测试 Pod/FUSE 挂载/FD store 为 0，四项服务 active、节点 Ready、FS 当日无 error 日志。

## 2026-11-20：隔离 VM 64 路 FUSE 故障读取与空闲 CPU

- 在 218/server1 隔离 VM 的 v47 FS、8 个双容器 Pod 上，聚合 Sysbox 服务重启后并发执行 64 路 `/proc/meminfo` 首读。第一轮 47 成功、17 个旧会话读取失败；eBPF 报告 28 个故障事件、丢弃 0，恢复尝试 17 次、成功 16 次、失败 1 次。唯一失败是保留 `/proc/sys/fs/binfmt_misc` 子挂载时新 FUSE 树暂时返回 `ENOENT`；随后同一容器重试成功，16 个容器均可重新打开虚拟 `/proc`。第二轮首读 29 成功、35 失败，故障事件 30、丢弃 0，16 次恢复全部成功、失败 0。两轮后 8 个 Pod UID、16 个容器 ID 与重启次数均未变化，FD store 为 24，节点 Ready。此结果证明并发故障最终恢复，但第一轮有一次可重试错误，不能称零失败稳定通过；也不是正式发布或 218 业务节点故障注入。
- 同一 v47 VM，测试 Deployment 为 0 时，FS 30 秒空闲消耗 0.17 CPU 秒；8 个双容器 Pod 各读取一次 `/proc/loadavg` 激活采样后，30 秒空闲消耗 0.53 CPU 秒。仅是 16 个视图的单轮观测，不能外推高密度整机 CPU 收益。测试后 Deployment/Pod、宿主 FUSE 挂载和 FD store 均为 0，四项服务 active、节点 Ready。

## 2026-11-20：高密度旧 unit 首次升级复测

- 218/server1 隔离 VM 从官方 v0.7.1-40 Chart/镜像实际装入无 `--session-recovery`/FD store 的旧 FS unit，启动 8 个 `sysbox-runc` 双容器 Pod；8/8 Ready，虚拟 `/proc/meminfo` 可读。随后 Helm 回升到含 `migrate_legacy_fs_pods` 的 v47 源码候选 Chart/镜像。installer 日志依次记录 cordon、删除全部 8 个旧 Pod、安装新服务、重启 K3s、uncordon 和完成；8 个新 Pod/16 个容器全部 Ready，旧 Pod UID 复用数为 0，16 个容器的 `/proc/meminfo`、`/proc/stat` 读取失败数为 0，新 FS unit 启用会话恢复并存储 24 个 FD。节点 Ready，四项 Sysbox 服务 active。测试 Deployment 缩至 0 后 Pod、宿主 FUSE 挂载、FD store 均为 0，相关服务近十分钟无 error 日志。此次证明可丢弃 VM 的高密度旧 unit 首次升级自动迁移；旧 Pod 按设计被删除重建，**不证明旧容器无重启**，也不是 218 业务节点或正式发布工件验收。

## 2026-11-19：PID namespace 按需读取未取得稳定 FS CPU 收益

- 2026-11-19 正确缓冲读取的分配 profile 定位到 `fuse.File.Read` 每次额外分配 8 KiB；16,000 次 `/proc/meminfo` 读取中该处分配约 217 MB，占测试窗口分配字节约 34%。Bazil 已预分配同尺寸响应缓冲，源码改为直接复用。相关包 race 测试通过；隔离 VM 8 Pod/16 容器、每轮 16,000 次成功读取的同源基线→候选→基线 FS CPU 秒数分别为 6.29/6.40→5.84/5.90→6.08/5.83，区间重叠，尚不能认定整进程 CPU 降耗。测试后恢复原 v47 FS SHA256 `33479bb0a11ecb0a9471ef38273bb228dabaad7f43ab44d07cc3db4b59ba9d65`；Deployment/Pod/FUSE 挂载/FD store 均为 0，Sysbox 服务 active、节点 Ready，近五分钟相关服务无 error 日志。
- 此前 PID namespace 按需读取实验使用 shell 内建 read 的逐字节压力方式；旧基线 56.76 秒、候选 55.16/56.60 秒不能代表常规缓冲读取的 CPU 收益。候选无稳定改进且已撤回；1016311 整体 CPU 降耗仍未验收。
- 隔离 VM 已恢复 v47 原 FS 和 Chart，测试 Deployment 缩容至零；FS/mgr/snapshotter 与聚合服务均 active，K3s 节点 Ready，近五分钟相关服务无 error 日志。此次仅为隔离 VM 源码实验，未改 218 宿主业务运行时。

## 2026-11-19：断连 FUSE 挂载注销失败阻断下一次恢复

- 仅在 218/server1 的隔离 QEMU VM 中，对上一轮已恢复的测试容器 FUSE 连接执行 `abort`；该连接不属于当前恢复器的旧 checkpoint 设备，分钟扫描两轮后恢复计数未变，不能以此判定原工单的旧会话兜底失败。
- 测试 Pod 缩容时，FS 对该容器宿主挂载执行 `fusermount3 -u` 返回 `Invalid argument`，日志报告挂载未能卸载。再次扩容一个双容器测试 Pod 后重启聚合 `sysbox.service`，FS 停止阶段又因同一旧挂载卸载失败退出，新进程检测到 `Clean=false` 的 checkpoint 后按安全策略拒绝启动。此轮无事件巡检验收因此未运行，不能算通过。
- 已将测试 Deployment 缩容至零，仅重启隔离 VM 清除断连挂载和运行期 checkpoint；四个 Sysbox 服务均 active，K3s 节点 Ready。宿主业务 Pod 未受影响。源码候选在 `fusermount3 -u` 失败时对已断连挂载尝试延迟卸载，若目标已无挂载则清理目录。本机受影响包 race 测试通过；隔离 VM 中中止一个新建双容器 Pod 的 FUSE 连接后，Pod 注销完成、对应宿主挂载目录消失，聚合服务随后重启成功。
- 2026-11-19 从当前 `sysbox-fs` 源码构建 FS（SHA256 `bf58b43e9d0bf6d0230fdd705dba62b5d41a4d0b1652e24e9ec8716c8a6f16b7`），仅在隔离 VM 安装。一个双容器 Pod 在聚合服务重启后恢复 3 个挂载（successes=3、failures=0）；两个容器的 `/proc/meminfo` 可读。对容器 `a` 的已恢复新连接 `174` 执行 `abort`，测试 Pod 缩容注销后目标目录消失、FUSE 挂载为 0；聚合服务再次重启成功，FS/mgr/snapshotter/K3s active，节点 Ready，无卸载、脏 checkpoint、panic/fatal/OOM/ENOMEM/error 日志。相关包 race 测试通过。VM 已恢复原 v47 FS SHA256 `33479bb0a11ecb0a9471ef38273bb228dabaad7f43ab44d07cc3db4b59ba9d65`，测试 Deployment 为 0、FUSE 挂载为 0。该修复尚未进行正式工件验收。

## 2026-11-19：隔离 VM 分钟兜底巡检端到端补测

- v47 源码候选、218/server1 的隔离 QEMU VM 中，仅保留一个双容器 `sysbox-runc` 测试 Pod，正常重启聚合 `sysbox.service`。重启后恢复指标为 attempts=0、successes=0、failures=0、fault_events=0、scan_count=0。下一轮分钟巡检在 08:35:24 扫描 3 个登记候选，记录 attempts=4、successes=3、failures=0、fault_events=1、scan_count=1；同一时间日志记录 3 个容器的 FUSE 挂载恢复。原测试 Pod UID `52147215-a233-4085-9c67-684af89b7251`、两个 container ID 均未变化，restartCount 均为 0，双容器重开 `/proc/meminfo`、`/proc/stat` 成功。同期 FS/mgr/snapshotter 无 error 日志；测试后 Deployment 缩容到零。
- 该轮同时出现一个 eBPF 故障事件，不能将其作为“完全没有读故障事件，仅靠分钟巡检恢复”的独立证明。测试时曾为写出 CPU profile 单独停止 FS，systemd 联动停掉 mgr/snapshotter；仅单独重启 FS 留下不完整的聚合服务状态，导致新 Pod 的 snapshotter socket 拒绝连接。已将该轮作废，并仅在隔离 VM 重启后确认完整 Sysbox 服务和节点恢复，再用聚合 `sysbox.service` 重启取得上述有效结果。
- VM 恢复原 v47 后再次以一个双容器测试 Pod 正常重启聚合服务：重启后 attempts/successes/fault_events/scan_count 全为 0；首次分钟扫描记录 attempts=4、successes=3、failures=0、fault_events=1、scan_count=1。Pod UID `78d1ac91-fabf-4c49-8590-520a8e165453`、两容器 ID 和 restartCount=0 均未变，两容器重开 `/proc/meminfo` 均读到 65536 kB。测试 Pod 已缩容为零，四个服务 active，本轮服务 error 日志为空。该轮仍有一个故障事件，不能单独证明零事件兜底。
- 2026-11-19 再在 218/server1 隔离 QEMU VM 的 v47 FS 上验证静默兜底。双容器 Pod UID 为 `b993ae3a-2ee5-4b26-941d-5c30680fea28`；正常重启首次巡检修复 3 个挂载，同时有 1 个事件。第二次重启后临时清空该 FS 进程的 eBPF `allowed` map，巡检在 `fault_events=0` 下记录 attempts=3、successes=3、failures=0、scan_candidates=3。Pod UID、两个 container ID 和 restartCount=0 均不变，两个容器可重开 `/proc/meminfo`、`/proc/stat`，内存均为 65536 kB。测试后 Deployment 缩至 0 并重启 Sysbox 恢复事件 map；挂载为 0，四项服务 active、K3s 节点 Ready，最近五分钟服务日志无 panic/fatal/OOM/ENOMEM/error。这仅证明受控 VM 的零事件巡检路径，未覆盖 218 业务高密度或正式工件。
- 同一隔离 VM 的原 v47 FS 又完成 8 Pod/16 容器聚合服务重启：全部 Pod UID、容器 ID 和 restartCount 保持不变，16/16 容器重开 `/proc/meminfo` 成功；分钟巡检 24 个候选，attempts=31、successes=24、failures=0，old_fd_events=7、fault_events=9、dropped=0，FD store 24→24。测试后 Deployment/Pod/FUSE 挂载均为 0，FD store=0，四项服务 active，VM K3s Ready，重启后无 error 日志。它验证高密度正常重启恢复，不覆盖正式工件、业务节点故障注入或旧版首次升级自动迁移。
- 旧 FD 对照：同一 VM 在重启前持有 `/proc/meminfo` FD；3/3 挂载恢复后旧 FD 返回 `Transport endpoint is not connected`，新 open 返回 `MemTotal: 65536 kB`。以 `sh -c 'sleep 3600'` 作测试容器 PID 1 时，探针 `kubectl exec` 退出约一秒后该容器两次提前以 exit 0 重建；改为直接 `sleep 3600` 作 PID 1 后，探针结束 15 秒时 Pod UID、两容器 ID/restartCount 均不变。前两轮重建可能与测试启动壳相关，具体信号机制未证实，不能作为 Sysbox 恢复导致业务容器重启的证据。最终一轮 attempts=5、successes=3、failures=0、old_fd_events=2、dropped=0，服务无 error 日志。模板已恢复、Deployment 为 0、Pod/FUSE 挂载/FD store 为 0，服务 active、节点 Ready，PVC 保留。

## 2026-11-19：旧逐字节 CPU 压力测法不能作为降耗验收

- 旧脚本在 8 个双容器 Pod 中用 shell 内建 read 反复读取首行。strace 证实每个字符对应一次 read(…, 1)，文件行长和 FUSE 请求数严重影响结果。原 v40 26.57/26.32 秒、v47 56.28/59.18 秒的 16,000 次对照仅能说明该逐字节压力方式下 CPU 增加，不能证明常规缓冲读取退化 2.2 倍；旧 CPU 降耗验收结论作废。
- 旧逐文件数值也受逐字节读取影响，尤其 swaps 的 44.74/44.36 秒是长表头放大的假热点。修正为单进程 open/read(8 KiB)/close 完整文件、每文件 4,000 次成功读取后，原 v47 的 swaps/meminfo/stat/loadavg/cpuinfo/uptime FS CPU 秒分别为 1.75/1.67/1.65/1.79/1.73/1.74；当前源码同源构建为 1.63/1.56/1.69/1.57/1.60/1.70。六项差异很小，不能认定 swaps 特别耗 CPU。v40 FS 单组件正确口径基线已补测：swaps/meminfo/stat/loadavg/cpuinfo/uptime 分别为 2.52/0.86/0.76/0.79/0.80/0.89 秒；v47 对应为 1.75/1.67/1.65/1.79/1.73/1.74 秒。除 swaps 外五项约增加 1.9–2.3 倍。v40 二进制来自官方 deb，SHA256SUMS 校验通过，临时匹配 v40 FS 启动参数；其余 mgr/runc 仍为 v47，因此这只是 FS 单组件 A/B，完整发布组合和业务负载 CPU 降耗仍未验收。
- 此前再次从 v40 切到 v47 时，8 副本 Deployment 不断补建旧运行时 Pod，installer 因残留 FUSE 挂载多次拒绝替换旧 FS。仅在这台可丢弃 VM 将测试 Deployment 缩到零、重启 VM 清除旧 mount namespace 后，v47 unit 才装入 `--session-recovery`/FD store；重新扩到 8 副本后全部 Ready，取样后再次缩容到零。FS/mgr active，节点 Ready，最近十分钟 FS error 日志为空。该轮不能宣称无人值守迁移通过；后续使用含自动迁移逻辑的 v47 候选重新完成高密度复测，见 2026-11-20 记录。

- 正确口径下，同源原实现六文件 CPU 总和两组为 9.75、9.42 秒，固定缓冲 cgroup 读取实验候选为 9.08 秒（每文件 4,000 次成功读取）。差异约 3–7% 且接近环境波动，远不足以抵消五文件回退；实验代码保持撤回。测试结束恢复原 v47，测试 Deployment 为 0，四项服务 active、节点 Ready。
- 2026-11-19 在隔离 VM 对正确缓冲读取的 `/proc/meminfo` 补做函数级 `perf`：8 Pod/16 容器共 16,000 次完整成功读取；带 `perf` 时原剥离符号的 v47 FS CPU 为 12.14 秒，当前源码未剥离构建为 12.06 秒，无法据此认定 CPU 已降耗。当前源码的约 12K 个 cycles 样本中，约 23% 的累计调用链经过逐请求资源视图读取，约 54% 经过 FUSE Serve，`readMemInfo` 约 7%；这些累计比例重叠，不能相加。旧 `swaps` perf 文件早于缓冲读取脚本，不能用于当前热点判断。测试后已等 8 个 Pod 全部删除，VM 恢复原 v47 SHA256 `33479bb0a11ecb0a9471ef38273bb228dabaad7f43ab44d07cc3db4b59ba9d65`，FUSE 挂载为 0，四项服务 active。

## 2026-11-19：v46 正式升级被旧 K3k Pod finalizer 阻塞

- GitHub Actions run `44468919805` 成功；v46 正式 Chart、lite、镜像元数据通过 `SHA256SUMS`，镜像从 `ghcr.nju.edu.cn` 拉取并按相同 manifest digest `sha256:46758bbfe6a0e3faf06cfa97933ea590642eda452cb277f24d4caf7e858e5bfd` 推至 CCR。218 Helm revision 46 安装后，FS 版本为 0.7.1-46，FS/mgr/snapshotter active，K3s 节点 Ready，六个 CKM 重建后均 Ready；containerd 2.3.4 的模板使用 `bin_dirs`。
- installer 在 `kubectl delete` 的默认等待行为下卡在旧 `ckm-snapshot-1109` Pod 的 `etcdpod.k3k.io/finalizer`；旧 Pod 为 Failed/Terminating，新 CKM 已 Ready，但 installer 无法写入完成标记，节点标签仍为 `installing`。因此 revision 46 不能算完整发布验收。直接重启原 installer 导致重新安装和重复删 Pod，已停止这种处理。
- 仅替换宿主脚本的同版本候选镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox-deploy-k3s:finalizer-nowait-v46-hotfix-20261119`（digest `sha256:70157371c07a3ba67fabb6cf0fd990aa42bde0f3e5e78a79395a54510c44605a`）在 218 Helm revision 47 完成安装，节点标签恢复 `running`，FS/mgr/snapshotter active、FD store 为 56，六个 CKM 最终均 Ready。旧 Failed Pod 的 K3k finalizer 仍由控制器负责，未手动移除。源码将删除命令改为 `--wait=false` 并撤回不再需要的 Pod watch 权限；Bats 16 项及 Helm lint 通过。该修复尚待正式工件验证。
- 完整源码候选 v47 Chart 和 CCR 镜像 `finalizer-v47-test-20261119`（digest `sha256:2167688232724e79a0211ae93d10bd9d2f13570ab4f27f1b99c1ec5e59211672`）通过 `SHA256SUMS`，在 218 Helm revision 48 完成升级；installer 删除旧 Sysbox Pod 后写入 `running`，FS/mgr/snapshotter active，六个 CKM Ready。L0 的 00/02、lite/full `local-path` 及 full Longhorn `disk-default` 的 03 均通过；指定 CKM `loadopt-218-1016` 的内层 04/05 通过，覆盖 PVC 初始化、重建后持久化和 CRI stats。临时测试 Deployment 已缩容零并保留 PVC；相关服务近 15 分钟日志无 panic、fatal、OOM、ENOMEM 或 error。按用户要求此轮以源码候选验证，不等待正式 Release；不能称正式工件验收。

## 2026-11-19：隔离 VM 首次升级与 K3s CNI 模板

- 正式 `v0.7.1-45` GitHub Actions 已成功，但其 CNI 模板补丁只使用 `bin_dir`；218/server1 运行 K3s v1.37.1/containerd 2.3.4，而该字段从 containerd 2.3 起移除。因此未将 v45 安装到 218，改为按节点 containerd 版本选择 2.0 的 `bin_dir` 或更新版本的 `bin_dirs`，等待下一正式标签验证。
- 在 218/server1 的独立 QEMU VM（K3s v1.32.4、containerd 2.0.4）中，官方 v0.7.1-40 Chart/镜像的摘要校验通过。旧 unit 无会话恢复或 FD store；可丢弃的双容器 `sysbox-runc` Pod 使用虚拟 `/proc`。用完整 v45 源码候选升级时，installer 自动迁移旧 Pod，安装新 unit，重启 K3s，节点恢复 Ready，新 Pod 双容器均正常。这验证旧 unit 首次升级路径，但仅限隔离 VM 和源码候选。
- v40 宿主 containerd 模板遗漏 CNI 路径；无临时修正时，重启后的节点会 NotReady。先在 VM 加入 `bin_dir`/`conf_dir` 临时 drop-in 建立旧版基线；随后移除该 drop-in，使用只更新宿主安装脚本的测试镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox-deploy-k3s:cni-template-v45-test2-20261119`（digest `sha256:96a4e76f1320205cd0eb4ffb46c038ce4ceb8c33a0436d27020f9f308a291b0c`）重新执行更新。installer 为已有模板写入 K3s CNI 路径并实际重启 K3s；CRI 加载 `binDir=/var/lib/rancher/k3s/data/cni`、`confDir=/var/lib/rancher/k3s/agent/etc/cni/net.d`，节点 Ready，新建双容器 Pod 的虚拟 `/proc/meminfo` 均为 65536 kB。此轮尚非完整新工件发布验收。
- 完整源码候选镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox-deploy-k3s:legacy-upgrade-cni-rbac-v45-test-20261119`（digest `sha256:4c3d01176713818b71f395e54ecf43aab67dbbd8264a408ebae7a3665ac88a9d`）及 Chart、deb、lite 等产物通过 `SHA256SUMS`。VM Helm revision 5 安装该 Chart 后，installer ServiceAccount 的 `pods/watch` 权限为 yes；版本更新实际重启 K3s、重建双容器 Pod，最终升级完成，日志无 `forbidden`，宿主服务日志无相关异常，节点 Ready、CRI CNI 路径正确，两个容器的虚拟 `/proc/meminfo` 均为 65536 kB。临时 Deployment 已缩容到零，VM 保留以供后续隔离 A/B。此测试仍是源码候选，正式 GitHub 工件和 218 发布验收未完成。
- 版本适配补丁的 `bin_dir`/`bin_dirs` 两个分支通过 Bats；只替换宿主脚本的候选镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox-deploy-k3s:cni-version-v46-test-20261119`（digest `sha256:0d96c356b533c142a7bb4fce5a45c421547f1d1d6b3e677aa8dfd1680e3cc415`）在 VM 删除旧 CNI 节后重新生成 `bin_dir`，实际重启 K3s，CRI 加载正确目录、节点 Ready；新建双容器 Pod 两边的虚拟 `/proc/meminfo` 均为 65536 kB。测试 Pod 已再次缩容到零。containerd 2.3 分支尚待 218 正式工件安装验证。

## 2026-11-19：双容器故障并发下的 binfmt_misc 子挂载恢复

- 218/server1 的正式 v0.7.1-44 FS 重启后，8 个临时双容器 Pod 的 64 路首次 `/proc/meminfo` 读取均先返回 `Transport endpoint is not connected`。这是 eBPF 在失败读取后异步触发修复的时序；但五个容器随后连续三轮恢复失败，日志为 `open staged child target /proc/sys/fs/binfmt_misc: no such file or directory`，失败计数达到 15。旧 FUSE 下的 binfmt_misc 子挂载必须保留，不能跳过；这些临时容器最终重启，故原轮验收失败。临时 Deployment 已删除。
- 源码候选在 `ProcSysFs.Lookup` 中为 sysbox-mgr 固定挂载的 `binfmt_misc` 提供目录，保持恢复器原有的子挂载 ID、传播校验和原子替换。本地 handler/recovery 测试及私有 mount namespace 的特权 FUSE 测试通过。以正式 v44 镜像为底只替换 FS 二进制构建测试镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox-deploy-k3s:fs-binfmt-recovery-v44b-20261119`（digest `sha256:24bfc7575b48859b213d69f52fe530e7603fab5febd1b6067c32bf9c2996cea6`）；在 218 备份原 FS SHA256 `c33c478136ead672e8a035f72e43c7a9b1d512ec8a3addf5f1a41043c15897fc` 至 `/var/lib/sysbox-test/binfmt-recovery-20261119/sysbox-fs.before`，安装候选 SHA256 `1332cdc9e27e544c85227747f023be53a27a6c1f1e10c40cd6c3a7cb06f23656`。
- 候选下重建 8 个双容器 Pod，再重启 FS 并并发读取：16 个原容器的 UID、容器 ID 和重启次数均不变，后续各自的 `/proc/meminfo` 均读到 65536 kB，binfmt_misc 子挂载仍在。随后的分钟巡检指标为 attempts=449、successes=361、failures=0、unsupported=26；六个 CKM Ready，指定 `loadopt-218-1016` 的 Pod UID 和 k3s-server 容器 ID 未变，FS/mgr/snapshotter 均 active，未见 panic/fatal/OOM/ENOMEM 或恢复失败日志。8 个临时 Pod 已删除。此次是源码候选的 FS-only 验证，尚非正式发布工件验收。

## 2026-11-19：v0.7.1-44 正式工件在 218 的功能验收

- GitHub Actions run `44446923969` 成功；下载的 Chart、lite 二进制和镜像元数据通过 `SHA256SUMS`。`ghcr.nju.edu.cn` 返回的正式镜像 manifest digest 为 `sha256:4e0b0521f7c474e39965e7cf0592c69a6151d642fb0b3a7f4a23cb4674c81bc0`；拉取后推送至 `ccr.ccs.tencentyun.com/afan-public/sysbox-deploy-k3s:v0.7.1-44`，CCR 回读 digest 相同。DaoCloud 镜像入口因白名单拒绝该仓库，直连 GHCR 拉取反复重试层下载。
- 218 外层 Helm 升至 revision 45，installer 完成宿主 `0.7.1-44` 安装并重建旧会话 Pod。正式 Chart 的 `00`、`02`、三组 `03`（lite/local-path、full/local-path、full/Longhorn `disk-default`）及指定 `loadopt-218-1016` 的 `04`、`05` 均通过：无显式 Pod 挂载的 `/dev/fuse`、无注解空 PVC 初始化、Pod 重建后的 PVC 和特殊目录持久化、内层 lite handler、CRI writable-layer 与 kubelet `/stats/summary` 可用。重复 `04` 后 L1 K3s 进程 PID/starttime 仍为 `188/821`、`332/1113`，没有因相同配置重启。
- 正式 FS 以 `--session-recovery` 运行，重启前 FD store 为 62。单次重启后所选 CKM Pod UID `f7b84666-214f-44b4-8a4f-f78cf8aaccf4` 和 k3s-server 容器 ID `566442903b6d5adcb91edc978a2ea0a48af865cebf06e2f5f01f9c0ac5bc784a` 未变；虚拟 `/proc/meminfo`、内层 `/readyz` 可读，六个 CKM Ready。FD store 为 63，恢复指标 attempts=22、successes=22、failures=0、unsupported=0、dropped=0；FS/mgr/snapshotter 近 15 分钟无 error 级日志。三组 L0 和一组 L2 测试 Deployment 均缩容到零，PVC 保留。
- 此次验证了包含并发 eBPF 冷却键修复的正式版本安装及正常重启恢复；64 路故障并发和双设备批量修复的直接证据仍是隔离 mount namespace 的内核测试，不是 218 业务多容器故障注入。固定负载旧→新→旧 FS CPU 秒 A/B、旧 unit 首次升级的无人值守迁移、正式 Alertmanager 通知仍未完成；218 未提供告警接收地址。

## 2026-11-19：正式版分钟巡检持续运行与并发故障本机回归

- 后续源码修复了 eBPF 冷却键首次故障和到期故障的并发重复上报竞态；本地私有 mount namespace 中两个独立断连 FUSE 设备的批量修复、64 路首次并发故障去重和到期后去重在真实内核下通过三轮 race 测试。同一测试二进制在 218/server1 的 privileged installer 容器中，临时创建的私有 mount namespace 挂载 tracefs 后又通过三轮；首次直接运行因容器缺 tracefs 而未加载 tracepoint。测试后临时二进制已删除，installer 未重启，FS/mgr/snapshotter active，六个 CKM Ready。此改动尚未构建正式发布工件或替换 218 正在运行的 FS，不能算正式版高密度故障验收。
- 218/server1 的正式 `v0.7.1-42` FS 持续 active，FD store 为 56；分钟兜底指标已有 761 轮、当前候选 56，恢复 attempts=412、successes=340、failures=0、dropped=0。六个 CKM Ready，FS/mgr/snapshotter 近一小时无 error 级日志。这证明巡检在正式版运行，不等于静默故障已现场触发并修复。
- 在隔离 mount namespace 的真实 FUSE/eBPF 特权测试中，将旧挂载断开后的顺序重复读取改为 64 个并发读取者，验证它们均失败且内核冷却期间不重复上报。该测试在 race 模式下连续三次通过；相关恢复器和事件队列 race 测试通过。这覆盖单一挂载的并发故障，尚不是 218 多容器高密度真实故障注入。
- 现有 218 业务会话不适合直接回滚 v41 做旧→新→旧 CPU A/B：v41 会拒绝 v42 可修复的 shared/slave 目标挂载，回滚可能使这些视图断连。严格 A/B 仍需隔离、可丢弃的会话和相同负载，不能用现有单轮数据宣称 FS CPU 下降。

## 2026-11-18：v0.7.1-42 正式工件在 218 的功能验收

- GitHub Actions 正式发布成功；下载的 Chart、lite 二进制和镜像元数据均通过 `SHA256SUMS`。正式镜像原 manifest digest `sha256:6c5eb66c84b550a3effc7178791510bf9aaba048cacbbd97c561fc1753c7bc89` 已原样推送至 `ccr.ccs.tencentyun.com/afan-public/sysbox-deploy-k3s:v0.7.1-42`。218 外层 Helm 升至 revision 44，installer 报告安装完成并重建旧会话 Pod；宿主 FS 版本为 `0.7.1-42`，FS/mgr/snapshotter 均 active，六个 CKM Ready。
- 正式 Chart 的 `00`、`02`、三组 `03`（lite/local-path、full/local-path、full/Longhorn `disk-default`）及指定 CKM `loadopt-218-1016` 的 `04`、`05` 均通过。覆盖 `/dev/fuse`、无注解 PVC 初始化、Pod 重建后的 PVC 与特殊目录持久化、内层 lite handler、CRI writable-layer stats 和 kubelet `/stats/summary`。重复 `04` 后 L1 `k3s-server` 容器 ID 未变；该项没有记录进程 PID/starttime，不能据此单独证明 K3s 进程未重启。
- 升级后宿主三个 Sysbox 服务无 error 级日志，也未检出 panic、fatal、OOM、ENOMEM。升级期间 admission 有短暂的 API lease 续约错误，发生在外层 K3s 重启窗口；最终 admission Ready。三个 L0 和一个 L2 测试 Deployment 已缩容到零，PVC 保留。
- 正式版 FS unit 以 `--session-recovery` 启动，`FileDescriptorStoreMax=65536`、`FileDescriptorStorePreserve=yes`，重启前存储 56 个 FD。单次宿主 FS 重启后 FD store 仍为 56，所选 CKM Pod UID `1c18f6cb-dea9-4b8a-b8ae-21754e1a4655` 和 k3s-server 容器 ID `e0b2fc269731c02fa024a6179c6531f497ebd4eeebd95e96bc05ea15cb809e54` 未变；虚拟 `/proc/meminfo`、内层 `/readyz` 可读，六个 CKM Ready。该轮恢复指标 successes=22、failures=0、dropped=0，FS 近五分钟无 error 级日志。
- 本次证明正式版的上述功能路径，不证明所有工单验收项完成。仍缺固定 CKM/宿主负载的旧→新→旧 FS CPU 秒 A/B、真实内核高并发故障注入，以及正式 Alertmanager 通知链路；218 尚无告警接收地址。旧会话首次升级仍需人工迁移，安全预检只阻止断连式替换。

## 2026-11-18：v0.7.1-42-rc2 源码候选在 218 的完整安装

- 后续 installer 源码增加升级前预检：若任一进程的 mount namespace 仍有 `fuse sysboxfs` 挂载，只有运行中 FS 启用 `--session-recovery` 且 systemd 保留非空 FD store 才允许替换；旧 FS 会在停服务前拒绝升级。当前 218 rc2 的 unit 有 `FileDescriptorStoreMax=65536`、`FileDescriptorStorePreserve=yes`，56 个挂载和 56 个存储 FD；在 installer Pod 内只读运行新预检已通过。本地 Bats 与完整 release 构建、产物校验通过。候选镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox:upgrade-precheck-20261118` 已推送（digest `sha256:1c7f172bd4a31bc4c68d659a7e173717b8b57790eb4e33e8f5e12c3a2da6a669`）；该保护尚未在 218 触发安装验证，也不等于无人值守首次迁移已实现。

- CCR 镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox:v0.7.1-42-rc2` 已推送（digest `sha256:3045528c91f4feaa937d1bfa26a7b64121235b68da8e210c0dcc18900a6bac53`）；本地完整构建的 `dist/SHA256SUMS` 全部通过。它没有 Git tag/GitHub Release，仍是源码候选。
- 218/server1 首次升级因 `/run/sysbox/fs-restart.json` 中遗留 56 个容器的旧 checkpoint 而被新版 FS 安全拒绝。先回滚 v40 恢复服务，再备份该文件至 `/var/lib/sysbox-test/rc2-20261118/`，停旧服务并定向卸载 58 个旧 `sysboxfs` 挂载，随后安装 rc2；installer 重建旧会话 Pod。当前 Helm revision 43，FS/mgr/snapshotter active，19/19 个现存 full-runtime Pod 和六个 CKM Ready。此过程需要人工迁移，不能称无人值守首次升级通过。
- 新会话下连续重启 FS 两次，所选 `loadopt-218-1016` 的 Pod UID `52904ddc-5b9a-4ca4-ade4-34ed84535e51`、主容器 ID `0a84194a990aba63dc874758905a728d874db921e84ef659d5cdb790f06f2bc5` 不变；虚拟 `/proc` 与内层 `/readyz` 可用，FD store 为 56。分钟巡检近期 `failures=0`、`unsupported=26`，稳态约 157–232 ms/轮；共享父挂载仍安全拒绝。重启后无 fatal/error/panic/OOM 日志。
- `00`、`02`、三组 `03`（full/local-path、lite/local-path、full/Longhorn `disk-default`）、所选 CKM 的 `04`、`05` 均通过；重复 `04` 后内层 K3s PID/starttime 不变。测试 Deployment 已缩容到 0，PVC 保留；内层 `default` 的 CCR 拉取 Secret 仍供 Chart 使用。
- 单进程各重新打开四个 `/proc` 文件首行 10,000 次，共 40,000 次，rc2 一轮用 33.53 FS CPU 秒、14.10 秒墙钟；此前旧版一轮约 36.58 CPU 秒。样本和负载条件不足，不能宣称稳定 CPU 收益。尚缺同负载旧→新→旧 A/B、正式 Release 资产验收，以及没有接收地址的 Alertmanager 通知链路。

## 2026-11-18：218 从 v0.7.1-40 直接替换 FS 二进制导致旧 FUSE 会话断连（已恢复）

- 在 218/server1 的 Helm revision 40、当时七个 Ready CKM 上，以单进程固定 40,000 次 `/proc` 打开读取测得旧 FS 79.74 CPU 秒、耗时约 25.4 秒；测试窗口宿主 1 分钟负载由 11.58 升至 17.69。其中一个新 CKM 随后消失，实例数量并非始终稳定。这只是旧版基线，未取得新版可比数据。
- `BUILD_PROFILE=test TEST_COMPONENTS=fs TEST_TARGETS=deploy` 生成静态 FS SHA256 `1ebf3d2a1f4195ef41ac7640a18ef5be9bc9ad25889af720cdeaa43d42261744`；测试镜像 `ccr.ccs.tencentyun.com/afan-public/sysbox:fs-key-silent-218-20261118` 已推送（digest `sha256:6cc2bfa759c020302e22ecbc75584abdd43459ac41d7fb1a52c50baf1290329e`）。ZPK 仓库推送被 `unauthorized` 拒绝。只替换宿主 FS 二进制并重启后，现存完整 Sysbox 容器的虚拟 `/proc` 持续返回 `Socket not connected` / `Transport endpoint is not connected`，FS 日志反复报告 unknown container；内层 K3s `/readyz` 仍为 `ok`。此候选未进入 CPU A/B。
- 根因是当前 218 的 v0.7.1-40 `sysbox-fs.service` 仍以 `/usr/bin/sysbox-fs` 启动，且没有 `FileDescriptorStoreMax` / `FileDescriptorStorePreserve`；源码新 unit 才启用 `--session-recovery` 和 FD store。首次跨此边界仅替换二进制无法恢复旧会话，必须先按完整安装流程更新 unit 并迁移旧 Pod，后续重启才可验证同 UID 恢复。
- 已将宿主 FS 恢复到原 SHA256 `dbb9a209ad7d2966ae42fb5d4b79fd6bd4fb68461871b6a411be9bddda8ebbb0`（备份：`/var/lib/sysbox-test/fs-key-silent-20261118/sysbox-fs.before`），并逐个重建六个受影响 CKM Server 和十三个其他完整运行时 Deployment。最终 19 个现存 `sysbox-runc` Pod 的 `/proc/meminfo` 均可读，六个 CKM Ready；所选 CKM 内层 K3s `/readyz=ok`，FS/mgr/snapshotter active。此次验证失败，不能称源码候选、正式发布或 CPU 收益验收通过。

## 2026-11-18：218 默认 AppArmor profile 拒绝 nginx 热重载（已修复）

`default/nginx-osucsvlk-dd6b7455-sqf4j` 使用默认 `runc`，执行 `nginx -s reload` 时 `kill(1, 1)` 返回 `EPERM`。宿主审计确认 `cri-containerd.apparmor.d` 拒绝向 `cri-containerd.apparmor.d//&unconfined` 发送 HUP；同版本 K3s containerd `v2.3.4-k3s1` 默认模板只允许未堆叠的同名 peer。此故障不经过 Sysbox RuntimeClass。

218/server1 已基于该版本默认模板在 `/etc/apparmor.d/cri-containerd.apparmor.d` 增加 `signal (send,receive) peer=cri-containerd.apparmor.d//&unconfined,`，保留其余规则；原模板及修改稿备份于 `/var/lib/sysbox-test/apparmor-nginx-20261118/`。宿主 `apparmor_parser -QK` 校验及 `-r` 加载通过；原 Pod reload 成功、仍 Ready、重启 0，内核未再记录该次 nginx signal 拒绝。此规则适用于该宿主所有使用 containerd 默认 profile 的容器，其他节点未修改。

## 2026-11-17：v0.7.1-41 正式 Chart 在 218 镜像拉取超时

- GitHub Actions 发布的 deb、Chart、lite 和元数据通过 `SHA256SUMS` 校验；镜像元数据指定 `ghcr.io/w7panel/sysbox-deploy-k3s:v0.7.1-41`。在 218/server1 以该正式 Chart 和镜像升级宿主 Helm release，installer 与 admission Pod 持续停在 `ContainerCreating`，事件仅显示 `Pulling`；10 分钟后 Helm revision 32 因超时失败，未进入新版组件安装或功能验收。本机拉取同一镜像也有多个层反复重试，未取得完整镜像。不能将之前仅替换正式 FS 二进制的快测算作完整发布验收。
- 已回滚失败的 Helm 升级；回滚期间另一次操作将 release 升至 Chart/镜像 `0.7.1-40`（revision 34），宿主 installer 重新安装并重建了节点上的 Sysbox Pod。最终 installer/admission Ready，FS/mgr/snapshotter active，FS 报告 `0.7.1-40`；六个 CKM 均恢复 Ready，指定 `loadopt-218-1016` 内层 K3s `/readyz` 返回 `ok`。本次未完成 `v0.7.1-41` 的正式发布验收；重试前需先解决 GHCR 镜像分发并核对现场 revision，避免覆盖并发变更。

## 2026-11-17：1016311 `/proc/stat` 状态查找优化（218）

- 后续 P1 快照表清理优化：瞬时资源快照 TTL 仍为 100–200 ms，但全表过期清理改为最多每分钟一次；每次读取仍按原 TTL 判断命中，容器注销仍即时清理所属快照。1000 视图本地微基准原版三轮约 193/192/192 ns/op，修改后约 214/160/160 ns/op（108 B/op）；首轮有波动，仅为缓存路径微基准。`go test -race -count=1 ./...` 全包通过。以本地 `fs-procstat-check-20261117` 为基线生成未推送的 `fs-snapshot-prune-20261117` 测试镜像，静态 FS SHA256 `bd898397681c366afaee8fff7fda277fc9586590fce72092471d8548b47dc069` 临时安装在 218/server1，旧版备份于 `/var/lib/sysbox-test/fs-snapshot-prune-20261117/sysbox-fs.before`。6 轮分钟巡检后候选数 69、恢复失败 0；FS/mgr/snapshotter active，指定 CKM UID 与容器 ID 未变、Ready，L1 `/proc/stat` 和 `/proc/meminfo` 可读、K3s `/readyz` 为 `ok`，FS 近十分钟无 panic/fatal/OOM/ENOMEM/waitid/error。相同 L1 8 并发、每轮 1600 次 `/proc/stat` 读取三轮为 382/368/360 ms，不能证明高密度端到端收益。本次不是正式发布工件验收。
- 2026-11-17 多视图 A/B：在 218/server1 同一组 24 个无 PVC 的 L0 full-runtime、`hostUsers:false` Pod 中，每 Pod 每轮重新打开 `/proc/stat` 1000 次，24 个 Pod 并发，总计 24000 次打开。候选版首次三轮 5869/5909/6174 ms；旧版三轮 6437/6286/6395 ms；再次候选版 6285/6232/6221 ms；再次旧版 6710/6391/6279 ms，全部读取成功。旧→新→旧三段区间重叠，不能证明稳定的端到端吞吐提升；单节点 24 Pod 也不能外推到更高密度。每次切换都等首轮挂载恢复后再测，恢复失败 0，24 个 Pod 的 UID 与容器 ID 全程不变。最终恢复候选 FS SHA256 `bd898397681c366afaee8fff7fda277fc9586590fce72092471d8548b47dc069`：测试 Pod 存在时 117 候选，删除后回落到 69，恢复失败 0，FS/mgr/snapshotter active，指定 CKM Ready，L1 虚拟 `/proc` 与 K3s `/readyz` 可用，FS 无 panic/fatal/OOM/ENOMEM/waitid/error。24 个临时 Pod 已删除；这是本地候选 A/B，不是正式发布验收。

- 原实现每次获取 `/proc/stat` 状态都持有全局锁遍历所有视图，逐个加锁检查五分钟过期，即使当前没有状态需要回收。现在每分钟最多清理一次；容器注销仍即时释放对应状态，输出和请求视图逻辑未变。1000 视图本地查找基准三轮为 11528/11490/11558 ns/op，改后为 13.26/13.25/13.25 ns/op（均 0 B/op）；这是状态表查找微基准，不代表 FUSE 吞吐。
- `go test -race -count=1 ./handler/implementations ./handler ./fuse ./cmd/sysbox-fs` 通过。`BUILD_PROFILE=test TEST_COMPONENTS=fs TEST_TARGETS=deploy` 以 `tapd-fs-reaper-20261117` 为基线生成本地 `tapd-fs-procstat-20261117`，未推送。FS 二进制 SHA256 `85c44f410ba477627cdfddbad384241ec289a2938804b5bda54e394283e1b628` 已临时安装在 218/server1；旧版备份于 `/var/lib/sysbox-test/procstat-20261117/sysbox-fs.before`。
- 替换前指定 L1 CKM 8 并发、每轮 1600 次 `/proc/stat` 只读三轮为 385/409/382 毫秒；替换后首轮恢复巡检 69 候选约 56409 毫秒、失败 0，下一轮 168 毫秒，稳态同负载为 409/388/381 毫秒。CKM Pod UID `f2c05c72-fe9e-4725-b6a4-f52fc8ee441c` 与 k3s-server 容器 ID `a02c0a5cc664a7acf21b167e246f0589750cf704fb05ae116f3f5092ed9dd787` 未变，K3s `/readyz` 为 `ok`，`/proc/stat` 有 8 行 CPU，FS/mgr/snapshotter active；FS 近十分钟无 panic/fatal/OOM/ENOMEM/waitid/error。临时调试 Pod 已删除，无 PVC。此次未证明端到端吞吐提升，也不是正式发布验收。

## 2026-11-17：FS reaper 与恢复 helper 竞争复测（218）

- 当前提交的非发布本地完整 release 构建通过：deb、静态 lite 二进制、deploy 镜像和 Chart 均生成，`/tmp/sysbox-release-check-20261117/SHA256SUMS` 所列产物全部校验通过；镜像未推送，GitHub Release 未创建，因此不算正式发布工件或集群安装验收。当前源码的 `SYSBOX_RECOVERY_PRIVILEGED=1 go test -race -count=1 ./recovery -v` 也通过，包括 BPF 加载和独立 FUSE 挂载恢复测试。
- 同一组 24 个无 PVC 的 L0 `sysbox-runc`、`hostUsers:false` Pod 在 server1 做旧版→修复版→旧版 A/B，再恢复修复版。旧版两段首次扫描约 73.8/72.1 秒、稳态约 243/234 毫秒，并出现一次 `mount recovery helper: waitid: no child processes`；修复版首次约 71.6 秒、稳态 279/285 毫秒、恢复失败 0。不能据此声称吞吐提升。
- 旧版故障同秒的 `reaper: reaped pid` 与全局 `Wait4(-1)` 抢走恢复 helper 子进程相符。源码现使 helper 从 `Start` 到 `Wait` 期间阻止 reaper 扫描。`go test -race ./nsenter ./recovery ./cmd/sysbox-fs` 和特权恢复测试通过。本地 `BUILD_PROFILE=test` FS 镜像 `tapd-fs-reaper-20261117` 的二进制 SHA256 为 `6c0e2c3580ae7e40b24c950d46711575ad6a317f7c69a0b0a3eb86c6b91cbf33`；临时安装后 114 候选首次扫描约 73.9 秒、稳态约 272 毫秒，失败 0，未再见 `waitid`。重启后的快速并发探针首次只有 3/24 成功，数秒后复测 24/24 首次成功；恢复有短暂窗口，不能称立即无感。
- 全部临时 Pod 和 Deployment 已删除，无 PVC；最新监控候选数由 114 回到 66，恢复失败和队列丢弃均为 0。所选 `ckm-snapshot-1109` 仍 Ready。此次仅是源码候选验证，未证明高密度性能收益或正式发布验收。
- 边界修正：L2 `sysbox-runc-lite` 不启动、不注册 FS/mgr，`/proc` 按已有挂载与运行时默认行为处理。继承 L1 FUSE 挂载时出现的读数差异只是现象，不作为 lite 的独立虚拟化需求或本次 FS 修复验收项；验收对象为宿主 FS 与使用完整运行时的 L1 CKM。

## 2026-11-16：TAPD 1016310 源码补充及 218 复测

- 2026-11-17 checkpoint 候选滞留及修复：在 218/server1 创建 24 个临时 L0 `sysbox-runc`、`hostUsers:false` Pod，重启宿主 FS 后 24 个 Pod 的 UID/容器 ID 均未变，新打开的 FUSE `/proc` 可读，指定 CKM 保持 Ready。扫描候选由 66 增至 114，首轮含恢复耗时 72281 毫秒，后续两轮 192/247 毫秒，失败与队列丢弃为 0。删除全部临时 Pod 后，注销日志已出现，但连续两轮候选仍为 114；原因是启动时加载的 checkpoint owner map 从未随注销缩减。源码现按登记状态与原 PID start time 清理失效 owner，`go test -race ./cmd/sysbox-fs ./recovery/...` 通过。用本地测试镜像 `tapd-fs-prune-20261117` 的静态 FS SHA256 `f26024e4b9c4d3f31fc3d94a8f798051f925dc0d6a587395561fdba35f03e135` 临时安装到 server1，原版备份于 `/var/lib/sysbox-test/fs-prune-20261117/sysbox-fs.before`；另建 4 个完整运行时 Pod，重启 FS 使其进入 checkpoint 后候选数为 74，删除 4 Pod 并确认 8 个容器注销后下一轮降至 66，扫描耗时 198 毫秒、恢复失败 0。指定 CKM UID/主容器 ID 不变且 Ready，L1 `/proc/meminfo` 和 K3s `/readyz` 可读，FS/mgr/snapshotter active，FS 无 error 级日志。全部临时 Pod/Deployment 已删除，无 PVC；这是源码候选测试，不是正式发布验收。
- 2026-11-17 监控 Chart 现场升级：`w7panel-metrics/dev-v1` 提交 `995de50` 修复旧版 release 用 `--reuse-values` 升级时新增可选配置缺失导致的模板空指针；`helm lint`、旧值/启用告警/启用 Longhorn 与 kubelet 的渲染及 218 服务端 dry-run 通过。以旧 release 用户值叠加新默认值、显式关闭无关 Longhorn 采集后，将 218 `w7panel-metrics` 从 revision 14 / Chart 1.0.28 升到 revision 15 / Chart 1.0.39。Node Exporter 1/1 Ready，Helm 管理的参数包含 `/host/root/run/sysbox-metrics`，vmagent operational 且 AppArmor 注解由 Chart 管理；Node Exporter 暴露 `sysbox_fs_recovery_attempts_total=394`、`node_textfile_scrape_error=0`，VMSingle 实际查询到 `instance=server1` 的恢复计数 394 和 textfile 错误 0。无正式 Alertmanager 地址，未创建 Sysbox VMAlert/VMRule；从源码 Chart 安装不是正式发布工件验收。旧 Higress/traffic 资源已由独立 `metrics-traffic` release 接管，升级会移除原 Chart 所属副本。
- 2026-11-17 L0/L1 性能基线：218 server1 仍运行 FS SHA256 `26fe20423166df7d67c84708023982f3a5c47b1894dd674671ece123efa5f13c`，指定 `ckm-snapshot-1109` Ready，L1 Server 为 `sysbox-runc`、`hostUsers:false`。仅在该 L1 内运行 8 个并发读者、每人 100 次依次打开 meminfo/stat/uptime/loadavg/pressure/cpu（每轮共 4000 次打开），三轮耗时 643/661/631 毫秒，读取均成功；FS 恢复失败及队列丢弃计数均为 0。这是单 CKM、当前候选版本的短时读基线，不是高密度旧→新→旧 A/B，也不包含本轮新增的 CPU online/present 缓存改动；未切换正在承载业务的 FS 到不兼容的旧 checkpoint 版本。
- 2026-11-17 CPU 回退缓存候选验收：`BUILD_PROFILE=test TEST_COMPONENTS=fs TEST_TARGETS=deploy` 从上述基线镜像生成本地 `tapd-fs-cpu-input-20261117`；静态 FS SHA256 `3d7be9bfad0c42003810cd3b04b888dd093da1db46d50b0523f4b029b163817c` 临时安装于 218 server1，旧版备份在 `/var/lib/sysbox-test/fs-cpu-input-20261117/sysbox-fs.before`。重启后指定 L1 Server Pod UID `f2c05c72-fe9e-4725-b6a4-f52fc8ee441c`、k3s-server 容器 ID `a02c0a5cc664a7acf21b167e246f0589750cf704fb05ae116f3f5092ed9dd787` 未变，CKM Ready；L1 FUSE `/sys/devices/system/cpu/online` 和 present 可读，内层 K3s `/readyz` 为 `ok`。首轮恢复后 successes=313、failures=0、unsupported=27、dropped=0；FS 近五分钟无 error 级日志。相同 8 并发、4000 次打开的三轮耗时 682/638/648 毫秒，与旧候选 643/661/631 毫秒接近，不能宣称吞吐提升。此负载主要读 `/proc`，不保证命中修改的 CPU cpuset 缺失回退；本次是单 CKM 源码候选回归，不是高密度旧→新→旧 A/B 或正式发布验收。
- 2026-11-17 同 cgroup 双 PID namespace 现场验证：在指定 CKM `ckm-snapshot-1109` 的 k3s-server 容器内，用一次性静态探针仅为子进程 `clone(CLONE_NEWPID)`，未改 cgroup 或 mount namespace。父、子读取 `/proc/self/cgroup` 均为 `0::/init.scope`；PID namespace 分别为 `pid:[4026534351]`、`pid:[4026534294]`。同一继承 FUSE `/proc/loadavg` 在父视图输出 `0/344 2940053`，子视图输出 `0/1 1`，证明同 cgroup 不会串用 PID namespace 采样器。临时探针已从 CKM `/tmp` 删除，外层读数仍正常。这是单次功能隔离证据，不是高密度性能 A/B。
- 2026-11-17 工单验收口径确认：1016310 保留分钟级静默故障兜底巡检，因此不宣称“零轮询”；218 暂无正式 Alertmanager 地址，Chart 保持可选告警配置，正式通知链路待地址确定后验收。
- 2026-11-17 监控 Chart 后续交付：`w7panel-metrics` 的 `sysbox-fs-recovery-metrics` 分支经快进合入并推送 `dev-v1`，当前提交 `df4b4af`。在合入后的工作区重跑 `helm lint`，默认渲染仅配置 Node Exporter textfile 采集，不创建 VMAlert/VMRule；设置 `sysboxRecoveryAlerts.notifierURL` 后渲染两条恢复告警及 vmalert AppArmor 注解，均通过。本地 1.0.38 Chart 包 SHA256 `16fea6ae67520037d27c4a5811817fd54c373ea6ea304359120ef4995c913d72`，包内告警资源通过 218 API 的 server-side dry-run。218 当前仍没有 Alertmanager 服务或正式 VMAlert/VMRule；尚未配置正式通知地址、安装新 Chart 或做发布工件验收，不能称正式告警上线。
- 2026-11-17 挂载策略刷新重试修复：此前若 FUSE 挂载已替换而 `mountinfo` 解析暂时失败，下一轮扫描不再能从旧挂载重新取得路径，导致旧挂载 ID 可能留在不可变策略中。现保留待刷新路径并在下一次同容器外层命名空间恢复尝试中重试；故障注入、FS 相关 race 测试及特权 FUSE/eBPF 测试均通过。`BUILD_PROFILE=test TEST_COMPONENTS=fs TEST_TARGETS=deploy` 基于本地 `tapd-fs-queue-pidns-20261117` 生成本地候选 `tapd-fs-policy-refresh-20261117`，二进制 SHA256 `26fe20423166df7d67c84708023982f3a5c47b1894dd674671ece123efa5f13c`。218 server1 原版备份于 `/var/lib/sysbox-test/fs-policy-refresh-20261117/sysbox-fs.before`，仅替换 FS 并重启；目标 CKM UID `f2c05c72-fe9e-4725-b6a4-f52fc8ee441c`、k3s-server 容器 ID `a02c0a5cc664a7acf21b167e246f0589750cf704fb05ae116f3f5092ed9dd787` 均未变且 Ready，L1 `/proc` 与 K3s API 正常。首轮 66 个候选含恢复耗时 57178 毫秒，次轮 172 毫秒；恢复失败及队列丢弃为 0，不支持传播仍为 27。此现场测试没有主动注入策略解析故障，故重试分支的直接证据来自本地故障注入测试；候选镜像未推送，非正式发布验收。
- 2026-11-17 FS 队列与 PID namespace 隔离候选验收：`BUILD_PROFILE=test TEST_COMPONENTS=fs TEST_TARGETS=deploy` 从本地 `tapd-fs-scan-20261117` 构建 `tapd-fs-queue-pidns-20261117`（仅本地），静态 FS SHA256 `4d314234c935b528d4329d3628012bb17819259659f44ef276de86ef6ce9d837`。218 server1 原二进制 SHA256 `973f4816a38dfa872bcdd040ace8b45b32cb5c10824fc5ad45430ed6833951ca` 备份于 `/var/lib/sysbox-test/fs-queue-pidns-20261117/sysbox-fs.before`，临时安装候选并重启。目标 CKM UID `f2c05c72-fe9e-4725-b6a4-f52fc8ee441c`、k3s-server 容器 ID `a02c0a5cc664a7acf21b167e246f0589750cf704fb05ae116f3f5092ed9dd787` 未变且 Ready；FS/mgr/snapshotter active、L1 K3s API 可用。新建 100m CPU/128MiB L2 lite Pod 继承 `fuse sysboxfs` loadavg 挂载，实读 1 核、`MemTotal=131072 kB`，采样后 loadavg 为 `0/2 78`；L1 同时读到约 342 个进程，未串用采样器。两轮分钟扫描去重候选均为 66，首轮含恢复耗时 54525 毫秒，下一轮 179 毫秒；恢复失败和队列丢弃均为 0。重启后相关服务无 error、panic、fatal、OOM 或 ENOMEM 日志；临时 L2 与宿主调试 Pod 已删除。源码相关包 race 测试通过，受控慢修复测试覆盖队列读入、去重、满队列计数和关停；尚未做真实内核高并发故障注入或同 cgroup 双 PID namespace 的集群测试。这不是同负载旧→新→旧 A/B 或正式发布验收。
- 2026-11-17 最新 FS 候选验收：`BUILD_PROFILE=test TEST_COMPONENTS=fs` 从本地 `tapd-recovery-218-20261116-r2` 基线构建测试镜像 `tapd-fs-scan-20261117`（仅本地，未推送）。静态 FS SHA256 `973f4816a38dfa872bcdd040ace8b45b32cb5c10824fc5ad45430ed6833951ca` 临时安装到 218；旧版 SHA256 `2c7beb2897505ae92d8d87b7457dbb60f06be2fd5ce29f7793782de9946f4b89` 备份于 `/var/lib/sysbox-test/fs-scan-20261117/sysbox-fs.before`。重启后 FS/mgr/snapshotter active，目标 CKM UID、容器 ID 不变且 Ready，L1 虚拟 `/proc` 与内层 K3s 可用；新建 100m CPU/128MiB L2 Pod 实读 1 个 CPU、`MemTotal=131072 kB`，首次 loadavg 的 lastPID 为 1，四个文件均为继承的 `fuse sysboxfs` 挂载。临时 L2 和宿主调试 Pod 均已删除。三轮分钟兜底去重候选数为 66；逐轮日志耗时 55232/190/约 167 毫秒，累计指标 55.590015322 秒、count=3。首轮包含重启后的挂载恢复，不能当作稳态扫描成本。Node Exporter 暴露新指标且 `node_textfile_scrape_error=0`，VMSingle 查询到新序列；重启后 FS/mgr/snapshotter 无 error 级日志。这是单节点源码候选验收，不是同负载旧→新→旧 A/B 或正式发布验收。
- 2026-11-17 源码补充：首次创建 `/proc/loadavg` 采样视图时，`lastPID` 此前为 0，须等第一次 5 秒采样后才更新；回归测试先复现该问题。现从当前视图的 PID namespace 读取初值，不复用外层 init 的 PID；后续 EWMA 采样不变。`go test -race ./handler/...` 通过；最新候选的 218 实测见上一条。
- 2026-11-17 L2 请求视图补测：指定 `ckm-snapshot-1109` Ready，L1 Server 为 `sysbox-runc`、`hostUsers:false`。临时 L2 `sysbox-view-probe-1117b` 使用 `sysbox-runc-lite`、100m CPU/128MiB 内存限额；`/proc/loadavg`、`/proc/uptime`、`/proc/cpuinfo` 都是继承的 `fuse sysboxfs` 文件挂载。L1 `cpuinfo` 有 8 条 processor，L2 为 1 条；L1 loadavg 为 `1/355 2740972`，L2 稳态为 `0/2 78`。L2 uptime 首列约 71043 秒，与 L1 系统运行时间相同；空闲列约 71043 秒，而 L1 为 565327 秒。这符合 L2 读取 L1 虚拟系统 uptime、其余部分按请求者视图计算的边界，不能把 L1 注册快照直接复制为 L2 输出。创建期完整 uptime/loadavg/cpuinfo 种子会固化错误视图；当前已有 L1 创建时间和全局宿主原始数据缓存，是否预热需先量化首读收益。临时 Pod 已删除；测试窗口宿主 `sysbox-fs` error 日志无记录。
- 2026-11-17 告警链路补测：`w7panel-metrics/sysbox-fs-recovery-metrics` 新增可选 VMAlert/VMRule，配置现有 Alertmanager URL 后评估恢复失败和高频旧 FUSE FD 读取；默认不创建告警资源。Chart lint 与两种渲染通过。在 218 `default` 临时部署 Chart 渲染的 VMAlert/VMRule 和 HTTP 接收器，恒真测试规则触发多次 `POST /api/v2/alerts`，接收器均返回 200。首次规则热重载因 AppArmor 拒绝 vmalert 自发 SIGHUP 而重启；Chart 补充仅针对 vmalert 容器的 unconfined 注解后，Pod Ready、重启 0，实际规则组正常启动。临时规则、VMAlert/VMRule、接收器均已删除。本次只验证告警评估及投递链路；正式 Alertmanager 地址未提供，Chart 未合入或发布，不能称为生产告警或发布验收。
- 2026-11-17 定向纠偏：218 目标 `ckm-snapshot-1109` Ready，L1 Server 仍为 `sysbox-runc`、`hostUsers:false`。临时 L2 `sysbox-proc-view-probe-1117` 使用 `sysbox-runc-lite` 和 128 MiB 内存限额；其 `/proc` 根目录是 procfs，但 `/proc/meminfo` 的 mountinfo 明确为继承的 `fuse sysboxfs` 文件挂载。L2 实读 `MemTotal=131072 kB`、`memory.max=134217728`，同一时刻 L1 `MemTotal=16777216 kB`。这证明继承的 FUSE 文件按实际请求者生成了 Pod 限额视图，并不证明所有 `/proc` 文件均独立虚拟化或 L2 有独立 sysbox-fs 实例。临时 Pod 已删除。先前“L2 lite 不提供按 Pod 的 `/proc` 视图隔离”的笼统表述不准确。
- `sysbox-fs` 6039eb4 在 `/run/sysbox-metrics/sysbox-fs.prom` 每 15 秒原子发布同一组恢复计数和延迟直方图，保留 root-only `/run/sysbox` socket。`go test -race ./handler/... ./fuse/... ./state/... ./recovery/... ./cmd/sysbox-fs` 通过。218 临时安装静态 FS SHA256 `2c7beb2897505ae92d8d87b7457dbb60f06be2fd5ce29f7793782de9946f4b89`，上版备份于 `/var/lib/sysbox-test/textfile-20261116/sysbox-fs.before`；目录 `0755`、文件 `0644`，以 Node Exporter 的 UID 65534 实测可读。目标 CKM UID/容器 ID 未变且 Ready，FS/mgr/snapshotter active，重启后相关服务无 error 级日志。218 现有 Node Exporter DaemonSet 临时增加 `--collector.textfile.directory=/host/root/run/sysbox-metrics` 并滚动成功：其 `/metrics` 实际暴露 `sysbox_fs_recovery_*`，`node_textfile_scrape_error=0`。持久配置在独立 `w7panel-metrics` 仓库分支 `sysbox-fs-recovery-metrics` 提交 00250dc，尚未合入或发布；218 当前监控 Chart v1.0.28 的直接 DaemonSet patch 可能被后续 Helm 升级覆盖。
- 218 的 vmagent 原先在 `cri-containerd.apparmor.d` 下自发 SIGHUP 被内核拒绝而 CrashLoopBackOff。监控 Chart 同一分支提交 043fa37 仅将 vmagent 容器标为 AppArmor unconfined，已推送但未合入或发布；218 的 VMAgent CR 临时加入该注解后为 operational，新 Pod `2/2 Running`、重启 0，日志确认 SIGHUP 配置重载成功。通过 vmagent 访问 VMSingle API 再查得 `sysbox_fs_recovery_attempts_total{instance="server1"}=353`，证明采集和入库持续正常。集群与 Chart 均无 vmalert/Alertmanager，平台告警评估及通知未实现；临时 CR patch 也可能被 Helm 升级覆盖。本次不是正式镜像/Chart 发布验收。
- `sysbox-fs` c2136c3 将恢复耗时补齐为 Prometheus 直方图（分桶、`_sum`、`_count`），可计算延迟分位数；`go test -race ./cmd/sysbox-fs ./recovery/...` 通过。218 临时安装静态 FS SHA256 `932f400df10ca281d2b9b6066c0d3cc3f92ec94d8b771582a12c4c062b282e74`，上版备份于 `/var/lib/sysbox-test/duration-hist-20261116/sysbox-fs.before`。两轮巡检稳定为 attempts=count=363、successes=309、failures=0、unsupported=27；`le=0.25` 为 337、`le=0.5` 与 `+Inf` 均为 363，桶计数单调，累计耗时 58.102691415s。目标 CKM UID 和容器 ID 未变且 Ready，虚拟 `/proc` 与内层 K3s 可用，FS/mgr/snapshotter 无 error 级日志。平台 vmagent 仍异常，未证明平台采集/告警；本次不是正式发布验收。
- `sysbox-fs` 1917830 增加 `sysbox_fs_recovery_duration_seconds_sum`，可与尝试次数计算平均修复耗时；相关 race 测试通过。218 宿主临时安装静态 FS SHA256 `4923488c09532c783a54514e6a23d5ac75b74ee21ba821ed73896fe1cc00f6ee` 并重启，原二进制备份于 `/var/lib/sysbox-test/duration-20261116/sysbox-fs.before`。两轮巡检后本机指标稳定为 attempts=350、successes=309、failures=0、unsupported=27、duration=57.069548159s；309 条成功修复日志的单次耗时 p50=176ms、p95=268ms、最大=491ms。这是当前节点负载下的修复耗时，不代表整体重启完成时间或高密度吞吐收益。目标 CKM UID、容器 ID 未变，虚拟 `/proc`、内层 K3s Ready，FS/mgr/snapshotter active，重启后相关服务无 error 级日志。socket 仍为 `0600`；218 现有 vmagent 容器处于 CrashLoopBackOff，平台采集和告警尚未验证。本次仍是临时二进制验收，不是正式镜像发布。
- 后续源码新增本机 `/run/sysbox/recovery-metrics.sock` 的 HTTP `/metrics`：提供恢复尝试、成功、失败、不支持、旧 FD 故障事件及高频故障进程计数。`go test -race ./handler/... ./fuse/... ./state/... ./recovery/... ./cmd/sysbox-fs` 通过。静态 FS SHA256 `38d1e0236779b117b9c251934915295535720d416a1f2779aae16ae0164f782b` 已在 218 临时安装：socket 权限 `0600`，指标可读取；目标 CKM UID 与两个容器 ID 未变，L1 `/proc/meminfo`、`/proc/uptime` 和内层 K3s Ready。11:40:10、11:41:10 两轮恢复计数均为 attempts=362、successes=309、failures=0、unsupported=27，FS/mgr/snapshotter active，重启后无 error 级日志。首轮扫描约需 55 秒且遍历了大量内层 mount namespace，不能据此声称零轮询或容器数量无关；旧 FD 故障事件仍会增长。平台采集和告警规则未接入，此次也不是正式镜像发布验收。
- 218 再测发现新增的内层分钟巡检会对使用 shared propagation 的命名空间每分钟重试，持续产生 `FUSE recovery incomplete`；曾先回退测试 FS 止损。修复后仍保留恢复器的传播拒绝，但把此类命名空间标记为不支持并停止分钟重试。本地 race 测试通过；修复版静态 FS SHA256 为 `6d51c65df7034b1ade25632e05d7208fe70505fa9cb68cc4854dd4561ce0c17a`。218 宿主重启 FS 后 CKM UID、主容器 PID 未变，L2 nginx 重建后普通 PVC 和特殊目录标记、CRI writable-layer、kubelet stats/summary 均通过。09:39、09:40、09:41 的巡检计数分别为 attempts=385/386/386、failures=0/0/0、unsupported=27/27/27；不支持挂载仅首轮警告。本次是节点二进制临时安装，不是正式镜像或发布验收。L0 三组功能测试此前通过，收尾时三组 L0 和 L2 Deployment 缩容为 0，PVC 保留。

- 分钟兜底现扫描已登记容器的 cgroup 子树，按 mount namespace 去重，对静默失效的内层挂载调用既有白名单修复器；同一命名空间成功检查后不再重复修复。反复访问旧 FUSE FD 的进程每分钟达到 5 个故障事件时单独警告，修复日志新增耗时毫秒字段。本节上一条记录了修复后的构建与 218 复测。
- 分钟扫描开销随登记容器和存量进程数增长，不能同时承诺“零轮询”和“容器数量无关”。失败后透传宿主 `/proc` 仍因隔离风险不实现。高密度性能、告警接入和毫秒级恢复需部署后量化；1016311 的完整初始快照种子及透传/模拟名单仍未实现。
- 当前 FS 的并发热读微基准（本机 QEMU 4 vCPU，3 次、每次 2 秒）为缓存单键命中 63–64 ns/op、1000 键缓存中单键命中 59–68 ns/op，均为 0 B/op；loadavg 格式化读取为 80–86 ns/op、24 B/op。该基准不包括 FUSE、cgroup I/O 或内层 K3s，不能证明 218 高密度端到端吞吐收益。FS 资源视图、缓存、恢复模块 race 测试通过；runc 和 IPC 相关测试在关闭既存 vet 报错后通过。
- 1016311 的 `/proc/swaps` 不能按工单所述缓存为全局最终输出：当前输出由读取者 cgroup 的 swap 使用量生成。现改用按请求视图的短 TTL 快照，保证分段读取一致，并随 FUSE owner 退出清理。`handler/implementations`、`handler`、`fuse`、`cmd/sysbox-fs` 的 race 测试通过；218 临时安装 FS SHA256 `966ed85236d946841c26174839464606d1d48c803a48b15079dd7a4b080c034f` 后，所选 CKM UID/容器 ID 保持不变，L1 与新建 L0 full 容器 `/proc/swaps`、`/proc/meminfo` 可读，L1 K3s Ready；L0 测试 Deployment 已缩回 0。11:12、11:13 的 FUSE 恢复计数均为 attempts=363、failures=0、unsupported=27，期间无 error 级日志。本次未构建或验证正式镜像。

## 2026-11-16：TAPD 1016310/1016311 最终源码候选验收（218）

- 最终 `BUILD_PROFILE=release` 本地构建完成，`dist/SHA256SUMS` 全部通过；镜像 `tapd-recovery-218-20261116` 仅导入 218 与所选 CKM 的 containerd，未推送或发布 tag。这是源码候选验收，不是正式发布验收。
- 首次连续重启发现两个恢复问题：容器挂载命名空间的 `/tmp` 不可靠，子挂载 staging 失败；历史 checkpoint 中的 FUSE 设备号可被新会话复用，单所有者路由导致 FS 拒绝启动。已改用宿主私有挂载命名空间 staging、按故障命名空间选择多个候选。短命读取进程退出导致 helper 报错，现按 `ESRCH` 计为过期事件，不再误报为挂载恢复失败。
- 最终 deb 中 FS SHA256 为 `c1062245327144c098cdc85848f0efece2e85ea981fd2bbbdba738ffe7218cb4`。安装并重启后，所选 `k3k-console-164315/ckm-snapshot-1109` 的 UID `f2c05c72-fe9e-4725-b6a4-f52fc8ee441c`、主容器 init PID `2085883` 不变；虚拟 `/proc/uptime`、`binfmt_misc` 和内层 K3s API 可用。FS listener FD store 为 66，恢复计数在 07:28–07:30 稳定为 attempts=119、successes=69、failures=0；四项宿主服务 active，重启以来无 error 级日志。
- L0 full/lite local-path 与 full Longhorn CSI 先前已 FUNCTIONAL PASS。最终 Chart 在所选 CKM 升级后，agent/admission Ready，CRI 默认 `runc`、lite snapshotter `sysbox`，无内层 full handler。独立 L2 nginx 测试 FUNCTIONAL PASS：空 PVC 初始化、Pod 重建后 rootfs/普通 PVC/特殊目录持久化、CRI writable-layer 与 kubelet stats/summary 均通过。相同配置重复 Helm upgrade 后 K3s PID 116、starttime 1293 不变。
- 临时 L0 与 L2 Deployment 均缩容为 0，PVC 保留。宿主最终替换前的 FS 备份在 `/var/lib/sysbox-test/tapd-recovery-20261116/sysbox-fs.before-final`；installer 后续重新部署可能覆盖该临时安装。
- 对照两张工单原文，本次是核心路径验收，不代表所有文字要求均已落地。1016310 的分钟兜底后来已扩展到登记容器 cgroup 内的 mount namespace，但 shared propagation 挂载仍不支持安全替换；恢复统计写入日志，尚无独立监控指标或量化毫秒级恢复/资源开销证据。失败后透传宿主 `/proc` 会泄露宿主视图，因此未实现该降级。1016311 仅前置登记 cgroup/PID 命名空间等稳定身份；实际到达外层 FUSE 的请求按调用者解析视图。L2 lite 没有独立 sysbox-fs 实例，但继承的部分 FUSE 文件可按 Pod 限额返回不同视图，见上方 2026-11-17 实测；不能据此推断整个 `/proc` 树均按 Pod 虚拟化。完整初始快照种子、透传/模拟名单及高密度吞吐收益量化仍未完成。

## 2026-11-16：sysbox-fs 缓存生命周期源码验收（218）

- 使用 `BUILD_PROFILE=test TEST_COMPONENTS=fs`，以 `v0.7.1-37` 为基线构建 `docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:fs-cache-218-20261116`。新 FS SHA256：`98ce143faa62742369656725259bea3e364a19843309e56346ecccb6a79e7a01`；本地 race 测试通过。本次是源码测试，未验证正式发布产物或宣称集群性能提升。
- 经授权备份、替换宿主 FS 并重启，保留所有 PVC。旧 FUSE 会话失效，逐个重建现存目标 Pod：24 项原始清单中 22 项新 UID 且 Ready；`w7-sitemanager-site-manager-nginx` 重建后仍有原有 upstream DNS 故障（基线已不 Ready、重启 1052 次）；`w7-traditiontool-qffdahvq` Deployment 在期间消失，来源未知，未擅自重建。
- 5 个 CKM 新 Pod Ready，meminfo/loadavg/uptime 可读。选定 `k3k-console-164315/ckm-snapshot-1109` 在部署前已存在 `Socket not connected`，重建后恢复。部分旧 CKM Pod 因 `etcdpod.k3k.io/finalizer` 保留终止对象；恢复等待以旧进程已终止和替代 Pod Ready 为准，未移除 finalizer。`ckm-bkhnk` 的部分业务另有历史 root-ca 权限故障，抽样主容器停止于 11 月 10 日；未将其计为本次回归成功。
- L0 `02` FUSE 设备检查，以及 full/local-path、lite/local-path、full/disk-default 三组 `03` 均 FUNCTIONAL PASS。选定 CKM 内两个 cgroup 分别设置 128/256 MiB，8 个并发 worker 完成 800 次 MemTotal 隔离断言及 5600 次附加 proc 读取；临时 cgroup 已删除。
- 测试镜像通过导入 containerd 使用，未推送。nested Chart 默认 `Always` 曾导致 ImagePullBackOff；通过 Helm 将本次安装的 installer/admission pullPolicy 设置为 `IfNotPresent` 后恢复。内层安装、agent/admission Ready、native/lite handler、`05` rootfs/PVC 初始化和重建持久化、特殊目录 bind、CRI stats 均通过；重复相同 Helm 配置后 K3s PID/starttime 不变。其余 CKM 未升级 Chart；`ckm-wulpt` 原未安装 nested runtime，不宣称其通过 lite 验收。
- 宿主 fs/mgr/snapshotter active，替换以来 error 级日志无记录；验收窗口内层 agent/admission 日志无 panic/fatal/OOM/ENOMEM。三组 L0 测试 Deployment 与 L2 `fs-cache-1116-nginx` 已缩容到 0，保留测试 PVC。
- 宿主是临时二进制安装：installer DaemonSet 仍指向 v0.7.1-37，后续重装可能覆盖测试 FS。旧二进制保留于 `/var/lib/sysbox-test/fs-cache-20261116/sysbox-fs.before`；证据在 `.cache/sysbox/fs-cache-218-20261116/`。本次未实现 FUSE 会话跨 FS 重启自动恢复。

## 2026-11-13：v0.7.1-38 子集群安装验收与镜像下载阻塞

- 目标：218 `k3k-console-164315/ckm-snapshot-1109`；仅验收该子集群，未升级 L0。
- 提交 `2c0ba60`、tag `v0.7.1-38`；GitHub Actions `43407336213` 全部成功后下载正式 Chart、lite 二进制及 SHA256SUMS，校验通过。
- 首次升级时新 agent 镜像长时间未下载完成，旧 agent 退出后 snapshotter socket 连接拒绝。安装等待超时，临时回滚 revision 9；清除两个尚未启动且卡在 Terminating 的升级 Pod 后恢复 Ready。
- 本机 GHCR blob 下载也复现连接重置（包含 IPv6 链路）；使用 IPv4 Range 分片下载、逐层 SHA256 校验后导入同一正式镜像，未修改镜像源配置。镜像 digest：`sha256:a03714f01dde30166bd52b6f973465d40666acd60255d84882bbaf221e667cb0`。此次结果不代表原有网络拉取路径已修复。
- 重试正式安装和重复安装均通过（最终 Helm revision 13），agent/admission Ready，重复安装未重启 K3s Server。安装 lite SHA256 与 release 一致：`b5b3408eeb54a7fa9785a32ae66572a5a01f6bd0207617d75fdd9c2b12b6fffa`。
- RuntimeClass `scheduling` 与新 nginx Pod `nodeSelector` 均为空；HTTP 返回 nginx 欢迎页。local-path 空 PVC 初始化、Pod 重建后的 webroot/rootfs 特殊目录标记、CRI writableLayer 与 kubelet stats/summary 均通过；本次未做 CSI 或 L0 回归。
- 恢复后 agent/admission 检查日志无 panic、fatal、OOM、ENOMEM。`sysbox-release-38-check` 已缩容至 0，测试 Pod 已删除；保留 `sysbox-release-38-rootfs`、`sysbox-release-38-webroot` PVC。

## 当前实施范围（2026-09-24）

步骤 3（L2 workload 的 `hostUsers:false`）暂不实施，也不作为本轮验收门槛。L1 CKM
Server 仍必须保持 `runtimeClassName=sysbox-runc` 与 `hostUsers:false`；L2 nginx 回归
不设置该字段。当前验收覆盖 rootfs 持久化、空目录初始化复制、特殊目录 bind 挂载，
以及 snapshotter/webhook 复用。明确放弃 proc 强隔离、视图隔离和 system workload；
实现基于官方 runc/libcontainer 局部修改，不引入 L2 `sysbox-fs` 或 `sysbox-mgr`。

### 2026-11-09：宿主 fusermount3 AppArmor 策略阻止 Sysbox 沙箱创建

60 面板集群的 `server1`（Ubuntu 26.04.1 LTS）上，
`default/w7-traditiontool-fmvkwtkm` 长期 `ContainerCreating`，事件为
`FailedCreatePodSandBox` / `failed to pre-register container with sysbox-fs`。
`sysbox-fs` 日志报 `fusermount3: mount failed: Permission denied` 和
`FuseServer InitWait error`；内核审计明确记录：

```text
apparmor="DENIED" profile="fusermount3" operation="mount"
info="failed mntpnt match" error=-13
name="/var/lib/sysboxfs/<container-id>/" fstype="fuse" srcname="sysboxfs"
flags="rw, nosuid, nodev"
```

直接对比 218：其宿主为 Ubuntu 26.04 LTS，同样加载 `fusermount3 (enforce)`，
但 `/etc/apparmor.d/local/fusermount3` 已有 Sysbox 目录的 mount/umount 放行规则；
60 节点没有该文件。218 文件的修改时间为 7 月 29 日，添加来源未确认。
这是宿主策略适配差异；`sysbox-fs` 自身为 unconfined、`/dev/fuse` 为 0666
均不能绕过执行 fusermount3 后的独立 AppArmor 策略。

项目此前没有自动安装这项规则。现由 `sysbox-deploy-k8s.sh` 的宿主 `install`
流程在依赖安装之后、启动 Sysbox 之前补齐 local/fusermount3：

```text
mount fstype=fuse options=(rw,nosuid,nodev) sysboxfs -> /var/lib/sysboxfs/*/,
umount /var/lib/sysboxfs/*/,
```

相同版本再次安装也执行；保留管理员已有内容，重复运行不重复追加规则。
无主 profile、AppArmor 未启用或 profile 已禁用时跳过；通过 `nsenter --root`
使用宿主根目录执行 `apparmor_parser -r`，重载失败终止安装。
本次只处理 Sysbox FUSE 目录，不复制 218 中另有的 K3s rootfs 卸载规则。

验证：本地安装逻辑回归覆盖缺少/禁用策略、规则保留和幂等、重载失败与重试；
策略通过 apparmor_parser 离线编译。尚未构建发布镜像或部署到 60/218，
不能将此代码修复记为现场恢复。部署后需确认目标 Pod Ready，且对应容器的
`failed mntpnt match` 与 FUSE 初始化错误消失。

现场处置补记（2026-11-09）：经授权在 60 的 server1 直接补齐上述两条规则并
重载，已通过退出码断言确认文件内容、`fusermount3 (enforce)` 与 parser 重载成功。
目标 Deployment 已为 1/1 Available，原 Pod 于 08:12:15 UTC Ready，重启次数 0，
Nginx 日志显示 worker 正常启动。操作时 installer 已被外部升级为 v0.7.1-34 且
尚处 ContainerCreating，因此使用已有 hostPID 特权容器执行宿主规则配置；
此次不作为 v0.7.1-34 完整安装验收。面板 exec 存在输出丢失，服务日志检查
未取得足够完整证据，不能声明节点所有相关错误均已消失。

#### 手动放行步骤（宿主 root 执行）

适用于内核审计已确认 `profile="fusermount3"`、`failed mntpnt match`，且目标为
`/var/lib/sysboxfs/<容器ID>/` 的情况。下面只增加该目录的 FUSE 挂载/卸载权限，
AppArmor 继续保持 enforce。所有宿主命令都在故障 Pod 所在节点执行。

可通过 SSH 登录宿主；通过现有 hostPID 特权调试容器进入时，先确认 kubeconfig
指向目标集群，再执行（替换 Pod 和容器名称）：

```bash
kubectl --kubeconfig /home/.kubeconfig -n default exec -it <调试Pod> -c <容器名> -- \
  nsenter -t 1 -m -u -i -n -p --root -- /bin/bash
```

60 本次通过面板 MCP 的 exec 进入 `server1` 上已有的
`w7panel-offline-agent-89r84` / `k8s-offline-ds` 容器，再执行同样的 nsenter。
Pod 名称可能变化，应重新确认其节点、hostPID 和 privileged 配置。

**1. 确认策略并备份，然后补齐规则。** 在同一个宿主 Bash 会话执行：

```bash
set -euo pipefail
hostname
cat /sys/module/apparmor/parameters/enabled
grep -Fx 'fusermount3 (enforce)' /sys/kernel/security/apparmor/profiles
test -f /etc/apparmor.d/fusermount3
grep -F 'include if exists <local/fusermount3>' /etc/apparmor.d/fusermount3

sysbox_aa_rules=/etc/apparmor.d/local/fusermount3
mkdir -p /etc/apparmor.d/local
sysbox_aa_backup=$(mktemp -d /root/sysbox-apparmor-backup.XXXXXX)
if [ -f "$sysbox_aa_rules" ]; then
  cp -p "$sysbox_aa_rules" "$sysbox_aa_backup/fusermount3"
else
  touch "$sysbox_aa_backup/was-absent"
fi
printf '备份目录：%s\n' "$sysbox_aa_backup"

while IFS= read -r rule; do
  if ! grep -qxF -- "$rule" "$sysbox_aa_rules" 2>/dev/null; then
    printf '\n%s\n' "$rule" >> "$sysbox_aa_rules"
  fi
done <<'EOF'
mount fstype=fuse options=(rw,nosuid,nodev) sysboxfs -> /var/lib/sysboxfs/*/,
umount /var/lib/sysboxfs/*/,
EOF
```

此方式保留 local 文件已有内容，重复执行不重复追加。记录输出的备份目录，
回滚时使用。若前置检查不通过，先确认实际策略，不继续套用本步骤。

**2. 检查语法并重载单个 profile。** 无需重启 Sysbox 或 K3s：

```bash
apparmor_parser -Q -T /etc/apparmor.d/fusermount3
sysbox_aa_since=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
apparmor_parser -r /etc/apparmor.d/fusermount3
cat "$sysbox_aa_rules"
grep -Fx 'fusermount3 (enforce)' /sys/kernel/security/apparmor/profiles
```

`-Q -T` 仅编译检查、不加载内核策略；`-r` 才替换已加载策略。
任一步失败都不能视为放行完成；修正或回滚后重新加载。

**3. 验证恢复。** Kubelet 会自动重试原来失败的沙箱，先等待目标应用恢复。
以下命令在具有目标集群 kubeconfig 的管理端执行：

```bash
kubectl --kubeconfig /home/.kubeconfig -n default rollout status \
  deployment/w7-traditiontool-fmvkwtkm --timeout=180s
kubectl --kubeconfig /home/.kubeconfig -n default get deployment w7-traditiontool-fmvkwtkm
kubectl --kubeconfig /home/.kubeconfig -n default logs \
  deployment/w7-traditiontool-fmvkwtkm -c tradition-tool --tail=30 --timestamps
```

在前面的宿主 Bash 会话检查重载之后的日志，结合目标容器 ID 判断：

```bash
journalctl -k --since "$sysbox_aa_since" --no-pager --grep 'fusermount3|sysboxfs'
journalctl -u sysbox-fs --since "$sysbox_aa_since" --no-pager
```

验收目标是 Deployment Available、Nginx 正常启动，且对应容器不再出现
AppArmor 挂载拒绝或 FUSE 初始化失败；同时检查 panic、fatal、OOM、ENOMEM。
仅看到规则文件或 parser 返回成功，不能替代应用恢复验证。

**4. 必要时回滚。** 先确认备份之后没有其他管理员修改同一 local 文件。
在原宿主会话执行；重新登录时，将 `sysbox_aa_backup` 设置为步骤 1 输出的真实目录：

```bash
sysbox_aa_rules=/etc/apparmor.d/local/fusermount3
if [ -f "$sysbox_aa_backup/was-absent" ]; then
  rm -- "$sysbox_aa_rules"
else
  cp -p "$sysbox_aa_backup/fusermount3" "$sysbox_aa_rules"
fi
apparmor_parser -r /etc/apparmor.d/fusermount3
```

回滚会撤销本次授权，之后新建 Sysbox 容器可能再次遇到原故障。

### 2026-11-03：两个 CKM 的 seccomp listener 丢失，agent 删除卡住

218 的 `k3k-console-117057/ckm-slowdisk-test` 与
`k3k-console-164315/loadopt-218-1016` 均出现内层 w7panel agent 长期
Terminating。此次只读取日志、进程状态并短时运行自动退出的 BPF 观测，未重启
CKM、Sysbox 服务或删除资源。

已确认的故障链：

- agent 实际进程已退出，`ctr tasks list` 为 STOPPED，但 `crictl inspect` 仍为
  CONTAINER_RUNNING。两个内层 containerd 日志均反复重试 TaskExit，报
  `failed to handle container TaskExit event: failed to stop container: failed to
  delete task: failed rootfs umount: function not implemented`，随后 kubelet 的
  StopContainer / StopPodSandbox 超时。agent 使用原生 runc / overlayfs。
- 同时内层 kubelet 访问 `/sys/fs/cgroup/...` 的 `openat2` 返回 ENOSYS，QoS
  cgroup 更新、指标采集及新 registry Job Pod 的 cgroup 创建失败。
- 在宿主 BPF `sys_exit_openat2` 与 `seccomp_do_user_notification*` 探针中，两个
  K3s 进程均进入 USER_NOTIF 路径，唯一 seccomp filter 的 `notif` 为 NULL：

  | CKM | 宿主 PID / CKM PID | filter | notif |
  | --- | --- | --- | --- |
  | ckm-slowdisk-test | 3879813 / 145 | 0xffff8e5ed2d7bd00 | 0 |
  | loadopt-218-1016 | 2623427 / 115 | 0xffff8e664ecfac00 | 0 |

  两个 filter 的 `prev` 均为 NULL，`Seccomp_filters` 均为 1；8 秒观测分别捕获
  368 / 434 次 `openat2=-38`。这是 listener 已丢失的现场证据，不能仅凭
  sysbox-fs 进程存活或 inner-agent Ready 判断拦截链路正常。
- 宿主 kernel 为 `7.0.0-31-generic`；sysbox-fs PID 3671294 自
  `2026-10-16 10:22:47 UTC` 持续运行，systemd NRestarts=0。运行二进制为
  `0.7.1-32`，构建时间 `2026-10-16 10:01:01 UTC`，SHA256 为
  `e813b6df69f19b5ed90c0114a23758710ab04215ec899accd5af26508cd28418`。

源码对应关系：`sysbox-runc/libsysbox/syscont/spec.go` 的
`cfgSyscontSyscallTraps` 拦截 openat2；`sysbox-fs/seccomp/openat2.go` 对普通
cgroup 路径返回 CONTINUE。`seccomp/tracer.go` 的 `connHandler` 在容器启动时接收
listener；poll 非 EINTR 错误或 `Revents != POLLIN` 会结束会话并关闭 FD。
kernel >=5.8 使用 listener 的 unused-filter 通知，不走旧内核的进程 pidfd 退出
分支。现有 poll 退出日志为 Debug，且未记录实际 Revents；没有旧 filter 的
listener 恢复机制。

外层 Pod 重建会建立新的 filter/listener，因此符合用户观察到的恢复现象；本次未
执行重建验证。**尚未确定旧 listener 最初为何被关闭，不能把 poll 分支直接认定为
根因补丁，也不能归因于本次 sysbox-fs 服务重启。** 后续应捕获 listener release 的
filter、PID、用户态调用栈，以及 tracer 退出时的 container ID、FD、Revents 和
errno，区分 poll 主动退出、FD 误关与其他释放来源。修复应针对 listener 生命周期，
单独给 cgroup openat2 增加 fallback 无法解决已证实的 rootfs 卸载失败。

后续对照 Linux v7.0 `kernel/seccomp.c` 找到具体的误关闭风险：
`seccomp_notify_poll()` 的 `mutex_lock_interruptible(&filter->notify_lock)` 若被
信号打断，返回 `EPOLLERR`；这不代表 filter 已无人使用。正常结束通知是
`refcount_read(&filter->users) == 0` 时返回 `EPOLLHUP`。Sysbox 却在
`Revents != POLLIN` 时统一退出并关闭 listener，因此临时 POLLERR 也会触发永久
失效；此时 poll 可成功返回，`err == EINTR` 的重试分支无法处理。内核
`seccomp_notify_release()` 会 detach listener 并将 `filter->notif` 置 NULL，之后
USER_NOTIF 返回 ENOSYS，与现场证据吻合。这是源码可确认的错误处理缺陷，但仍未
捕获两次历史故障发生时的 Revents，不能声称已证明它们均由此路径触发。

当前源码已改为对暂时性 `POLLERR` 重试，并记录导致 listener 关闭的实际
`Revents`；本地 seccomp race 测试通过。历史两次失效时未采到 `Revents`，
仍不能断言该分支就是当时的触发点。

2026-11-17 在 218/server1 复测：`loadopt-218-1016` 的旧 Server 仍有失效
listener，内层新 Pod 的 tmpfs mount 与 cgroup `openat2` 均返回
`Function not implemented`。从 GitHub Actions 成功的 `v0.7.1-41` 下载 deb、
Chart、lite 二进制和元数据，SHA256SUMS 全部校验通过；仅将 deb 中的正式
`sysbox-fs`（SHA256 `ba489e056fb1b76423112df60cb55fd7c815b651f09633a41265bc5c6969457e`）
按快测方式安装到宿主。原二进制备份在
`/var/lib/sysbox-test/fs-listener-03cfcf3-20261117/sysbox-fs.before`。
重建指定 CKM Server Pod 后，上述原生 runc Pod 变为 Running 并正常删除；
再次仅重启 FS，CKM Pod/容器身份不变，新的原生 runc Pod 再次成功创建和删除，
虚拟 `/proc` 与 K3s `/readyz` 正常。宿主 FS/mgr/snapshotter active，恢复计数
attempts=402、successes=330、failures=0、unsupported=27；VMSingle 查到
server1 的 attempts=402。旧 CKM Pod 仍带 `etcdpod.k3k.io/finalizer` 等待
K3k 清理，未强制移除。宿主 Chart 仍为 `0.7.1-37`，本次不是完整 Chart 安装验收，
也未直接诱发或观测到瞬态 `POLLERR`。

### 2026-10-16：Longhorn 限速导致单节点 CKM 的 cluster-reset 超时

在 218 新建 `k3k-console-117057/ckm-slowdisk-test`，其 K3s 数据位于独立的
Longhorn `disk-default` PVC `disk-alh1y`。通过宿主 cgroup v2 `io.max` 仅对该
Pod 的卷设备设置读写 1 MiB/s、100 IOPS 上限；8 MiB 同卷写入实测耗时 8.1 秒。
不限速时重启容器，`cluster-reset` 约 8 秒完成。限速时重启同一容器，etcd
两次 `fdatasync` 分别耗时 7.39 秒、8.13 秒，`cluster-reset` 在约 46 秒后以
`rangeKeys: context cancelled: context deadline exceeded` 退出，启动模板报
`cluster reset failed`，容器重启。由对照确认，慢速同步写入会使当前模板的
启动前 reset 超时，表现为长时间停在 `Starting single node setup`。

已清除 `io.max` 限制；该 CKM 恢复 Ready、Server `2/2 Ready`、内层
`/readyz=ok`。测试 CKM 和 PVC 保留；其他 CKM 未设置限速。

本次使用 installer 的宿主 PID namespace 进入节点，再写测试 Pod 的 cgroup v2
`io.max`。执行前确认 Pod UID、卷设备和 cgroup 路径；以下值只对应本次测试，Pod
重建或卷重新挂载后必须重新查询。挂载信息显示设备为 `66:0`，`stat -c %t:%T`
以十六进制输出 `42:0`。

```sh
kubectl --kubeconfig /root/.kube/218.config -n k3k-console-117057 \
  get pod k3k-ckm-slowdisk-test-server-875b967f6-2945l -o jsonpath='{.metadata.uid}'
kubectl --kubeconfig /root/.kube/218.config -n k3k-console-117057 \
  exec k3k-ckm-slowdisk-test-server-875b967f6-2945l -c k3s-server -- \
  grep ' /var/lib/rancher/k3s ' /proc/self/mountinfo

host() {
  kubectl --kubeconfig /root/.kube/218.config -n default \
    exec w7panel-sysbox-installer-djnrg -c w7panel-sysbox -- \
    nsenter -t 1 -m -u -i -n -p -- "$@"
}
cg=/sys/fs/cgroup/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod866d8b41_c59d_45bf_8aad_e1028ad84a28.slice
dev=/dev/longhorn/pvc-2e9446e0-e092-4cf6-bae7-e350396467db
host find /sys/fs/cgroup -type d -name '*866d8b41_c59d_45bf_8aad_e1028ad84a28*'

host sh -ec 'cg=$1; dev=$2; test -d "$cg"; test "$(stat -c %t:%T "$dev")" = 42:0; printf "66:0 rbps=1048576 wbps=1048576 riops=100 wiops=100\n" > "$cg/io.max"; cat "$cg/io.max"' sh "$cg" "$dev"

# 测试结束后解除限制；io.max 应为空。
host sh -ec 'cg=$1; printf "66:0 rbps=max wbps=max riops=max wiops=max\n" > "$cg/io.max"; test ! -s "$cg/io.max"' sh "$cg"
```

### 2026-10-16：218 拉取 v0.7.1-15-k3s137 镜像超时

首次升级时，节点 containerd 拉取镜像层 `sha256:8b9b96d66975…` 超过 22 分钟，
active ingest 仅有 3.146 MB；Helm 10 分钟超时并自动回滚。将本机已校验的正式镜像
导入节点后，`31 → 15-k3s137 → 31` 完成：`ckm-uejsa` 受控重建到 Pod Ready
分别为 64.0 秒、66.1 秒，内层 `/readyz` 均成功。再次尝试升级到 `15-k3s137`
时，Kubelet 对已由 `crictl inspecti` 确认存在的本地镜像返回
`ErrImageNeverPull`；原因尚未确认，Helm 已自动回滚到 `0.7.1-31` revision 25。
最终节点和全部 8 个 CKM 均为 Ready。

### 2026-10-16：v0.7.1-15 无法安装到 218 的 Kubernetes v1.37

218 单节点运行 K3s `v1.37.1+k3s1`。已校验 GitHub Release 的
`w7panel-sysbox-0.7.1-15.tgz`、`sysbox-runc-lite-v0.7.1-15-amd64` 和
`image-metadata.txt` checksum；Chart 默认镜像的实际 digest 与发布元数据一致。
升级后 installer 立即退出，日志为 `Sysbox is not supported on this Kubernetes
version (v1.37)`。因此该 tag 未完成宿主安装，也不能据此验收新 CKM 启动。
已将 Helm release 回滚到原 `0.7.1-31`；installer、admission、节点及原有 7 个
CKM 均恢复 Ready。要验证此 tag 的 CKM 启动，需使用其支持的 Kubernetes 版本。

### 2026-10-10：CKM Server 移除 `/dev/fuse` 后内层 Sysbox 失效

在 218 集群将 `w7panel-ckm` 测试镜像切换为
`docker.cnb.cool/i0358/ai-cvm:no-fuse-template-20261010-1`。当前 `k3s.v1`
系统模板不含 `/dev/fuse`；新 controller 不再自动注入该设备，7 个 Sysbox CKM 的
Server Deployment 因此移除挂载并重建。在 `k3k-console-117057/ckm-nmmhh` 确认
Server 内 `/dev/fuse` 不存在，而内层 K3s API 和节点仍可启动。内层
`w7panel-sysbox-inner-agent` 的 hostPath 挂载报
`/dev/fuse is not a character device`；`ckm-k3s-nginx` 的 sandbox 因
`/run/sysbox/sysbox-snapshotter.sock: connect: connection refused` 无法创建。
这证明当前内层 Sysbox Chart 与 snapshotter 仍依赖 L1 的 FUSE 设备。

测试后 controller 恢复为 `docker.cnb.cool/i0358/ai-cvm:v1.1.290`，7 个 CKM 的
Server Deployment 均恢复 `/dev/fuse`、达到 `1/1 Ready`；`ckm-nmmhh` 的内层
agent 和 nginx 均恢复 Running，nginx HTTP 返回欢迎页。

随后把 `/dev/fuse` 的 `hostPath`（`CharDevice`）和 mount 移入 `SystemTemplate/k3s.v1`，
controller 不再硬编码注入。测试镜像
`docker.cnb.cool/i0358/ai-cvm:fuse-in-template-20261010-1` 上线后，7 个 CKM
保持 Ready；`ckm-nmmhh` 按新模板 hash 重建，内层 agent、nginx 和 HTTP 均通过。
另建不挂载 `/dev/fuse` 的普通 `sysbox-runc`、`hostUsers:false` 测试 Pod，
容器内执行 `mknod /dev/fuse c 10 229` 返回 `Operation not permitted`，节点不存在，
只读打开也失败。测试 Pod 已删除，因此当前不能用容器内 `mknod` 代替模板挂载。
特权对照 Pod 也未声明设备挂载，但启动时 `/dev/fuse` 已存在且可打开；
对新路径执行 `mknod /dev/fuse-selftest c 10 229` 仍返回
`Operation not permitted`。因此不能把特权 Pod 中已有的节点视为容器自行创建成功。
对照 Pod 已删除。

### 2026-10-09：内层 `SystemdCgroup=true` 无法启动 lite Pod

在 `k3k-console-164315/ckm-cristats-0929` 仅将内层 containerd 的
`sysbox-runc-lite` handler 改为 `SystemdCgroup=true`，重启 K3s 后通过
`crictl info` 确认运行时已加载该值。新建无 PVC 的 nginx 测试 Pod 时，sandbox
持续创建失败：`expected cgroupsPath to be of format "slice:prefix:name" for
systemd cgroups, got "/kubepods/besteffort/pod.../..." instead`。内层 Kubelet
仍生成 cgroupfs 路径，与 runc 的 systemd cgroup 模式不匹配；失败发生在
sandbox 创建阶段，尚未进入 nginx 容器。

已恢复启动脚本、containerd 模板和运行时配置为 `false`，重启 K3s 后
`crictl info` 确认生效。同一 RuntimeClass 和镜像的对照 Pod 达到 `1/1 Running`；
两个临时测试 Pod 与配置备份均已删除。

### 2026-10-08：内层 UID/GID remap 尚未通过实测

`ckm-disk-ubuntu-0930` 使用单层 bootstrap 测试镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-29-inner-remap-20261008-test-flat`
（digest `sha256:b99e3ee0412c24c310ea0fbaa8843db1e865af8d3005e6e0e3bf3be8507f2271`）。
内层 containerd 显示 Sysbox proxy snapshotter `Capabilities: remap-ids`；未启用
`hostUsers:false` 的 lite nginx 已通过 rootfs、空 PVC、特殊 bind 和 CRI stats 回归。

另建 `default/ckm-inner-remap-userns-1008`，指定 `sysbox-runc-lite` 与
`hostUsers:false` 后，Kubelet 在 sandbox 前拒绝：`RuntimeClass handler
"sysbox-runc-lite" does not support user namespaces`。`crictl info` 确认该 handler 的
`features.user_namespaces` 未声明。官方 runc 的现存 `runc-sysbox-probe` handler 声明了
该能力，但对照 Deployment `default/ckm-inner-remap-native-1008` 在创建网络命名空间时
失败：`fork/exec /proc/self/exe: operation not permitted`。因此两条路径均未到达
snapshotter 的 UID/GID 映射挂载阶段；`remap-ids` 配置加载成功不等于映射功能通过。
两个诊断 Deployment/PVC 保留，不能将此结果算作支持内层 `hostUsers:false`。

### 2026-09-30：移除 vendored gocapability 的缺失 proc 容错

lite 原先在读取 `/proc/self/status` 返回 ENOENT 时仍把 capability `Load` 当作成功；
官方 runc v1.1.4 的同版 gocapability 直接返回错误。已移除这段容错，静态测试二进制
SHA256 为 `159d844f87523cb436538df85bcab742a82ffa89dd09ff903836e8301277a315`。
只替换 218 宿主 `/usr/bin/sysbox-runc-lite` 与测试 CKM
`ckm-disk-ubuntu-0930` 的 `/opt/sysbox/bin/generic/sysbox-runc-lite`；原二进制均保存为
`.before-proc-upstream-0930`。L0 的 `local-path` 和 `disk-default`、L2 的 `local-path`
均通过 rootfs 重建持久化、无注解空 PVC 初始化与特殊 bind；L2 CRI stats、双层 TTY
exec、`/proc/self/status` 和字符设备检查也通过。测试 Deployment/PVC 保留。
随后将同一二进制装入单层 bootstrap 镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-28-proc-upstream-20260930-flat`
（digest `sha256:fdbba2452f8f2edb674fdaafc6785c64981f36c65fa902a3495a15d43086da3d`）。
`default/w7panel-ckm` 的环境变量切换后，测试 CKM Server 重建、恢复 Ready，镜像安装的
lite SHA256 仍一致；新建 `ckm-k3s-nginx-proc-image-0930` 再次通过 L2 完整回归。
Chart 默认值已更新，但尚未验证新的 tagged Release。BusyBox `tar` 不支持
`kubectl cp` 使用的 `-m`，初次现场二进制传输改用标准输入经 base64 完成。

### 2026-10-09：全新 CKM 需要 runtime_platforms 映射

新建无 bootstrap 的 `ckm-inner-chart-1009` 暴露出历史实例未覆盖的启动路径：
containerd 首次启动时，若内层模板缺少
`runtime_platforms.sysbox-runc-lite → sysbox`，CRI stats 为 lite 容器返回空
`writableLayer.fsId.mountpoint`，kubelet `/stats/summary` 因此失败。历史 CKM 曾在
bootstrap 阶段带有该映射，后续移除模板并没有覆盖首次启动场景。现已恢复这项映射，
保留 lite handler 的 Pod 注解传递，并继续不使用 container annotations。Chart 升级
`inner-chart-fix5-20261009` 后，新 CKM 的 rootfs 持久化、空 CSI PVC 初始化、特殊
bind、CRI stats 和 `/stats/summary` 完整回归通过。

### 2026-09-30：移除 runtime_platforms 与 container_annotations 的现场回归通过

当前源码已移除内层 K3s 的 `runtime_platforms.sysbox-runc-lite → sysbox` 和 lite
handler 的 `container_annotations`，保留 `snapshotter = "sysbox"` 与
`pod_annotations = ["sysbox/rootfs-rw-layer"]`。在新建的
`ckm-disk-ubuntu-0930`（外层 PVC 使用 `disk-default`）中，仅替换该 CKM 的共享启动脚本
并重启 `k3s-server` 容器后，生成的 `config-v3.toml.tmpl` 和 `config.toml` 均不含这两项。
`05-test-ckm-k3s.sh` 完成 rootfs 持久化、无注解空 PVC 初始化复制、special bind、CRI
writable-layer 非空路径和 kubelet `/stats/summary` 验证；内层 `kubectl exec` 与普通
runc Ubuntu 持久化探针也仍正常。测试资源保留。

这是运行中脚本替换的结果，尚非新 bootstrap 镜像的功能验收。随后构建并推送了单层镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-28-lite-minimal-20260930-flat`
（digest `sha256:81ea917b0412829682b71e6725a2d218184e8ed840a6692e91b51cf8c157d1b4`）；
镜像层数、lite 可执行文件及启动脚本已检查。对测试 CKM 单独修改 Deployment 镜像会被
共享 controller 恢复为旧值，未完成新镜像的 CKM 功能回归；该 CKM 已恢复 Ready。
随后按授权将 `default/w7panel-ckm` 的 `CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE` 切换到该
压平镜像，`ckm-disk-ubuntu-0930` 重建并恢复 Ready。内层新建
`default/ckm-k3s-nginx-v28`，`05-test-ckm-k3s.sh` 报 `FUNCTIONAL PASS`，验证 rootfs
持久化、无注解空 PVC 初始化、特殊目录 bind 和 CRI stats。测试 Deployment/PVC 保留。
下节旧版本的空
mountpoint 故障是历史事实，但不能据此断定该映射在当前版本仍为必要条件。

同日，进一步移除了 lite 的 `/dev`、`/proc`、sysfs 和挂载失败兼容绕过，仅保留上游
标准设备/proc 安全逻辑与 lite 的空 PVC 初始化、特殊目录挂载。静态测试二进制 SHA256
为 `9268ef44e1d5a6823c1bb3a4f878974496bb5d2f3bedf0e8f72c52459678befa`：只替换
新 CKM 的 lite 与 218 宿主 `/usr/bin/sysbox-runc-lite`，旧二进制各留一份备份。
L2 `ckm-k3s-nginx-minimal` 完整回归通过；L0
`sysbox-l0-rootfs-lite-minimal-0930`（local-path）与
`sysbox-l0-rootfs-lite-minimal-disk-0930`（disk-default/Longhorn）均通过空 PVC 复制、
rootfs 和 special bind 重建持久化。L0/L2 TTY exec、字符设备 `/dev/null`、普通
`/proc`/`/sys` 挂载也通过。上述结果仍是本地二进制现场测试，不等于发布镜像验收。
此后源码又移除了一处无关的 `/dev/null` 预检查；该最终源码通过单元测试，但未进行
同版本二进制的 L0/L2 现场回归。

同日审查发现：`w7panel` HEAD 的 admission 会拒绝带 `sysbox/rootfs-rw-layer`
注解却未选择 Sysbox RuntimeClass 的 Pod，但已发布的 v0.7.1-25/26/27 镜像均不含
该拒绝逻辑。旧 webhook 匹配条件也未覆盖此类 Pod。已将 218 和两个测试 CKM 的
匹配条件扩展到 rootfs 注解，并将仅更新 admission 的测试镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-27-admission-strict-20260930`
部署到三处（digest `sha256:5257dc0d33cb219e38c10533c3fd42e29962ad8667500aa2a8a5097a722ffb11`）。
三处 rollout 完成；错误配置在三处均被 server dry-run 拒绝，普通 Pod dry-run 仍通过。
这是临时测试镜像与现场 webhook 修正，尚未进入 Helm chart 或正式 release。

### 2026-09-29：内层 CRI 空 mountpoint 已修复；用量统计仍待完善

内层 K3s 的默认 `runc` 使用 overlayfs，显式 `sysbox-runc-lite` 使用 sysbox snapshotter。
此前 containerd 的 CRI image service 只登记了默认 overlayfs 的文件系统路径，没有为
`sysbox-runc-lite` 配置 `runtime_platforms` 映射。因此 lite 容器的
`writableLayer.fsId.mountpoint` 为空，kubelet 每约 10 秒报 `stat failed on ""`，
节点 `/stats/summary` 返回 InternalError。此问题与 CKM 启动脚本移除 `mountpoint -q`
检查无关；空路径报错在该脚本移除前就已记录。

当时版本的 `sysbox-inner-k3s.sh` 生成了 `runtime_platforms.sysbox-runc-lite → sysbox` 映射。
218 使用 bootstrap 镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-26-cri-stats-20260929`
（digest `sha256:745625402f0efa7ee3a32b6bee6780d653dbd54eb4fa4b07d41becbf410f45fa`）。
新建 `ckm-cristats-0929` 的 Helm/nested lite 回归通过 rootfs 持久化、无注解空 PVC
初始化复制和 special bind；其 CRI statsp 中 lite 容器路径为
`/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox`，
节点 `/stats/summary` 可返回。`ckm-wwvg5` 和 `ckm-4ss8n` 被 controller 自动滚动到
同一镜像后，分别检查 26 个容器记录均无空路径，节点统计接口也成功返回。
测试 Deployment、PVC、Helm release 和旧 Error Pod 均保留现场。

剩余限制：sysbox 容器的 writable-layer `timestamp`、`usedBytes` 仍为 `0`，
而普通 overlayfs 容器正常。这不再阻塞 `/stats/summary`，但精确用量统计需要另行
排查 containerd 对外部 snapshotter 的采集；不能将其误报为已修复。

### 2026-09-28：移除内层完整 handler，Helm 安装与轻量回归通过

bootstrap 脚本已停止生成 `sysbox-runc-nested`、`sysbox-runc.real` 和内层完整
`containerd.runtimes.sysbox-runc`。普通 Pod 使用原生 `runc`/overlayfs，需要 rootfs
能力的 Pod 显式选择 `sysbox-runc-lite`/sysbox snapshotter。增量镜像为
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-24-no-nested-runc-20260928`
（digest `sha256:ca82b8949da8f0d8f593bbcdb9dc90f91b7001ec626b8716bd4e6db70fd656bb`）。

218 的 `k3k-console-117057/ckm-4ss8n` 已由 controller 使用该镜像正常重建；Server 2/2，
snapshotter 自报 `0.7.1-24`，containerd 生效配置与模板均无完整 handler，旧 wrapper 文件
不存在。内层通过真正的 `helm upgrade --install` 创建 `w7panel-sysbox` release
`0.7.1-24`，admission 1/1。`ckm-4ss8n-no-nested-check` 在 Pod 重建后通过 rootfs
持久化、无注解 local-path 空 PVC 初始化复制和 special bind；按用户要求 Deployment、
rootfs PVC、webroot PVC 与 Helm release 均保留现场。首次 admission 启动曾因证书初始化
慢被 liveness 重启一次，随后 Ready 并通过全部功能断言，不属于运行时失败。

### 2026-09-28：同版本 bootstrap 与 CKM 无启动挂载验收（已通过）

218 宿主 Helm chart 与运行时为 `v0.7.1-24`。CKM bootstrap 从同一 Release 的
`sysbox-deploy-k3s-bootstrap:v0.7.1-24` 文件系统在本地重新压平、校验关键二进制
SHA256 后推送为
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-24-local-flat-20260928`
（digest `sha256:def033806802801fd138322fea3fff408babd246084d3eebb7d737751395ddb3`）。
该镜像内 `sysbox-runc`、snapshotter、admission 均自报 `0.7.1-24`；lite 二进制
SHA256 与 Release 静态产物相同。先前 `native-default-20260928` bootstrap 的
snapshotter 自报 `0.7.1-1`，不能用于同版本验收。

复测发现 `w7panel-ckm` 的 README 与默认命令测试虽已声明/检查无挂载命令，实际
`SystemTemplate/k3s.v1` 仍含 `mount --make-*`、`mountpoint`、`mount --bind`，会被
controller 原样带进 CKM Server。直接删去后，cert-manager CSI 报
`/tmp/cert-manager-csi-driver ... not a shared mount`，Agent 的旧
`.../agent/k3s-run/...` hostPath 也不存在。这不是 bootstrap 版本问题，而是旧模板和
挂载传播路径遗漏。现已备份模板，在 `w7panel-ckm` 中删除启动挂载命令，CKM rootfs
注解增加 `/run/k3s`、`/tmp/cert-manager-csi-driver` specialPath（由 runc 以
`rbind,rshared` 挂载），Agent 改为直接使用 `/run/k3s`。

218 的 `k3k-console-164315/ckm-release24-disk-0928` Server PVC 为 `disk-default`
80Gi，外层 `hostUsers:false`、内层 workload 未设置该字段。测试 Pod 启动命令中已无
上述挂载操作；`/run/k3s`、CSI 临时目录、kubelet、K3s 目录的 mountinfo 均显示
`shared`。cert-manager CSI 与 Agent DaemonSet 均 1/1；`05-test-ckm-k3s.sh` 通过
rootfs 持久化、无注解空 PVC 初始化和 special bind。默认 runtime nginx HTTP 200，
默认与 lite `kubectl exec` 均成功；重建 CKM Server 后这些 DaemonSet 和持久标记仍正常。
随后用校验通过的 `w7panel-sysbox-0.7.1-24.tgz` 在该 CKM 内层 K3s 以
`installMode=nested` 重新应用 chart，资源均显示 `unchanged`，admission `1/1`，
`sysbox-runc-lite` RuntimeClass 存在，snapshotter 自报 `0.7.1-24`。独立测试
`ckm-chart24-verify` 的 rootfs 持久化、无注解空 PVC 初始化复制、special bind
均通过；测试 Deployment/PVC 已清理，原 chart 资源保留。
同一 Release 下，L0 `sysbox-runc`（`hostUsers:false`）与 `sysbox-runc-lite` 均通过
`disk-default` 的 rootfs/空 PVC/special bind 回归，临时 Deployment 与 PVC 已清理。
测试 controller 镜像为
`docker.cnb.cool/i0358/ai-cvm:v1.1.271-nomount-v24-20260928`。
旧 CKM `k3k-console-117057/ckm-abi9n` 的 Deployment 仍固定在旧模板，启动命令保留
挂载操作；它未参与上述验收，需走显式模板升级才会迁移。另一既有 CKM
`k3k-console-164315/ckm-3dxvl` 仍因其 10Gi PVC 报 `No space left on device`，
与本次无挂载测试不同。

### 2026-09-28：CKM 内默认使用原生 runc（已通过）

bootstrap 生成的内层 containerd 配置不再为默认 `runc` handler 指定
`sysbox-runc-lite` 二进制；普通 Pod 使用原生 runc 和 overlayfs。admission 也不再因
`sysbox/rootfs-rw-layer` 注解自动选择 RuntimeClass；需要 rootfs 功能时须显式设置
`runtimeClassName: sysbox-runc-lite`。

218 测试镜像：

```text
deploy:    docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-native-default-20260928
bootstrap: docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-native-default-20260928
```

`ckm-mountprop-20260924` 重建后，内层 config 的 `runc` handler 只有
`runtime_type=io.containerd.runc.v2`、`snapshotter=overlayfs` 和 `SystemdCgroup=false`，
不再包含 lite `BinaryName`。未指定 RuntimeClass 的 BusyBox Pod 达到 Ready，
`/dev/null`、`/dev/ptmx` 和双层 `kubectl exec -it` 正常。只有 rootfs 注解而未指定
RuntimeClass 的 server dry-run 保持 RuntimeClass 为空且不注入 sidecar。显式 lite 的
`05-test-ckm-k3s.sh` 返回 `FUNCTIONAL PASS`，rootfs 持久化、CSI 空卷初始化和
special bind 均通过。

复测时 nginx 首次启动仍偶发 `sysbox sidecar oci spec unavailable`，重试后正常；
内层 kubelet 当时还持续记录 `Failed to get the info of the filesystem with mountpoint`
（空 mountpoint）统计错误；该项于 2026-09-29 修复，见上节。

### 2026-09-24：由 runc special mount 提供递归传播（已通过）

`sysbox-runc` 现在为 `persistentSpecialMounts: true` 的 rootfs `specialPath` 生成
`rbind,rshared`，不再使用 `rprivate`。当时只检查了 CKM 默认生成命令与 wrapper，
遗漏已保存的 `SystemTemplate/k3s.v1`；其旧挂载命令于 2026-09-28 的验收中被发现并移除，
见上节。

`libcontainer/specconv` 对该 OCI 选项的解析由单测锁定为
`MS_SHARED|MS_REC`；`persistent_special_mounts_test.go` 同时锁定 special mount 的
`rbind,rshared` 选项。L2 非特权 user namespace 的 mountinfo 不一定显示 shared peer group，
因此本轮以 runc 实际参数、解析标志和业务 special-bind 回归为验收依据，不将该 mountinfo
显示差异误报为 CKM 启动脚本回退。

变更前的 CKM 分支已推送回滚 tag
`backup-before-ckm-mount-removal-20260924`。218 使用以下测试镜像：

```text
deploy:    docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-mountprop-20260924
bootstrap: docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-mountprop-20260924
CKM:       docker.cnb.cool/i0358/ai-cvm:v1.1.269-mountprop-20260924
```

新建 `k3k-console-164315/ckm-mountprop-20260924` 并正常重建一次 L1 Server 后，L0
full/lite rootfs 回归和 L2 `05-test-ckm-k3s.sh` 均返回 `FUNCTIONAL PASS`。内层
cert-manager CSI、nested agent 均为 Running；未设置 RuntimeClass 的 L2 busybox Pod
双层 `kubectl exec -it` 成功，`/dev/null` 与 `/dev/ptmx` 均为字符设备。

### 2026-09-24：移除 retired proc/special 注解（已通过）

`sysbox/skip-special-mounts` 和 `sysbox/allow-proc-exec` 是已回退的 command-mode
兼容路径，当前轻量 nested runtime 不消费它们。CKM 不再向 L1 Server 注入
`allow-proc-exec`；`sysbox-inner-k3s.sh` 的 default、lite、full handler 仅保留
`sysbox/rootfs-rw-layer` annotation allow-list，nested wrapper 也不再设置
`SYSBOX_ALLOW_PROC_EXEC`。

218 使用以下制品重新创建同一 CKM Server：

```text
deploy:    docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-no-legacy-annotations-20260924
bootstrap: docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-no-legacy-annotations-20260924
CKM:       docker.cnb.cool/i0358/ai-cvm:v1.1.270-no-legacy-annotations-20260924
```

L1 template、active inner containerd config、nested wrapper 与 L2 nginx annotations
均不含 retired key。L2 `/proc` 仍为 `noexec`，但 CNI、rootfs persistence、无注解
CSI 初始化、special bind、`kubectl exec -it` 全部通过。

### 2026-09-24：缓存构建与干净 L0/L2 回归（已通过）

`BUILD_PROFILE=test` 现可将 `runc-lite`、snapshotter、admission 和 inner bootstrap
script 增量写入指定的已验证 deploy/bootstrap 基线；Go 与 buildx 缓存位于仓库 `.cache`
下。该模式不会隐式重启 CKM，操作者必须显式切换输出的 bootstrap tag 并走正常 Server
rollout。Chart、打包或最终制品仍需 `BUILD_PROFILE=release`。

一次完整 release 构建及随后的干净回归使用：

```text
deploy:    docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-buildcache-rootfs-webhook-20260924
bootstrap: docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-buildcache-rootfs-webhook-20260924
```

在 218 的新 CKM `k3k-console-164315/ckm-build2-20260924` 中，首次安装 nested handler
后正常替换一次 Server，`05-test-ckm-k3s.sh` 输出：

```text
FUNCTIONAL PASS: rootfs persistence, annotation-free CSI empty-volume init and special bind mount verified
```

L0 full/lite RuntimeClass、full handler 的 `hostUsers:false` nginx:80、local-path 空 PVC
初始化、rootfs marker 与 `/srv/data` marker 跨 Pod 重建也通过。L2 nginx 未设置
`hostUsers:false`。这属于工作区完整构建验收；`v0.7.1-20` 的 GitHub Release 仍须下载
chart、lite binary 与 `SHA256SUMS` 后独立验收。

### 2026-09-10：L0 双 RuntimeClass 临时 tag 回归通过

宿主安装器现同时安装 `sysbox-runc` 与 `sysbox-runc-lite`，并为两个 handler 写入相同的
snapshotter、`sysbox/rootfs-rw-layer` annotation 与 systemd-cgroup 配置。host chart 创建
两个 RuntimeClass；nested chart 保持只创建 lite，避免 L1 K3s 引用未安装的完整 runtime。
在 218 上以临时镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-13-dual-runtime-test`
升级 host chart 后，`sysbox-runc` 和 `sysbox-runc-lite` smoke Pod 均达到 `Ready`，且
未自动注入 `/dev/fuse`。随后在 `ckm-test` 的 L1 K3s 运行
`05-test-ckm-k3s.sh`，通过 rootfs 持久化、无 `sysbox/volume-init` 注解的空 CSI PVC
初始化复制及 `/srv/data` special bind mount 回归。

已观察到 lite L0 smoke Pod 在删除后可能短暂/持续停于 `Terminating`，因此 smoke 脚本采用
非阻塞删除；这不影响本次“可创建、可 Ready”的验收，需作为 runtime teardown 的后续观察项。

### 2026-09-10：完整 `sysbox-runc` 的 PVC 初始化与低端口边界

完整 handler 原有的 `initializePVCVolumes()` 仅识别
`kubernetes.io~csi/<volume>/mount`，未识别 K3s `local-path` 的实际 kubelet 源
`kubernetes.io~local-volume/<pvc>`。现已在同一受限、当前 Pod 所有的路径校验下补齐
`local-volume`，不放宽至 `emptyDir`、`hostPath`、projected 或未知插件。单测覆盖两种
路径；218 使用临时镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-14-runc-pvc-test` 实测完整
`sysbox-runc` 的 local-path 和 Longhorn (`driver.longhorn.io`)：空 webroot PVC 获得
nginx 镜像默认文件，Pod 重建后 webroot marker 及 `/srv/data` special bind marker 均保留。

首次不设置 `hostUsers` 的 L0 完整 handler 中 nginx 绑定 `0.0.0.0:80` 返回 `EPERM`：
Kubelet 已在初始 userns 建立 CNI netns，随后 sysbox-runc 的子 userns capability 不能
授权该 netns 的低端口。现场随后以 `hostUsers:false` 重新创建 Pod，Kubernetes 在 CNI 前
创建 Pod userns，nginx:80 成功 Ready 且 Service 可访问。故 L0 完整 handler 的 rootfs
回归默认写入 `hostUsers:false`；这不改变本轮“L2 runc-lite 不设置 hostUsers”的约束。

### 2026-09-09：外层 `/dev/fuse` 注入与 Service 环境变量冲突（已修复）

外层普通 `runtimeClassName: sysbox-runc` workload 不需要 `/dev/fuse`。此前
admission 对这类 Pod 自动追加 `sysbox-fuse` hostPath/mount；生成的 JSON Patch 在现场
留下了没有对应 volume 的 mount 引用，API 因而拒绝创建 Pod（`volumeMounts[].name: Not
found: "sysbox-fuse"`）。该注入已从 admission 移除。L1 CKM bootstrap 仍自行提供
snapshotter 所需的 FUSE 设备，两条路径不可混用。

修复后又发现名为 `sysbox-runtime-relay` 的 Kubernetes Service 会自动向 Pod 注入
`SYSBOX_RUNTIME_RELAY_SERVICE_HOST` 等变量。`sysbox-runc` 过去将未知 `SYSBOX_*`
变量一律视为非法运行时配置，导致 OCI create 失败。现仅解析明确支持的 Sysbox 配置键，
其余同前缀变量原样交给 workload。

在 218（`/root/.kube/218.config`）以
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-service-env-20260909`
重新部署后，临时 `sysbox-runc` Pod 已 `Running`；其规格仅含 Kubernetes
service-account volume，未含 `sysbox-fuse` volume 或 `/dev/fuse` mount。

### 2026-09-09：GitHub Release 静态 runtime 构建前置条件（已修复）

首次 tag release 的 `sysbox-runc-lite` 静态构建在 GitHub Actions runner 上失败：Go 默认
VCS stamping 会读取异常的 `.gitconfig`，随后 seccomp 构建又缺少 `libseccomp.pc`。
静态构建现显式使用 `-buildvcs=false`，release workflow 也在构建前安装
`libseccomp-dev`。`v0.7.1-11` 的 GitHub Actions 已成功完成。

已下载并校验该 GitHub Release 的 `w7panel-sysbox-0.7.1-11.tgz`，以其而非工作区
chart 安装到 218 外层，以及 `ckm-test` 的 L1 K3s。外层 `sysbox-runc` Pod 启动成功且
无 FUSE 注入；L1 admission 使用
`ghcr.io/w7panel/sysbox-deploy-k3s:v0.7.1-11`，`sysbox-runc-lite` nginx 回归通过
rootfs 持久化、无注解 CSI 空目录初始化和 special bind mount。

### 2026-09-03 最新现场复测补充

当前现场 `ckm-test` 的 nginx `runc-lite` 容器直接读取到：

```text
uid_map: 0 3004104704 65536
gid_map: 0 3004104704 65536
```

这次 L2 workload 未设置 `hostUsers:false`，因此该结果是普通 nested userns 的
实际映射，不应与旧文档中 `0 0 65536` 的专用 inner-runtime 历史结果混用。当前
验收仍只覆盖 nginx rootfs/snapshotter 路径；L2 `hostUsers:false` 测试继续暂缓。

尝试将仓库中的 `k3s.v1.yaml` 直接 apply 到 218 外层集群时，被当前 CRD 严格解码
拒绝：`unknown field "spec.active"`。这属于 live CRD schema 与仓库模板版本不一致，
未改变现有 CKM；当前 CKM 仍为 `Ready`。新模板应通过匹配版本的 controller/chart
升级流程发布，而不是直接套用不兼容的 YAML。

该问题已修复：`w7panel-ckm` 提交 `3b03f68` 从 `v1alpha2/k3s.v1.yaml` 删除
不受支持的 `spec.active` 字段；删除后模板可通过 `kubectl apply --dry-run=client`
并已成功应用到 218 外层集群，现有 CKM 未发生重启。

## 当前轻量 `runc-lite` 回归（2026-09-04 已通过，持续观察）

> 本节是当前权威状态，优先于下方 2026-08-24 的旧 `sysbox-runc`/inner
> `sysbox-fs` 验收基线。后者保留为历史证据，**不能**用来宣称下面的轻量路径已经
> 通过 Docker、systemd、`/proc` 隔离或视图隔离。

本轮目标是仅保留官方 runc/libcontainer 的小幅改动、`sysbox-snapshotter` 和
admission/CKM 注入；内层不再启动 `sysbox-fs` 或 `sysbox-mgr`。验收 workload
缩小为 nginx，不测试 Docker 或 systemd。外层 CKM 仍是 Sysbox workload，因而
`hostUsers: false` 仍由 CKM controller 设置；本轮 L2 nginx manifest 没有设置
`hostUsers`。

### 为什么“之前通过”后又出现问题（2026-09-04 文档审查）

之前的 `FUNCTIONAL PASS` 不是同一运行基线上的稳定性结论，主要有以下差异：

1. 通过记录来自旧 CKM/旧 deployment 镜像的一次或多次最终重试成功；随后测试切换到
   新的 CentOS 9 扁平 bootstrap tag，并重新创建 CKM Server。镜像层、工具路径和
   initContainer 时序变化会影响 `sysbox-inner-k3s.sh`、snapshotter handoff 及 FUSE
   rootfs 的首次创建。
2. `runc-lite` 后续合并了 snapshotter 配置、exec fd/ptmx 兼容处理和 w7panel 子模块
   更新。direct workload 仍可启动，但 PVC rootfs 会依次暴露 pivot bind、child
   procfd、handoff/socket 等更靠后的问题；这不是“已通过功能被随机破坏”，而是测试
   路径和运行时输入已改变。
3. 旧文档把“删除后重试成功”记为通过，同时记录了首次
   `sysbox sidecar oci spec unavailable` 竞态；当前约定忽略该一次性 sidecar 错误，
   但不能忽略 nginx 未进入 `Running`、snapshotter socket 超时或 handoff JSON 缺失。

因此本轮必须用同一 CKM、同一镜像 tag、同一 chart 渲染结果执行一次干净回归；旧
`HISTORY.md` 的成功输出只保留为历史证据，不覆盖本节的当前阻塞状态。

当前对象：外层 218 集群（`/root/.kube/218.config`），CKM 为
`k3k-console-164315/ckm-6ur35`，L1 server Pod 为该 CKM 当前的
`k3k-ckm-6ur35-server-*`。部署时使用的镜像和 controller 为：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-51-centos9-runc-lite-fuse
sha256:9a4764e60c80282fa0804ee7545b3852030c612a018619d675ffd7df237f2ff8

docker.cnb.cool/i0358/zpk/w7panel-ckm:v0.7.1-runc-lite-fuse
sha256:837be1e5ee638ea111f5530c204b82e680ac821b82d48e5e5993d95a2032f917
```

CKM controller 现会为 `sysbox-runc` L1 Pod 显式注入 `/dev/fuse` 的
`hostPath`（`CharDevice`）及对应 mount；L1 中已实际确认它为字符设备 `10,229`。
这不能靠 outer admission 假设完成：controller 创建的 L1 Pod 在现场没有经过预期的
admission mutation，故注入必须在 controller template 内完成。

### 已通过的窄范围证据

- `runc-lite` 使用静态二进制构建；修改后的官方 libcontainer 单测已通过：

  ```sh
  cd /root/workspace/sysbox/sysbox-runc-lite
  GOCACHE=/tmp/runc-lite-final-cache go test . ./libcontainer/specconv
  ```

- 内层 K3s 中一个不使用 rootfs PVC/snapshotter 的 direct workload
  `runc-lite-static-test` 曾到达 `Running`，日志为 `runc-lite-fuse-ok`，其
  `runtimeClassName` 为 `runc-lite`，`hostUsers` 字段为空。这证明当前静态
  runc-lite handler 可启动普通 nginx 类容器，**不证明** FUSE rootfs、初始化复制或
  special bind mount。

- nginx 测试脚本为 `05-test-ckm-k3s.sh`。它创建 `ckm-k3s-nginx` 和 rootfs PVC，
  设置 `sysbox/rootfs-rw-layer`（`persistentSpecialMounts: true`、
  `specialPath: ["/srv/data"]`），随后在 Pod 重建前后检查 marker 与 `/srv/data`：

  ```sh
  cd /root/workspace/sysbox
  bash w7panel-doc/sysbox-in-sysbox/05-test-ckm-k3s.sh
  ```

  该脚本曾确认 `/usr/share/nginx/html/.runc-lite-marker` 跨 Pod 重建保持为
  `persisted`。在目前失败轮次中，Pod 未能完成启动，故这项旧 marker 观察不能作为
  当前完整回归的成功结论。

### 当前 FUSE-rootfs 阻塞链（按实际出现顺序）

以下错误均发生在 `runc-lite` + sysbox snapshotter 的 nginx PVC 路径；每次修正前
一步后才暴露下一步，不能把后一个错误误写成 direct-runc 基础能力失败。

1. **root pivot bind mount 被拒绝。** FUSE rootfs 首次启动报
   `mount ., flags 0x84000: permission denied (EPERM)`。这是 runc pivot/rootfs
   bind 的权限兼容问题，不是 nginx image、PVC 或 userns 配置错误。已在
   runc-lite 的 pivot/bind 路径加入兼容处理；该精确错误不再是当前最前置失败。
2. **proc fd 在 child 侧不可用。** 上一步后报
   `error closing exec fds: open /proc/self/fd: no such file or directory`。
   该问题对应 runc init 子进程建立 rootfs 后再经 `/proc/self/fd` 关闭 exec fd 的
   时机；已继续调整相关路径，但它说明 FUSE rootfs 上不能假定 child 的 procfd
   入口已经可用。
3. **此前前置失败（已解决）：父进程预绑定 procfd 为 ENOENT。** 为避开 child procfd，改为在
   父进程预打开/预绑定 fd 后，当前错误变为预绑定目标/源不存在（`ENOENT`）。因此
   nginx 尚未 `Running`，不得声明 rootfs 持久化、空目录初始化复制或 `/srv/data`
   special bind mount 已全部验收。

`persistentSpecialMounts` 也尚未取得 snapshotter handoff 的独立证据。runc-lite
读取 `/run/sysbox/rootfs-pvc-handoff/<sha256(container-id)>.json`；失败轮次中该目录
为空。已为 `runc-lite` handler 尝试配置 `snapshotter = "sysbox"`、proxy plugin 和
`pod_annotations`，并重启内层 containerd 让配置生效；后续回归必须同时确认该 JSON
出现，并在容器内检查 `/srv/data` 是实际 bind mount，而不是只检查目录存在。

### 传播/模板风险

当前 `SystemTemplate` 的旧 mount 设计将 `/` 和 `/run` 标为 `shared`。FUSE rootfs
路径会继承这个传播关系，导致 mount 操作向不应参与的父级传播。现场已尝试把相关
路径改为 `rslave`，但尚未完成 nginx 成功回归，不能声称它已解决。后续排查应在
修改 template 后重建 L1，分别记录 L1 与 nginx mountinfo，避免将临时容器内
`mount --make-rslave` 结果当作持久配置。

### 2026-09-04 修复后回归结果

现场修正了轻量模式的启动语义：`SYSBOX_INNER_START_DAEMONS=false` 现在只跳过
inner `sysbox-mgr`/`sysbox-fs`，仍会启动 `sysbox-snapshotter`；CKM Server readiness
只检查 K3s API 和 snapshotter socket。bootstrap 使用：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-ptmx-24119f8-flat9
docker.cnb.cool/i0358/ai-cvm:runc-lite-inner-snapshotter-20260904
```

清理卡住的旧 `PreinstallExecution` 并重建 `ckm-test` Server 后，取得以下证据：

- CKM `ckm-test` 为 `Ready`，L1 Server `2/2 Running`，保持 `runtimeClass=sysbox-runc`
  和 `hostUsers=false`；
- `/run/sysbox/sysbox-snapshotter.sock` 存在且可用，inner mgr/fs socket 不存在（符合
  轻量范围）；
- `05-test-ckm-k3s.sh` 返回
  `FUNCTIONAL PASS: rootfs persistence, empty-volume init and special bind mount verified`；
- handoff JSON 已出现在 `/run/sysbox/rootfs-pvc-handoff/`，内层 admission Deployment
  为 `1/1`，nginx Deployment 为 `1/1`。

随后在不改变 CKM、镜像 tag 或 chart 配置的情况下再次删除/重建 nginx Deployment，第二轮
仍返回同一 `FUNCTIONAL PASS`；本次没有再出现 snapshotter socket timeout。

因此本轮四项目标（rootfs 持久化、空目录初始化复制、special bind、snapshotter/webhook
复用）已在同一 CKM/tag 组合下重新验证。L2 workload 不设置 `hostUsers:false`，该项仍
不实现、不测试。`sysbox sidecar oci spec unavailable` 仍按约定忽略；后续只需继续做
多轮重建和长时间稳定性观察。

### 本轮验收门槛

在一次干净的 `05-test-ckm-k3s.sh` 运行中，需要同时取得：nginx `Running`、空 PVC
从 image rootfs 初始化后的预期内容、marker 在 Pod 重建后仍为 `persisted`、snapshotter
handoff JSON 存在，以及 `/srv/data` 的 bind-mount 证据。仅 direct workload 成功或
仅 PVC marker 存在均不足以通过。

## 当前验收基线（2026-08-24）

当前权威测试对象为 218 的
`k3k-console-164315/ckm-sysbox-manual`，CKM Server Pod 为
`k3k-ckm-sysbox-manual-server-647fddbd67-gmfpp`。本轮使用：

```text
image: docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-47-nested-tty-exec
digest: sha256:e10b0f5905fc1d0dbf913079fc396cea4a5984b69810ed1ce04d029555c946a2
sysbox-runc commit: 5208ebbfae1fb487f765df7f0ffec2f0cdbe2ffb
chart: w7panel-sysbox 0.7.1-15, installMode=nested
```

已经实跑通过：

- `05-test-ckm-k3s.sh`：独立 child userns、`uid_map=0 0 65536`、CNI、HTTP、nginx
  rootfs PVC marker/inode/属主在 Pod 重建后保持。
- 历史 `09-test-docker-rootfs.sh`（已从当前流程移除）：systemd/dockerd、腾讯云 nginx pull/run、Dockerfile build、
  `overlay2` 均通过；`/var/lib/docker` 为 PVC 上 `ext4 idmapped` special mount，Pod 重建后
  marker、inode、构建镜像 ID 和镜像层保持。
- `10-test-cgroup-delegation.sh`：L2 实际路径以 `sysbox.delegate/init.scope` 结尾，L1
  父边界为 `memory.max=2147483648`、`cpu.max=100000 100000`，Docker 子 cgroup 可创建，
  父边界前后不变。
- `11-test-nested-agent-lifecycle.sh`：nested-agent Pod 删除重建后 launcher=1、
  snapshotter=1、socket=listening；CKM K3s identity 保持 `376:2996`，新 nginx Sysbox Pod
  随后成功创建并可访问。
- `12-test-interactive-exec.sh`：nginx `/bin/sh` 与 systemd/Docker `/bin/bash` 均获得真实
  `/dev/pts/*`，可以接收命令并正常退出，退出后无残留 `sysbox-runc exec` 进程。

当前未解决但不阻塞本轮功能验收：

1. `/proc noexec` 强隔离已明确放弃；inner CNI 需要执行 `/proc/self/exe`。
2. CPU/内存的 Pod 内系统视图隔离已明确放弃；L2 仍看到宿主 CPU/内存视图，真实父
   cgroup 限制必须从 L1 验证。
3. 尚未完成长时间并发 Pod/agent 重启压力和 L0 宿主 Sysbox 服务重启后的全量恢复测试。
4. 历史大 rootfs 的 rsync single-flight、取消和 orphan 回收仍需单独压力验证。
5. 因前两项隔离能力缺失，该模式不能作为多租户或不可信负载的安全边界。

下方带更早日期的章节保留问题演进记录；其中“尚未验证”“只能使用 vfs”或“取消 child
userns”等结论只描述当时旧镜像/旧 PoC，不代表 v47 当前状态。

## 固定能力边界（2026-08-21 决定）

218 的 `k3k-console-164315/ckm-sysbox-manual` 实测无法提供 `/proc` 强隔离和 Pod 内
Sysbox 系统视图隔离。决定不是放弃 Sysbox-in-Sysbox，而是接受这两个能力缺口：继续构建、
部署和验证 nested runtime 的 K3s、CNI、Docker、rootfs、cgroup 实际边界与生命周期功能，
不再把 `/proc noexec` 和资源视图虚拟化作为目标。该方案不能用于多租户或不可信负载隔离。

测试对象为 L2 Deployment `default/ckm-k3s-nginx`，资源限制为 1 CPU/2GiB，
`runtimeClassName=sysbox-runc`。实测证据：

```text
/proc: rw,nosuid,nodev,relatime             # 缺少 noexec
L1 pid namespace: pid:[4026548708]
L2 pid namespace: pid:[4026541853]          # PID 隔离正常
L2 /proc/cpuinfo: 72 processors             # 期望 1
L2 /proc/meminfo: MemTotal 63457684 kB      # 期望不超过 2GiB
L2 /sys/fs/cgroup/cpu.max: max 100000
L2 /sys/fs/cgroup/memory.max: max
L1 所见 L2 祖先 cpu.max: 100000 100000
L1 所见 L2 祖先 memory.max: 2147483648
```

`/proc` 缺少 `noexec` 是当前 CNI 可用的必要降级。K3s/containerd 的 netns helper
需要执行 `/proc/self/exe`；恢复 `noexec` 会使 inner CNI 创建 network namespace
失败。`sysbox/allow-proc-exec: "true"` 或 runtime 无条件删除 `noexec` 都不满足强隔离，
不能作为解决方案。

视图失败也不是资源限额未生效。L2 的父 cgroup 已正确限制到 1 CPU/2GiB，实际 throttling
和 OOM 边界此前均已验证；但 nested-identity 代码明确跳过每个 L2 的 sysbox-fs 子挂载，
导致 `/proc/cpuinfo`、`/proc/meminfo` 和 `/sys/fs/cgroup` 暴露错误视图。即使单独补齐
FUSE 视图，也无法解决 `/proc noexec` 与 inner CNI 的冲突。

### 重复 seccomp user-notify listener 的直接根因

这里的“重复 notify”不是普通 seccomp filter 重复。普通 seccomp filter 可以逐层叠加；
Sysbox 使用的 seccomp user-notification 则需要一个 listener FD 和对应的用户态处理者。
L1 进程树会继承外层 L0 的 listener，L2 若再为同类 `mount`、`umount`、`openat2`
syscall 创建一套由 L1 `sysbox-fs` 处理的 listener，当前内核场景会返回：

```text
device or resource busy (EBUSY)
```

即使强行复用外层 listener，L0/L1 helper 也未必拥有 L2 mount/user namespace 中的权限，
可能把 L2 的请求处理成 `EPERM` 或 `EINVAL`。因此 nested-identity 当前跳过 L2 的完整
Sysbox-FS syscall trapping，优先保证 K3s、CNI、nginx 和 Docker 可以启动。

这个降级直接解释了 `free -h` 的结果：没有 L2 专属 Sysbox-FS `/proc` 视图时，
`free` 读取的 `/proc/meminfo` 仍是内核的宿主级内存统计，而不是 L1 的 2GiB cgroup
上限。实际 CPU/内存限制仍由 L1 父 cgroup 和 L2 delegated 子树执行，必须从 L1 cgroup
路径或压力测试验证，不能用 L2 内 `free -h` 判断限额是否生效。

仍不接受共享 L1 user namespace、跳过 `/proc`、使用 host network，或用 Pod Running
代替功能验证。当前明确接受的是 L2 `/proc` 不含 `noexec` 和资源视图不虚拟化；真实 cgroup
CPU/内存边界仍必须生效。方案状态为 **功能继续支持，强隔离与视图隔离不支持**。

## 当前问题清单（更新至 2026-08-24）

本节区分“仍未闭环”和“曾经出现但已经解决”。两项隔离能力已放弃，其余功能与稳定性
问题继续跟踪。

### 当前仍未解决或未闭环

1. **长时间及并发重启稳定性尚未完成。**

   v47 已通过 nested-agent 删除重建、服务单实例检查和随后 workload 重建，但还未覆盖
   多 CKM 并发、连续重启风暴或 24 小时以上运行。

2. **L0 宿主 Sysbox 服务重启后的完整恢复尚未在 v47 重跑。**

   nested-agent 自身重建不会重启 CKM K3s且已通过；L0 mgr/fs/snapshotter 重启属于更大的
   故障范围，仍需单独保存事件与 seccomp 注册日志后验收。

3. **L2 内再次部署完整 CKM 业务工作负载尚未验收。**

   当前默认拓扑已经验证 CKM K3s 创建普通 nginx 与 systemd/Docker Sysbox workload；
   “在该 workload 内再部署完整 CKM controller + K3s”属于额外三层业务场景，不是本轮默认流程。

4. **mgr 大目录 rsync 生命周期控制尚未完成。**

   `ckm-old4` CrashLoop 曾造成单次约 16.7GiB rsync 和并发写入。当前通过清理资源规避，但 rsync single-flight、取消、临时目录和 orphan 进程回收仍未完整实现或压力验证。

5. **CKM pre-install RBAC 仍有权限缺口。**

   `w7panel-ckmv3` ServiceAccount 曾缺少 `pods/exec` 权限。本轮只 skip 了已知历史 execution，正式部署仍需补齐最小 RBAC 并验证脚本范围。

6. **L2 cgroup 资源显示与实际限制存在视图差异。**

   实际边界已通过压力测试：512MiB 内存 Pod 被 `OOMKilled`，500m CPU 压测出现 throttling；但 L2 内部仍显示 `cpu.max=max`、`memory.max=max`。如果要求 L2 视图准确反映父级限制，还需继续改进 Sysbox cgroup 视图。

   2026-08-21 决定放弃该视图隔离能力，不再继续改进显示；后续只验证真实 CPU、内存
   和子 cgroup 边界仍然生效。

### 曾经遇到、目前已解决或已验证通过

- `innerSysbox.enabled=false` 导致 CKM 不创建 L1 nested runtime。
- L1/L2/L3 UID/GID mapping 不正确；当前统一验证为 `0 0 65536`，并使用独立 child userns。
- `sysbox-runc-inner`/`sysbox-runc-nested` wrapper 兼容问题。
- `nsenter -p` 后错误使用外层 PID 检查 daemon，已改为 socket/RPC 健康检查。
- stale PID、`/proc/*/exe` 的 `(deleted)` 后缀导致 daemon 被误判。
- L1/L2 mount namespace 看不到 containerd、mgr、fs、snapshotter socket。
- `/run/k3s`、`/run/sysbox` 未 shared 导致 nested-agent 报 `path is not a shared mount`。
- 极简 K3s 镜像缺少 `modprobe`、`iptables`、`mount.fuse3` 和 `rsync`。
- L2 CNI、IPAM、iptables、veth 回收和腾讯云 nginx HTTP 已通过。
- systemd、dockerd、Docker bridge、端口映射、overlay2 和 systemd cgroup driver 已通过。
- `rootfs-rw-layer` marker 在 L2 Pod 及 L1 Pod 重建后保持。
- overlay2 问题最终定位为 special mount/backing filesystem 处理，不是简单的 `make shared` 缺失。
- 镜像解包 `no space left on device`、`DiskPressure` 和磁盘占满问题已通过清理旧 BuildImage 资源、清理磁盘和扩容处理。
- snapshot/content-store 缺失 digest 导致 sandbox 创建失败的问题已通过恢复/重新拉取内容处理。
- L1 K3s identity 在 chart 安装和 nested-agent rollout 前后保持不变。
- cgroup 二次 delegation 的 CPU throttling、OOM 边界和子 cgroup 可写性已通过压力测试。
- v47 的 `/var/lib/docker` 已确认使用 PVC-backed `ext4 idmapped` special mount，dockerd
  使用 `overlay2`；Docker Pod 重建后构建镜像缓存保持。
- CKM nginx Deployment 的 rootfs marker、inode、属主与 HTTP 已在 218 Pod 重建后通过。
- nested-agent 生命周期清理已通过主动删除重建测试：单 launcher、单 snapshotter、socket
  监听，且 CKM K3s identity 不变。

### 当前判断

嵌套 userns、L2 CNI、Docker/overlay2、rootfs-rw-layer、实际 cgroup 资源边界和 nested-agent
单次重建生命周期均已有 v47 实测通过证据。当前功能流程已跑通；剩余工作是长期/并发压力、
L0 宿主服务故障恢复和额外完整 CKM 三层业务场景，不能把这些未覆盖范围外推为已验证。

该判断只覆盖功能和稳定性，不包含已经放弃的 `/proc` 强隔离与 Sysbox 系统视图隔离；
后续回归继续验证实际 cgroup 边界、K3s、CNI、Docker、rootfs 和生命周期。

## 历史验证快照：2026-08-21 `ckm-bzhrq`

CKM 新 Pod：

```text
k3k-ckm-bzhrq-server-7788d8fbf6-lj89t
```

本轮结果：

- `nested-chart-smoke.sh` 通过；L1 K3s identity `346:2407` 在 chart apply、nested-agent rollout 和 L2 nginx 测试前后保持不变。
- L2 腾讯云 nginx 镜像拉取成功，digest 为 `sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`；`uid_map=0:0:65536`，HTTP 和 CNI/IPAM 清理通过。
- L2 `nested-l2-k3s-final` 恢复为 `2/2 Running`，rootfs marker 保持：inode `576074`、owner `0:0`、内容 `bzhrq-rootfs-verified`。
- L3 腾讯云 nginx 连续两次通过：示例地址 `10.245.0.7`、`10.245.0.15`；L3 userns 独立、UID map 为 `0 0 65536`、HTTP 和 CNI 清理均通过。
- L2 `RuntimeClass/sysbox-runc.handler=sysbox-runc`，节点最终带有 `sysbox.w7panel.io/nested-runtime=ready`。

本轮遇到但已恢复的问题：

1. L2 admission webhook 曾缓存旧 endpoint `10.245.0.4:9443`，而当前 Service endpoint 已变为 `10.52.0.10:9443`，创建 L3 时出现 API server 502。删除并等待重建 `w7panel-sysbox-admission` 后 endpoint 更新为 `10.52.0.19:9443`，L3 创建恢复。
2. nested-agent 曾因 `CKM-prepared Sysbox daemon health check failed` 重启并短暂移除节点 ready label。删除单个 nested-agent Pod 后重新 rollout，Pod `w7panel-sysbox-nested-agent-7b9td` Ready，L3 再次通过。

因此本轮已证明：在 CKM 新 Pod 已能稳定运行的窗口内，L1 → L2 → L3 主链路、rootfs-rw-layer 和 CNI 均可通过；admission endpoint 刷新和 nested-agent daemon health check 的偶发抖动仍需作为稳定性问题继续观察。

> 2026-08-11 更新：专用 inner runtime 已实现显式 `nested-identity`、child userns `0:0:65536`、NoShift 和二次 cgroup v2 delegation。但真实 L1 Docker 验证发现 seccomp user-notify listener 不能在继承 L0 listener 的进程树中再次创建（`EBUSY`），所以该方案尚不能启动完整 L2 systemd/Docker。旧的“取消第三层 userns”路径仅保留为历史 PoC；下文涉及共享 outer userns 的结论只描述旧 PoC。

> 上游 Sysbox 不支持 Sysbox nesting。本文分析的方案仅用于 218 实验性
> PoC，不应作为生产能力或多租户隔离方案启用。

## 早期 PoC 结论（2026-08-11，已被后续实现替代）

Sysbox Pod 中再次运行 Sysbox，只能获得受限的嵌套容器能力，无法获得完整的
“系统容器套系统容器”语义。主要障碍不是单个程序缺陷，而是 Linux user
namespace、procfs/sysfs mount、cgroup、netfilter 和 seccomp 的权限都受
namespace 层级约束。

## 2026-08-11 nested-identity 实机结果

在本机以 Sysbox 启动 L1 systemd/Docker 容器，再运行本地构建的 L1
`sysbox-mgr`、`sysbox-fs` 与 `sysbox-runc`，已确认：

- L1 映射为 `0 165536 65536`，L2 allocator 返回相对 L1 的
  `0 0 65536`，且 `/etc/subuid`、`/etc/subgid` 哈希不变；
- L1 获得完整 cgroup v2 controller delegation；
- inner mgr/fs 可以在 L1 启动，`/dev/fuse` 和每容器 FUSE server 可用；
- cgroup-device BPF 不能由非 initial userns 加载，L2 必须继承 L1 device
  policy；CPU、内存、IO、PID controller 仍可按 L2 子树委托；
- L2 创建到 rootfs 阶段后，`procfs`、`sysfs` mount 会与继承的 L0
  Sysbox seccomp notifier 冲突；
- 尝试在 L2 安装 L1 sysbox-fs 的第二个 seccomp user-notify listener 时，
  内核明确返回 `device or resource busy (EBUSY)`。

最后一项是当前方案的硬阻塞。seccomp filter 和 listener 会沿进程树继承，
不能按“L0 sysbox-fs 一套、L1 sysbox-fs 再一套”的方式叠加。因此，仅修改
L1 mgr/runc/fs 无法实现完整 Sysbox nesting。后续架构必须二选一：

1. 由 L0 保留唯一 listener，并把 L2 注册、PID/userns 映射和请求路由到同一
   sysbox-fs；
2. L0 对专用 nested-host 容器不安装 notifier，再由 L1 接管，但这会失去 L1
   自身依赖 notifier 的普通 Sysbox mount 语义，必须补充等价实现。

在完成其中一种单 listener 架构前，不能把 systemd、Docker 或 K3s 验收标记
为通过，也不能用跳过 `/proc`、共享 L1 userns 或 Docker `vfs` 降级冒充完整
实现。

当前 PoC 为了让 inner Pod 启动并获得可用的 `/proc`，最终让
`sysbox-runc-inner` 不再创建第三层 user namespace。这样虽然绕过了 procfs
mount 的内核限制，但 inner root 与 outer Sysbox Pod root 位于同一个 user
namespace，失去了普通 Sysbox 最重要的一层隔离。因此该方案只能作为专用、
受限的 nested runtime，不能视为完整 Sysbox。

## 2026-08-11 PoC 运行层级（历史）

```text
宿主机
└─ outer Sysbox Pod user namespace
   ├─ inner K3s
   ├─ sysbox-mgr / sysbox-fs
   └─ inner Sysbox Pod
```

这里至少涉及三个权限视角：

1. 宿主机初始 user namespace；
2. outer Sysbox Pod 的 user namespace；
3. 原计划由 inner Sysbox Pod 创建的第三层 user namespace。

Linux capability 不是全局布尔值。即使进程在容器中显示为 UID 0，也只能管理
其 capability 生效范围内的资源。sysfs、cgroup、netfilter、mount superblock
等资源如果由更高层 user namespace 拥有，inner root 仍会得到 `EPERM`。
创建新的 mount 或 network namespace 也不会自动获得这些资源的管理权。

## 2026-08-11 PoC 核心局限（历史）

| 局限 | 根因 | PoC 处理 | 造成的损失 |
| --- | --- | --- | --- |
| 无法正常创建第三层 user namespace | 子 UID/GID mapping 必须完全落在父 mapping 内；outer 只暴露 `0..65535` | 专用 handler 不创建第三层 userns | inner root 与 outer Pod root 处于同一 userns，隔离弱于普通 Sysbox |
| procfs mount 返回 `EPERM` 或 `EINVAL` | inner userns 无权完成 procfs mount；outer Sysbox-FS 进入错误 userns 代理时语义不匹配 | 复用 outer userns 后挂载 `/proc` | 无法同时保留独立 userns 和可用 procfs |
| sysfs/cgroup 不可正常初始化 | `/sys` 与 controller 由更高层 namespace 拥有，inner capability 对其无效 | 跳过部分 sysfs、cgroup 操作 | systemd 无法完成完整启动和服务管理 |
| overlay mount 失败 | overlayfs mount 需要当前层级没有的挂载权限 | Docker 测试使用 `vfs` | 性能、空间利用率和语义都不同于正常 DIND |
| Docker iptables 失败 | inner Pod 无权管理外层拥有的 netfilter 表 | dockerd 关闭 iptables 和 bridge | 无 Docker bridge、端口映射及标准容器网络 |
| sysctl、只读路径检查无法执行 | proc/sys 与 sysfs 路径不具备普通系统容器语义 | nested handler 跳过这些步骤 | 不能保证 OCI sysctl 和只读路径配置生效 |
| 特殊目录无法按普通方式准备 | `/var/lib/docker` 等目录需要 UID shifting 和 chown，目标 ID 不在父 mapping 中 | 跳过隐式特殊目录 mount 和 rootfs shifting | 数据目录隔离、属主、持久化和性能语义不完整 |
| seccomp notifier 层级冲突 | inner runc 与 outer Sysbox-FS 同时处理 mount，请求 capability 和代理 namespace 不匹配 | 专用模式绕过部分 seccomp 路径 | 安全模型弱于普通 Sysbox |
| daemon 生命周期不完整 | inner mgr/fs 只是 outer Pod 内的后台命令，不是系统服务 | 启动脚本等待 Unix socket | 缺少标准重启、健康检查、升级及故障恢复 |

## 问题根因：UID/GID mapping

普通 Sysbox 通常需要至少 65536 个连续 subuid/subgid。outer Sysbox user
namespace 只向 K3s 容器暴露 `0..65535`，因此存在以下冲突：

- `100000:65536`：这些 ID 在父 namespace 中不可见；
- `0:65536`：inner root 再次使用父 ID 0 会破坏预期隔离，并被相关路径拒绝；
- `1:65535`：可以覆盖剩余父 ID，但不足普通 Sysbox 的完整 65536-ID 模型。

尝试使用父 namespace 无法表示的 ID 时，runc 在创建或处理 `exec.fifo`、
rootfs 和特殊目录时会出现：

```text
chown ...: invalid argument
```

这不是普通文件权限不足，而是内核无法把目标 UID/GID 翻译到父 namespace。
早期 PoC 将 mgr 与 runc 的专用范围同步为 `1:65535`；最终为了恢复 procfs，
专用 handler 进一步取消第三层 user namespace、OCI UID/GID mappings 和
rootfs UID shifting。

## 问题根因：procfs mount

inner runc 创建第三层 user namespace 后执行：

```text
mount("proc", "/proc", "proc", ...)
```

会被内核以 `EPERM` 拒绝。该请求还可能被 outer `sysbox-fs` 的 seccomp
notifier 截获：

- 直接让 inner 进程执行：返回 `EPERM`；
- 代理进入全部 child namespaces：userns 语义不匹配，返回 `EINVAL`；
- 在通用 `CAP_SYS_ADMIN` 预检中处理：inner runc 的 capability 对目标资源
  无效，请求在代理前就被拒绝。

早期通过跳过 procfs 让 Pod 进入 Running，但 `/proc` 是空目录，导致：

```text
free -h
top -b -n 1
ps -ef
```

全部失败。这种 Running 不能算验收成功。最终专用 handler 不再创建第三层
userns，才恢复可用 procfs。218 已验证 `/proc/meminfo` 和 PID 目录包含真实
数据。

## 问题根因：sysfs、cgroup 与 systemd

systemd 镜像可以让 `/sbin/init` 成为 PID 1，但这不代表 systemd 已完成系统
初始化。实测 Pod 状态为 `1/1 Running`、PID 1 为 systemd，同时：

```text
systemctl is-system-running
offline
```

原因是 inner 环境没有完整、可写且归当前 user namespace 管理的 sysfs/cgroup
层级。相关初始化可能出现：

```text
mkdir: cannot create directory 'cpu': Read-only file system
System has not been booted with systemd as init system
```

因此当前结果只能证明 `/sbin/init` 进程可以执行，不能证明 service manager、
unit、journald、cgroup delegation 或服务自动拉起可用。

## 问题根因：Docker daemon

在正确使用 `runtimeClassName: sysbox-runc-inner` 的 systemd 测试 Pod 中，
Docker daemon 不会自动启动。默认启动 dockerd 时实测出现：

```text
could not setup daemon root propagation: operation not permitted
failed to mount overlay: operation not permitted
iptables ... Permission denied (you must be root)
failed to create NAT chain DOCKER
```

这些错误分别来自 mount propagation、overlayfs 和 netfilter 的 namespace
权限边界。容器内显示为 root 并不能取得这些外层资源的管理能力。

为了只验证 image pull，PoC 使用：

```sh
dockerd \
  --storage-driver=vfs \
  --iptables=false \
  --bridge=none
```

该配置能启动 Docker API，但不具备标准 Docker-in-Docker 语义：

- 没有 overlay2；
- 没有 Docker bridge；
- 没有 iptables NAT；
- 没有端口映射；
- 未验证启动 Docker workload container。

因此成功执行 `docker pull` 只证明 registry 访问和 content store 的最小路径
可用，不能证明 DIND 已通过。

## 问题根因：inner CNI、DNS 与外网

inner K3s 使用的测试 CNI 配置为 `ipMasq:false`。Pod 地址位于
`10.244.0.0/16`，如果 outer K3s network namespace 没有对应 SNAT，外部网络
无法返回该源地址的响应。

systemd/Docker 测试中还观察到 inner Pod 的 `/etc/resolv.conf` 指向：

```text
nameserver 10.43.0.10
```

当 inner CoreDNS 未就绪时，registry DNS 查询会一直超时。218 的最终 pull
测试临时进行了两项环境修正：

1. 在一次性测试 Pod 中补充 registry hosts 解析；
2. 在 outer K3s Pod network namespace 为 `10.244.0.0/16` 增加临时
   MASQUERADE。

修正后成功拉取：

```text
ccr.ccs.tencentyun.com/afan-public/nginx:latest
sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f
```

DNS、CNI SNAT 和 registry 可达性属于测试环境问题，应与 inner runc 创建失败
分开判断。

## 问题根因：`/proc noexec` 与 inner CNI

outer Sysbox 默认将 `/proc` 挂为 `noexec`，inner CNI 创建 network namespace
时需要执行 `/proc/self/exe`，因此会因外层 mount flag 失败。

PoC 增加了严格 opt-in：

```yaml
metadata:
  annotations:
    sysbox/allow-proc-exec: "true"
```

containerd 必须把该 key 同时放入 `pod_annotations` 和
`container_annotations`，否则注解可能只到达 sandbox，实际 K3s workload
OCI spec 无法收到。该选项会降低 outer Pod 的 `/proc` 隔离，只能用于本 PoC。

## 问题根因：静态二进制与最小 K3s 镜像

K3s 镜像缺少普通发行版中的动态加载器和部分共享库。即使目标文件存在，执行
动态 ELF 时也可能得到：

```text
not found
exit status 127
```

这通常表示 ELF interpreter 或共享库不存在，而不是文件没有复制成功。当前
helper 必须提供静态构建的：

- `sysbox-runc`；
- `sysbox-fs`；
- `rsync`；
- `fusermount3`。

此外，K3s `/etc/os-release` 可能没有 `ID`，Sysbox 选择发行版路径前还需写入
generic distro ID。

## 问题根因：exec FIFO 卡死

早期 nested handler 为绕过缺失 procfs，不能再通过 `/proc/self/fd` 重新打开
`O_PATH` exec FIFO，因此改用 `O_RDWR` FIFO。一次修改过早关闭 writer，导致：

1. init 已写入启动握手字节；
2. writer 在 `runc start` 打开 reader 前关闭；
3. start 侧没有读到握手字节；
4. Pod 永久停在 `ContainerCreating`。

恢复 FIFO writer 的短暂保留后，pause Pod 才重新启动成功。这属于专用 nested
实现回归，不是网络或镜像拉取问题。

## 问题根因：交互式 exec 在附加终端前卡死

`v0.7.1-46` 中 nginx `/bin/sh` 和 systemd/Docker `/bin/bash` 均可执行非交互命令，
但 `kubectl exec -it` 会在终端附加前永久等待。根因是 nested-identity 同时启用了
`SkipSpecialMounts`，setns exec 路径因此跳过 `UnsafeCloseFrom()`，让目标 shell 继承了
runc 内部同步 socket。只要交互 shell 不退出，父 runc 就一直等待该 socket 的 EOF，
containerd 无法完成 PTY 附加。

`sysbox-runc` commit `5208ebb` 将 FD 清理策略与 special mount 策略分开：
nested-identity 的标准 init 和 setns exec 始终关闭内部 FD，只有旧的非 nested PoC 保留原行为。

## 默认 runtime 的 `/dev/ptmx` 回归（已修复）

未设置 `runtimeClassName` 的 L2 Pod 使用 inner K3s 默认 `runc` handler 时，原生
`runc` 不会在 nested rootfs 中建立 `/dev/ptmx`，因此交互式 `kubectl exec -it`
会报 `open /dev/ptmx: no such file or directory`。修复后默认 handler 调用
`runc-lite`（并保留 overlayfs 兼容普通 Pod），显式 `runc-lite` handler 继续使用
sysbox snapshotter。`ckm-test` 中无 runtimeClass 的 nginx Pod 已验证
`/dev/ptmx -> pts/ptmx` 和 exec 成功。
该修复同时恢复了 nested init 的 FD 清理保护。镜像
`v0.7.1-47-nested-tty-exec` 已在 nested-agent 删除重建前后验证：

```bash
bash ./12-test-interactive-exec.sh
bash ./06-enter-ckm-shell.sh nginx
bash ./06-enter-ckm-shell.sh docker
```

两个容器都获得 `/dev/pts/*`、能接收输入并正常退出，退出后没有残留
`sysbox-runc exec --console-socket` 进程。命令参数是 `-it`，不是 `-ut`。

## 需要区分的环境与时序问题

以下错误会干扰测试，但不直接说明 Sysbox runtime 逻辑失败：

| 现象 | 分类 | 原因 |
| --- | --- | --- |
| `serviceaccount "default" not found` | inner K3s 启动时序 | outer Pod Running 不代表 inner API 和 controller 已初始化完成 |
| `RuntimeClass "sysbox-runc" not found` | outer 集群环境 | cluster-scoped RuntimeClass 被删除或尚未创建 |
| CoreDNS、基础组件拉取超时 | 外部 registry/network | Docker Hub 或 DNS 不可达 |
| registry 域名解析超时 | inner DNS | inner CoreDNS Service 无可用响应 |
| registry HTTPS 建连超时 | inner CNI | `ipMasq:false`，Pod 出站流量没有 SNAT |
| binary `not found` | 镜像运行环境 | K3s 镜像缺少 ELF interpreter 或共享库 |

测试记录必须同时保留 Pod phase、事件、runc/mgr/fs 日志和网络证据，不能将
“构建成功”“outer Pod Running”或“镜像已拉取”单独作为 inner Sysbox 验收
成功。

## 2026-08-11 当时已证明的能力

- outer Sysbox Pod 内可以命令方式启动 `sysbox-mgr` 和 `sysbox-fs`；
- inner K3s、containerd 和基础 CNI 可以启动；
- 专用 `sysbox-runc-inner` 可以启动最小 pause Pod；
- inner Pod 的 `/proc` 可供 `free`、`top`、`ps` 使用；
- systemd 测试镜像可以让 `/sbin/init` 成为 PID 1；
- 使用 `vfs + no iptables + no bridge` 的受限 dockerd 可以完成指定
  `docker pull`。

## 2026-08-11 当时尚未证明的能力

- 独立的第三层 user namespace；
- 与普通 Sysbox 等价的 root 隔离；
- 完整 systemd service manager；
- cgroup delegation 和 controller 管理；
- Docker overlay2；
- Docker bridge、iptables、端口映射；
- 标准 Sysbox sysctl、sysfs、只读路径和特殊目录语义；
- 普通 Docker workload container 的创建与运行；
- 多租户安全与生产可用性；
- daemon 的标准生命周期、升级和故障恢复。

## 当前生产判断

v47 已恢复独立 child user namespace、`0 0 65536` 映射、Docker `overlay2` 和二次
cgroup delegation，但仍主动放弃 `/proc noexec` 强隔离和 Pod 内资源视图隔离。
因此当前方案可用于受控的功能性 CKM 工作负载，不能作为多租户或不可信负载的安全边界。
正式扩大使用范围前仍需完成长时间并发重启、L0 服务恢复和大 rootfs rsync 生命周期压力测试。

## 相关文档

- [README.md](./README.md)：当前人工验证流程；
- [HISTORY.md](./HISTORY.md)：218 PoC 实现和逐轮测试记录；
- [../README.md](../README.md)：w7panel 文档索引。
# runc-lite 边界

## 218 hostUsers=false sandbox 阻塞（2026-09-01）

在 L1 CKM 的 runc-lite workload 设置 `hostUsers:false` 后，Pod 尚未进入
runc-lite，inner containerd 先创建 sandbox network namespace 即失败：

```text
failed to create network namespace for sandbox: failed to start noop process for unshare:
fork/exec /proc/self/exe: operation not permitted
```

该错误来自 containerd CRI sandbox userns helper，不是 runc-lite 的 OCI create。
L1 的 `/proc/self/uid_map` 为 `0 3540385792 65536`，而 helper 请求的 child
映射使用 L0 偏移 UID（此前观测为 `231072`），不落在 L1 可见映射内。
`/etc/subuid` 在 L1 中虽为 `root:0:65536`，仍无法改变 helper 传入的范围。

已提供针对 CKM 使用的 containerd v2.2.5-k3s2 的最小补丁
`containerd-userns-local.patch`；它只修改 CRI sandbox 映射并可独立构建
containerd，不需要重新编译 K3s。应用补丁后需替换 CKM 数据卷中的
containerd 并重启 L1 Pod。

因此当前无需重编译 K3s 的可行项仅是让 containerd sandbox helper 支持
namespace-local（`0:0:65536`）映射，或关闭该 helper 并由 OCI runtime 创建
userns；现有 containerd 配置没有对应开关。除非修改/升级内嵌 containerd，
218 集群无法完成 hostUsers=false 的端到端验收。

CKM operator 的旧启动命令还会把 `/var/lib/rancher/k3s/runc-lite` 复制到
`/usr/local/bin/runc`。该路径是 K3s 默认 runtime；若复制未启用 seccomp 的
构建，会导致所有 Pod 报 `seccomp not supported`。runc-lite 应只通过
RuntimeClass 的 `BinaryName` 使用，不能替换默认 runc。

`runc-lite` 只服务内层 K3s，要求 Pod 使用 `hostUsers: false`，并依赖
`sysbox-snapshotter` 与 `sysbox-admission` 提供 rootfs/PVC 元数据。proc 强隔离、
系统视图隔离和 system container 功能不在支持范围内。

## 最新 218 验证（2026-09-01）

独立构建的 containerd v2.2.5 patched 已使 sandbox userns 和 overlayfs chown
阶段通过；随后失败进入 OCI create：`mounting "proc" to rootfs ... invalid
argument`。这是嵌套 userns 中 proc 挂载限制，尚未进入 runc-lite 进程，端到端
测试仍阻塞。后续需在 containerd/runc spec 中跳过该 proc 挂载（本项目已放弃
proc 强隔离）或改用可用的宿主 proc bind 挂载。

后续重测中 runc-lite 已跳过 proc/sysfs 挂载并成功创建 `hostUsers:false` Pod；
启用 sysbox-snapshotter 后，CKM 镜像缺少 `/dev/fuse`，`fuse-overlayfs` 自检失败，
特殊目录 PVC handoff 因此尚未验证。需要为 CKM 注入可用的 FUSE 设备。

## 218 patched containerd 替换限制（2026-09-01）

按 `containerd-userns-local.patch` 独立编译出的 `/tmp/containerd-patched6`
可启动，但 CKM 的 `/bin/k3s server` 仍会启动内嵌 containerd（日志中的 PID 362），
并占用 `/run/k3s/containerd/containerd.sock`。手工启动 patched containerd 只能
与其并存，强杀内嵌进程会连带 K3s 退出；因此当前部署未真正使用 patched binary，
workload 仍报 runc bundle rootfs `no such file or directory`。要在不重编译 K3s
的前提下生效，必须让 K3s 启动参数/配置指向独立 containerd socket，或替换其
内嵌 containerd；现有启动命令未提供该切换。

## L2 nginx `hostUsers:false` 复测（2026-09-02）

在已验证的 `runc-lite + sysbox-snapshotter` nginx Deployment 上设置 Pod
`spec.hostUsers: false` 后，Pod 保持 `ContainerCreating`，重复事件为：

```text
FailedCreatePodSandBox: failed to create network namespace for sandbox:
failed to start noop process for unshare: fork/exec /proc/self/exe: operation not permitted
```

该失败发生在内嵌 K3s 的 containerd CRI sandbox userns helper，早于 admission、
snapshotter、FUSE rootfs 和 runc-lite OCI create。因此不能将此结果归因为本轮
runc-lite/rootfs 改动。L1 CKM Pod 仍是 `runtimeClassName=sysbox-runc` 且
`hostUsers=false`；L2 的持久化 nginx 回归临时保持不设置该字段，直到 K3s 内嵌
containerd 应用 namespace-local userns 映射补丁。

进一步检查 containerd v2.2.5-k3s2 源码发现 sandbox helper 的
`UnshareAfterEnterUserns` 使用 `os.StartProcess` 携带 `Ptrace: true`。L1 进程状态
显示 `Seccomp: 2`，因此该 `PTRACE_TRACEME` 路径也可能被 Sysbox seccomp filter 拒绝。
但 `Ptrace` 同时用于在 exec 前暂停子进程；简单删除会破坏 helper 的同步语义，不能
直接部署。要完成 hostUsers 测试，仍需设计不依赖 ptrace 的握手、处理 namespace-local
UID/GID 映射，构建与 K3s 版本匹配的 patched containerd，并通过
`--container-runtime-endpoint` 让内层 K3s 使用它，再做完整回归。

## 持久化路径与真实 root 验证（2026-09-02）

快照器原先将 PVC upper/work 放在含 Pod UID 的 kubelet mount path，重建 Pod 后
marker 丢失。修复后从已受信任 sidecar OCI mount source 的 `/proc/self/mountinfo`
读取 Sysbox `/rootfs/special/...` bind root，并映射为 L1 可见、稳定的 PVC 路径。
当前 handoff 与 fuse-overlayfs 均使用：

```text
/var/lib/rancher/k3s/storage/pvc-74a3e152-7dc5-4579-becc-3406d7ac5301_default_ckm-k3s-nginx-rootfs/nginx/{upper,work}
```

marker 在该 upper 中跨 Pod 重建保留。验证脚本不再使用 `kubectl exec`（其 setns
root 仍错误），而是以 Pod `containerID` 在 L1 `/proc/*/cgroup` 精确查找进程，再
以 `nsenter -m -r/proc/<pid>/root` 检查真实 root；已确认 image `index.html`、marker
`persisted-v2` 和 `/srv/data` bind 均存在。

## CKM nginx rootfs 回归（2026-09-03）

在外层 218（`/root/.kube/218.config`）的 `ckm-test` 中重新安装 host 模式
`w7panel-sysbox` chart，并在其 K3s 内重新应用 nested chart。首次应用后发现
`04-install-ckm-chart.sh` 生成的 `runc-lite.toml` 漏写
`snapshotter = "sysbox"`，导致 rootfs workload 连续报
`sysbox sidecar oci spec unavailable`；补写该字段并受控重建 CKM Server 后恢复。

随后运行 `05-test-ckm-k3s.sh`（L2 不设置 `hostUsers:false`，按当前测试要求忽略该项），
最终返回 `FUNCTIONAL PASS`：空 PVC 自动初始化 nginx 文件，marker 和特殊
`/srv/data` bind 在 Pod 重建后保留；marker owner 为 `0:0`，snapshotter socket/handoff
JSON 存在，`sysbox-webhook-mutator` 已 Ready。CKM Server 重建后 Agent、admission 和
nginx 均恢复。首次启动偶发的 `sidecar oci spec unavailable` 按约定视为可接受竞态，
未加入 retry 逻辑。

### `kubectl exec` root 视图限制

#### 合并遗漏（2026-09-03）

此前 `sysbox-runc` 的 `w7panel-runc` 分支合并 `w7panel` 时遗漏了
`5208ebb fix nested interactive exec fd cleanup`。该提交只修复 nested
容器交互式 exec 的内部 FD 清理（TTY 附加/退出等待），不改变 rootfs 视图。
直接合并会连带 nested-identity 分支的约 35 个文件；该合并已撤销。当前仅保留
`w7panel` → `w7panel-runc` 的目标合并内容；`5208ebb` 不属于 `w7panel`，未纳入。

同一 nginx Pod 使用内层 `kubectl exec` 可以启动 shell 并返回 `id`，但执行环境的
`/` 仍是 L1 视图，`/usr/share/nginx/html` 等 workload rootfs 路径不可见。该行为在
当前 `runc-lite` 初始实现中已存在；与后续合入 `sysbox-runc` 的 proc pressure/sysctl
变更无关。功能验收继续使用 container ID 查找进程后执行
`nsenter -m -r/proc/<pid>/root`，以检查真实 rootfs。交互式 `kubectl exec -it` 还必须
从具有 TTY 的终端发起，经过另一层 `kubectl exec` 转发时需要同时保留外层 `-it`。

nginx 测试镜像为 BusyBox 变体，仅提供 `/bin/sh`，不包含 `/bin/bash`。因此使用
`kubectl exec ... -- /bin/bash` 会得到 `stat /bin/bash: no such file or directory`；
应改用 `/bin/sh`。这属于镜像内容，不是 OCI runtime exec 故障。

`w7panel-k3k-agent-console-*` Pod 若未设置 `runtimeClassName`，会走默认
`sysbox-runc` handler；当前该路径可能不创建 `/dev/ptmx`（仅有
`/dev/pts/ptmx`），因此 `kubectl exec -it` 报 `open /dev/ptmx: no such file or
directory`。显式设置 `runtimeClassName: runc-lite` 后可正常 exec；临时在容器内
创建 `/dev/ptmx -> pts/ptmx` 也能验证这一点。后续应决定 agent 是否统一使用
`runc-lite`，或修复默认 `sysbox-runc` 的 devpts/ptmx 初始化。

已在 `sysbox-runc` 提交 `24119f8` 修复：即使 `/dev` 为 bind mount，也会在
缺失时创建 `/dev/ptmx -> pts/ptmx`；父仓库提交 `4687407` 已更新 submodule 指针。
需重新构建并部署 CKM 镜像后复测。

### Bootstrap 镜像切换规则

CKM 的 bootstrap 镜像不要直接 patch 单个 CKM Server Pod。应修改外层
`w7panel-ckmv3` Operator Deployment 的环境变量
`CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE`（使用可被节点拉取的 tag），然后重启 Operator：

```bash
kubectl -n default set env deploy/w7panel-ckmv3 \
  CKM_INNER_SYSBOX_BOOTSTRAP_IMAGE=docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:<tag>
kubectl -n default rollout restart deploy/w7panel-ckmv3
```

Operator 重启后由 reconcile 更新 CKM Server 的 `sysbox-inner-bootstrap` initContainer；
直接修改 CKM Server Deployment 可能被控制器覆盖。

### Bootstrap layer 与 sidecar 已知问题（2026-09-04）

部分节点拉取 `sysbox-deploy-k3s-bootstrap` 时，CentOS Stream 9 基础层
`sha256:28fce384...` 会在 containerd content store 中缺失，表现为
`failed to extract layer`。将镜像导出/重新导入为单层后可正常下载；当前测试 tag 为：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s-bootstrap:v0.7.1-ptmx-24119f8-flat8
```

当前 CKM Server 仍可能出现 `sysbox sidecar oci spec unavailable`，按测试约定忽略，
不因该现象加入重试或阻断后续 nginx/rootfs 验证。`sysbox-inner-k3s.sh` 由 CKM
initContainer 明确检查，bootstrap 镜像必须包含该脚本及 `/opt/sysbox/bin/generic/rsync`。
该镜像还需包含实体 `fusermount3`；仅安装在 CentOS 基础层而不复制到工具目录时，
K3s 主容器会报 `inner K3s Sysbox requires fusermount3 in PATH`。补齐后若出现
`Sysbox daemon exited`，属于当前 sidecar/内层守护进程启动限制，按本轮测试约定继续
记录，不作为 layer 验证失败。

## 无注解 volume-init 回归（2026-09-09）

218 `default/w7panel-sysbox` 已升级到安装镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-volume-init-20260909`
（`sha256:24e5d8d89142…acb56c01`），随后重建
`k3k-console-164315/ckm-test` Server，使 L1 containerd 重新读取
`sysbox-runc-lite` handler。以该 CKM 执行：

```bash
SYSBOX_IN_SYSBOX_CONFIG=w7panel-doc/sysbox-in-sysbox/config.sh \
  bash w7panel-doc/sysbox-in-sysbox/05-test-ckm-k3s.sh
```

返回 `FUNCTIONAL PASS`。测试 Pod 没有 `sysbox/volume-init` 注解；普通空 CSI PVC
`ckm-k3s-nginx-webroot` 首次挂载后有 nginx 的 `index.html` 与 `50x.html`，并在 Pod
重建后保留 marker。独立 rootfs PVC 的 `/srv/data` special bind marker 也保留，两个
marker 的 owner 均为 `0:0`。L2 仍未设置 `hostUsers:false`。

旧 nested chart 的 installer DaemonSet 会因非 initial user namespace 拒绝 host install 而
CrashLoop；已修复为 nested 模式不渲染该 host installer。本流程由 CKM bootstrap 提供既有
snapshotter，再由 `04-install-ckm-chart.sh` 写入 lite 二进制和 handler。外层升级期间还观察到一次
`sysbox sidecar oci spec unavailable`，CKM controller 自动重建后恢复为 `2/2 Ready`。

## Nested installer 修复回归（2026-09-09）

`installMode` 原先没有被 `sysbox-deploy-k8s.yaml` 使用，导致 nested chart 也渲染完整
host installer DaemonSet。该 DaemonSet 在 L1 内执行 host 安装，因 `/proc/1/root` 不是
真实宿主根而 CrashLoop。模板现仅在 `installMode != nested` 时生成它；nested 渲染仍保留
admission Deployment 和 `RuntimeClass/sysbox-runc-lite`。

在 218 新建 `k3k-console-164315/ckm-test1`（`innerSysbox.enabled=true`），安装修复后的
chart 后确认 L1 没有 `w7panel-sysbox-installer` DaemonSet。重建 Server 加载 handler 后，
`05-test-ckm-k3s.sh` 返回 `FUNCTIONAL PASS`：无注解 CSI volume-init、rootfs 持久化和
special bind 全部通过。

## CKM 名称复用与遗留 PVC（2026-09-24）

218 曾遗留 `varlibrancherk3s-k3k-ckm-test-server-0`：它属于已删除 CKM 的旧 UID，处于
Terminating 状态且容量为 20Gi。新建同名 `ckm-test` 时，CKM controller 尝试将该 PVC
扩容到 40Gi；local-path 不支持该操作，因此 CR 永远停在 `creating`，L1 Pod 不会出现。
这不是 Sysbox 或 bootstrap 镜像故障。测试必须使用唯一的 `CKM_NAME`；
`01-create-ckm.sh` 现在会检测同名 Server PVC 后失败，不会删除或修改旧数据。

使用完整 release 测试镜像
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:v0.7.1-buildcache-rootfs-webhook-20260924`
及对应 bootstrap 镜像新建 `ckm-build2-20260924` 后，重启一次 L1 以加载 lite handler，
`05-test-ckm-k3s.sh` 返回 `FUNCTIONAL PASS`：webhook 自动提升 rootfs 注解 Pod 至
`sysbox-runc-lite` 并注入 sidecar；无注解 CSI 初始化复制、rootfs 持久化和 special bind
跨 Pod 重建均通过。
## console-145382 升级 v0.7.1-33（2026-11-09）

### ckm-2lmw8：PHP Pod 缺少 rootfs sidecar（2026-11-09）

面板集群 `k3k-console-164315/ckm-2lmw8` 内，Deployment
`default/w7-tradphp-hoejrjpt` 为 0/1。旧 Pod 使用 `sysbox-runc-lite`，有
`sysbox/rootfs-rw-layer` 注解，但只有 PHP 容器，没有 `sysbox-rootfs`；持续报
`failed to create containerd task: sysbox sidecar oci spec unavailable`。内层
已安装 v0.7.1-37，agent/admission Ready。用相同注解与 PVC 做服务端 dry-run，
当前 webhook 能正确注入 sidecar；尚未确定旧 Pod 当初为何未被注入。

仅删除失败的 PHP Pod，由原 ReplicaSet 重建，保留 `default-volume` PVC
及原持久化路径。新 Pod 起初遇到一次 sidecar 尚未就绪的启动重试，随后
2/2 Ready、Deployment 1/1 Available；PHP-FPM 日志为 ready to handle
connections，容器内 TCP 9000 连接检查通过。

另外，同组 `w7-traditiontool-hoejrjpt` 的 ReplicaSet 报 `RuntimeClass
"sysbox-runc" not found`；nested 集群只提供 lite，此独立问题本次未修改。
节点还出现过镜像文件系统占用 87% 的 ImageGC 告警，没有将其误判为本次
PHP 启动失败的直接原因，也未执行数据或镜像清理。

### 218：v0.7.1-37 正式 Release 验收（2026-11-09）

GitHub Actions run `42588984950` 成功后，从该 Release 下载 Chart、lite
二进制、`image-metadata.txt` 和 `SHA256SUMS`；三个实际下载附件校验全部通过。
正式镜像为 `ghcr.io/w7panel/sysbox-deploy-k3s:v0.7.1-37`：

- manifest digest：`sha256:6a4f78365b0effbf86fd19dc78f87741785179bf77d49e4a6ee85cb317c0e1cb`。
- image config ID：`sha256:1fc33f58628879037ca32f15e4b51913768093345f662bebc98a16bf49dbd655`。
- lite 附件与 L0 `/usr/bin/sysbox-runc-lite`、L1 实际 release 目录的二进制
  SHA256 均为 `a0c478eee57a2812d7610b790450857d7825e475f9ea599dfcd840b98d206f9a`。

使用 `/root/.kube/218.config`，主集群 `default/w7panel-sysbox` 从 v33 升到
正式 v37；独立子集群为 `k3k-console-164315/ckm-snapshot-1109`。两边都安装
下载的正式 Chart，未显式传入 installMode，分别自动生成 host installer 与
nested inner-agent；镜像均来自正式 Release。子集群实际 readiness timeout=10s。
宿主 installer 完成二进制替换、服务/K3s 重启及已有完整 Sysbox Pod 重建。
基线 24 个活动 Sysbox Pod 均恢复 Ready；4 个 CKM 内 `/readyz=ok`、节点 Ready。
其他 CKM 的内层 Sysbox 版本未迁移。

验收结果：

- L0 FUSE smoke：full 无显式 Pod 挂载仍可打开 `/dev/fuse`，lite 不注入设备。
- L0 lite/local-path、full/local-path、full/Longhorn `disk-default` 均通过
  无注解空 PVC 初始化、Pod 重建后的 PVC 与 `/srv/data` 持久化；full 使用
  `hostUsers:false`。Longhorn 用例首次因节点 110 Pod 上限超时，自动调度恢复后
  完整重试通过；不是 Sysbox 启动失败。
- L2 local-path 的 rootfs/特殊目录、PVC 初始化、CRI writable-layer mountpoint
  和 kubelet stats 全部通过，nginx HTTP 200。L2 没有 CSI，本轮 CSI 结果属于 L0。
- 新的 `nginx:1.27-alpine` 在 Sysbox 冷缓存中 10 秒 Ready、HTTP 200；新增
  8 个镜像解包层没有 materialization marker，未遗留 merged 挂载。
- 直接向 snapshotter 发出 300ms Prepare 请求，取消后 staging/merged 挂载
  清理成功；同一 snapshot 仍可查询、重试 Mounts 并正常移除。
- SIGKILL 独立测试 CKM 的 snapshotter，保留旧 socket，PID `9923` 在 2 秒内
  恢复为 `28088`；随后重建 nginx，持久化标记与 HTTP 200 均通过。
- 相同正式 Chart/配置重复 Helm upgrade，L1 Pod UID 未变，K3s restartCount
  保持 2；收尾日志检查未见 panic/fatal/OOM/ENOMEM。
- 本轮 3 个 L0、2 个 L2 测试 Deployment 均缩为 0，PVC 保留。

传输问题单独记录：218 的 containerd 仍配置了 `ghcr.io` 到
`ghcr.registry.cdn.w7.cc/v2` 的 registry mirror，Chart 的 GHCR 地址不等于
底层一定直连。L0 首次拉取出现一层 digest mismatch，重试后所有内容通过摘要
校验；日志不足以确定返回错误字节的上游。随后用独立 hosts-dir 直连 GHCR
确认 manifest/digest；L1 下载被宿主升级重建打断，改从 L0 导出已校验的同一
正式镜像并导入 L1，保留原 GHCR 名称与 digest。本轮未修改集群 registry mirror。

### 218：Chart GHCR 地址与 readiness 超时验证（2026-11-09）

Chart 默认镜像改为 `ghcr.io/w7panel/sysbox-deploy-k3s`。内层 agent 原先
未设置 `timeoutSeconds`，Kubernetes 默认只给 1 秒，而 status 的 snapshotter
RPC 自身允许 3 秒，之后还需查询 CRI；改为明确的 10 秒，保留原健康检查。

在 `k3k-console-164315/ckm-snapshot-1109` 用现有
`snapshot-fix-218-20261109c` 镜像升级工作区 Chart：探针确认为 10 秒，agent
1/1 Ready，三次 status 耗时 0.399–0.415 秒。启动期间出现一次无错误文本的
readiness 失败，随后恢复；未观察到 1 秒超时。L1 重启次数保持 3。
独立 `ghcr-probe-nginx` 通过空 PVC 初始化、rootfs/special bind 持久化、Pod
重建及 CRI/kubelet stats 回归，HTTP 200。测试 Deployment 已缩为 0，PVC 保留。

同一 CKM 通过 `crictl pull` 成功直连拉取 GHCR 的 `v0.7.1-34`，镜像 ID 为
`sha256:bfc4bd1f60a0509478b4560ae9cd099bb4a9e71b1f1ec88431d095a12adc4cc2`。
`v0.7.1-35` 此时仍在 GitHub Actions 构建，拉取返回 NotFound；因此本次是
Chart 探针验证及 GHCR 拉取验证，不是 v35 正式发布镜像的完整验收。

### 修复及 218 验证结果（2026-11-09）

按 `skills/sysbox-in-sysbox-test/SKILL.md` 执行，目标 kubeconfig 为用户指定的
`/root/.kube/218.config`，新建独立 CKM
`k3k-console-164315/ckm-snapshot-1109`（L1 `sysbox-runc`、`hostUsers:false`）。

源码修复：

- 用 `containerd.io/snapshot.ref` 排除镜像解包层，只物化容器可写 rootfs。
- 可取消的 GNU `cp -a --preserve=all` 复制到暂存目录，等待复制退出、成功卸载
  临时 merged 挂载后才原子交换 upper；同 snapshot/upper 串行化。work 中的
  marker 记录未来 upper inode，区分交换前后中断，兼容原 marker。
- snapshot 元数据提交后不在 Prepare 失败路径删除已注册目录；删除前必须
  成功卸载 merged，失败则保留目录，避免递归删除仍挂载的数据。
- agent 和 launcher 检查精确 Unix 监听路径，并直连 snapshotter RPC，超时
  3 秒。全新空库实测返回 `ctr: bucket does not exist: not found`，仅允许
  此精确整行作为健康空库；连接失败、超时及其他 NotFound 均失败。

使用 v33 镜像作为基线，经 `06-build-and-test.sh` 的 test 模式构建
snapshotter/inner-script，然后分阶段执行 00/02/03/04/05。测试镜像仅本地构建
并导入 L1，未推送：
`docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:snapshot-fix-218-20261109c`。
导入 manifest digest 为
`sha256:95705f88390ecf90fe07f05da463a4c06687bd5cfcd8a17411d3dd708e1603c4`；
运行中的 snapshotter SHA256 与构建结果一致：
`a9fddb9419600c3fe6f69d93986bafea258f430fc59d12b17c47aeec1c6f3d97`。
使用工作区 Chart（版本字段 `0.7.0-8`），nested Helm release deployed，
为本地导入镜像设置 `IfNotPresent`；这不是新 tag 或 GitHub release 验收。

验证结果：

- snapshotter 全模块 `go test -race ./...` 通过；脚本 9 个健康场景和
  6 个 CNI 场景通过。覆盖复制元数据、真实进程取消、卸载失败保护及发布中断。
- L0 保持原运行时：FUSE 注入/打开检查及 lite 的 local-path、Longhorn
  `disk-default` PVC/rootfs/special-bind 基线全部通过。
- L1 Sysbox snapshot 列表为空，仅预导入 nginx 镜像内容（`--no-unpack`）。
  7 层 nginx 首次创建到 Ready **12 秒**，HTTP **200**；镜像层保留差异大小，
  小层仅 8–16 KiB，只有 sandbox/容器可写层有物化 marker，无残留 merged 挂载。
- 直接 Prepare 注入 **300 毫秒** RPC 超时：复制进程退出，临时挂载及暂存目录
  随后清理，原 upper 仅 4 KiB、无完成 marker；同一 snapshot 的 Mounts 重试
  成功，之后正常删除目录。此为独立测试 snapshot，没有操作业务 snapshot。
- `05-test-ckm-k3s.sh` FUNCTIONAL PASS：无注解 local-path PVC 初始化、rootfs
  和 `/srv/data` 持久化、CRI writable-layer、kubelet stats/summary 通过。
  初次 sidecar spec unavailable 在重试后恢复，最终功能断言全部通过。
- 额外删除容器 `/etc/debian_version` 并写入 `/root/sysbox-extra-marker`，
  重建后文件保持删除，rootfs、普通 PVC、special-bind marker 均保留。
- 强制结束 snapshotter PID 10630 并保留 socket，agent **10 秒**内自动启动
  PID 30119；恢复后重新创建 nginx，HTTP 200 且持久化断言仍通过。健康检查
  实测约 0.33–0.37 秒，默认 1 秒 readiness 在本测试 CKM 为 Ready。
- 将同一模块测试二进制放到挂载 Longhorn PVC 的测试 Pod，实际执行复制属性、
  原子交换/中断 marker、失败不修改原 upper 三项测试，全部通过。
- 使用相同 Chart/镜像/values 再次 Helm upgrade，L1 Server restartCount
  保持 3，agent/admission Ready，没有再次重启 K3s。

依赖：nested rootfs 物化需要 GNU cp（本 CKM 为 9.5）和文件系统
`RENAME_EXCHANGE`。不支持时明确报错，不静默退化。L2 当前只提供 local-path；
Longhorn 验证在 L0 PVC 上完成，不声称已测试 L2 CSI 驱动。

测试 Deployment 已缩容为 0；保留测试 PVC 与 CKM。记录位于本地
`/tmp/sysbox218-{cold,l2,cancel,cancel-retry,recovery,csi-atomic}.log`。
此次没有更新 console-145382，也未提交、推送或发布修复 tag。

### 后续超时定位（同日 14:55–14:58）

重新启动同一 lite nginx 测试，并以同一缓存镜像创建普通 runc 对照：普通
runc 约 3 秒启动，lite 首次 CreateContainer 从 14:55:41 到 14:57:41
达到 120 秒后取消；第二次从 14:57:42 到 14:58:20 完成，nginx Ready，
HTTP 返回 200。这更新了下面的初次验证结果：基础 nginx 可以在缓存完成后
重试成功，但首次解包性能及失败清理缺陷仍未修复，未做 PVC/rootfs 完整验收。

已确认的代码和现场证据：

- `sysbox-snapshotter/fuseoverlayfs.go` 的 `mounts()` 对所有 nested
  `KindActive` 都执行 `materializeActiveSnapshot()`，包含镜像 extraction
  snapshot。`fs.CopyDir(upper, merged)` 每层重复复制完整父 rootfs，而不是
  只在容器可写层物化；`CopyDir` 不接受 context，超时后复制仍继续。
- 现场 snapshot 19–24 均生成物化 marker，每层 `fs` 为 170468–170500 KiB；
  nginx 压缩镜像仅 60.9 MiB。snapshotter fd 显示持续读取对应 `merged`
  并写入 `fs`；超时后同时持有 snapshot 25 和 26 的复制目录 fd。
- 已提交镜像层标签存在 `containerd.io/snapshot.ref`，与上游 containerd
  unpacker 给 extraction Prepare 添加的标记一致。后续修复可按此区分镜像
  解包和容器可写层，避免按 key 字符串前缀猜测。
- 初次失败残留的 snapshot 9、16 的 `merged` FUSE 挂载仍存在；卸载失败
  只记录警告，后续清理碰到活挂载并报目录非空。未直接删除或强卸载这些目录。

两个测试 Deployment 均已缩容为 0。`containerd-retry.log`、
`snapshotter-retry.log`、`snapshotter-retry-mountinfo.txt`、
`smoke-retry-description.txt` 和 `retry-result.txt` 保存在下述备份目录。
此次仅排障和复测，未修改运行时源码或替换线上二进制。

内层 `default/w7panel-sysbox` 从 `0.7.1-31` 升至官方 `0.7.1-33`，
使用 `installMode=nested`。官方 Chart、lite 二进制校验通过，部署镜像 digest 为
`sha256:8bcf4fc8b13bf14c5aa5055765e4ecd5a163aef2d03b6673454253823c7f2e74`。
K3s 重启后 CNI 配置为 `bin_dirs = ["/bin"]`，新测试 sandbox 获得
`10.52.0.27`，原 CNI 路径故障已修复；节点和 16 个原有 Pod Ready。
另一个原有业务 Pod 因 `Insufficient cpu` Pending，与本次故障独立。

仍存在两个问题，本次发布功能验收未通过：

- **残留 socket 导致健康误判（已确认）**：snapshotter pidfile 指向已退出的
  PID 9532，socket 文件存在但 `/proc/net/unix` 无监听，agent 仍 Ready，
  `ctr snapshots --snapshotter sysbox ls` 空输出并返回 0。agent 的 `status()`
  因此成功，`install_once()` 提前返回，跳过运行时启动；新 sandbox 报
  `connection refused`。确认无活跃进程及监听后，将 socket 改名为
  `/run/sysbox/sysbox-snapshotter.sock.stale-v33`，官方 agent 自动拉起
  v33 snapshotter（PID 17171）。这是现场恢复措施，未修改该缺陷源码。
- **lite 容器创建超时（原因未定）**：恢复 socket 后，使用本地已有 nginx
  镜像、`sysbox-runc-lite`、无 PVC/无 rootfs 注解的 `sysbox33-smoke`
  Deployment 可以创建 sandbox，但容器报 `CreateContainerError` /
  `context deadline exceeded`，不能据 agent Ready 判定功能通过。
  测试 Deployment 已缩容为 0，保留定义及证据，未创建测试 PVC。

现场备份和 Pod/containerd/snapshot 证据位于 Server 内
`/var/lib/rancher/k3s/sysbox-inner/upgrade-v0.7.1-33`；其中 Helm values/manifest
可能含凭据，不应公开。此前 readiness 超时现场调整（10 秒）升级后保留，
Chart 默认仍为 1 秒，不属于 v33 已修复项。
