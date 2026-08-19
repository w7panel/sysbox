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

解决方案：在 Sysbox runtime 中仅对这两个键的 `EPERM` 忽略，其他 sysctl 写入错误仍返回失败；同时移除旧 Ubuntu rootfs 不兼容的 `/proc/pressure/{io,cpu,memory}` bind mount。

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
