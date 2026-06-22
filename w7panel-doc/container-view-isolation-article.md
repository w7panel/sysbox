# 从进程隔离到视图隔离：微擎面板团队基于 Sysbox 的容器隔离增强实践

在容器技术已经高度普及的今天，很多人谈到容器隔离，第一反应通常是 namespace、cgroup、seccomp、capability 这些关键词。

这些机制确实构成了 Linux 容器的基础：

- namespace 负责隔离进程、网络、挂载点、用户等系统资源；
- cgroup 负责限制 CPU、内存、IO 等资源使用；
- seccomp 负责限制系统调用；
- capability 负责拆分 root 权限。

但在微擎面板团队的实际工程实践中，我们发现，容器隔离还有一个经常被忽略、但对平台化场景非常关键的问题：

> 容器不仅要限制“能做什么”，还要限制“能看到什么”。

换句话说，一个容器即使被限制了 2 核 CPU、4GB 内存，如果它在内部看到的仍然是宿主机的 64 核 CPU、256GB 内存、全局 loadavg、全局 pressure 指标，那么这个容器的运行环境仍然是不完整、不一致，甚至是不可信的。

这就是我们在近期实践中重点关注的方向：**容器视图隔离**。

微擎面板团队基于 Sysbox，对系统容器的 `/proc`、`/sys`、cgroup、CPU、内存、IO、loadavg 等关键资源视图进行了深入适配和增强，目标是让容器内部看到的系统信息与自身资源边界保持一致，从而让容器更接近一个可信的轻量系统环境。

---

## 目录

- [一、传统容器隔离的盲区：容器看到的系统信息并不总是自己的](#一传统容器隔离的盲区容器看到的系统信息并不总是自己的)
- [二、什么是视图隔离](#二什么是视图隔离)
- [三、为什么我们选择基于 Sysbox 实现系统容器视图隔离](#三为什么我们选择基于-sysbox-实现系统容器视图隔离)
- [四、启动阶段：sysbox-runc 如何把虚拟资源视图挂进容器](#四启动阶段sysbox-runc-如何把虚拟资源视图挂进容器)
- [五、读取阶段：sysbox-fs 如何生成容器自己的资源视图](#五读取阶段sysbox-fs-如何生成容器自己的资源视图)
- [六、同一个 `/proc/meminfo`，不同容器应该看到不同内容](#六同一个-procmeminfo不同容器应该看到不同内容)
- [七、为什么还要拦截 mount：防止容器绕过虚拟视图](#七为什么还要拦截-mount防止容器绕过虚拟视图)
- [八、资源视图如何与 cgroup 对齐](#八资源视图如何与-cgroup-对齐)
- [九、loadavg：一个容易被低估的隔离细节](#九loadavg一个容易被低估的隔离细节)
- [十、time namespace：系统视图隔离的进一步延伸](#十time-namespace系统视图隔离的进一步延伸)
- [十一、为什么这对微擎面板很重要](#十一为什么这对微擎面板很重要)
- [十二、从行业角度看：容器隔离的下一步是系统观一致性](#十二从行业角度看容器隔离的下一步是系统观一致性)
- [十三、总结：容器隔离不止是边界，也是视图](#十三总结容器隔离不止是边界也是视图)
- [附：核心代码路径](#附核心代码路径)

---

## 一、传统容器隔离的盲区：容器看到的系统信息并不总是自己的

在 Linux 系统中，很多应用都会通过 `/proc` 和 `/sys` 获取运行环境信息。例如：

```bash
cat /proc/cpuinfo
cat /proc/meminfo
cat /proc/loadavg
cat /proc/stat
cat /proc/pressure/cpu
cat /sys/devices/system/cpu/online
```

这些文件看起来像是“当前系统”的状态。

但在普通容器中，这个“当前系统”并不一定等于“当前容器”。

例如：

- `/proc/meminfo` 可能反映宿主机内存，而不是容器内存限制；
- `/proc/cpuinfo` 可能展示宿主机所有 CPU，而不是容器可用 CPU；
- `/proc/loadavg` 可能体现宿主机负载，而不是容器内部任务负载；
- `/proc/stat` 可能暴露宿主机 CPU 时间统计；
- `/proc/pressure/*` 可能无法准确表达容器 cgroup 维度的资源压力；
- `/sys/devices/system/cpu/online` 可能展示宿主机在线 CPU 集合。

这类问题在简单应用容器中可能不明显，但在面板、PaaS、多租户托管、开发环境、CI/CD、容器内运行 systemd / Docker / K3s 等场景中，会带来很实际的问题。

比如，一个应用根据 `/proc/cpuinfo` 自动决定 worker 数量；一个监控 agent 根据 `/proc/meminfo` 展示内存容量；一个调度组件根据 `/proc/loadavg` 判断系统压力。如果这些数据来自宿主机，而不是容器自身，那么容器内应用就会基于错误的系统视图做决策。

这不是单纯的显示问题，而是容器抽象不完整的问题。

我们可以把容器隔离拆成三层：

```text
执行隔离：进程、网络、挂载、用户权限
资源隔离：CPU、内存、IO、PID 数量
视图隔离：容器内部看到的系统状态
```

传统容器主要解决了前两层，而微擎面板团队这次重点推进的，是第三层：**视图隔离**。

---

## 二、什么是视图隔离

我们对视图隔离的定义是：

> 视图隔离，是指容器内部访问系统信息时，看到的不是宿主机原始视图，而是根据该容器 namespace、cgroup、mount、权限边界重新计算和裁剪后的容器视图。

也就是说，容器看到的系统状态，应该与它实际拥有的资源边界一致。

举几个例子：

| 系统路径 | 普通容器中可能的问题 | 视图隔离后的目标 |
|---|---|---|
| `/proc/meminfo` | 看到宿主机总内存 | 看到容器 cgroup 内存限制 |
| `/proc/cpuinfo` | 看到宿主机全部 CPU | 看到容器 cpuset / quota 对应 CPU |
| `/proc/loadavg` | 看到宿主机负载 | 看到容器内部任务负载 |
| `/proc/stat` | 看到宿主机 CPU 时间 | 看到容器维度 CPU 使用情况 |
| `/proc/pressure/*` | 看到宿主机压力或不准确压力 | 看到 cgroup 维度 CPU / memory / IO pressure |
| `/sys/devices/system/cpu/online` | 看到宿主机在线 CPU | 看到容器可见 CPU |

这背后有一个核心理念：

> Namespace 解决“进程在哪里”，cgroup 解决“资源能用多少”，而视图隔离解决“容器认为自己是谁”。

对微擎面板来说，这一点尤其重要。面板场景并不是单纯把一个进程跑起来，而是要为用户提供一个稳定、可信、可观测的运行环境。用户看到的资源、应用感知到的资源、平台实际限制的资源，三者必须尽可能一致。

---

## 三、为什么我们选择基于 Sysbox 实现系统容器视图隔离

Sysbox 是一个面向系统容器的容器运行时增强方案。它的目标不是替代 Docker 或 containerd，而是在 OCI runtime 层增强容器能力，让容器能够更接近轻量虚拟机的体验。

与普通容器相比，Sysbox 更关注这类场景：

- 容器内运行 systemd；
- 容器内运行 Docker；
- 容器内运行 Kubernetes / K3s；
- 容器作为开发环境、构建环境或轻量系统环境；
- 在不使用 privileged container 的前提下提供更完整的系统能力。

Sysbox 主要由几个组件协作完成：

| 组件 | 职责 |
|---|---|
| `sysbox-runc` | 基于 OCI runc 的运行时前端，负责容器启动阶段的 namespace、mount、spec 改造 |
| `sysbox-fs` | 用户态文件系统，负责虚拟化 `/proc`、`/sys` 等系统视图 |
| `sysbox-mgr` | 管理系统容器状态，并与 runtime、fs 协作 |

其中，和视图隔离最相关的是 `sysbox-runc` 与 `sysbox-fs`。

在 `sysbox-fs` 的代码中，其定位写得非常清楚：

```go
usage string = `sysbox-fs file-system

sysbox-fs is a daemon that emulates portions of the system container's
file system (e.g., procfs, sysfs). It's purpose is to make the
system container closely resemble a virtual host while ensuring
proper isolation.
`
```

这段代码来自：

```text
sysbox-fs/cmd/sysbox-fs/main.go
```

也就是说，`sysbox-fs` 的核心目标就是：

> 模拟系统容器中的部分文件系统，让系统容器更像一个虚拟主机，同时保证隔离性。

这正好契合微擎面板团队希望实现的目标：让容器不只是一个被限制的进程，而是一个拥有一致系统视图的轻量隔离环境。

---

## 四、启动阶段：sysbox-runc 如何把虚拟资源视图挂进容器

视图隔离的第一步，是在容器启动阶段，把关键系统路径替换成由 `sysbox-fs` 提供的虚拟路径。

相关代码位于：

```text
sysbox-runc/libsysbox/syscont/spec.go
```

其中有一个非常关键的结构：

```go
var SysboxfsMounts = []specs.Mount{
    {
        Destination: "/proc/cpuinfo",
        Source:      filepath.Join(SysboxFsDir, "proc/cpuinfo"),
        Type:        "bind",
        Options:     []string{"rbind", "rprivate"},
    },
    {
        Destination: "/proc/diskstats",
        Source:      filepath.Join(SysboxFsDir, "proc/diskstats"),
        Type:        "bind",
        Options:     []string{"rbind", "rprivate"},
    },
    {
        Destination: "/proc/meminfo",
        Source:      filepath.Join(SysboxFsDir, "proc/meminfo"),
        Type:        "bind",
        Options:     []string{"rbind", "rprivate"},
    },
    {
        Destination: "/proc/stat",
        Source:      filepath.Join(SysboxFsDir, "proc/stat"),
        Type:        "bind",
        Options:     []string{"rbind", "rprivate"},
    },
    {
        Destination: "/proc/loadavg",
        Source:      filepath.Join(SysboxFsDir, "proc/loadavg"),
        Type:        "bind",
        Options:     []string{"rbind", "rprivate"},
    },
}
```

这里的逻辑非常关键。

容器内部访问：

```text
/proc/meminfo
```

实际上会被 bind mount 到：

```text
/var/lib/sysboxfs/proc/meminfo
```

而 `/var/lib/sysboxfs/proc/meminfo` 并不是宿主机原始的 `/proc/meminfo`，而是 `sysbox-fs` 在用户态生成的虚拟文件。

也就是说，容器中的访问路径看起来没变：

```bash
cat /proc/meminfo
```

但它背后的数据源已经被替换成了“容器视图”。

`sysbox-runc` 在这里扮演的是“入口改造者”的角色：它在 OCI spec 层面对容器 mount 结构进行增强，把容易泄露宿主机信息、又被大量系统软件依赖的 `/proc`、`/sys` 节点替换成虚拟节点。

在当前实现中，被虚拟化的资源视图包括：

```text
/proc/cpuinfo
/proc/diskstats
/proc/meminfo
/proc/stat
/proc/slabinfo
/proc/pressure/io
/proc/pressure/cpu
/proc/pressure/memory
/proc/sys
/proc/swaps
/proc/uptime
/proc/loadavg
/sys/devices/system/cpu/online
/sys/devices/system/cpu/present
/sys/kernel
/sys/devices/virtual
/sys/module/nf_conntrack/parameters
```

可以把这个流程理解为：

```text
container process
      |
      | read /proc/meminfo
      v
/proc/meminfo inside container
      |
      | bind mount
      v
/var/lib/sysboxfs/proc/meminfo
      |
      | FUSE handler
      v
sysbox-fs calculates container-specific view
```

这一步解决的是：**容器访问系统信息时，入口必须先被接管。**

---

## 五、读取阶段：sysbox-fs 如何生成容器自己的资源视图

仅仅把路径挂进去还不够。真正决定视图隔离质量的，是 `sysbox-fs` 如何生成这些文件内容。

相关代码位于：

```text
sysbox-fs/handler/implementations/procResources.go
```

其中定义了一批资源 handler：

```go
var (
    ProcCpuinfo_Handler                = newReadOnlyResource("ProcCpuinfo", "/proc/cpuinfo", readCPUInfo)
    ProcDiskstats_Handler              = newReadOnlyResource("ProcDiskstats", "/proc/diskstats", readDiskstats)
    ProcMeminfo_Handler                = newReadOnlyResource("ProcMeminfo", "/proc/meminfo", readMemInfo)
    ProcStat_Handler                   = newReadOnlyResource("ProcStat", "/proc/stat", readProcStat)
    ProcSlabinfo_Handler               = newReadOnlyResource("ProcSlabinfo", "/proc/slabinfo", readSlabinfo)
    ProcLoadavg_Handler                = newReadOnlyResource("ProcLoadavg", "/proc/loadavg", readLoadavg)
    ProcPressureIO_Handler             = newReadOnlyResource("ProcPressureIO", "/proc/pressure/io", readPressure("io", "blkio", "io.pressure"))
    ProcPressureCPU_Handler            = newReadOnlyResource("ProcPressureCPU", "/proc/pressure/cpu", readPressure("cpu", "cpu", "cpu.pressure"))
    ProcPressureMemory_Handler         = newReadOnlyResource("ProcPressureMemory", "/proc/pressure/memory", readPressure("memory", "memory", "memory.pressure"))
    SysDevicesSystemCpuOnline_Handler  = newReadOnlyResource("SysDevicesSystemCpuOnline", "/sys/devices/system/cpu/online", readCPUOnline)
    SysDevicesSystemCpuPresent_Handler = newReadOnlyResource("SysDevicesSystemCpuPresent", "/sys/devices/system/cpu/present", readCPUPresent)
)
```

这段代码可以看出，`sysbox-fs` 并不是简单做文件转发，而是为不同系统资源定义了不同的读取逻辑。

例如：

- `/proc/meminfo` 由 `readMemInfo` 生成；
- `/proc/cpuinfo` 由 `readCPUInfo` 生成；
- `/proc/loadavg` 由 `readLoadavg` 生成；
- `/proc/pressure/cpu` 由 `readPressure("cpu", ...)` 生成；
- `/sys/devices/system/cpu/online` 由 `readCPUOnline` 生成。

这些函数背后会结合容器对应的 cgroup、cpuset、pressure、任务状态等信息，生成更贴近容器自身边界的资源视图。

这里还有一个很关键的抽象：

```go
type readOnlyResource struct {
    domain.HandlerBase
    read      resourceReader
    snapshots map[string]resourceSnapshot
    mu        sync.Mutex
}
```

每个虚拟资源都是一个只读资源：

- `read` 负责真正生成内容；
- `snapshots` 负责短时间缓存；
- `mu` 负责并发保护；
- `HandlerBase` 负责和 FUSE handler 框架集成。

这说明 `sysbox-fs` 的资源视图不是静态文件，而是一个按请求动态生成的用户态视图。

---

## 六、同一个 `/proc/meminfo`，不同容器应该看到不同内容

视图隔离有一个非常关键的要求：不同容器不能共享同一份资源视图。

在 `procResources.go` 中，有一段代码值得单独拿出来看：

```go
func (h *readOnlyResource) snapshotKey(req *domain.HandlerRequest) string {
    id := ""
    if req.Container != nil {
        id = req.Container.ID()
    }
    if id == "" {
        id = fmt.Sprintf("pid:%d", req.Pid)
    }
    return h.Path + ":" + id
}
```

这段代码的作用是为资源 snapshot 生成 key。

它不是简单以路径作为 key：

```text
/proc/meminfo
```

而是把路径和容器 ID 或 PID 组合起来：

```text
/proc/meminfo:<container-id>
```

这意味着，即使多个容器都在读取 `/proc/meminfo`，`sysbox-fs` 也可以为它们维护不同的缓存和不同的视图。

这就是视图隔离中的一个重要细节：

> 视图不是全局的，而是容器级的。

如果没有这个设计，就可能出现一个容器读取到另一个容器资源视图的风险，或者多个容器共享同一份不准确缓存。

在多租户、面板托管、系统容器场景中，这类细节非常重要。因为平台要保证的不仅是“资源限制生效”，还要保证“用户看到的资源边界可信”。

---

## 七、为什么还要拦截 mount：防止容器绕过虚拟视图

如果只在容器启动时把 `/proc/meminfo`、`/proc/cpuinfo` 等路径 bind 到 `sysbox-fs`，是否就足够了？

答案是不够。

因为系统容器内部可能运行 systemd、Docker、Kubernetes 或其他系统级软件，这些软件在运行过程中可能重新 mount `/proc` 或 `/sys`。

如果不处理这种情况，容器内部进程有可能通过重新挂载拿到内核原始 procfs / sysfs，从而绕过 `sysbox-fs` 的虚拟视图。

这也是 Sysbox 和普通容器增强方案很不一样的地方：它不仅处理启动时的静态挂载，还处理运行时的动态挂载行为。

相关代码位于：

```text
sysbox-fs/seccomp/mount.go
```

其中有一个函数专门处理 procfs mount：

```go
func (m *mountSyscallInfo) processProcMount(
    mip domain.MountInfoParserIface) (*sysResponse, error) {

    payload := m.createProcPayload(mip)
    if payload == nil {
        return nil, fmt.Errorf("Could not construct procMount payload")
    }

    nss := m.tracer.service.nss
    event := nss.NewEvent(
        m.syscallCtx.pid,
        m.syscallCtx.uid,
        m.syscallCtx.gid,
        &domain.AllNSs,
        0,
        &domain.NSenterMessage{
            Type:    domain.MountSyscallRequest,
            Payload: payload,
        },
        nil,
        false,
    )

    err := nss.SendRequestEvent(event)
    if err != nil {
        return nil, err
    }

    responseMsg := nss.ReceiveResponseEvent(event)
    ...
}
```

这段逻辑说明，当容器内部触发 procfs mount 时，`sysbox-fs` 会参与处理这个 mount 请求，并通过 nsenter 进入对应 namespace 执行挂载动作。

更关键的是 `createProcPayload`：

```go
func (m *mountSyscallInfo) createProcPayload(
    mip domain.MountInfoParserIface) *[]*domain.MountSyscallPayload {

    var payload []*domain.MountSyscallPayload

    payload = append(payload, m.MountSyscallPayload)

    mh := m.tracer.service.mts.MountHelper()

    procBindMounts := mh.ProcMounts()
    for _, v := range procBindMounts {
        relPath := strings.TrimPrefix(v, "/proc")

        newelem := &domain.MountSyscallPayload{
            domain.NSenterMsgHeader{},
            domain.Mount{
                Source: v,
                Target: filepath.Join(m.Target, relPath),
                FsType: "",
                Flags:  unix.MS_BIND,
                Data:   "",
            },
        }
        payload = append(payload, newelem)
    }

    return &payload
}
```

这段代码的核心逻辑是：

1. 先处理原始 `/proc` mount；
2. 找出 sysbox-fs 管理的 `/proc` bind mount；
3. 将这些虚拟节点重新 bind 到新的 `/proc` 下面。

也就是说，即使容器内部重新挂载 `/proc`，Sysbox 也会把虚拟化节点重新挂回去，确保容器不能绕过视图隔离。

`/sys` 也有类似处理逻辑：

```go
func (m *mountSyscallInfo) processSysMount(
    mip domain.MountInfoParserIface) (*sysResponse, error) {
    ...
}

func (m *mountSyscallInfo) createSysPayload(
    mip domain.MountInfoParserIface) *[]*domain.MountSyscallPayload {
    ...
}
```

这一点很关键。

视图隔离不是“启动时替换几个文件”这么简单，而是必须覆盖容器生命周期中的动态行为。尤其是系统容器内部运行 systemd、Docker、Kubernetes 时，mount 行为非常常见。如果不拦截，视图隔离就可能被绕过。

---

## 八、资源视图如何与 cgroup 对齐

视图隔离的最终目标不是“伪造一份看起来更小的数据”，而是要让容器内部系统信息与 cgroup 资源边界保持一致。

微擎面板团队在 `w7panel` 分支上的相关工作，也集中体现了这个方向。

从 `sysbox-fs` 的近期提交可以看到：

```text
411c926 Align loadavg sampling with LXCFS
29dbff0 Allow idle loadavg task counts
f0450e1 Align resource views with container cgroups
0ca850b Keep loadavg active for non-empty containers
8436935 Align proc resource fallbacks
d1c745c Align diskstats with cgroup io view
90b43a3 Align cpuinfo with cpuset view
198ce22 Prune init scope for loadavg cgroup scan
eb9848f Virtualize loadavg from cgroup tasks
fed0549 Align swap view with lxcfs semantics
53b5f36 Fix isolated swap view
37e9b57 feat: add stateful proc stat cpu view
```

这些提交背后体现的是一个持续演进的目标：让容器内部资源视图尽可能与容器实际资源边界一致。

可以按资源类型理解：

| 资源类型 | 对应方向 | 技术含义 |
|---|---|---|
| CPU | `Align cpuinfo with cpuset view` | 容器看到的 CPU 与 cpuset 对齐 |
| 内存 | `readMemInfo` / cgroup memory | `/proc/meminfo` 反映容器内存限制 |
| IO | `Align diskstats with cgroup io view` | 磁盘统计接近容器维度 |
| Loadavg | `Virtualize loadavg from cgroup tasks` | 负载从容器任务维度采样 |
| PSI | `/proc/pressure/*` handlers | pressure 指标对齐 cgroup |
| Swap | `Fix isolated swap view` | swap 视图不直接泄露宿主机全局信息 |
| CPU 时间 | `stateful proc stat cpu view` | `/proc/stat` 维护容器维度 CPU 时间视图 |

这也是我们认为“视图隔离”具备工程价值的原因。

它不是简单隐藏几个字段，而是把容器内不同系统接口统一拉回到同一套资源边界上：

```text
cgroup limit
    |
    +-- /proc/meminfo
    +-- /proc/stat
    +-- /proc/loadavg
    +-- /proc/pressure/*
    +-- /sys/devices/system/cpu/*
```

只有当这些视图都围绕同一个容器边界生成时，容器内部应用、监控、面板展示和平台限制之间才会保持一致。

---

## 九、loadavg：一个容易被低估的隔离细节

在普通 Linux 系统中，`/proc/loadavg` 是很多监控系统和应用组件判断系统负载的重要依据。

但在容器场景中，loadavg 的语义并不简单。

如果容器看到的是宿主机 loadavg，那么一个繁忙宿主机上的空闲容器也会误以为自己负载很高。反过来，如果只做简单裁剪，又可能无法反映容器内真实任务状态。

从提交历史可以看到，`sysbox-fs` 对 loadavg 做了多轮增强：

```text
Virtualize loadavg from cgroup tasks
Prune init scope for loadavg cgroup scan
Keep loadavg active for non-empty containers
Allow idle loadavg task counts
Align loadavg sampling with LXCFS
```

这说明 loadavg 并不是一个简单文件替换问题，而是需要围绕 cgroup 中的 task 状态持续采样和计算。

这类细节正是系统容器和普通应用容器的重要区别。

普通应用容器可能只关心进程能不能跑；系统容器需要让容器内的软件相信自己运行在一个相对完整、稳定、可信的系统环境中。

---

## 十、time namespace：系统视图隔离的进一步延伸

除了 CPU、内存、IO、loadavg 等资源视图，时间视图也是系统容器中的重要一环。

在 `sysbox-runc` 的近期提交中，可以看到 time namespace 相关工作：

```text
a6060c8d fix: set syscont time namespace offsets
cf83133d syscont: add time namespace to sys container by default
34bb0bb8 feat: add time namespace support (CLONE_NEWTIME)
```

这说明系统容器的隔离边界并不局限于传统资源指标，也包括 uptime、启动时间、时间偏移等系统语义。

对于容器内运行 systemd、监控 agent、调度组件、Kubernetes 节点组件等场景来说，时间视图同样会影响系统判断。

因此，从更大的角度看，视图隔离可以继续扩展为：

```text
资源视图隔离：CPU、内存、IO、pressure、loadavg
文件系统视图隔离：/proc、/sys
时间视图隔离：uptime、boottime、time namespace
权限视图隔离：capability、user namespace
```

这也是微擎面板团队持续关注 Sysbox 的原因：它提供的是一套更接近“系统级容器”的基础设施，而不只是一个普通 runtime 替换。

---

## 十一、为什么这对微擎面板很重要

微擎面板面对的不是单一容器运行场景，而是应用托管、运行环境管理、站点管理、多租户资源分配等更复杂的场景。

在这类场景下，容器要解决的不只是“跑起来”，还包括：

- 用户看到的资源是否真实；
- 应用感知到的资源是否准确；
- 面板展示的数据是否与容器内部一致；
- 多租户之间是否存在宿主机信息泄露；
- 容器内部是否可以运行更复杂的系统级服务；
- 平台是否能提供接近轻量虚拟机的隔离体验。

这也是我们推进 Sysbox 视图隔离实践的核心原因。

我们希望构建的不是一个简单的容器沙箱，而是一个更可信的轻量系统环境。

可以用一句话概括：

> 微擎面板团队希望把容器从“受限制的进程”推进到“拥有一致系统观的隔离环境”。

在这个目标下，视图隔离是非常关键的一环。

---

## 十二、从行业角度看：容器隔离的下一步是系统观一致性

过去几年，容器行业更多关注的是：

- 启动速度；
- 镜像分发；
- 编排能力；
- 资源限制；
- 网络与存储；
- 安全加固。

这些都很重要。

但随着容器承载的工作负载越来越复杂，尤其是当容器开始承载开发环境、CI/CD 环境、轻量虚拟机、系统服务、边缘节点、面板托管环境时，单纯的进程级隔离已经不够。

下一阶段的容器隔离，应该更多关注系统观一致性。

所谓系统观一致性，就是：

> 容器能使用多少资源，容器内部就应该看到多少资源；
> 容器被隔离在哪个边界内，它的系统接口就应该反映哪个边界；
> 平台如何限制容器，容器内部就应该如何感知自己。

Sysbox 提供了一个非常有价值的方向：通过 runtime、FUSE、seccomp、namespace、cgroup 的协同，把容器的系统视图重新构造出来。

微擎面板团队基于这一方向进行了工程化落地和增强，让它更贴近面板、托管、多租户和系统容器场景中的实际需求。

这不是对传统容器技术的否定，而是对容器隔离能力的一次补足。

---

## 十三、总结：容器隔离不止是边界，也是视图

容器技术发展到今天，已经不仅仅是“把进程装进 namespace”这么简单。

对于面板、PaaS、多租户、系统容器这类场景，我们需要更完整的容器抽象：

```text
可信容器环境 = 执行隔离 + 资源隔离 + 视图隔离
```

执行隔离让容器不能越界操作。

资源隔离让容器不能超额使用。

视图隔离让容器不能看到不属于自己的系统状态。

微擎面板团队基于 Sysbox 的实践，正是围绕第三点展开：通过 `sysbox-runc` 在启动阶段注入虚拟化挂载点，通过 `sysbox-fs` 在用户态接管 `/proc`、`/sys` 关键节点，通过 cgroup、cpuset、pressure、loadavg 等信息重新生成容器自己的系统视图，并通过 mount syscall 拦截防止运行时绕过。

这项工作让容器内部系统信息与资源边界更加一致，也让容器更接近一个可信、稳定、可观测的轻量系统环境。

我们相信，容器行业下一阶段的进步，不只是更快的启动速度、更高的资源密度，也包括更真实、更一致、更可信的隔离体验。

微擎面板团队也会继续在这个方向上投入，让容器成为更可靠的应用运行底座。

---

## 附：核心代码路径

本文涉及的关键实现主要分布在以下路径。

### `sysbox-runc/libsysbox/syscont/spec.go`

用于在容器启动阶段注入 sysbox-fs 虚拟化挂载点，例如：

```text
/proc/cpuinfo
/proc/meminfo
/proc/stat
/proc/loadavg
/proc/pressure/*
/sys/devices/system/cpu/online
```

### `sysbox-fs/handler/implementations/procResources.go`

用于实现 `/proc`、`/sys` 关键资源的用户态 handler，例如：

```go
ProcCpuinfo_Handler
ProcMeminfo_Handler
ProcStat_Handler
ProcLoadavg_Handler
ProcPressureCPU_Handler
ProcPressureMemory_Handler
ProcPressureIO_Handler
SysDevicesSystemCpuOnline_Handler
```

### `sysbox-fs/seccomp/mount.go`

用于处理容器运行时 mount 行为，确保重新挂载 `/proc`、`/sys` 时仍然保留 sysbox-fs 提供的虚拟视图。

### `sysbox-fs/cmd/sysbox-fs/main.go`

用于说明 sysbox-fs 的定位：模拟系统容器中的部分文件系统，让系统容器更接近 virtual host，同时保证隔离性。
