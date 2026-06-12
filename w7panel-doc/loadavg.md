# /proc/loadavg 与 LXCFS 对齐说明

本文记录 Sysbox-FS 对 `/proc/loadavg` 的实现差异、之前没有对齐 LXCFS 的原因，以及本次对齐时最关键的 PID 获取问题。

## LXCFS 的基准行为

LXCFS 的 `/proc/loadavg` 不是直接读取宿主 `/proc/loadavg`，也不是每次 read 时即时扫描并计算。它的核心行为是：

1. 第一次读取 `/proc/loadavg` 时，根据读取进程找到容器 init pid 和对应 cgroup，创建一个 loadavg 节点。
2. 新节点初始值是 `0.00 0.00 0.00 0/1 <initpid>` 这一类空负载视图。
3. 后台线程每 5 秒刷新一次该节点。
4. 刷新时递归扫描 cgroup 下的 `cgroup.procs`，统计任务总数、处于 `R` 或 `D` 状态的任务数、最大 namespace pid。
5. 使用 Linux 内核同类固定点 EWMA 常量计算 1/5/15 分钟负载：
   - `FSHIFT = 11`
   - `EXP_1 = 1884`
   - `EXP_5 = 2014`
   - `EXP_15 = 2037`

所以 LXCFS 的重要语义是：

- 首次读取不会立刻把当前进程数刷新进去。
- 空闲容器里 `top` 长时间不应显示宿主 load。
- 有 runnable worker 后，下一轮 5 秒采样应看到容器内负载上升。
- `/proc/loadavg` 第 4 列 `running/total` 和第 5 列 last pid 应来自容器视图，而不是宿主全局视图。

## Sysbox 之前没有完全对齐的原因

Sysbox-FS 原先的实现是请求式 handler，更偏向每次 read 时即时生成视图。这个模型和 LXCFS 的后台 loadavg 节点模型不同。

主要差异包括：

1. 读取时立即刷新

   早期 Sysbox-FS 在读取 `/proc/loadavg` 时会直接刷新 loadavg 统计。这与 LXCFS 首次读取只创建节点、等待后台 5 秒采样的行为不同。

   影响是空闲容器里 `top` 可能看到非 LXCFS 风格的即时结果，也不利于复现 LXCFS 的初始 `0/1` 行为。

2. PID 选择不稳定

   Sysbox-FS handler 里同时能拿到两个容易混淆的 PID：

   - `req.Pid`：当前触发 procfs 读取的进程在宿主上的 PID。
   - `req.Container.InitPid()`：Sysbox 注册容器时记录的 init PID。

   在普通 Docker/sysbox 场景中，`req.Container.InitPid()` 往往就是目标系统容器的 PID 1，直接使用它通常没问题。

   但在 Kubernetes/CRI 场景中，一个 Pod 会有 sandbox/pause 容器和业务容器。sysbox-fs 可能先注册 sandbox，再注册业务容器。如果 `/proc/loadavg` 读取请求关联到的 container init pid 是 sandbox/pause 的 init pid，而不是业务容器的 systemd PID 1，就会出现统计对象错误。

   典型现象是：

   ```text
   0.00 0.00 0.00 0/1 1
   ```

   即使业务容器里已有 systemd、containerd、dockerd、测试 worker 等进程，loadavg 仍长期停在 `0/1 1`，因为采样的是错误的 PID namespace 或错误的 cgroup。

3. cgroup 层级和 systemd 子 cgroup 不一致

   LXCFS 主要按 cgroup 路径递归扫描 `cgroup.procs`。在 systemd 容器里，业务进程可能被放到 `system.slice/*.service`、`init.scope` 等更深层级。

   Kubernetes + systemd + cgroup v2 场景下，单纯从某个 init scope 递归固定深度扫描，可能只能看到 init 或 pause，不能稳定覆盖业务容器内所有任务。

4. 实现结构不同

   LXCFS 是独立 FUSE 文件系统，围绕 file handle、pthread、hash bucket、cgroup 节点生命周期实现。

   Sysbox-FS 是 Go handler，已有资源文件多是请求式生成。逐行移植 LXCFS 的线程和锁模型会增加复杂度，也不符合当前代码结构。

## 本次对齐策略

本次实现目标不是逐行复制 LXCFS，而是强制对齐对用户态工具最关键的行为。

已对齐的部分：

1. 首次读取只创建节点

   新建 loadavg node 时只初始化：

   ```text
   avenrun = 0
   running = 0
   total = 1
   lastPID = namespace PID
   lastRefresh = now
   ```

   同一次 read 不再立即 refresh。这样第一次读取和 LXCFS 一样会看到初始空负载视图。

2. 后台 5 秒采样

   sampler goroutine 每 5 秒刷新活跃 node。读取时只在超过 5 秒周期后做补偿刷新，避免长时间无人读取后节点停滞。

3. 使用 LXCFS/内核固定点 EWMA 常量

   Sysbox-FS 使用同样的 fixed-point 常量计算 loadavg，保证 1/5/15 分钟负载变化趋势与 LXCFS 一致。

4. 优先按读取进程的 PID namespace 找 PID 1

   这是本次修复最关键的一点。

   当前逻辑不再优先信任 `req.Container.InitPid()`，而是：

   1. 优先取 `req.Pid`。
   2. 读取 `/proc/<req.Pid>/ns/pid`，得到读取进程所在 PID namespace。
   3. 扫描宿主 `/proc/<hostpid>/ns/pid`，找到同一个 PID namespace 下 namespace pid 为 `1` 的宿主 PID。
   4. 用这个宿主 PID 作为 loadavg 的 sample PID。

   这样更接近 LXCFS 的“从发起读取的进程反查其容器 init pid”的语义。

   这能避免 Kubernetes/CRI 里误用 sandbox/pause init pid。实际测试中，修复前 worker 阶段一直是：

   ```text
   0.00 0.00 0.00 0/1 1
   ```

   修复后，空闲初始阶段仍保持 LXCFS 风格的 `0/1 1`，5 秒采样后变成容器内任务数；启动 worker 后 load1 上升，例如：

   ```text
   0.64 0.13 0.04 8/34 1712
   ```

## PID 获取问题详解

### 不能直接使用 req.Container.InitPid()

`req.Container.InitPid()` 表示 sysbox-fs 当前关联容器对象的 init pid，但在 Kubernetes 里这个对象不一定等于业务容器。

Pod 至少可能包含：

- sandbox/pause 容器
- 业务容器

两者可能共享部分 namespace，也可能在 sysbox-fs 注册顺序和 handler 请求关联上产生歧义。如果 `/proc/loadavg` 采样绑定到 sandbox/pause，看到的任务数可能只有 1，业务容器内 systemd 及其子进程都不会被统计进去。

因此 `req.Container.InitPid()` 只能作为 fallback，不能作为首选。

### 为什么 req.Pid 更可靠

`req.Pid` 是实际打开或读取 `/proc/loadavg` 的进程在宿主上的 PID。

当容器内执行：

```sh
cat /proc/loadavg
top -b -n1
```

触发 sysbox-fs read 的就是这些容器内进程。用 `req.Pid` 找 PID namespace，再反查该 namespace 的 PID 1，可以稳定定位到“当前读取者所在容器”的 init 进程。

这比从注册容器对象推断更准确，尤其适合 Kubernetes/CRI 场景。

### 为什么不只用 cgroup

纯 cgroup 扫描在 systemd 容器里有两个问题：

1. 读取进程的 cgroup 可能位于某个 service scope 下，不一定是容器根 cgroup。
2. systemd 会把服务放进不同 slice/scope，固定深度递归可能漏掉任务。

因此当前策略是：

1. 用读取进程 PID namespace 找 namespace PID 1。
2. 优先尝试该 PID 的 cgroup 统计。
3. 如果 cgroup 只看到 init 或没有 runnable 任务，则回退到同 PID namespace 扫描。
4. 必要时再回退到容器 root `/proc` 或 cgroup 可见进程统计。

这样既保留 LXCFS 的 cgroup 优先方向，又能覆盖 Kubernetes/systemd 场景中 cgroup 层级不稳定的问题。

## 当前仍未逐行等同 LXCFS 的地方

本次已经对齐主要语义，但仍不是逐行移植：

- LXCFS 使用 pthread + hash bucket 锁；Sysbox-FS 使用 Go goroutine + mutex。
- LXCFS 按 cgroup 节点生命周期删除 load node；Sysbox-FS 使用 `lastSeen` TTL 回收长时间未读取的 node。
- LXCFS 的 cgroup 递归逻辑和 Sysbox-FS 的 PID namespace fallback 逻辑不同。
- cgroup v2 下，部分进程层级和 systemd slice 的表现依赖运行时和发行版，Sysbox-FS 需要 fallback 保证结果可用。

这些差异主要影响内部实现和少数边界场景，不影响常见工具的核心行为：

- `cat /proc/loadavg` 格式正确。
- `top` 显示的 load average 与 `/proc/loadavg` 前三列一致。
- 空闲容器不会暴露宿主 load。
- 启动 CPU worker 后 loadavg 会按 5 秒采样周期上升。
- `running/total/lastpid` 不再长期卡在错误的 sandbox/pause 视图。

## 测试覆盖

`w7panel-doc/test-pod.sh` 已加入空闲 `top` 长时间负载为 0 的检查：

- 连续采样 `/proc/loadavg` 和 `top -b -n1`。
- 校验 `top` 的 load average 与 `/proc/loadavg` 前三列一致。
- 校验 total task count 不是宿主全局任务数。
- 校验 last pid 有效。

完整 Pod 测试还会启动 worker 验证 load1 能上升，覆盖“空闲为 0”和“有 runnable 任务后非 0”两个方向。
