# Sysbox v0.7.0-1 打包、部署和验证流程

本文记录 `w7panel` 分支生成 Sysbox release、构建 K3s 部署镜像、并在 K3s/containerd 环境验证 `sysbox-runc` 的流程。

## 产物

- Sysbox release tag: `v0.7.0-1`
- Sysbox deb: `sysbox-pkgr/deb/build/amd64/ubuntu-jammy/sysbox-ce_0.7.0.linux_amd64.deb`
- K3s deploy image: `docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s:v0.7.0-1`
- Kubernetes RuntimeClass: `sysbox-runc`

注意：当前 deb 包文件名沿用 packager 的 `0.7.0.linux` 格式；二进制自身版本应为 `0.7.0-1`。

## 关键源码改动

- `VERSION` 设置为 `0.7.0-1`。
- `sysbox-pkgr/deb/ubuntu-jammy/Dockerfile`:
  - Ubuntu apt 源默认使用阿里云，可通过 `UBUNTU_MIRROR` 覆盖。
  - Docker CE apt 源默认使用阿里云，可通过 `DOCKER_APT_MIRROR` 覆盖。
  - Go 模块代理默认使用 `https://goproxy.cn,direct`，可通过 `GOPROXY` 覆盖。
  - GitHub release/raw 下载走 `https://gh-proxy.org/`。
- `sysbox-pkgr/k8s/Dockerfile.sysbox-k3s`:
  - `kubectl` 从阿里云 Kubernetes rpm 源安装，默认 `1.32.13`。
  - EPEL 使用阿里云镜像。
  - GitHub release/clone 下载走 `https://gh-proxy.org/`。
- `sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh`:
  - `SYSBOX_MGR_CONFIG` / `SYSBOX_FS_CONFIG` 默认空字符串，避免 `set -u` 退出。
- K8s manifest:
  - DaemonSet 增加 `sysbox-runtime` taint toleration。
  - RBAC 增加 pods `list/delete`，用于删除旧的 sysbox runtime Pod。

## 本地测试

在当前仓库根目录执行：

```bash
sudo ./w7panel-doc/deploy-local-test.sh
```

默认行为：

1. 确认 `sysbox-pkgr/sources/sysbox` 指向当前 sysbox 源码。
2. 使用 `sysbox-pkgr/deb` 构建 generic deb。
3. 从 deb 解出 Sysbox runtime 与 admission 二进制到 `sysbox-pkgr/k8s/bin/sysbox-ce/generic/`。
4. 构建 K3s 部署镜像。
5. 可选推送镜像到 registry。
6. 将镜像导入本机 K3s containerd。
7. 应用 RBAC、RuntimeClass、DaemonSet。
8. 给目标节点打 `sysbox-install=yes` 标签。
9. 等待 `sysbox-runtime=running`。
10. 创建 `sysbox-release-test` Pod 并验证 PID 1 是 systemd、容器内存在 sysboxfs 挂载。

常用变量：

```bash
SYSBOX_VERSION=0.7.0-1
IMAGE_REPO=docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s
PUSH_IMAGE=true
NODE_NAME=server1
TEST_POD=sysbox-release-test
./w7panel-doc/deploy-local-test.sh
```

## 正式环境部署

正式环境不重新构建，只使用已发布镜像部署：

```bash
sudo IMAGE=docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s:v0.7.0-1 \
  NODE_SELECTOR_VALUE=yes \
  ./w7panel-doc/deploy.sh
```

默认行为：

1. 应用 `sysbox-rbac.yaml`。
2. 应用 `sysbox-runtimeclass.yaml`。
3. 基于官方 DaemonSet manifest 临时替换镜像。
4. 给节点打 `sysbox-install=yes` 标签。
5. 等待 DaemonSet ready、节点 `sysbox-runtime=running`。
6. 可选创建测试 Pod 并验证 systemd/sysboxfs。

常用变量：

```bash
IMAGE=docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s:v0.7.0-1
NODE_NAME=server1
RUN_TEST=true
TEST_POD=sysbox-release-test
./w7panel-doc/deploy.sh
```

如果集群无法直接拉私有 registry，可先在每个节点导入镜像：

```bash
docker save -o /tmp/sysbox-deploy-k3s.tar docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s:v0.7.0-1
k3s ctr images import /tmp/sysbox-deploy-k3s.tar
```

## 验证命令

```bash
kubectl get node server1 --show-labels
kubectl -n kube-system get ds sysbox-deploy-k8s -o wide
systemctl is-active sysbox sysbox-fs sysbox-mgr k3s
/usr/bin/sysbox-runc --version
kubectl get pod sysbox-release-test -o wide
kubectl exec sysbox-release-test -- ps -p 1 -o pid,comm,args
kubectl exec sysbox-release-test -- sh -lc 'mount | grep -E "sysboxfs|proc|cgroup" | head -20'
```

期望结果：

- 节点 Ready。
- 节点标签包含 `sysbox-runtime=running`。
- `sysbox-deploy-k8s` DaemonSet `1/1 Ready`。
- `sysbox`、`sysbox-fs`、`sysbox-mgr`、`k3s` 均为 `active`。
- 测试 Pod `Running`。
- Pod 内 PID 1 为 `systemd /sbin/init`。
- Pod 内可见 `sysboxfs` 挂载。

## GitHub Release

本地完整 release 脚本：

```bash
./w7panel-doc/release.sh
```

默认行为：

1. 使用当前源码构建 generic deb。
2. 从 deb 解出 Sysbox runtime 与 admission 二进制。
3. 构建并验证 K3s deploy 镜像。
4. 写入 `dist/` 产物和 `SHA256SUMS`。
5. 如果设置了 `GITHUB_TOKEN`，自动创建或复用 GitHub Release，并上传 `dist/` 内文件。

常用变量：

```bash
SYSBOX_VERSION=0.7.0-1
SYS_ARCH=amd64
IMAGE_REPO=docker.cnb.cool/i0358/docker-images-chrom/sysbox-deploy-k3s
PUSH_IMAGE=true
CNB_USERNAME=...
CNB_PASSWORD=...
GITHUB_TOKEN=...
GITHUB_REPOSITORY=w7panel/sysbox
./w7panel-doc/release.sh
```

可选变量：

```bash
RELEASE_TAG=v0.7.0-1
RELEASE_NAME="Sysbox v0.7.0-1"
RELEASE_BODY="Sysbox v0.7.0-1 release artifacts."
SAVE_IMAGE_TAR=true
VERIFY_IMAGE=false
DIST_DIR=/tmp/sysbox-release
USE_BUILDX=false
```

`SYS_ARCH` 默认根据当前机器自动识别，支持 `amd64` 和 `arm64`。建议在对应架构的机器上原生构建；如果要在 amd64 机器上构建 arm64 release，需要先配置 Docker buildx/qemu，并显式设置 `ALLOW_CROSS_ARCH=true`。跨架构时脚本会自动用 `docker buildx build --platform linux/${SYS_ARCH} --load` 构建 deploy image。

```bash
SYS_ARCH=arm64
ALLOW_CROSS_ARCH=true
./w7panel-doc/release.sh
```

顶层 `sysbox/.github/workflows/release-v0.7.yml` 支持：

- tag `v*` 自动触发。
- 手动 `workflow_dispatch` 触发。

工作流会：

1. 初始化子模块。
2. 构建 generic deb。
3. 解出 Sysbox 二进制并构建 K3s deploy 镜像。
4. 上传 deb、checksums、镜像元数据。
5. tag 触发时创建 GitHub Release 并上传 release 文件。

推送 CNB 镜像需要仓库 secrets：

- `CNB_USERNAME`
- `CNB_PASSWORD`
