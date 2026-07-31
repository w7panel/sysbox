# Sysbox 持久 rootfs 的 Raw Upper 特殊挂载方案

> 2026-07-31 更新：当前实现不再建立独立 `special/`，七个特殊目录直接使用 PVC rootfs 的 raw `upper/<容器目标路径>`。PVC source 通过 snapshotter root-only handoff 交给 runc，不出现在业务 Pod YAML。本文后半部分保留的旧方案测试仅用于追溯。

## 当前结论

同时启用以下两个 annotation 的业务容器使用如下 PVC 布局：

```yaml
sysbox/rootfs-rw-layer: '[{"name":"system","volumeName":"rootfs","path":"rootfs"}]'
sysbox/persistent-special-mounts: "true"
```

```text
<PVC>/<annotation.path>/
├── upper/
│   ├── var/lib/docker/
│   ├── var/lib/kubelet/
│   ├── var/lib/k0s/
│   ├── var/lib/rancher/k3s/agent/
│   ├── var/lib/rancher/rke2/
│   ├── var/lib/buildkit/
│   └── var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/
└── work/
```

`sysbox-snapshotter` 从 `sysbox-rootfs` sidecar 解析目标 PVC，在宿主机 `/run/sysbox/rootfs-pvc-handoff/` 写入 root-only handoff。业务容器的 Pod YAML 和 OCI mount 列表都不再注入整块 PVC。`sysbox-runc` 在创建容器时完成以下操作：

1. 先确认 `sysbox/persistent-special-mounts` 精确为 `"true"`，再用容器名、Pod UID 和 `sysbox/rootfs-rw-layer` 匹配当前 entry。
2. 按 container ID 读取 handoff，校验 Pod UID、容器名、volumeName 及 source 确实属于当前 Pod 的 kubelet volume 目录。
3. 在 layer 文件锁内初始化七个 raw upper 目标目录：目录首次缺失时，先从 merged image rootfs 的对应目录一次性复制到同级 staging 目录，再原子发布；镜像目录不存在时发布空目录。不生成 metadata。
4. 校验目标没有路径逃逸、symlink、普通文件或父子重叠。
5. 把 raw upper 目录以 `rbind,rprivate` bind mount 回相同容器路径，使内层运行时直接看到 Longhorn ext4；handoff 不进入 Pod 或容器 mount namespace。
6. 复用现有 `sysbox-mgr PrepMounts()` 完成 idmapped/ownership 准备，不再申请 `/var/lib/sysbox/<kind>/<container-id>` 节点本地 volume。

没有显式设置 `sysbox/persistent-special-mounts: "true"`、没有 rootfs annotation，或 rootfs entry 不匹配当前容器时，仍使用原来的节点本地 special volume。两个注解都匹配时，Docker、Kubelet、k0s、K3s agent、RKE2、BuildKit 和 standalone containerd overlay 全部直接使用同一 PVC 的 raw upper。

### 为什么仍需要显式 bind

数据虽然位于 `upper/var/lib/...`，但容器若只通过 outer merged rootfs 访问，看到的仍是 FUSE。runc 因此把 raw upper 真实目录 bind mount 到相同容器路径；这样数据属于 outer upper 的备份边界，同时内层 Docker/K3s 获得 Longhorn ext4，避免 overlay-on-FUSE。首次 bind 前已一次性复制镜像 lowerdir 的对应目录，避免 bind 遮盖预置内容；之后目录一旦存在就以 PVC 为准，重启或镜像升级都不会自动合并或覆盖。

### K3s snapshotter 结论

历史测试中 K3s 必须使用 `native`，是因为数据目录位于外层 FUSE merged rootfs，内层 kernel overlayfs 返回 `EINVAL`。新方案把 `upper/var/lib/rancher/k3s/agent` 的 raw ext4 目录直接 bind mount 到容器，K3s 可继续使用 `overlayfs`。

部署验证顺序为：

1. 用 `findmnt -T /var/lib/rancher/k3s/agent` 确认 mount root 为 `/rootfs/upper/var/lib/rancher/k3s/agent`，FSTYPE 为 Longhorn ext4。
2. 使用 K3s `overlayfs`，创建全新 Pod 并验证镜像解包、容器启动和重建。
3. 检查内层 containerd 的 lowerdir、upperdir 和 workdir 均位于 PVC raw upper 的 K3s agent 树。

### 上线迁移约束

线上旧 CKM 来自 `dev-v1-k3k-deployment`，其 PVC 直接挂载在 `/var/lib/rancher/k3s`。模板升级必须先停止旧 Pod，再把 PVC 顶层 K3s 数据迁移到 `rootfs/upper/var/lib/rancher/k3s`，创建其余空 raw upper 目标目录，完成 manifest 校验后才能删除旧布局并启动新模板。此前开发阶段的 `special/meta.json` 布局不兼容，测试必须使用全新 PVC。

### 218 集成验证结果

#### 全新 CKM `ckm-z79us`

2026-07-31 使用当前 `v3.4` 模板新建 `k3k-console-164315/ckm-z79us`，对应 10 GiB Longhorn PVC `pvc-f13cf644-a7e2-4cb9-bfdc-274d17a10520`。自动化测试结果：

- CKM 最终为 `ready / Ready`，Server Pod 为 `2/2 Running`。
- Pod YAML 不包含 `/var/lib/sysbox/rootfs-special-volume/...`。
- Docker、Kubelet、k0s、K3s agent、RKE2、BuildKit、standalone containerd overlay 七个目录全部来自同一个 Longhorn ext4 PVC，mount 带 `idmapped`，容器内 root ownership 正确。
- 分别在七个目录写入第一轮 marker，删除 Pod；新 Pod 全部读回后写入第二轮 marker，再次删除 Pod；第三个 Pod 再次读回全部 marker。
- 首次启动曾出现一次 `sysbox sidecar oci spec unavailable`，kubelet 重试后恢复；后续两次重建和最终状态正常。该现象属于 sidecar OCI spec 可见性的启动时序，未造成数据回退到节点本地目录。

复现命令：

```bash
KUBECONFIG=/root/.kube/218.config \
NAMESPACE=k3k-console-164315 \
DEPLOYMENT=k3k-ckm-z79us-server \
CONTAINER=k3k-ckm-z79us-server \
bash w7panel-doc/persistent-special-mount-test.sh
```

默认 `TEST_WAIT_MODE=exec`：每次重建后只等待目标业务容器进入 Running 且 `kubectl exec` 可用，不等待内层 K3s/coredns 使外层 Pod 达到 `2/2 Ready`。脚本仍重建两次并验证两轮 marker，同时把七个目录的 mount、ext4、idmapped、属主及 marker 操作合并到每个 Pod 一次 exec。218 上 `ckm-z79us` 两次重建实测约 14 秒完成。需要同时诊断 CKM Ready 时可设置 `TEST_WAIT_MODE=ready`，恢复原来的慢速等待。

本地编译到目标集群测试拆为两个步骤：

```bash
# 1. 本地增量编译三个组件、构建并 push debug 镜像
DEBUG_IMAGE=docker.cnb.cool/i0358/zpk/sysbox-debug-deploy:<tag> \
bash w7panel-doc/build-sysbox.sh --debug-build

# 2. 218 直接拉取镜像、原子安装并快速重建 CKM 两次
KUBECONFIG=/root/.kube/218.config \
TARGET_NODE=server1 \
DEBUG_IMAGE=docker.cnb.cool/i0358/zpk/sysbox-debug-deploy:<tag> \
TEST_NAMESPACE=k3k-console-164315 \
TEST_DEPLOYMENT=k3k-ckm-z79us-server \
TEST_CONTAINER=k3k-ckm-z79us-server \
bash w7panel-doc/build-sysbox.sh --debug-test
```

`--debug-deploy` 保留为兼容入口，内部依次执行以上两个步骤。`--debug-test` 会逐一比较镜像内与宿主 `/usr/bin` 的二进制 SHA256，不一致立即失败。

#### 二进制与镜像一致性

218 宿主 `/usr/bin`、本地最新源码构建产物、可拉取的 debug 镜像以及节点缓存的 deploy 镜像内 artifact SHA256 全部一致：

| 二进制 | SHA256 |
|---|---|
| `sysbox-runc` | `2dcb9a10cd87565c72573ab11f79e88dbed0e4871aadd3a94a8661859f1573bc` |
| `sysbox-snapshotter` | `8d27db8f6d63bcdf8c479c87b84cd603e50d333a0e07f54cb5e1b81d3ca12918` |
| `sysbox-admission` | `ac47fa2167ff26501ff85d186febb1120fc5b37f8e414511e6b1606042e4bcf0` |

debug 镜像为 `docker.cnb.cool/i0358/zpk/sysbox-debug-deploy:rootfs-handoff-20260731-1`，远端 digest 为 `sha256:12e1ac6da5820a75359aba5740cf0535a8a52538df8d3b82597a55e863642467`，218 使用 `Always` 已验证可重新拉取。

Deployment 当前引用的 `docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:rootfs-handoff-20260731-1` 在 server1 缓存中的 artifact 哈希也一致，但从 registry 重新拉取返回 `artifact not found`，现有 Pod 因 `IfNotPresent` 使用缓存仍可运行。发布或换节点前必须重新构建并 push deploy 镜像，不能把节点缓存视为可用发布物。admission 容器实际执行 hostPath 挂载的 `/host/usr/bin/sysbox-admission`，当前运行二进制已经过上述哈希确认。

#### 历史 Deployment 验证

Deployment `220` 完成了迁移、重建和再次重建验证：

- `/var/lib/docker` source 为 Longhorn PVC 的 `/special-v4-220/special/docker`，FSTYPE 为 ext4，mount 带 `idmapped`。
- K3s agent 和 standalone containerd overlay 目录也指向同一 PVC 的对应 `special/` 子目录。
- 业务 Pod YAML 和最终 mountinfo 均不包含 `/var/lib/sysbox/rootfs-special-volume/...`。
- Docker `overlay2` 正常拉取并运行 `ccr.ccs.tencentyun.com/afan-public/nginx:latest`。
- 外层 Pod 再次重建后 Docker marker 与镜像 metadata 均保留。

独立测试 Deployment `k3s-pvc-special-overlay-test` 使用 15 GiB Longhorn PVC 和 K3s `v1.35.6-k3s1`：

- containerd 配置为 `snapshotter = "overlayfs"`。
- `/var/lib/rancher/k3s/agent` 直接来自 Longhorn PVC `special/k3s-agent`，FSTYPE 为 ext4。
- 内层 Pod sandbox 和 coredns 容器均成功创建，mountinfo 出现真实 kernel `overlay` mount；coredns 的多层 lowerdir、upperdir、workdir 均位于 `special/k3s-agent/containerd/io.containerd.snapshotter.v1.overlayfs`。
- 因此之前必须使用 `native` 的限制来自 overlay-on-FUSE；PVC special-on-ext4 下 K3s overlayfs 可用。
- 初次拉取 pause 镜像曾因外层 ClusterFirst DNS `10.43.0.10` 在嵌套 K3s 环境不可达而失败。改用可达 DNS 并配置 `registries.yaml` mirror 后拉取成功；该问题与 overlay mount 无关。测试期间多次改变外层 Deployment Pod 名造成 K3s node identity 变化，coredns 最终因测试集群 CA 状态不一致保持 `0/1`，但其 overlay rootfs 已成功挂载并启动；生产模板必须设置稳定的 `K3S_NODE_NAME`。

w30at 的历史验证保留为性能与容量参考，不再作为 annotation 灰度模型。

## 历史方案与测试记录（已被当前 PVC special 方案取代）

# Sysbox 持久 rootfs 取消特殊目录挂载说明（历史）

本文说明启用 `sysbox/rootfs-rw-layer` 后，为什么不再为 Docker、K3s 和 containerd overlay 数据目录创建 Sysbox 特殊挂载，以及该调整的收益、代价、兼容性和测试结论。

## 结论

启用持久 rootfs 的 Sysbox 容器中，以下目录直接保存在 PVC-backed rootfs 读写层，不再由 `sysbox-mgr` 准备独立的宿主目录并 bind mount 到容器：

- Docker 默认 `/var/lib/docker`，或镜像内 `/etc/docker/daemon.json` 预设的 `data-root`。
- K3s 默认 `/var/lib/rancher/k3s`，或镜像内 `/etc/rancher/k3s/config.yaml` 预设的 `data-dir`。
- `/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs`。

没有配置 `sysbox/rootfs-rw-layer` 的普通 Sysbox 容器保持原行为。`/var/lib/kubelet`、`/var/lib/k0s`、RKE2 data-dir 和 `/var/lib/buildkit` 也仍保留特殊挂载。

该调整让一个 PVC 快照完整覆盖 rootfs 与嵌套运行时数据，减少恢复时的隐藏状态和额外挂载层。主要代价是内层运行时不能继续假设其数据目录位于原生 ext4：K3s 在当前实现中必须使用 containerd `native` snapshotter，磁盘占用和镜像解包成本会显著增加。

## 背景：特殊目录挂载是什么

Sysbox 原本会识别嵌套容器运行时和 Kubernetes 组件常用的数据目录，请求 `sysbox-mgr` 为这些目录准备宿主侧存储，再 bind mount 到系统容器中。宿主 backing filesystem 通常是 ext4；具体形态由部署方式决定，不能统一假设一定是 loop 镜像。

这类特殊挂载的目标是让内层 Docker、containerd、K3s 等获得接近原生文件系统的语义，特别是支持内层 kernel overlayfs。它也将高频运行时数据从容器自身的 overlay rootfs 中隔离出来。

引入 PVC-backed rootfs 后，容器根目录本身已经持久化：

```text
Longhorn PVC
  └─ sysbox-snapshotter 管理的 upper/ 与 work/
       └─ Sysbox 容器持久 rootfs
            ├─ /etc
            ├─ /var/lib/docker
            └─ /var/lib/rancher/k3s
```

如果继续创建特殊挂载，则数据路径会分裂：

```text
PVC-backed rootfs
  ├─ 普通 rootfs 文件
  └─ /var/lib/rancher/k3s  ← 被后创建的特殊挂载遮盖

宿主 Sysbox 管理目录
  └─ K3s / containerd 实际数据
```

此时只恢复 PVC rootfs 并不能保证恢复运行时实际数据，`du`、`df`、mountinfo 和备份内容也更难对应。

## 生效规则

逻辑位于 `sysbox-runc/libsysbox/syscont/spec.go` 的 `getSpecialDirs()`。

只有 OCI spec 中存在非空的：

```text
sysbox/rootfs-rw-layer
```

才删除下列 implicit special mount：

| 目录 | 持久 rootfs Pod | 普通 Sysbox Pod |
|---|---|---|
| Docker `data-root` | 不创建特殊挂载 | 保持特殊挂载 |
| K3s `data-dir` | 不创建特殊挂载 | 保持特殊挂载 |
| containerd overlay snapshot 目录 | 不创建特殊挂载 | 保持特殊挂载 |
| `/var/lib/kubelet` | 保持特殊挂载 | 保持特殊挂载 |
| `/var/lib/k0s` | 保持特殊挂载 | 保持特殊挂载 |
| RKE2 `data-dir` | 保持特殊挂载 | 保持特殊挂载 |
| `/var/lib/buildkit` | 保持特殊挂载 | 保持特殊挂载 |

Docker 自定义 `data-root` 和 K3s 自定义 `data-dir` 必须在容器镜像中预先配置，Sysbox 在创建容器、读取 OCI rootfs 时才能识别。

## 取消前后对比

| 项目 | 保留特殊挂载 | 取消特殊挂载 |
|---|---|---|
| 数据位置 | rootfs 与宿主 Sysbox 管理目录分离 | 统一位于 PVC-backed rootfs |
| PVC 快照 | 可能不包含被挂载遮盖的运行时数据 | 同时包含 rootfs 与运行时数据 |
| Pod 重建 | 依赖特殊目录重新准备、重新挂载 | rootfs 恢复后数据直接可见 |
| mount namespace | 挂载点多，传播关系更复杂 | 挂载点更少，路径更直观 |
| 容量观察 | `du`、`df` 可能分别落在不同 backing store | 容量统一计入 rootfs PVC |
| kernel overlayfs | 底层通常为 ext4，兼容性较好 | 底层为 FUSE 时不可用 |
| 小文件和 fsync | 通常更接近原生 ext4 | 受 FUSE 与 Longhorn 链路影响 |
| 故障定位 | 需要同时检查 rootfs、特殊挂载和宿主目录 | 主要检查 PVC rootfs 与内层 snapshotter |
| 空间回收 | 删除文件不一定反映在 rootfs PVC | 文件删除统一反映在 PVC 数据层 |

## 优点

1. **备份与恢复边界清晰**：rootfs、etcd、containerd metadata、镜像内容和 Docker 数据都由同一 PVC 生命周期管理。
2. **避免挂载遮盖**：恢复后的 rootfs 数据不会被一个新建的空特殊挂载覆盖，从而出现“数据莫名丢失或恢复”的错觉。
3. **减少外部状态**：Pod 重建不再依赖宿主侧同名特殊目录是否仍存在、是否挂到了正确实例。
4. **容量更容易解释**：运行时写入统一计入 PVC，便于监控、扩容和快照容量评估。
5. **实现更简单**：持久 rootfs 已承担 durable storage 职责，不再对同一数据目录建立第二套持久化路径。

## 缺点与风险

1. **失去 kernel overlay-on-ext4 路径**：持久 rootfs 当前为 `fuse-overlayfs`，Linux kernel overlayfs 叠加在其上会返回 `EINVAL`。
2. **K3s 必须使用 `native`**：containerd native snapshotter 会为 snapshot 复制、解压完整文件树，镜像层不能像 overlayfs 一样高效共享。
3. **磁盘放大**：相同镜像的多个 snapshot 可能产生多份文件；测试中 30 GiB PVC 在 native 解包后使用约 24 GiB，达到 80%。
4. **启动和创建容器更慢**：首次切换 native 需要重新解包镜像，空间不足时会出现 `copy_file_range failed: no space left on device`。
5. **I/O 链路更长**：etcd WAL、containerd metadata 和大量小文件经过内层文件操作、Sysbox FUSE rootfs 与 Longhorn，尾延迟可能高于独立 ext4。
6. **Docker 默认配置不可直接类推**：普通文件写入 `/var/lib/docker` 成功，不等于 Docker `overlay2` 可以在 FUSE rootfs 上运行。
7. **PVC 故障影响面扩大**：rootfs 和运行时数据统一后，PVC 空间耗尽或 I/O 异常会同时影响容器系统盘、etcd 和内层工作负载。

## K3s snapshotter 选择

### overlayfs

当前不可用。内层 kernel overlayfs 以 Sysbox 的 `fuse-overlayfs` rootfs 为底层时，mount 返回：

```text
failed to mount overlay: invalid argument
```

将宿主 `fuse-overlayfs` 从 Ubuntu 包升级到上游静态 v1.17 后结果不变，因为失败的是内层 kernel overlay 的底层文件系统约束，而不是外层 FUSE 版本过旧。

### fuse-overlayfs

镜像层共享和空间效率优于 native，但当前也不可用。补齐 `/dev/fuse` 和 `mount.fuse3` helper 后，snapshotter 能创建 FUSE rootfs；内层 runc 随后对该 rootfs 执行 `MS_PRIVATE` 时失败：

```text
error preparing rootfs:
remount-private dst=/run/k3s/containerd/io.containerd.runtime.v2.task/.../rootfs,
flags=MS_PRIVATE: no such file or directory
```

最小实验中，FUSE 挂载点可正常读文件，但：

```bash
mount --make-private <fuse-mountpoint>
```

返回 `ENOENT`。提前将父挂载设为 private，以及从 K3s 启动脚本删除 `mount --make-shared /`、`mount --make-shared /run`，都不能消除此错误。因此问题位于 runc 对每个 FUSE rootfs 的传播属性设置，不是 K3s 启动脚本的两条 mount 命令。

### native

当前稳定选择。native 不创建内层 overlay 或 FUSE mount，而是将镜像内容复制、解压为普通目录，因此避开 overlay-on-FUSE 和 runc `MS_PRIVATE` 问题。

```text
Longhorn PVC
  └─ Sysbox fuse-overlayfs rootfs
       └─ K3s containerd native snapshots
            └─ 内层 Pod 普通目录 rootfs
```

`w7panel-ckm` 当前策略为：

- `runtimeClassName: sysbox-runc`：`K3S_SNAPSHOTTER=native`。
- 非 Sysbox runtime：继续使用 `overlayfs`。
- 旧的 `w7.cc/k3s-snapshotter=fuse-overlayfs` 注解不再启用 FUSE，也不再挂载 `/dev/fuse`。

## Docker storage driver 影响

| Docker driver | 预期兼容性 | 代价 | 当前结论 |
|---|---|---|---|
| `overlay2` | 低 | 性能和空间效率最好 | FUSE 底层大概率不支持，尚未形成可用方案 |
| `fuse-overlayfs` | 待修复 | 空间效率较好，需要 `/dev/fuse` | 可能遇到与 runc/FUSE mount propagation 相同的问题 |
| `vfs` | 高 | 完整复制，磁盘大、创建慢 | 原理类似 containerd native，建议作为兼容 fallback 测试 |

Deployment `220` 使用 nginx 镜像完成了 `/var/lib/docker` 普通文件读写和 Pod 重建持久化测试，但没有在容器内启动 dockerd。该测试只能证明目录位于持久 rootfs 且普通文件语义正常，不能证明 Docker `overlay2` 已可用。

## 已完成测试

### 自动化单元测试

`sysbox-runc` 已覆盖：

- 持久 rootfs 删除默认 Docker data-root 特殊挂载。
- 持久 rootfs 删除自定义 Docker data-root 特殊挂载。
- 持久 rootfs 删除 K3s data-dir 和 containerd overlay 特殊挂载。
- 普通 Sysbox 容器仍保留原特殊挂载。
- 显式 OCI `/dev/fuse` mount 的 device 与 cgroup 权限处理。

验证命令：

```bash
cd sysbox/sysbox-runc
go test ./libsysbox/syscont
```

对应提交：

```text
sysbox-runc 15b6d3c fix: keep nested runtime data on persistent rootfs
sysbox      877509c chore: update sysbox-runc for persistent rootfs
```

### w30at K3s 集成测试

测试对象为 218 集群中的 `ckm-w30at`，rootfs 使用 Longhorn PVC。

| 场景 | 结果 |
|---|---|
| 取消 K3s/containerd 特殊挂载后启动 K3s | K3s server 与 etcd 可以启动 |
| kernel overlay snapshotter | `invalid argument`，不可用 |
| fuse-overlayfs v1.17 | snapshotter 可准备 rootfs，但 runc `MS_PRIVATE` 返回 ENOENT |
| 删除两条 `mount --make-shared` 后再试 fuse | 错误不变，排除启动脚本传播设置 |
| native snapshotter | coredns、local-path-provisioner、cert-manager 可启动 |
| 新建 `default/native-smoke` | sandbox、容器创建成功并 Ready |
| native 磁盘占用 | 30 GiB PVC 使用约 24 GiB，剩余约 6.1 GiB |

切换 snapshotter 后曾遇到 pause 镜像 metadata 引用的 content blob 已被 GC：

```text
content digest sha256:fbe1...: not found
```

重新拉取 `rancher/mirrored-pause:3.6` 补齐 blob 后，native 正常创建 sandbox；fuse 则继续稳定复现 `MS_PRIVATE` 错误。该 content 缺失不是特殊挂载取消的根因。

### 普通文件与持久化测试

Deployment `220` 使用：

```text
ccr.ccs.tencentyun.com/afan-public/nginx:latest
```

测试确认：

- `/var/lib/docker` 与 `/` 位于同一个持久 `fuse-overlayfs` rootfs。
- `/var/lib/docker` 没有独立 ext4 特殊挂载。
- 64 MiB 顺序写测试约为 171 MB/s；该数值只代表当次环境，不作为长期性能基线。
- Pod 重建后 marker 文件保留。
- nginx HTTP 返回 200。

## 上线验收

### 检查是否仍有特殊挂载

进入 Sysbox 容器后执行：

```bash
findmnt -T /var/lib/rancher/k3s
findmnt -T /var/lib/docker
grep -E '/var/lib/(rancher/k3s|docker|containerd)' /proc/self/mountinfo
```

持久 rootfs 场景中，这些路径应归属容器根目录的 `fuse.fuse-overlayfs`，不应出现独立 ext4 mount。普通 Sysbox 容器则应保持原特殊挂载。

### 检查 K3s native

```bash
grep -n 'snapshotter' /var/lib/rancher/k3s/agent/etc/containerd/config.toml
k3s kubectl get pods -A -o wide
k3s crictl info
```

预期 containerd 配置显示：

```text
snapshotter = "native"
```

至少应验证 coredns Running，并新建一个全新 smoke Pod，不能只观察重启前已经存在的 sandbox。

### 容量和性能

上线前建议 PVC 预留至少 30% 空间，并持续观察：

```bash
df -h / /var/lib/rancher/k3s
du -xsh /var/lib/rancher/k3s/*
```

etcd 重点检查：

```bash
grep -E 'slow fdatasync|apply request took too long|deadline|leader' /var/log/k3s.log
```

性能测试应同时覆盖顺序吞吐和同步小写入，不能只使用大文件 `dd`：

```bash
fio --name=seq-write --directory=/var/lib/rancher/k3s \
  --rw=write --bs=1M --size=1G --direct=1

fio --name=etcd-like --directory=/var/lib/rancher/k3s \
  --rw=randwrite --bs=4k --size=512M --iodepth=1 \
  --fsync=1 --direct=1
```

测试数据必须写入单独目录，测试前后记录 PVC 使用量；生产数据目录中禁止执行破坏性随机删除。

## 故障处理与回退

1. **空间接近耗尽**：优先在线扩容 PVC；native 解包过程中不要依赖删除少量文件立即恢复服务。
2. **镜像 content 缺失**：确认缺失 digest 所属镜像，删除该镜像的旧 metadata 后重新拉取，避免直接清理整个 containerd 数据目录。
3. **etcd 变慢**：检查 PVC/Longhorn 延迟、磁盘空间、etcd WAL 与 backend 大小，再判断是否需要将 etcd 单独放回原生文件系统；不要仅凭目录大小判断。
4. **需要回退特殊挂载**：移除或停用持久 rootfs annotation 不是数据迁移方案。必须先停止写入，明确 PVC rootfs 与特殊目录中哪一份数据是权威副本，再迁移并验证后切换。
5. **不要混用 snapshotter 数据**：overlayfs、fuse-overlayfs 和 native 的 snapshot metadata/目录结构不同，切换时必须准备充足空间并验证镜像内容完整性。

## 适用边界

当前方案适用于“以 PVC-backed rootfs 作为系统容器唯一持久化边界，并接受 K3s native 空间成本”的场景。

以下场景应继续保留或重新设计特殊挂载：

- 必须使用内层 kernel overlayfs/overlay2。
- PVC 容量不足以承担 native snapshot 文件复制。
- etcd 对 fsync 尾延迟有严格要求且 FUSE/Longhorn 无法满足。
- 希望运行时数据与系统 rootfs 使用不同备份、容量或性能策略。

长期优化方向是修复 runc 对 FUSE rootfs 执行 `MS_PRIVATE` 的兼容问题，使 K3s 能使用 fuse-overlayfs snapshotter；在该问题解决并完成集成测试前，native 是当前稳定方案。
