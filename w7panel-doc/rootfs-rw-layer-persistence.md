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
4. 不实现跨镜像自动迁移旧 `upper/`。
5. 不通过停止时 rsync 或打包 merged rootfs 实现持久化。
6. 不把业务容器可见 mount 或 `sysbox-runc` remount 作为主实现路径。
7. 不使用文件型 Pod metadata registry；rootfs intent 来自 sidecar OCI spec 中的 `ROOTFS_RW_LAYER_SPEC`。

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
| `sysbox-admission` | 校验 annotation，注入 canonical `sysbox-rootfs` sidecar，把 intent 写入 sidecar 的 `ROOTFS_RW_LAYER_SPEC` 环境变量。 |
| `sysbox-snapshotter` | 作为 containerd proxy snapshotter，在 writable snapshot mount 返回前解析 intent 与 sidecar mount，改写 overlay `upperdir/workdir`。 |
| `sysbox-snapshotter/rootfs.LocalPreparer` | 在 PVC-backed 路径下准备并校验 `upper/`、`work/`、`meta.json`。 |
| `sysbox-runc`、`sysbox-fs`、`sysbox-mgr` | 继续承担既有 Sysbox runtime、proc/sys 虚拟化、user namespace 和管理职责；不作为 rootfs rw-layer 主实现。 |

当前实现中，rootfs rw-layer 目录准备逻辑位于 `sysbox-snapshotter/rootfs`，不是 `sysbox-mgr` API。

## Sidecar Intent 契约

`sysbox-admission` 为启用该能力的 Pod 注入或替换名为 `sysbox-rootfs` 的 sidecar。sidecar 将用户引用的 PVC Volume 挂载到固定路径：

```text
/var/lib/sysbox/rootfs-rw-volume/<volumeName>
```

sidecar 不使用 `subPath`。这样 kubelet/CSI 会在节点侧完成 PVC 根目录挂载，`sysbox-snapshotter` 可以从 sidecar OCI mounts 中读取真实宿主 source path。

同一个 sidecar 的 `ROOTFS_RW_LAYER_SPEC` 环境变量保存完整 intent：

```json
{
  "version": 1,
  "entries": [
    {
      "containerName": "c1",
      "volumeName": "rootfs",
      "path": "containers/c1",
      "pvcClaimName": "sysbox-rootfs-pvc"
    }
  ]
}
```

业务容器通过 `containerName` 匹配对应 entry。`sysbox-snapshotter` 不从 `volumeName` 猜测宿主路径，也不读取 Pod 文件型 registry；它只信任当前 Pod 的 sidecar OCI spec 与 OCI mounts。

## Snapshotter 数据路径

容器启动时，数据路径如下：

1. containerd 使用 `sysbox` proxy snapshotter 为 `sysbox-runc` runtime path 准备 writable snapshot。
2. `sysbox-snapshotter` 根据 snapshot key 与 containerd labels 解析 Pod namespace、Pod name、Pod UID、业务容器名和 userns remap labels。
3. `SidecarMetadataResolver` 读取当前 Pod 的 `sysbox-rootfs` sidecar OCI spec，并从 `ROOTFS_RW_LAYER_SPEC` 找到业务容器对应的 intent entry。
4. `PVCMountPathResolverFromSidecar` 从 sidecar OCI mounts 解析 `/var/lib/sysbox/rootfs-rw-volume/<volumeName>` 对应的宿主 source path。
5. `LocalPreparer` 在 `<pvc-source>/<path>/` 下准备并校验：

```text
upper/
work/
meta.json
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

未配置 rootfs rw-layer 的容器走原生 overlay 行为。`SidecarMetadataResolver` 找不到 sidecar OCI spec、sidecar 中没有 `ROOTFS_RW_LAYER_SPEC`、或 intent 中没有当前业务容器 entry 时，都表示当前容器没有可信 rootfs rw-layer intent，snapshotter 返回原生 mounts。

一旦从 `ROOTFS_RW_LAYER_SPEC` 解析出当前业务容器的 intent entry，后续错误必须 fail closed，不能静默回退到原生 overlay `upperdir/workdir`：

1. sidecar OCI spec malformed。
2. `ROOTFS_RW_LAYER_SPEC` 解析失败或版本不支持。
3. intent entry 的 `path` 逃逸 PVC 根目录。
4. intent entry 引用的 PVC mount 在 sidecar OCI mounts 中不存在。
5. PVC mount source 为空。
6. `LocalPreparer` 无法准备或校验 `upper/`、`work/`、`meta.json`。

因此，“sidecar spec 不可用”本身不是运行时策略分支：在 metadata 解析阶段它等价于没有可信 intent；在已获得 intent 后，PVC/mount 解析错误才是该 rootfs rw-layer 请求的 fail-closed 条件。

## 元数据与复用

`LocalPreparer` 在 backing root 下维护 `meta.json`。当前 metadata 用于记录该目录由 Sysbox 管理以及与本次请求相关的 namespace、Pod、容器、PVC/path、snapshot key、id mapping 等信息。

复用规则以当前实现为准：

1. backing root 不存在时创建目录并初始化 `upper/`、`work/`、`meta.json`。
2. backing root 已存在但没有 `meta.json`，且目录非空时 fail closed，避免复用外部数据。
3. `upper/` 与 `work/` 必须存在或可创建，并满足 overlayfs 的基本目录要求。
4. `meta.json` 是本地目录状态，不是 Pod 间 intent registry。

当前实现保留 `imageChainID` 字段用于 metadata 记录与后续兼容性校验扩展，但 Kubernetes sidecar intent 路径不把它作为解析 PVC 或匹配业务容器的主身份。

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
7. 验证已解析到 rootfs intent 后，PVC mount 缺失、path 逃逸、非空外部目录等错误均 fail closed。
7. 验证 `overlayutils` 与 idmapped mount capability 契约未回归。

集群验收时还应检查 `sysbox-snapshotter`、`sysbox-fs`、`sysbox-mgr` 日志，确认没有 panic、fatal、unsafe rootfs metadata key、snapshot prepare/unpack 错误或 sysbox-fs 旧容器注册状态误报。

## 参考入口

- `sysbox-admission/admission/sidecar.go`
- `sysbox-snapshotter/rootfs/contract.go`
- `sysbox-snapshotter/rootfs/sidecar_metadata.go`
- `sysbox-snapshotter/rootfs/pvc_mountpath_resolver.go`
- `sysbox-snapshotter/rootfs/local_preparer.go`
- `sysbox-snapshotter/snapshotter/rootfs_hook.go`
- `sysbox-snapshotter/README.md`
