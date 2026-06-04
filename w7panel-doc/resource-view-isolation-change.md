# Sysbox 资源视图隔离改动说明

## 涉及仓库

本次需求在以下仓库修改：

- 主仓库 `sysbox`
  - 新增本文档。
  - 新增编译与 Docker 验证脚本：`docs/build-and-test-resource-view.sh`。
  - 主仓库本身没有改业务代码，但会记录 `sysbox-fs`、`sysbox-runc` 子模块指针变化。
- 子模块 `sysbox-fs`
  - 分支：`w7panel`
  - 负责新增 FUSE 虚拟文件实现，并把新增资源加入 sysbox-fs handler 与 mount 跟踪列表。
- 子模块 `sysbox-runc`
  - 分支：`w7panel`
  - 负责把新增 sysbox-fs 虚拟资源 bind mount 到容器内。

未修改业务逻辑的仓库：

- `sysbox-mgr`：仅用于编译和运行测试，没有保留代码改动。
- `sysbox-ipc`：仅生成过 protobuf 文件用于编译，最终没有保留代码改动。
- `sysbox-libs`：编译时 Go 曾自动调整 `go.mod/go.sum`，已还原，没有保留代码改动。

## 修改文件

### `sysbox-fs/handler/implementations/procResources.go`

新增文件，提供通用只读资源 handler 和资源视图模拟逻辑。

主要改动：

- 新增 `readOnlyResource` 通用 handler，用于只读 `/proc`、`/sys` 单文件模拟。
- 新增 cgroup 读取 helper：
  - 通过请求进程或容器 init pid 读取 `/proc/<pid>/cgroup`。
  - 支持 cgroup v2 与 cgroup v1。
  - 对 cgroup v2 支持向父 cgroup 回溯读取有效限制，解决 Sysbox 容器内 init 进程位于 `/init.scope`，而 Docker 限制位于父 cgroup 的情况。
- 新增以下虚拟文件 handler：
  - `/proc/cpuinfo`
  - `/proc/diskstats`
  - `/proc/meminfo`
  - `/proc/stat`
  - `/proc/slabinfo`
  - `/proc/pressure/io`
  - `/proc/pressure/cpu`
  - `/proc/pressure/memory`
  - `/sys/devices/system/cpu/online`
- `/proc/meminfo`
  - 从 cgroup `memory.max`、`memory.current` 等信息生成容器内存视图。
  - 对无限制或无法解析的情况回退宿主 `/proc/meminfo`，避免破坏容器启动。
- `/proc/cpuinfo`
  - 根据 `cpu.max`、`cpuset.cpus.effective` 等信息限制 CPU 条目数量。
  - 重新编号 `processor` 字段。
- `/proc/stat`
  - 根据有效 CPU 数限制 `cpuN` 行，并重算汇总 `cpu` 行。
- `/sys/devices/system/cpu/online`
  - 优先返回容器 cpuset 视图，否则回退宿主文件。
- `/proc/pressure/*`
  - cgroup v2 下优先读取对应 cgroup 的 pressure 文件。
  - 找不到时回退宿主 `/proc/pressure/*`。
- `/proc/diskstats`
  - 返回空内容，避免继续暴露宿主磁盘统计。
- `/proc/slabinfo`
  - 返回保守 header，避免暴露宿主 slab 详情。
- 修正通用 handler 的 FUSE read 分片逻辑：
  - 按 `req.Offset` 和 `req.Data` 大小拷贝数据。
  - 避免返回超过 FUSE read 请求大小导致 `Input/output error`。

### `sysbox-fs/handler/implementations/procPressure.go`

新增文件，提供 `/proc/pressure` 父目录 handler。

主要改动：

- 新增只读目录 `/proc/pressure`。
- 目录下声明 `io`、`cpu`、`memory` 三个只读文件。
- 解决 bind mount `/proc/pressure/{io,cpu,memory}` 时父目录 lookup 不稳定的问题。

### `sysbox-fs/handler/handlerDB.go`

修改默认 handler 注册表。

主要改动：

- 注册新增 handler：
  - `ProcCpuinfo_Handler`
  - `ProcDiskstats_Handler`
  - `ProcMeminfo_Handler`
  - `ProcStat_Handler`
  - `ProcSlabinfo_Handler`
  - `ProcPressure_Handler`
  - `ProcPressureIO_Handler`
  - `ProcPressureCPU_Handler`
  - `ProcPressureMemory_Handler`
  - `SysDevicesSystemCpuOnline_Handler`

### `sysbox-fs/mount/service.go`

修改 sysbox-fs 管理的 procfs/sysfs mount 跟踪列表。

主要改动：

- 在 `ProcfsMounts` 中新增：
  - `/proc/cpuinfo`
  - `/proc/diskstats`
  - `/proc/meminfo`
  - `/proc/stat`
  - `/proc/slabinfo`
  - `/proc/pressure/io`
  - `/proc/pressure/cpu`
  - `/proc/pressure/memory`
- 在 `SysfsMounts` 中新增：
  - `/sys/devices/system/cpu/online`
- 作用：
  - 让这些路径在 sysbox-fs mount 管理、嵌套 procfs/sysfs、chroot、容器内重新 mount procfs/sysfs 等路径中被识别为 sysbox-fs 管理资源。

### `sysbox-runc/libsysbox/syscont/spec.go`

修改容器 spec 中的 sysbox-fs mount 列表。

主要改动：

- 在 `SysboxfsMounts` 中新增以下 bind mount：
  - `/proc/cpuinfo`
  - `/proc/diskstats`
  - `/proc/meminfo`
  - `/proc/stat`
  - `/proc/slabinfo`
  - `/proc/pressure/io`
  - `/proc/pressure/cpu`
  - `/proc/pressure/memory`
  - `/sys/devices/system/cpu/online`
- 删除注释区里已实现路径的旧 TODO 注释，保留尚未实现的 `/proc/cgroups`、`/proc/devices`、`/proc/loadavg` 等未来项。
- 作用：
  - Docker 使用 `--runtime=sysbox-runc` 创建容器时，新增路径会从 `/var/lib/sysboxfs/<container-id>/...` bind mount 到容器内。

## 编译与验证

编译脚本：

```bash
docs/build-and-test-resource-view.sh
```

脚本做的事情：

- 安装运行和编译依赖：
  - `protobuf-compiler`
  - `pkg-config`
  - `libseccomp-dev`
  - `rsync`
  - `fuse3`
- 安装旧版 `protoc-gen-go@v1.3.5` 到 `/tmp/sysbox-build-bin`。
- 使用 `GOPROXY=https://goproxy.cn,direct` 编译：
  - `sysbox-ipc`
  - `sysbox-fs`
  - `sysbox-runc`
  - `sysbox-mgr`
- 安装本地编译二进制到 `/usr/bin`。
- 备份 `/etc/docker/daemon.json` 到 `/etc/docker/daemon.json.sysbox-test-bak`。
- 为 Docker 增加 runtime：
  - `sysbox-runc` -> `/usr/bin/sysbox-runc`
- 启动：
  - `sysbox-mgr`
  - `sysbox-fs`
- 使用 Docker 启动测试容器：

```bash
docker run --rm --runtime=sysbox-runc --memory=128m --cpus=1 busybox:latest ...
```

已验证结果：

- 下列路径都挂载为 `fuse sysboxfs`：
  - `/proc/cpuinfo`
  - `/proc/diskstats`
  - `/proc/meminfo`
  - `/proc/stat`
  - `/proc/slabinfo`
  - `/proc/swaps`
  - `/proc/uptime`
  - `/proc/pressure/io`
  - `/proc/pressure/cpu`
  - `/proc/pressure/memory`
  - `/sys/devices/system/cpu/online`
- 文件读取正常，无 `Input/output error`。
- 使用 `--memory=128m` 时，容器内 `/proc/meminfo` 显示：

```text
MemTotal:          131072 kB
```

- 使用 `--cpus=1` 时，容器内 `/proc/stat` 只暴露一个 CPU 行：

```text
cpu ...
cpu0 ...
```

## 注意事项

- 脚本会修改本机 Docker runtime 配置，并会启动本地编译的 Sysbox daemons。
- 脚本不会保存 sudo 密码；需要 sudo 时由终端提示输入。
- Docker 配置备份文件为：

```text
/etc/docker/daemon.json.sysbox-test-bak
```

- 当前实现是第一版资源视图隔离：
  - `/proc/meminfo`、`/proc/cpuinfo`、`/proc/stat` 已按 cgroup 限制生成主要视图。
  - `/proc/diskstats`、`/proc/slabinfo` 当前采用保守模拟，避免暴露宿主详细信息。
  - `/proc/pressure/*` 优先使用 cgroup v2 pressure 数据，找不到时回退宿主 pressure 文件。
