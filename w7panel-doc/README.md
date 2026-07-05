# Sysbox 从源码构建 & 对接 K3s/containerd

在 **Debian 13 (trixie)** + **kernel 6.12** + **containerd 2.x** 环境下，从源码构建 Sysbox 并配置 K3s 使用 sysbox-runc 运行系统容器。

---

## 目录

- [Sysbox 组件详解](#sysbox-组件详解)
- [背景与问题](#背景与问题)
- [环境要求](#环境要求)
- [快速开始](#快速开始)
- [构建步骤](#构建步骤)
- [K3s 配置](#k3s-配置)
- [验证](#验证)
- [已知限制](#已知限制)
- [参考](#参考)

---

## Sysbox 组件详解

Sysbox 是一个开源的"系统容器"运行时，由以下 **4 个核心组件** + **2 个辅助组件**组成：

### 核心组件（需要编译）

| 组件 | 二进制 | 是否需编译 | 编译命令 | 大小 | 说明 |
|---|---|---|---|---|---|
| **sysbox-runc** | `/usr/bin/sysbox-runc` | **是** | `make -C sysbox-runc` | ~27MB | Sysbox 的前端：OCI 容器运行时，Docker/containerd 调用它来启动系统容器 |
| **sysbox-fs** | `/usr/bin/sysbox-fs` | **是** | `make -C sysbox-fs` | ~20MB | FUSE 守护进程：模拟容器内的 procfs/sysfs 和部分系统调用 |
| **sysbox-mgr** | `/usr/bin/sysbox-mgr` | **是** | `make -C sysbox-mgr` | ~22MB | Sysbox 管理器：分配 subuid/subgid、管理卷挂载和数据 |
| **sysbox-ipc** | （无独立二进制，已编译到其他组件中） | **是** | `make sysbox-ipc` | — | 组件间 gRPC 通信层，其他三个组件都依赖它 |

### 辅助组件（无需编译）

| 组件 | 是否需编译 | 说明 |
|---|---|---|
| **sysbox-libs** | **否** | 共享库：capability 管理、UID/GID 映射、overlayfs 工具、pid 监控等。其他组件编译时引用源码 |
| **sysbox-pkgr** | **否** | 打包工具：将编译产物打包为 `.deb` / `.rpm` 安装包 |
| **sysbox-dockerfiles** | **否** | 官方系统容器镜像的 Dockerfile 示例（如 ubuntu-bionic-systemd-docker） |
| **scr** | **否** | 辅助脚本：容器调试、PID 追踪、清理等 |

### 各组件详细说明

#### sysbox-runc — 容器运行时前端

- **作用**：OCI 兼容的容器运行时，是 runc 的 fork，增加了对系统容器（system container）的支持
- **核心功能**：
  - 设置 Linux user namespace，为容器分配独立的 UID/GID 映射
  - 接管 namespace（mount、pid、net、ipc、uts、time、cgroup、user）
  - 与 sysbox-fs/sysbox-mgr 通过 gRPC 通信，完成容器初始化
- **依赖**：sysbox-ipc、sysbox-libs
- **上游**：fork 自 [opencontainers/runc](https://github.com/opencontainers/runc)

#### sysbox-fs — 文件系统仿真守护进程

- **作用**：FUSE 守护进程，在容器内 `/proc` 和 `/sys` 上覆盖 FUSE 文件系统，暴露和模拟内核尚未 namespace 化的资源
- **核心功能**：
  - **procfs/sysfs 仿真**：模拟 `/proc/cpuinfo`、`/proc/meminfo`、`/proc/sys/kernel/*` 等文件
  - **系统调用拦截**：通过 seccomp BPF 拦截 mount/umount 等系统调用
  - 确保容器看到的 `/proc` 和 `/sys` 看起来像真实的虚拟机
- **依赖**：sysbox-ipc、sysbox-libs
- **通信**：通过 gRPC 与 sysbox-runc 交互

#### sysbox-mgr — Sysbox 管理器

- **作用**：为 sysbox-runc 和 sysbox-fs 提供后台服务
- **核心功能**：
  - **subid 分配**：为每个系统容器分配独立的 subuid/subgid 范围
  - **shiftfs/idmapped mount 管理**：在需要时启用 ID 映射挂载
  - **Docker/containerd 数据卷管理**：为容器内的 `/var/lib/docker` 等目录在宿主机创建独立存储，避免 overlayfs 嵌套
  - **Kubelet 数据卷管理**：管理容器内 `/var/lib/kubelet` 的存储
- **依赖**：sysbox-ipc、sysbox-libs

#### sysbox-ipc — 进程间通信层

- **作用**：组件间 gRPC 通信的协议定义和代码生成
- **内容**：
  - `sysboxFsGrpc/` — sysbox-fs 的 gRPC 协议（protobuf 定义 + 生成的 Go 代码）
  - `sysboxMgrGrpc/` — sysbox-mgr 的 gRPC 协议
  - `unix/` — Unix 域套接字工具
- **编译产物**：生成的 `.pb.go` 文件被其他组件引用，无独立二进制

#### sysbox-libs — 共享库

- **作用**：提供各组件共用的工具函数和库
- **内容**：
  - `idMap/` — UID/GID 映射管理
  - `mount/` — 挂载工具
  - `shiftfs/` — shiftfs 模块管理
  - `overlayUtils/` — overlayfs 工具
  - `pidmonitor/` — PID 监控
  - `capability/` — Linux capability 管理
  - `idShiftUtils/` — ID 偏移工具

#### sysbox-pkgr — 打包工具

- **作用**：将编译产物打包为 `.deb`（Debian/Ubuntu）或 `.rpm`（Fedora/CentOS）格式
- **用途**：用于制作官方发行版安装包

#### sysbox-dockerfiles — 示例镜像

- **作用**：官方提供的系统容器镜像 Dockerfile
- **包含**：
  - `ubuntu-bionic-systemd-docker` — Ubuntu 18.04 + systemd + Docker
  - `alpine-test` — 轻量测试镜像
- **位置**：`registry.nestybox.com/nestybox/` 或 `ghcr.io/nestybox/`

### K3s/containerd 配置中只需要 sysbox-runc

在 K3s/containerd 的配置中，**只需要注册 sysbox-runc 一个组件**即可：

```toml
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc.options]
  SystemdCgroup = true
  BinaryName = "/usr/bin/sysbox-runc"
```

> **关于 `SystemdCgroup` 说明**：旧版 sysbox 文档要求设为 `false`，这是针对 systemd < v244 的兼容性要求。
> 现代 systemd（v244+）+ cgroup v2 支持 `Delegate=yes`，设为 `true` 后 systemd 会将 cgroup 控制器委托给
> sysbox-runc，不影响 user namespace 隔离功能，同时使 kubelet 写入的 memory limit 能正常生效。

**为什么只配 sysbox-runc？**
- `runtime_type = "io.containerd.runc.v2"` — 告诉 containerd 这是一个兼容 runc v2 接口的运行时
- `BinaryName = "/usr/bin/sysbox-runc"` — containerd 实际调用的二进制路径
- containerd 只与 sysbox-runc 交互（命令行调用），sysbox-runc 内部再通过 gRPC 与 sysbox-fs 和 sysbox-mgr 通信
- sysbox-fs 和 sysbox-mgr 作为 systemd 服务独立运行，containerd **不直接感知**它们的存在

**配置不涉及的其他组件：**
| 组件 | 是否需要配置 | 原因 |
|---|---|---|
| sysbox-runc | ✅ 必须配置 | containerd 通过它创建容器 |
| sysbox-fs | ❌ 不配 | 作为 systemd 服务独立运行，sysbox-runc 自动连接它 |
| sysbox-mgr | ❌ 不配 | 作为 systemd 服务独立运行，sysbox-runc 自动连接它 |
| sysbox-ipc | ❌ 不配 | 编译时生成代码，无独立二进制 |
| sysbox-libs | ❌ 不配 | 编译时引用源码，无独立运行形态 |
| sysbox-dockerfiles | ❌ 不配 | 只是镜像 Dockerfile，不是 Sysbox 运行组件 |

### Sysbox 服务管理

Sysbox 安装后包含三个 systemd 服务：

```bash
# 查看服务状态
systemctl status sysbox          # 顶层服务（编排 sysbox-fs + sysbox-mgr）
systemctl status sysbox-fs       # FUSE 文件系统仿真守护进程
systemctl status sysbox-mgr      # Sysbox 管理器

# 启动（分步启动，确保顺序）
systemctl start sysbox-mgr       # 先启动管理器
systemctl start sysbox-fs        # 再启动文件系统仿真
# 或一键启动（通过 sysbox.service 编排）
systemctl start sysbox

# 停止
systemctl stop sysbox sysbox-fs sysbox-mgr

# 重启
systemctl restart sysbox-fs sysbox-mgr

# 查看日志
journalctl -u sysbox-fs -u sysbox-mgr --no-pager -n 50
```

**启动顺序说明**：
1. `sysbox-mgr` 必须先启动（提供 subid 分配等基础服务）
2. `sysbox-fs` 随后启动（提供 FUSE 文件系统仿真）
3. `sysbox-runc` 不需要 systemd 管理，它由 containerd/Docker 按需调用

**安装后重启 Docker**（如果使用 Docker 运行 Sysbox 容器）：

```bash
# 注册 sysbox-runc 到 Docker
cat /etc/docker/daemon.json | grep -q "sysbox-runc" || {
  sudo tee -a /etc/docker/daemon.json <<<'EOF'
{
    "runtimes": {
        "sysbox-runc": {
            "path": "/usr/bin/sysbox-runc"
        }
    }
}
EOF'
}

# 重启 Docker 使其生效
sudo systemctl daemon-reload
sudo systemctl restart docker

# 验证运行时已注册
docker info | grep -A5 "Runtimes"
```

**验证服务是否运行**：

```bash
# 检查进程
pgrep -a sysbox

# 输出示例：
# 12345 /usr/bin/sysbox-fs
# 12346 /usr/bin/sysbox-mgr

# 验证 sysbox-runc 可执行
/usr/bin/sysbox-runc --version

# 验证 features 命令（containerd 兼容性检测）
/usr/bin/sysbox-runc features 2>&1 | head -5
```

### 组件依赖关系

```
sysbox-ipc  ←── sysbox-runc
    │              ├── sysbox-fs     ←── sysbox-libs
    │              └── sysbox-mgr    ←── sysbox-libs
    └── (gRPC protobuf 代码，编译时生成)
```

**编译顺序**：
1. 先编译 `sysbox-ipc`（生成 gRPC 代码）
2. 再并行编译 `sysbox-runc`、`sysbox-fs`、`sysbox-mgr`

---

## 背景与问题

Sysbox 官方发布的 v0.7.0 deb 包在 containerd 2.x + kernel 6.x 环境下存在两个兼容性问题：

| 问题 | GitHub Issue | 状态 |
|---|---|---|
| containerd v2 插件命名空间不匹配（v1 vs v2） | [#1013](https://github.com/nestybox/sysbox/issues/1013) | 手动配置模板可解决 |
| `mount through procfd: operation not permitted`（kernel 6.x 收紧的 /proc/fd 挂载语义） | [#1006](https://github.com/nestybox/sysbox/issues/1006) | 需 runc 1.2+ 基础，官方尚未修复 |

**简单容器（如 ubuntu:22.04 + sleep）可以运行，但需要挂载 sysfs 的系统容器（如含 systemd 的镜像）在 kernel 6.12 上不可用。**

---

## 环境要求

- Debian 13 (trixie) / Ubuntu 22.04+
- Linux kernel 6.x
- containerd 2.x（通过 K3s 或 RKE2）
- Docker（用于编译构建）
- Go 1.21+
- make / gcc / pkg-config / libseccomp-dev / protobuf-compiler

---

## 快速开始

```bash
# 一键构建 & 安装
bash /root/doc/build-sysbox.sh
```

---

## 构建步骤

### 1. 安装依赖

```bash
apt-get update
apt-get install -y make gcc pkg-config libseccomp-dev protobuf-compiler golang-go
go env -w GOPROXY=https://goproxy.cn,direct
go install github.com/golang/protobuf/protoc-gen-go@v1.5.3
ln -sf ~/go/bin/protoc-gen-go /usr/local/bin/protoc-gen-go
```

### 2. 克隆源码（通过 gh-proxy 代理）

```bash
git clone --recursive https://gh-proxy.org/https://github.com/nestybox/sysbox.git /tmp/sysbox
```

### 3. 编译（按依赖顺序）

编译顺序：**sysbox-ipc → sysbox-runc + sysbox-fs + sysbox-mgr**（三个可并行）

```bash
export GOPROXY=https://goproxy.cn,direct
cd /tmp/sysbox

# 第 1 步: 编译 gRPC 通信层（被其他三个组件依赖）
make sysbox-ipc

# 第 2-4 步: 编译三个核心组件（可并行）
make -C sysbox-runc   # OCI 容器运行时 (~27MB)
make -C sysbox-fs     # FUSE 文件系统仿真器 (~20MB)
make -C sysbox-mgr    # 后台管理器 (~22MB)
```

### 4. 安装

```bash
systemctl stop sysbox sysbox-fs sysbox-mgr

cp /tmp/sysbox/sysbox-runc/build/amd64/sysbox-runc /usr/bin/sysbox-runc
cp /tmp/sysbox/sysbox-fs/build/sysbox-fs /usr/bin/sysbox-fs
cp /tmp/sysbox/sysbox-mgr/build/sysbox-mgr /usr/bin/sysbox-mgr

systemctl start sysbox-fs sysbox-mgr
```

---

## Docker 配置

如果使用 Docker 而非 K3s/containerd，需要在 Docker daemon 中注册 sysbox-runc 运行时。

### 配置 daemon.json

```bash
# 创建 /etc/docker/daemon.json（如果已存在则追加配置）
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json <<-'EOF'
{
    "runtimes": {
        "sysbox-runc": {
            "path": "/usr/bin/sysbox-runc"
        }
    },
    "registry-mirrors": [
        "https://docker.m.daocloud.io",
        "https://docker.1panel.live",
        "https://mirror.ccs.tencentyun.com"
    ]
}
EOF
```

> **注意**：`registry-mirrors` 是可选的，用于加速国内 Docker Hub 镜像拉取。如果已有 daemon.json，只需添加 `runtimes` 段。

### 重启 Docker

```bash
sudo systemctl daemon-reload
sudo systemctl restart docker
```

### 验证配置

```bash
# 确认 sysbox-runc 运行时已注册
docker info | grep -A5 "Runtimes"

# 预期输出包含 sysbox-runc
# Runtimes: io.containerd.runc.v2 runc sysbox-runc
```

### 使用 Docker 启动系统容器

```bash
# 启动含 systemd + Docker 的系统容器
docker run -d --runtime=sysbox-runc --name my-syscontainer \
  registry.nestybox.com/nestybox/ubuntu-bionic-systemd-docker \
  /sbin/init

# 进入容器
docker exec -it my-syscontainer bash

# 查看容器内 systemd 和 Docker 运行状态
docker exec my-syscontainer systemctl status docker

# 停止并清理
docker stop my-syscontainer
docker rm my-syscontainer
```

### Docker 与 K3s/containerd 的区别

| 对比项 | Docker | K3s/containerd |
|---|---|---|
| 配置方式 | `daemon.json` 中注册 `runtimes` | containerd 模板中注册 `BinaryName` |
| 系统容器（含 systemd） | 在 kernel 6.12 上 **可用** ✅ | 可能遇到 `mount through procfd` ❌ |
| 简单容器 | 可用 ✅ | 可用 ✅ |
| 调用链 | Docker → sysbox-runc → sysbox-fs/mgr | containerd → sysbox-runc → sysbox-fs/mgr |

---

## K3s 配置

### 创建 containerd 模板

文件：`/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl`

```toml
version = 3
imports = ["/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/*.toml"]
root = "/var/lib/rancher/k3s/agent/containerd"
state = "/run/k3s/containerd"

[grpc]
  address = "/run/k3s/containerd/containerd.sock"

[plugins.'io.containerd.internal.v1.opt']
  path = "/var/lib/rancher/k3s/agent/containerd"

[plugins.'io.containerd.grpc.v1.cri']
  stream_server_address = "127.0.0.1"
  stream_server_port = "10010"

[plugins.'io.containerd.cri.v1.runtime']
  enable_selinux = false
  enable_unprivileged_ports = true
  enable_unprivileged_icmp = true
  device_ownership_from_security_context = false

[plugins.'io.containerd.cri.v1.images']
  snapshotter = "overlayfs"
  disable_snapshot_annotations = false
  use_local_image_pull = true

[plugins.'io.containerd.cri.v1.images'.pinned_images]
  sandbox = "rancher/mirrored-pause:3.6"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
  SystemdCgroup = true

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc.options]
  SystemdCgroup = true
  BinaryName = "/usr/bin/sysbox-runc"

[plugins.'io.containerd.cri.v1.images'.registry]
  config_path = "/var/lib/rancher/k3s/agent/etc/containerd/certs.d"
```

### 重启 K3s

```bash
systemctl restart k3s
```

### 创建 RuntimeClass

```bash
kubectl apply -f - <<EOF
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: sysbox-runc
handler: sysbox-runc
EOF
```

---

## 验证

### 测试简单容器（可运行）

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ubuntu-sys
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  containers:
  - name: ubuntu
    image: ubuntu:22.04
    command: ["sleep", "40000000000"]
  restartPolicy: Never
EOF
```

### 验证运行时注册

```bash
k3s crictl info | grep -A10 "sysbox-runc"
```

### 检查 Sysbox 服务

```bash
systemctl status sysbox-fs sysbox-mgr
```

---

## 已知限制

| 限制项 | 说明 |
|---|---|
| Kernel 兼容性 | kernel 6.x（含 6.12）上 `mount through procfd: operation not permitted` 问题未修复 |
| 系统容器 | 含 systemd 的镜像（需挂载 sysfs）在 kernel 6.12 上不可用 |
| 简单容器 | `ubuntu:22.04` + `sleep` 等无需 sysfs 挂载的容器可正常运行 |
| 上游状态 | sysbox 官方尚未将 runc 基础升级到 1.2+，[Issue #1006](https://github.com/nestybox/sysbox/issues/1006) / [#1013](https://github.com/nestybox/sysbox/issues/1013) 跟踪中 |
| 替代方案 | 如需系统容器，建议在 kernel < 6.2 的环境上使用 Sysbox，或等待官方发布新版 |

---

## 参考

- [Sysbox GitHub](https://github.com/nestybox/sysbox)
- [Sysbox-runc GitHub](https://github.com/nestybox/sysbox-runc)
- [Sysbox Design Doc](https://github.com/nestybox/sysbox/blob/master/docs/user-guide/design.md)
- [K3s + Sysbox 集成教程](https://www.cnblogs.com/k3s2019/p/19171867)
- [Issue #1006: mount through procfd](https://github.com/nestybox/sysbox/issues/1006)
- [Issue #1013: containerd v2 + kernel 6.8 兼容](https://github.com/nestybox/sysbox/issues/1013)
