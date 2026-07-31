# Sysbox rootfs 读写层持久化实现说明

本文记录当前 `sysbox-admission` 与 `sysbox-snapshotter` 协作实现的 rootfs 读写层持久化路径。它替代早期“需求方案 / 组件规划”草稿，只描述当前实现边界与应保持的运行契约。

## 目标

Sysbox 容器的 rootfs 由 overlayfs 组成：镜像只读层作为 `lowerdir`，容器写入进入 `upperdir`，overlay 工作目录为 `workdir`。默认情况下，`upperdir` 与容器实例生命周期绑定，Pod 删除后 rootfs 内直接写入的数据会丢失。

rootfs 读写层持久化让用户通过 Pod annotation 为指定容器声明一个 PVC Volume 与 Volume 内相对路径。容器启动时，`sysbox-snapshotter` 将该容器 overlay rootfs 的 `upperdir` 和 `workdir` 改写到 PVC-backed 目录下。Pod 删除并用相同配置重建后，rootfs 内的写入、删除产生的 whiteout、opaque directory、xattr 等 overlay 语义由 PVC 中的 `upper/` 持续保存。

该能力的关键点是：在 rootfs 首次 overlay mount 之前接管 `upperdir/workdir`，不在 `sysbox-runc` helper 阶段从已挂载 rootfs 的 mountinfo 反推 `lowerdir` 或重挂 rootfs。

## 非目标

1. 不创建、删除或管理 PVC、PV、StorageClass。
2. 不持久化 `/proc`、`/sys`、`/dev`、tmpfs、Kubernetes volume 或 bind mount 内容。
3. 不支持非 PVC Volume 作为 rootfs rw-layer backing store。
4. 不实现跨镜像自动迁移或转换旧 `upper/`；同一 PVC/path 的复用由用户显式选择。
5. 不通过停止时 rsync 或打包 merged rootfs 实现持久化。
6. 不把业务容器可见 mount 或 `sysbox-runc` remount 作为主实现路径。
7. 不使用文件型 Pod metadata registry；rootfs intent 来自业务容器 OCI spec 中转发的 `sysbox/rootfs-rw-layer` annotation，sidecar OCI spec 只用于解析 PVC mount source。

## 用户接口

用户通过 Pod annotation 声明要持久化的业务容器、PVC Volume 和 Volume 内相对路径：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: sysbox-rootfs-persist
  annotations:
    sysbox/rootfs-rw-layer: |-
      [
        {
          "name": "c1",
          "volumeName": "rootfs",
          "path": "containers/c1"
        }
      ]
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  containers:
  - name: c1
    image: ubuntu:24.04
    command: ["sleep", "infinity"]
  volumes:
  - name: rootfs
    persistentVolumeClaim:
      claimName: sysbox-rootfs-pvc
```

字段规则：

| 字段 | 必填 | 说明 |
|---|---:|---|
| `name` | 是 | 目标业务容器名称，必须匹配 `spec.containers[].name`。 |
| `volumeName` | 是 | Pod 中的 PVC Volume 名称，必须匹配 `spec.volumes[].name`，且该 Volume 必须是 `persistentVolumeClaim` 类型。 |
| `path` | 否 | PVC 内用于保存该容器 rootfs 读写层的相对路径；为空表示 Volume 根目录。 |

`path` 必须是相对路径，不允许绝对路径或 `..` 逃逸。未配置 annotation 的容器保持原生 snapshotter 行为。

## 组件职责

| 组件 | 当前职责 |
|---|---|
| Pod annotation | 用户声明目标业务容器、PVC Volume 与 Volume 内路径。 |
| `sysbox-admission` | 校验 annotation，注入 canonical `sysbox-rootfs` sidecar；PVC 只挂载到 sidecar，不挂载到业务容器。 |
| `sysbox-snapshotter` | 从业务容器 OCI annotation 读取 intent，从 sidecar OCI mount 解析 PVC source，改写 overlay `upperdir/workdir`；启用 special 持久化时写入 root-only handoff。 |
| `sysbox-snapshotter/rootfs.LocalPreparer` | 在 PVC-backed 路径下准备并校验 `upper/`、`work/`；不生成顶层 `meta.json`。 |
| `sysbox-runc` | 消费 handoff；raw upper 目标首次缺失时从 merged image rootfs 一次性初始化，之后以 PVC 为准，并将其 bind mount 回容器。 |
| `sysbox-fs`、`sysbox-mgr` | 继续承担 proc/sys 虚拟化、user namespace、ID shifting 和既有管理职责。 |

当前实现中，rootfs rw-layer 目录准备逻辑位于 `sysbox-snapshotter/rootfs`，不是 `sysbox-mgr` API。

## Sidecar Intent 契约

`sysbox-admission` 为启用该能力的 Pod 注入或替换名为 `sysbox-rootfs` 的 sidecar。sidecar 将用户引用的 PVC Volume 挂载到固定路径：

```text
/var/lib/sysbox/rootfs-rw-volume/<volumeName>
```

sidecar 不使用 `subPath`，也不携带 intent 环境变量。containerd 将 Pod 的 `sysbox/rootfs-rw-layer` annotation 转发到业务容器 OCI spec；snapshotter 用容器名匹配 annotation entry，再从当前 Pod 的 sidecar OCI mounts 解析真实宿主 source。它不从 `volumeName` 猜测路径，也不读取 Pod 文件型 registry。

## Snapshotter 数据路径

容器启动时，数据路径如下：

1. containerd 使用 `sysbox` proxy snapshotter 为 `sysbox-runc` runtime path 准备 writable snapshot。
2. `sysbox-snapshotter` 根据 snapshot key 与 containerd labels 解析 Pod namespace、Pod name、Pod UID、业务容器名和 userns remap labels。
3. `ContainerdIdentityResolver` 从业务容器 OCI spec 读取 `sysbox/rootfs-rw-layer`，`SidecarMetadataResolver` 按业务容器名找到 intent entry，并加载当前 Pod 的 `sysbox-rootfs` sidecar OCI spec。
4. `PVCMountPathResolverFromSidecar` 从 sidecar OCI mounts 解析 `/var/lib/sysbox/rootfs-rw-volume/<volumeName>` 对应的宿主 source path。
5. `LocalPreparer` 在 `<pvc-source>/<path>/` 下准备并校验：

```text
upper/
work/
```

6. `sysbox-snapshotter` 只改写 overlay mount options 中的 `upperdir=` 与 `workdir=`，保持 `lowerdir=` 由 containerd / overlay snapshotter / idmapped mount 逻辑原样决定。

最终语义：

```text
lowerdir = 原生镜像只读层与 parent chain
upperdir = <pvc-source>/<path>/upper
workdir  = <pvc-source>/<path>/work
merged   = task rootfs
```

## 失败语义

未配置 `sysbox/rootfs-rw-layer`，或 annotation 中没有当前业务容器 entry 时，容器走原生 overlay 行为。

一旦 OCI annotation 声明了当前业务容器，后续错误必须 fail closed，不能静默回退到原生 overlay `upperdir/workdir`：

1. `sysbox/rootfs-rw-layer` JSON 格式错误。
2. 当前 Pod 的 sidecar OCI spec 尚不可用或格式错误。
3. intent entry 的 `path` 逃逸 PVC 根目录。
4. intent entry 引用的 PVC mount 在 sidecar OCI mounts 中不存在。
5. PVC mount source 为空。
6. `LocalPreparer` 无法准备或校验 `upper/`、`work/`。

因此，“sidecar spec 不可用”不是回退条件。已匹配到当前容器 entry 后，它会让本次容器创建失败，由 kubelet 重试；218 上全新 CKM 的首次启动曾触发一次该时序，随后自动恢复且没有把数据写入节点本地目录。

## 目录复用与 metadata 边界

`LocalPreparer` 当前不在 rootfs backing root 下生成或读取 `meta.json`。顶层只维护 `upper/` 与 `work/`；二者必须是实际目录，不能是 symlink 或普通文件。

复用规则以当前实现为准：

1. backing root 不存在时创建 `upper/` 与 `work/`。
2. backing root 已存在时复用合法的 `upper/` 与 `work/`；路径逃逸、symlink 或同名非目录对象会 fail closed。
3. 同一个 PVC/path 不能被多个运行中容器并发用作同一 overlay upper/work，生命周期互斥由上层保证。

当前实现的 PVC 中完全不使用 `meta.json`，也不创建独立 `special/`。七个目标直接位于 `upper/<容器内绝对路径>`；runc 每次根据代码中的固定语义和镜像配置动态计算目标，校验路径无逃逸、无 symlink、无普通文件且彼此不重叠。目标目录首次缺失时，runc 在 bind mount 前从 merged image rootfs 对应目录一次性复制内容到 staging 目录后原子发布；若镜像目录不存在则发布空目录。

同一个 rootfs rw-layer 的复用身份是用户配置的 PVC-backed `volumeName + path`，不是容器镜像。Pod 使用相同 PVC/path 重建时，可以在不同镜像上复用已有 `upper/` 与 `work/`；这是当前持久化语义的一部分，用于允许用户显式保留 rootfs 写入状态。已有目标目录完全由 PVC 内容决定，镜像升级不会自动把新文件合并进来，也不会覆盖已有文件；用户需要为不同生命周期或不希望共享状态的容器选择不同 `path`。

当前实现不使用 image chain identity 拒绝跨镜像复用，也不把镜像身份作为解析 PVC 或匹配业务容器的主身份。

## Idmapped Mount 契约

`sysbox-snapshotter` 是否启用 idmapped overlay mount 由 containerd proxy plugin 的 `remap-ids` capability 决定。

启用时，containerd 配置应包含：

```toml
[proxy_plugins."sysbox"]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
  capabilities = ["remap-ids"]
```

配置生效后，`ctr plugins ls -d id==sysbox` 应显示 `Capabilities: remap-ids`。此时 snapshotter 才能启用 `overlay.WithRemapIDs` 并返回 `uidmap=` / `gidmap=` mount options。

未声明 `remap-ids` 时，snapshotter 必须降级为非 idmapped mount 视图，不返回 `uidmap=` / `gidmap=`。不要让 containerd 创建 fallback `*-remap` parent，同时 snapshotter 又返回 idmapped active mount；这种混合 remap rootfs 会导致下游 runc rootfs setup 错误。

## 并发边界

同一个 `volumeName + path` 对应一个 overlay `upperdir/workdir` 实例。overlay upper/work 不是普通共享目录，不支持多个运行中容器并发写入同一个 rootfs rw-layer。

推荐用户为不同业务容器配置不同 `path`。跨 Pod 或跨容器复用同一 PVC/path 需要外部策略保证生命周期与互斥；当前实现不引入全局 lease 或 controller 状态。

## 验收重点

基础功能验收应覆盖：

1. 创建带 `sysbox/rootfs-rw-layer` annotation、`runtimeClassName: sysbox-runc`、`hostUsers: false` 的 Pod。
2. 在业务容器 rootfs 内安装软件、写入文件、修改镜像内文件、删除镜像内文件。
3. 删除 Pod 并用相同 annotation、PVC 和 path 重建。
4. 验证 rootfs 写入、修改和 whiteout 仍然存在。
5. 验证未配置该 annotation 的 sysbox 容器仍走原生 overlay 行为。
6. 验证未注入 rootfs intent 的普通 sysbox pod 仍走原生 overlay 行为。
7. 验证已解析到 rootfs intent 后，PVC mount 缺失、path 逃逸、symlink 或同名非目录等错误均 fail closed。
8. 验证 `overlayutils` 与 idmapped mount capability 契约未回归。

集群验收时还应检查 `sysbox-snapshotter`、`sysbox-fs`、`sysbox-mgr` 日志，确认没有 panic、fatal、unsafe rootfs metadata key、snapshot prepare/unpack 错误或 sysbox-fs 旧容器注册状态误报。

## 参考入口

- `sysbox-admission/admission/sidecar.go`
- `sysbox-snapshotter/rootfs/contract.go`
- `sysbox-snapshotter/rootfs/sidecar_metadata.go`
- `sysbox-snapshotter/rootfs/pvc_mountpath_resolver.go`
- `sysbox-snapshotter/rootfs/local_preparer.go`
- `sysbox-snapshotter/snapshotter/rootfs_hook.go`
- `sysbox-snapshotter/README.md`
