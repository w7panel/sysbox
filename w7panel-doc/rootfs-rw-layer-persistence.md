# Sysbox rootfs 读写层持久化需求方案

本文整理基于 Sysbox 实现容器 rootfs 读写层持久化的生产级需求方案。方案参考火山引擎 VCI 的容器 rootfs 持久化模型：用户通过 Pod Annotation 指定目标容器、目标 Volume 以及 Volume 内存储路径，运行时将容器 overlay rootfs 的读写层放到该 Volume 中，使 Pod 删除并用相同配置重建后，容器 rootfs 修改仍然保留。

## 背景

Sysbox 容器 rootfs 通常由 overlayfs 组成：

```text
lowerdir = 镜像只读层
upperdir = 容器读写层
workdir  = overlayfs 工作目录
merged   = 容器内看到的 rootfs
```

当前 `upperdir` 由 containerd、Docker 等上层 runtime 管理，生命周期绑定具体容器实例。Pod 删除后，容器内直接写入 rootfs 的数据会随 runtime upper 层被清理，例如：

- 在容器内执行 `apt-get install vim` 安装的软件包；
- 写入 `/etc`、`/usr/local`、`/root` 等 rootfs 路径的文件；
- 修改镜像内已有文件；
- 删除镜像内文件产生的 overlay whiteout。

Sysbox 已经具备 rootfs、user namespace、ID-mapped lower layer、upper ownership shift、pause/resume、stop/unregister 等运行时能力；但当前 `sysbox-runc` helper 接手 rootfs 时，task rootfs overlay 已经由 containerd/runtime 首次挂载完成，mountinfo 中的 lowerdir 也已经变成 `hostUsers: false` 机制下的运行时展示值（例如 `/tmp/ovl-idmapped...`），不适合作为新的 overlay mount 输入。因此，本需求的根治方案不再依赖晚期 helper 重挂 rootfs，而是前移到 **containerd snapshotter / rootfs 首次组装层**，在首次 overlay mount 前接管 `upperdir/workdir`，保持 `lowerdir` 完全沿用现有 runtime 逻辑。

## 目标

1. 用户可通过 Pod Annotation 为指定 Sysbox 容器开启 rootfs 读写层持久化。
2. 用户可指定 rootfs 读写层使用的 Pod PVC Volume。
3. 用户可指定 rootfs 读写层在 Volume 内的相对路径。
4. Pod 删除并使用相同配置重建后，容器 rootfs 中的变更仍可恢复。
5. 支持一个 Pod 内多个容器分别配置 rootfs 持久化。
6. `volumeName` 只允许引用 `spec.volumes[]` 中的 `persistentVolumeClaim` 类型 Volume；只要 Kubernetes 能将该 PVC 挂载给 Pod，Sysbox 就进入尝试路径。
7. 保持 Sysbox 现有 user namespace、ID-mapped lower layer、upper chown/revert、sysbox-fs、sysbox-mgr、特殊 volume 管理能力不回归。
8. 根治方案不得依赖在 rootfs overlay 已经挂好后再次从 mountinfo 读取 `lowerdir` 并重挂 rootfs。
9. 根治方案应保证 rootfs **首次** overlay mount 就直接使用持久化 upperdir/workdir。
10. 删除 Pod 时不删除持久化 rootfs 数据。
11. 复用已有 rootfs 读写层时必须具备生产级安全校验，避免错误复用导致数据污染或 rootfs 损坏。

## 非目标

1. 不负责创建 PVC、PV 或 StorageClass。
2. 不负责删除 PVC、PV 或用户 Volume。
3. 不限制或推荐具体 PVC 存储类型。
4. 不实现跨镜像自动迁移 rootfs upper 数据。
5. 不把容器 merged rootfs 打包成快照。
6. 不持久化 `/proc`、`/sys`、`/dev`、tmpfs、Kubernetes volume、bind mount 内容。
7. 不绕过 Kubernetes Volume 访问模式限制。
8. 不支持非 PVC Volume 作为 rootfs rw layer backing store。
9. 首期不主动拒绝多个运行中容器并发使用同一个 `volumeName + path`；该场景的稳定性由 Kubernetes PVC 访问模式、CSI、底层存储和 overlayfs 行为共同决定，Sysbox 只负责明确记录元数据并在自身状态不确定时 fail closed。

## 用户接口

新增 Pod Annotation：

```yaml
sysbox/rootfs-rw-layer: |-
  [
    {
      "name": "c1",
      "volumeName": "rootfs",
      "path": "containers/c1"
    },
    {
      "name": "c2",
      "volumeName": "rootfs",
      "path": "containers/c2"
    }
  ]
```

字段说明：

| 字段 | 必填 | 说明 |
|---|---:|---|
| `name` | 是 | 目标容器名称，必须匹配 `spec.containers[].name` |
| `volumeName` | 是 | Pod 中的 PVC Volume 名称，必须匹配 `spec.volumes[].name`，且该 Volume 必须是 `persistentVolumeClaim` 类型 |
| `path` | 否 | Volume 内用于保存该容器 rootfs 读写层数据的相对路径；为空表示使用 Volume 根目录 |

Annotation 规则：

1. Annotation value 必须是 JSON array。
2. 每个元素对应一个容器。
3. 同一个容器最多只能配置一次。
4. 未配置的容器保持现有 Sysbox 行为。
5. `path` 必须是相对路径。
6. `path` 不允许包含 `..`。
7. `path` 不允许是绝对路径。
8. `path` 为空时使用 Volume 根目录。
9. 同一 Pod 配置中允许多个容器引用相同 `volumeName + path`；该用法进入尝试路径，具体并发语义以底层存储和 overlayfs 实际行为为准。
10. Annotation 解析失败时，对应容器启动失败，并输出明确错误。

## Pod 示例

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: sysbox-rootfs-persist
  namespace: default
  annotations:
    sysbox/rootfs-rw-layer: |-
      [
        {
          "name": "c1",
          "volumeName": "rootfs",
          "path": "containers/c1"
        },
        {
          "name": "c2",
          "volumeName": "rootfs",
          "path": "containers/c2"
        }
      ]
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  containers:
  - name: c1
    image: ubuntu:24.04
    command: ["sleep", "infinity"]
  - name: c2
    image: ubuntu:24.04
    command: ["sleep", "infinity"]
  volumes:
  - name: rootfs
    persistentVolumeClaim:
      claimName: sysbox-rootfs-pvc
```

> 注意：用户仍然通过 Pod annotation 声明 rootfs rw layer 持久化意图，但根治方案不再依赖 `sysbox-runc` 在 rootfs 已挂好后通过内部 `volumeMount` 重挂 rootfs。真正的数据路径接管发生在 snapshotter / 首次 overlay rootfs 组装层；annotation 只承担声明意图，而 `sysbox-admission` 负责注入一个持有 PVC 根目录的 sidecar 与 metadata bridge。

PVC 示例仅作为用法示例，不限制具体 StorageClass 或 PVC 底层存储类型：

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: sysbox-rootfs-pvc
  namespace: default
spec:
  accessModes:
  - ReadWriteOnce
  volumeMode: Filesystem
  resources:
    requests:
      storage: 20Gi
```

## 组件架构（根治方案）

根治方案引入一个新的 **snapshotter 集成组件**（可为包装型 snapshotter、扩展型 snapshotter，或等价的 rootfs 首次组装层组件），使 rootfs 持久化在 **首次 overlay mount 之前** 完成，而不是在 `sysbox-runc` helper 阶段晚期 remount。

### 组件职责

| 组件 | 职责 |
|---|---|
| Pod annotation | 用户声明目标容器、PVC、路径 |
| sysbox-admission（必需桥接组件） | 把 Kubernetes 意图桥接成 snapshotter 可消费 metadata，并向 Pod 注入必要挂载配置 |
| snapshotter 集成组件 | 在 containerd 准备 writable snapshot 时，将 `upperdir/workdir` 接到 PVC-backed 目录，使首次 overlay mount 直接生效 |
| sysbox-mgr | 管理 PVC 内 rootfs rw layer 目录、metadata、ownership 迁移、复用校验、并发/生命周期策略 |
| sysbox-runc | 保持现有 userns、sysbox-fs、sysbox-mgr 协调能力；不再作为 rootfs rw layer 主实现层 |

### 关键原则

1. `lowerdir` 完全沿用 containerd/k8s/`hostUsers: false` 原有逻辑，Sysbox 不设计、不改写、不重新解析它。
2. `upperdir/workdir` 必须在 **首次** overlay mount 之前接到 PVC 目录。
3. 不依赖 rootfs 已挂好后的 mountinfo 中 `lowerdir=/tmp/ovl-idmapped...` 这种运行时展示值。
4. 对未配置 `sysbox/rootfs-rw-layer` 的容器，snapshotter 和 Sysbox 行为必须完全与当前线上保持一致。

## PVC 解析与 metadata 桥接

`sysbox/rootfs-rw-layer` 中的 `volumeName` 只允许引用 Pod 内的 PVC Volume：

```yaml
spec:
  volumes:
  - name: rootfs
    persistentVolumeClaim:
      claimName: sysbox-rootfs-pvc
```

解析规则：

1. `volumeName` 必须匹配 `spec.volumes[].name`。
2. 匹配到的 `spec.volumes[]` 必须是 `persistentVolumeClaim` 类型。
3. `spec.volumes[]` 的存在用于让 Kubernetes 识别该 Pod 引用了哪个 PVC，从而完成 PVC 存在性校验、绑定/动态供给、调度阶段的 VolumeBinding、PV nodeAffinity 约束等前置流程。
4. Kubernetes 侧必须存在 `sysbox-admission`，把 `Pod UID + container name -> PVC volume + path` 映射成 snapshotter 可消费的数据。
5. `sysbox-admission` 必须向目标 Pod 注入一个 sidecar，用于触发 kubelet/CSI 在节点侧完成该 PVC 的真实挂载；该 sidecar 的 `volumeMount.mountPath` 应为统一固定的系统级路径，例如 `/var/lib/sysbox/rootfs-rw-volume`。
6. 该 sidecar 只挂载 PVC 根目录，**不使用** `subPath`。`annotation.path` 仅由 snapshotter 在宿主侧路径解析后拼接为 `<pvc-root>/<path>`。
7. 该桥接机制可以是 admission、controller，或其他本地 registry，不要求固定实现形式；但在当前方案下 `sysbox-admission` 是必需组件。
8. snapshotter 集成组件在 prepare writable snapshot 时，读取该映射并向 `sysbox-mgr` 请求准备 PVC-backed `upperdir/workdir`。
9. snapshotter 集成组件继续沿用原生 lower layer / snapshot parent chain，不修改 lowerdir 来源。
10. snapshotter 集成组件在首次 rootfs overlay mount 之前，使 overlay 使用 PVC-backed `upperdir/workdir`。
11. `sysbox-runc` 不再依赖内部 `volumeMount` / initContainer / PVC 内 metadata 文件来驱动主实现。
12. `sysbox-admission` 注入的 sidecar 挂载只用于触发 kubelet/CSI 完成节点侧 PVC 挂载与 metadata 对齐，不属于最终业务容器自己的可见挂载。
13. 业务容器自身不应看到用于保留 PVC 根目录的 sidecar mountPath。
14. 如果 sidecar 未成功挂载 PVC 根目录，视为实现错误，snapshotter 必须 fail closed 或至少输出明确诊断。

CSI PVC 的典型宿主路径为：

```text
/var/lib/kubelet/pods/<podUID>/volumes/kubernetes.io~csi/<pvName>/mount
```

不同 volume plugin 的目录名可能不同，不能把 `volumeName` 直接当宿主目录名使用。根治方案中，PVC 宿主路径解析不再依赖 `sysbox-runc` 从 OCI 内部 mount 读取，而是由 snapshotter 集成组件结合 sidecar 已触发的节点侧真实挂载状态完成。sidecar 只保证 PVC 根目录已经在宿主上出现；snapshotter 再把 `annotation.path` 拼接为 `<pvc-root>/<path>`，并在该目录下管理 `upper/`、`work/`、`meta.json`。

已在当前测试集群验证两个与根治方案相关的事实：

1. `sysbox-runc` 当前 helper 接手时，task rootfs overlay 已经挂好；此时 mountinfo 中的 lowerdir 已经表现为 `hostUsers: false` 机制下的 `/tmp/ovl-idmapped...` 运行时展示值。
2. 这些 `/tmp/ovl-idmapped...` 目录在 host 文件系统上并不是可重新解析、可稳定复用的真实路径，因此不能作为新的 overlay mount 输入。

这说明：在当前 helper 时机，Sysbox 已经晚于首次 overlay mount，因此根治方案必须前移到更早的 rootfs 组装层，而不是继续依赖晚期内部 mount 注入、initContainer 或从 mountinfo 反推 lowerdir。

## Volume 内数据布局

Sysbox 在用户指定路径下管理以下目录和文件：

```text
<volume>/<path>/
  upper/
  work/
  meta.json
```

| 路径 | 说明 |
|---|---|
| `upper/` | overlayfs upperdir，保存 rootfs 写入数据 |
| `work/` | overlayfs workdir |
| `meta.json` | Sysbox 元数据 |

示例：

```text
/rootfs-volume/
  containers/
    c1/
      upper/
      work/
      meta.json
    c2/
      upper/
      work/
      meta.json
```

## 核心机制（snapshotter 根治方案）

容器启动时，snapshotter 集成组件在 **首次** overlay rootfs mount 前完成以下步骤：

```text
1. 保留 runtime 原始 lowerdir 来源与 lower layer 逻辑
2. 通过 metadata 桥接找到当前容器对应的 PVC/path
3. 调用 sysbox-mgr 准备 <volume>/<path>/upper 与 <volume>/<path>/work
4. 若为首次使用，可按策略初始化 upperdir
5. 首次 overlay mount 直接使用 PVC-backed upperdir/workdir
```

最终 overlay 语义为：

```text
lowerdir = 原镜像 lower layers，由 containerd/k8s/hostUsers:false 原逻辑处理
upperdir = PVC-backed upperdir
workdir  = PVC-backed workdir
merged   = task rootfs
```

容器内对 rootfs 的所有写入都会进入用户 Volume。删除 Pod 不会删除 Volume 内的 `upper/`、`work/`、`meta.json`，使用相同 Annotation 和相同 Volume/path 重建后可复用已有 rootfs 读写层。

该能力不能通过停止时 rsync 实现；必须从容器启动开始就让 overlayfs upperdir 指向用户 Volume，确保 whiteout、opaque directory、xattr、rename、hardlink 等 overlay 语义由底层文件系统直接承载。

## 启动流程

1. Kubernetes 接收 Pod，annotation 声明目标容器、PVC、路径。
2. `sysbox-admission` 对 annotation 做校验，向目标 Pod 注入必要挂载配置，并把 `Pod UID + container name + volumeName + path` 写入 snapshotter 可读 metadata。
3. `sysbox-admission` 为 Pod 注入一个 sidecar；该 sidecar 以固定 `mountPath=/var/lib/sysbox/rootfs-rw-volume` 挂载 PVC 根目录，且不使用 `subPath`。kubelet/CSI 基于该 sidecar 配置完成节点侧 PVC 根挂载。
4. containerd 为容器准备 writable snapshot。
5. snapshotter 集成组件读取 metadata，判断该容器是否启用 rootfs rw layer。
6. 未启用时，snapshotter 完全走原生行为。
7. 启用时，snapshotter 结合 metadata 与 sidecar 已触发的节点侧 PVC 根挂载状态解析真实宿主路径，再将 `annotation.path` 拼接为当前容器专属 backing root，并调用 `sysbox-mgr` 准备/校验 PVC-backed `upperdir/workdir`。
8. snapshotter 保持原有 lower layer / snapshot parent chain，不修改 lowerdir 逻辑。
9. snapshotter 在首次 overlay mount 前，使 writable snapshot 使用 PVC-backed `upperdir/workdir`。
10. task rootfs 第一次 overlay mount 直接完成为持久化 rootfs。
11. 在业务容器进入用户进程前，保留挂载配置必须从最终容器可见 mount 列表中过滤掉。
12. `sysbox-runc` 继续执行 userns、sysbox-fs、sysbox-mgr 协调等现有逻辑，但不再承担 rootfs rw layer 主实现。

## 首次使用语义

如果 `<volume>/<path>` 不存在：

1. Sysbox 创建该目录。
2. Sysbox 创建 `upper/`、`work/`、`meta.json`。
3. Sysbox 按当前容器 userns mapping 初始化 ownership。
4. Sysbox 记录当前镜像、image ID、chain ID、UID/GID mapping 等元数据。
5. 容器以空 upper 层启动。

如果 `<volume>/<path>` 已存在但没有 `meta.json`：

1. 目录为空时，Sysbox 初始化为新 rootfs 读写层。
2. 目录非空时，启动失败。
3. 错误信息提示用户指定新的 `path` 或清空目录。

如果 `<volume>/<path>` 已存在且有 `meta.json`：

1. Sysbox 执行兼容性校验。
2. 校验通过后复用 `upper/`。
3. 校验失败时启动失败。

## 复用校验

复用已有 rootfs 读写层时，必须校验：

1. `meta.json.version` 为当前 Sysbox 支持版本。
2. `imageChainID` 与当前容器镜像 rootfs chain ID 一致。
3. UID/GID mapping 与当前容器兼容。
4. `upper/` 存在且为目录。
5. `work/` 存在且为目录。
6. `upper/` 与 `work/` 满足 overlayfs workdir 要求。
7. 上次状态不是未完成初始化状态。
8. 若上次状态为异常退出，Sysbox 必须先执行恢复流程。

默认不允许 image chain 不一致时复用旧 upper。该规则用于避免旧 upper 的 whiteout、文件修改、opaque directory 污染新镜像 rootfs。

## 元数据格式

`meta.json` 建议格式：

```json
{
  "version": 1,
  "state": "available",
  "containerName": "c1",
  "volumeName": "rootfs",
  "path": "containers/c1",
  "image": "ubuntu:24.04",
  "imageID": "sha256:...",
  "imageChainID": "sha256:...",
  "uidMappings": [
    {
      "containerID": 0,
      "hostID": 100000,
      "size": 65536
    }
  ],
  "gidMappings": [
    {
      "containerID": 0,
      "hostID": 100000,
      "size": 65536
    }
  ],
  "createdAt": "2026-06-27T00:00:00Z",
  "lastAttachedAt": "2026-06-27T00:00:00Z",
  "lastDetachedAt": "2026-06-27T00:00:00Z",
  "lastSysboxVersion": "..."
}
```

状态定义：

| 状态 | 说明 |
|---|---|
| `initializing` | 正在初始化，不能复用 |
| `available` | 上次正常卸载，可复用 |
| `attached` | 当前启动流程已附加到该 rootfs rw layer；该状态只用于故障恢复判断，不表达 PVC 并发控制 |
| `recovering` | 正在恢复异常状态 |
| `error` | 上次操作失败，需要人工处理或显式恢复 |

## 停止与删除语义

容器停止时：

1. Sysbox 不删除 `upper/`。
2. Sysbox 不删除 `work/`。
3. Sysbox 不删除 `meta.json`。
4. Sysbox 不清理 Volume 内数据。
5. Sysbox 执行当前已有的 rootfs upper ownership revert 逻辑。
6. Sysbox 更新 `meta.json`：
   - `state=available`；
   - 更新 `lastDetachedAt`。

Pod 删除时：

1. Sysbox 不删除 rootfs 读写层数据。
2. Sysbox 不删除 Volume 内用户指定路径。
3. Sysbox 不删除 PVC、PV 或任何 Kubernetes 存储对象。
4. 用户若要删除持久化 rootfs 数据，应自行清理对应 Volume 内容。

启用 `sysbox/rootfs-rw-layer` 后，Pod 删除保留 rootfs 数据是默认且唯一语义，不设计额外 retain policy。

## 异常恢复

Sysbox 必须处理以下异常：

1. 容器启动过程中失败。
2. `sysbox-runc` 在 rootfs 重建后异常退出。
3. `sysbox-mgr` 重启。
4. 节点重启。
5. Pod 被强制删除。
6. runtime 没有正常调用 unregister。
7. `meta.json` 显示 `attached`，但上次启动未完成或容器已不存在。

恢复策略：

1. `sysbox-mgr` 启动时不强制扫描所有 rootfs rw layer 目录，因为 Volume 可能尚未挂载。
2. 每次容器启动时，以目标 `volumeName + path` 为恢复入口。
3. 如果 `state=attached`，Sysbox 基于当前容器 ID、Pod UID、rootfs 状态和 runtime 可见状态判断上次附加是否已经结束。
4. 如果上次附加已经结束，则进入 `recovering`。
5. 恢复 ownership 到宿主管理态。
6. 更新状态为 `available`。
7. 再进入正常复用流程。

如果无法确认旧容器状态，Sysbox 不做额外并发仲裁；是否允许同一路径再次挂载由 Kubernetes 和底层存储语义决定。Sysbox 只负责避免在自身元数据或 ownership 状态不确定时继续挂载导致数据损坏。

## 并发与共享边界

同一个 `volumeName + path` 对应一个 rootfs 读写层实例。该实例由一组 overlay `upperdir/workdir` 表示，不能等同于普通文件共享目录。

并发语义遵循以下边界：

1. 同一个 PVC 能否被多个 Pod 同时挂载，由 PVC access mode、CSI 和底层存储决定。
2. 同一个 PVC Volume 能否被同一 Pod 内多个容器同时使用，由 Kubernetes 挂载模型和用户配置决定。
3. 同一个 `volumeName + path` 被多个运行中容器同时作为 rootfs rw layer 使用是不支持场景；PVC 支持 RWX 不代表 overlay upper/work 可被多个 overlay rootfs 实例并发写入。
4. 若存在桥接层，它应在同一 Pod 内尽早拒绝重复的 `volumeName + path` 配置。
5. 跨 Pod 的重复 `volumeName + path` 无法仅靠单个 admission 请求完整判断，首期标记为不支持；后续如需支持，需要引入全局状态或 lease 机制。

产品文档必须要求不同容器配置不同 `path`，避免 overlay upper 语义相互污染。

## PVC 存储类型策略

Sysbox 只接受 PVC Volume，但不限制 PVC 底层存储类型。只要 Kubernetes 将 PVC 成功挂载给 Pod，Sysbox 就按配置尝试作为 rootfs upper 使用。

Sysbox 必须在运行时做能力校验：

1. 目标路径可读写。
2. 支持创建目录。
3. 支持必要的 chmod/chown。
4. 支持 overlayfs upper/work 所需语义。
5. 支持 whiteout。
6. 支持 opaque directory xattr。
7. 支持 rename。
8. 支持 hardlink/symlink。
9. 支持必要的 xattr。
10. `upper/` 与 `work/` 满足 overlayfs 要求。

如果底层存储不满足要求，容器启动失败，并给出明确错误。需求层不限制 PVC 存储类型，但实现必须 fail fast，不能启动后产生隐性数据损坏。最终判定应以实际 overlay mount 成功为准，静态能力检查只作为提前报错和诊断辅助。

## 安全要求

1. `path` 必须经过路径清洗。
2. 不允许逃逸 Volume 根目录。
3. 不允许符号链接逃逸。
4. 创建 `upper/`、`work/` 前必须确认最终路径仍位于目标 Volume 内。
5. 不允许用户通过 annotation 指定宿主绝对路径。
6. 不允许复用非 Sysbox 管理且非空的目录。
7. 不允许 image chain 不一致时默认复用 upper。
8. 不允许 UID/GID mapping 不兼容时复用 upper。
9. 所有错误必须 fail closed。
10. Sysbox 不能删除 Volume 根目录或用户未声明的目录。

## 与现有 Sysbox 机制的关系

根治方案与现有 Sysbox 机制的关系如下：

- lowerdir 继续使用 containerd/k8s/`hostUsers: false` 原逻辑，Sysbox 不重新设计 lowerdir；
- `sysbox-runc` 现有 rootfs helper/remount 能力可保留为诊断、兼容或过渡路径，但不再作为本功能主实现；
- `sysbox-mgr` 继续负责 PVC-backed upper/work 的目录准备、ownership shift/revert、metadata、恢复与复用校验；
- rootfsCloner 体现了“覆盖原生 rootfs/upper/work 路径”的相关思路，但其现有实现用于 clone/chown 场景，不直接等同于本需求主路径；
- volMgr 现有 sync-in/sync-out 思路仅可作为参考，不应通过停止时 rsync 实现持久化。

## 实现模块

### snapshotter 集成组件

这是根治方案的新增主组件，负责在 writable snapshot 准备阶段接入 PVC-backed `upperdir/workdir`。它可以是：

1. 包装型 overlay snapshotter；
2. 扩展型 snapshotter；
3. 等价的更早 rootfs 组装层组件。

它负责：

1. 识别当前 container/snapshot 是否启用 `sysbox/rootfs-rw-layer`；
2. 读取 metadata 桥接层提供的 `PVC/path` 映射；
3. 结合 sidecar 已触发的节点侧 PVC 根挂载，解析真实 `pvcHostPath`；
4. 请求 `sysbox-mgr` 准备/复用 PVC-backed `upperdir/workdir`；
5. 在首次 overlay mount 前接管 writable snapshot 的 `upperdir/workdir`；
6. 对未启用 rootfs rw layer 的容器保持完全透明；
7. 保持 lowerdir 完全沿用原生 snapshot parent chain。

### sysbox-admission（必需组件）

`sysbox-admission` 是根治方案的必需组件，职责包括：

1. 解析 annotation；
2. 校验 `volumeName` 与 `path`；
3. 向目标 Pod 注入 sidecar，确保 kubelet/CSI 会在节点上真正挂载该 PVC 根目录；sidecar 使用固定 `mountPath=/var/lib/sysbox/rootfs-rw-volume`，不使用 `subPath`；
4. 把 `Pod UID + container name + volumeName + path` 写入 snapshotter 可读 metadata；
5. 保持幂等和 fail-closed。

它不再承担当前 helper 方案中的内部 mount 注入、initContainer、PVC 内配置文件写入等主实现职责。若采用 admission webhook 实现，则仍需要常规 Kubernetes 资源（Deployment / Service / WebhookConfiguration / RBAC / TLS），但其定位已经从“helper 主实现组件”收敛为“挂载配置注入 + sidecar intent bridge”。

建议的 sidecar intent 数据契约如下，`sysbox-admission` 将完整配置写入 `sysbox-rootfs` sidecar 的 `ROOTFS_RW_LAYER_SPEC` 环境变量：

```json
{
  "version": 1,
  "namespace": "default",
  "podName": "sysbox-rootfs-persist",
  "podUID": "11111111-2222-3333-4444-555555555555",
  "containerName": "c1",
  "volumeName": "rootfs",
  "path": "containers/c1",
  "pvcClaimName": "sysbox-rootfs-pvc"
}
```

推荐要求：

1. sidecar intent 的每个 entry 以 `containerName` 匹配业务容器。
2. sidecar intent 的 value 必须包含 `volumeName`、`path`、`pvcClaimName`；`PVCMountPath` 由 snapshotter 从 sidecar OCI mounts 或 kubelet fallback 解析。
3. `sysbox-admission` 负责注入 canonical `sysbox-rootfs` sidecar、PVC volume mounts 和 `ROOTFS_RW_LAYER_SPEC`。
4. sidecar intent 必须由当前 Pod spec 派生，不保留跨 Pod 文件状态。
5. sidecar intent 生成必须 fail closed；若注入失败，Pod admission 应失败或显式拒绝 rootfs rw layer 特性启用。

### sysbox-runc

1. 保持现有 user namespace、sysbox-fs、sysbox-mgr 协调能力。
2. 保持现有 rootfs helper/remount 能力作为兼容、诊断或过渡路径。
3. 不再承担 rootfs rw layer 主实现，也不负责把 annotation 注入成容器挂载配置。
4. 如未来保留有限参与，职责应仅限于消费 snapshotter 已准备好的 rootfs 状态并输出诊断信息。

### sysbox-mgr

1. 新增或扩展 rootfs rw layer manager。
2. 接收 snapshotter 集成组件传入的容器身份、PVC/path、镜像身份、uid/gid mapping。
3. 管理目标目录初始化。
4. 管理 `meta.json`。
5. 在容器启动前对持久化 `upper/` 执行与现有 overlay upper chown 相同语义的 ownership shift。
6. 在 unregister、pause、resume、异常恢复阶段对持久化 `upper/` 执行 revert 或状态修复。
7. 管理异常恢复。
8. 提供本地接口给 snapshotter 集成组件与（如需要）`sysbox-runc` 使用。

建议接口草案如下，最终字段以实现时的状态恢复和审计需要为准：

```text
PrepareRootfsRwLayer(containerID, podUID, namespace, podName, containerName, volumeName, pvcHostPath, path, imageRef, imageChainID, uidMap, gidMap)
  -> upperdir, workdir

DetachRootfsRwLayer(containerID)
  -> ok

RecoverRootfsRwLayer(containerID, podUID, namespace, podName, containerName, volumeName, pvcHostPath, path, imageChainID, uidMap, gidMap)
  -> ok
```

字段约束：

1. `pvcHostPath` 是节点上真实的 PVC 根挂载路径，不允许为空。
2. `path` 是 PVC 内相对路径，可为空，不能为空绝对路径。
3. `imageChainID` 继续作为复用兼容性校验主字段。
4. `uidMap/gidMap` 继续用于 ownership 校验与迁移。
5. `upperdir/workdir` 的返回值必须位于 `pvcHostPath` 之下。

### sysbox-snapshotter 内部接口建议

建议在 `sysbox-snapshotter/` 内部形成三段式接口：

```text
ResolveRootfsRwLayer(podUID, containerName)
  -> volumeName, path, pvcClaimName

ResolvePVCMountPath(podUID, volumeName)
  -> pvcHostPath

PrepareWritableSnapshot(snapshotKey, parentChain, pvcHostPath, path, imageRef, imageChainID, uidMap, gidMap)
  -> upperdir, workdir
```

说明：

1. `ResolveRootfsRwLayer()` 负责从 sidecar OCI spec 的 `ROOTFS_RW_LAYER_SPEC` 读取逻辑配置。
2. `ResolvePVCMountPath()` 优先从 sidecar OCI mounts 解析真实 `pvcHostPath`（PVC 根目录），仅在 sidecar spec 不可用时回退到 kubelet/CSI 挂载状态。
3. `PrepareWritableSnapshot()` 在拿到 `pvcHostPath` 后，再把 annotation 中的相对 `path` 拼接成当前容器的 backing root。
4. `PrepareWritableSnapshot()` 负责在 writable snapshot prepare 阶段调用 `sysbox-mgr`，取得 PVC-backed `upperdir/workdir` 并接入首次 overlay mount。
5. snapshotter 不能仅凭 `volumeName` 推断 PVC 宿主路径；必须结合 sidecar 已触发的节点侧真实挂载状态解析。
6. 若任一步骤失败，snapshotter 必须 fail closed，并输出明确错误。

### sysbox-fs

原则上不需要新增职责。

## 项目文件规划

基于当前仓库已经采用的多组件并列布局（`sysbox-runc/`、`sysbox-mgr/`、`sysbox-fs/`、`sysbox-ipc/`、`sysbox-admission/` 等），根治方案建议继续沿用“按运行组件拆目录”的方式，而不是把新能力塞进现有组件内部。

推荐的顶层组件规划如下：

```text
sysbox-admission/
sysbox-snapshotter/
sysbox-mgr/
sysbox-runc/
sysbox-fs/
sysbox-ipc/
sysbox-libs/
sysbox-k8s-manifests/
charts/
```

说明：

1. `sysbox-admission/` 继续保留为独立组件，职责是 Kubernetes 控制面侧的 annotation 解析、挂载配置注入与 metadata bridge；它不能被 `sysbox-runc` 取代。
2. 新增 `sysbox-snapshotter/` 作为独立组件，负责在 containerd writable snapshot prepare 阶段接管 PVC-backed `upperdir/workdir`。
3. `sysbox-mgr/` 保持为独立节点本地管理组件，负责 rootfs rw layer 目录、metadata、ownership、恢复与复用策略。
4. `sysbox-runc/` 保持为独立 OCI runtime 组件，不承载 snapshotter 或 admission 的主实现逻辑。
5. `sysbox-fs/` 原则上不扩职责。
6. `sysbox-k8s-manifests/` 与 `charts/` 需要同时支持部署 `sysbox-admission` 与 `sysbox-snapshotter`。

不建议的文件规划：

1. 不建议把 snapshotter 直接塞进 `sysbox-runc/`，因为二者职责层次不同：前者是 containerd 存储/快照层，后者是 OCI runtime 执行层。
2. 不建议把 snapshotter 直接塞进 `sysbox-mgr/`，因为 `sysbox-mgr` 是节点本地管理 daemon，而 snapshotter 是 rootfs 首次组装层。
3. 不建议把 admission 与 snapshotter 做成单一运行组件；二者可以在产品规划上归为同一个 rootfs-persistence 子系统，但在代码目录与部署形态上应保持独立。

## 可观测性

Sysbox 必须输出结构化日志：

1. Annotation 解析成功/失败。
2. rootfs rw layer 初始化。
3. rootfs rw layer 复用。
4. image chain 校验失败。
5. UID/GID mapping 校验失败。
6. overlay mount 失败。
7. ownership shift/revert 失败。
8. detach 成功/失败。

建议 Kubernetes Event：

1. `RootfsRwLayerPrepared`
2. `RootfsRwLayerReused`
3. `RootfsRwLayerRejected`
4. `RootfsRwLayerRecovering`
5. `RootfsRwLayerMountFailed`

## 验收标准

### 基础持久化

1. 创建启用 `sysbox/rootfs-rw-layer` 的 Pod。
2. 进入容器执行：

```bash
vim --version || true
apt-get update
apt-get install vim -y
echo hello > /root/sysbox-rootfs-persist
```

3. 删除 Pod。
4. 使用相同 YAML 重建 Pod。
5. 验证：

```bash
vim --version
cat /root/sysbox-rootfs-persist
```

期望：`vim` 存在，文件内容为 `hello`。

### whiteout 持久化

1. 删除镜像内已有文件。
2. 删除 Pod 并重建。
3. 验证该文件仍然不存在。

### 文件修改持久化

1. 修改镜像内已有文件。
2. 删除 Pod 并重建。
3. 验证修改仍存在。

### 多容器

1. `c1` 配置 `path: containers/c1`。
2. `c2` 配置 `path: containers/c2`。
3. 两个容器分别写入 rootfs。
4. 删除 Pod 并重建。
5. 两个容器分别恢复自己的 rootfs 数据，互不污染。

### 同 path 复用

1. 两个容器配置相同 `volumeName + path`。
2. 若存在桥接层，它应在可判断范围内尽早拒绝明显冲突配置。
3. 跨 Pod 复用相同 PVC/path 的最终并发语义由产品策略、sidecar intent 与底层存储能力共同决定；根治方案阶段应至少保证错误可被清晰归因，而不是隐式损坏数据。

### snapshotter 节点侧准备

1. `sysbox/rootfs-rw-layer` 引用的 `volumeName` 存在于 `spec.volumes[]`，但不是 `persistentVolumeClaim` 类型，容器启动失败。
2. `sysbox-admission` 注入的 sidecar 必须保证 kubelet/CSI 在节点侧生成该 PVC 根目录的真实挂载，且 sidecar intent 必须能在 snapshotter prepare writable snapshot 时可见。
3. snapshotter 集成组件能在首次 overlay mount 前调用 `sysbox-mgr`，拿到 PVC-backed `upperdir/workdir`。
4. lowerdir 继续沿用原生 snapshot parent chain。
5. 若 sidecar intent 缺失、PVC/path 无法解析或 `sysbox-mgr` 准备失败，容器启动失败并输出明确错误。

### 非空目录保护

1. 指定一个已有非空、但无 `meta.json` 的目录。
2. 容器启动失败。
3. 错误信息提示该目录不是 Sysbox 管理的 rootfs rw layer。

### 镜像变更保护

1. 使用镜像 A 创建并写入 rootfs。
2. 改为镜像 B，复用同一 path。
3. 容器启动失败。
4. 错误信息指出 image chain mismatch。

### userns 权限

1. 使用 `hostUsers:false`。
2. 容器内 root 写入 `/root`、`/etc`、`/usr/local`。
3. 文件不显示为 `nobody:nogroup`。
4. 重建后权限仍正确。

### 异常恢复

1. 模拟 `sysbox-mgr` 重启。
2. 删除并重建 Pod。
3. rootfs upper 可恢复复用。
4. 不因为 Sysbox 自身状态残留导致重建阻塞。

### 存储能力失败

1. 使用一个不满足 overlay upper 语义的 Volume。
2. 容器启动失败。
3. 错误明确指出 overlay upper/work capability check failed。

### Sysbox 回归

1. Docker-in-Docker 正常启动。
2. K3s-in-Docker 正常启动。
3. sysbox-fs proc/sys 虚拟化不回归。
4. sysbox-mgr 日志无 unknown container、unsafe procfs、unexpected seccomp notify 错误。

## 生产级边界结论

该功能的本质不是停止时把 rootfs 拷贝到 PVC，而是从容器启动开始就把 overlayfs `upperdir/workdir` 放到用户 Volume 中。这样 rootfs 数据天然随 Volume 持久化，删除 Pod 也不会丢失。

Sysbox 需要完成的核心工作是：

1. 提供简洁 Annotation：`sysbox/rootfs-rw-layer`。
2. 通过 `sysbox-admission` 把用户意图转换成 sidecar PVC 根挂载与 snapshotter 可消费 metadata。
3. 在 snapshotter prepare writable snapshot 阶段准备 Volume 内的 `upper/work`。
4. 严格校验复用安全。
5. 在首次 overlay mount 前，将 rootfs overlay 的 `upperdir/workdir` 接到持久化路径。
6. 管好 userns ownership、异常恢复和可观测性。

## 可行性结论

基于当前代码与集群验证，需求的根治方案结论如下：

1. `sysbox-runc` 当前 helper 接手时，task rootfs overlay 已经挂好；此时 mountinfo 中的 lowerdir 已经表现为 `hostUsers: false` 机制下的 `/tmp/ovl-idmapped...` 运行时展示值。
2. 这些 `/tmp/ovl-idmapped...` 路径不是可重新解析、可稳定复用的真实 lowerdir 输入，因此 helper 层晚期 remount 不是可靠主路径。
3. snapshot 原生 upper/work 目录在运行时确实存在，但在当前 helper 接入点已经晚于首次 overlay mount，因此“在 helper 里预先 bind 覆盖原生 upper/work”也不可行。
4. 因此，若要彻底解决，必须把持久化 upper/work 的接入点前移到 snapshotter / 首次 rootfs 组装层。
5. 现有 Sysbox 代码中，`rootfsCloner` 已经体现了“覆盖原生 rootfs/upper/work 路径”的相关思路，说明这类路径替换在更早阶段是合理的，但当前 rootfs rw layer 主实现不应继续停留在 helper/remount 层。
6. 额外的集群验证表明：如果只给业务容器注入带 `subPath=<annotation.path>` 的保留挂载，那么在 snapshotter prepare 阶段，宿主上往往还看不到该 Pod 对应的 PVC 宿主路径，时序上无法稳定作为 `upperdir/workdir` 输入。
7. 改为给 Pod 注入一个 sidecar，并让 sidecar 只挂载 PVC 根目录（不带 `subPath`）后，宿主上在 snapshotter prepare 阶段已经能看到该 PVC 根目录的真实挂载；因此 sidecar-root-mount 是当前验证通过的根治路径前提。

## 参考

- 火山引擎《容器 rootfs 持久化》：https://www.volcengine.com/docs/6460/1256367?lang=zh
- Sysbox rootfs overlay helper 现状：`sysbox-runc/libcontainer/rootfs_init_linux.go`
- Sysbox rootfs upper ownership 生命周期：`sysbox-mgr/mgr.go`
- Sysbox volume sync 参考实现：`sysbox-mgr/volMgr/volMgr.go`
