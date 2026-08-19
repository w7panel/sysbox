# CentOS Stream 9 K3s 验收问题记录

## 验收环境

- 节点：`server1`
- 操作系统：CentOS Stream 9
- 内核：`5.14.0-722.el9.x86_64`
- K3s：`v1.36.3+k3s1`
- containerd：`2.3.2-k3s2`
- kubeconfig：`/home/afan/.kube/114.config`
- 测试镜像：`docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker`
- 启动命令：`/sbin/init`
- rootfs 注解：`io.kubernetes.cri-o.userns-mode: auto:size=65536`

## 问题与原因

### 1. CentOS 7 基础镜像无法构建

旧 K3s 部署镜像使用 CentOS 7，构建时访问 `vault.centos.org` 返回 403，导致 `yum install` 失败。

解决方案：改用国内 CentOS Stream 9 基础镜像：

```text
docker.cnb.cool/i0358/docker-images-chrom/centos-centos:stream9
```

并将镜像依赖安装改为 `dnf --allowerasing`，补充 `procps-ng` 提供 `sysctl`。

### 2. CentOS 9 发行版识别错误

`/etc/os-release` 的 `ID` 值带引号，脚本最初解析为 `"centos"-9`，未命中 `centos-9` 支持分支。

解决方案：解析 `ID` 时去除双引号，并将 `centos-9` 映射到通用 Sysbox 二进制目录。

### 3. 安装容器无法调用宿主 systemd

容器内 `systemctl` 报 `Failed to connect to bus`。CentOS Stream 9 使用 DBus broker，容器内 systemd 客户端不能直接操作宿主 PID 1。

解决方案：

- DaemonSet 设置 `hostPID: true`。
- 挂载宿主 `/run/dbus` 和 `/var/run/dbus`。
- CentOS Stream 9 分支通过 `nsenter` 调用宿主 PID 1 的 `/proc/1/root/usr/bin/systemctl`。

### 4. Debian 专用依赖脚本不适用于 CentOS

`sysbox-installer-helper.sh` 默认执行 `dpkg`、`apt-get`，在 CentOS 上失败；CentOS Stream 9 官方仓库也不提供 `dkms`。

解决方案：

- CentOS 使用 `dnf` 安装 `rsync`、`fuse3`、`fuse-overlayfs`、`iptables`。
- CentOS 跳过 Ubuntu 专用 Shiftfs DKMS 安装，使用内核现有的 idmapped mount 能力。

### 5. CentOS 内核缺少可写的可选 sysctl

CentOS Stream 9 没有 `/proc/sys/kernel/unprivileged_userns_clone`，原脚本的 `sysctl -p` 因该可选项失败。

解决方案：允许该 sysctl 配置步骤继续执行，其他 sysctl 错误仍保留；同时在镜像中安装 `procps-ng`。

### 6. K3s sandbox sysctl 与旧 Sysbox runtime 冲突

Pod 创建时旧 runtime 对以下 K3s sandbox sysctl 写入失败：

```text
net.ipv4.ip_unprivileged_port_start
net.ipv4.ping_group_range
```

解决方案：在 Sysbox runtime 中仅对这两个键的 `EPERM` 忽略，其他 sysctl 写入错误仍返回失败；仅在 CentOS Stream 9 宿主跳过旧 Ubuntu rootfs 不兼容的 `/proc/pressure/{io,cpu,memory}` bind mount，Ubuntu 等宿主仍保留原有 PSI 虚拟化。

### 7. Sysbox Pod 内的嵌套 K3s 无法创建 Pod sandbox

外层 Pod `k3k-admin/k3k-ckm-6hsjh-server-5b896f9fd4-lhgrx` 使用 `sysbox-runc` 正常启动，但其内部 K3s 集群的 CoreDNS、metrics-server、local-path-provisioner 和业务 Pod 均停留在 `ContainerCreating`：

```text
error mounting "proc" to rootfs at "/proc":
mount src=proc, dst=/proc, dstFd=/proc/thread-self/fd/11,
flags=MS_NOSUID|MS_NODEV|MS_NOEXEC: no such file or directory
```

内层复现命令：

```bash
kubectl --kubeconfig /home/afan/.kube/114.config \
  -n k3k-admin exec k3k-ckm-6hsjh-server-5b896f9fd4-lhgrx \
  -c k3k-ckm-6hsjh-server -- \
  sh -c 'KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl -n kube-system describe pod coredns-8b64bcf7c-dhx4m'
```

已确认：

- 错误发生在内层 K3s containerd 使用普通 `runc` 创建 pause sandbox 时。
- 失败的是 OCI spec 的基础 `/proc` 挂载，不是 `/proc/pressure/{io,cpu,memory}` 挂载。
- 外层 Sysbox 容器的 `/proc` 和 sysbox-fs 挂载正常。
- 外层容器根目录为 PVC-backed `fuse-overlayfs`。
- 因所有内层 Pod 同时失败，问题与 CoreDNS 镜像无关。

已测试将内层 K3s snapshotter 从 `overlayfs` 切换为 `native`：配置文件确认 `snapshotter = "native"`，但 CoreDNS、metrics-server、local-path-provisioner 仍以相同的 `/proc` 挂载 `ENOENT` 失败。因此该问题不是内层 overlayfs snapshotter 单独造成的，`native` 不能作为此场景的规避方案。

Ubuntu 可用而当前 CentOS Stream 9 失败仍是待验证差异。高概率关联点是当前外层根目录为 `fuse-overlayfs`，影响内层 runc 对 rootfs 挂载点的解析和 mount propagation；需要在相同 PVC/rootfs 与 K3s 版本下完成 Ubuntu/CentOS 对照，或直接修复 runc/FUSE rootfs 的基础 `/proc` 挂载兼容性。pressure 兼容修改不能解决此问题。

## 最终验证

安装成功后：

- `sysbox-deploy-k8s` DaemonSet：`1/1 Running`
- 节点标签：`sysbox-runtime=running`
- K3s containerd 已注册 `sysbox-runc`
- 测试 Pod：`Running`
- Pod 使用 `runtimeClassName: sysbox-runc`
- Pod 注解包含 `io.kubernetes.cri-o.userns-mode: auto:size=65536`
- Pod PID 1：`systemd /sbin/init`
- Pod 内可见 `sysboxfs` 挂载

测试 Pod 清理命令：

```bash
kubectl --kubeconfig /home/afan/.kube/114.config \
  -n default delete pod sysbox-centos9-rootfs-test
```
