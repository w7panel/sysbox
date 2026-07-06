# sysbox-snapshotter remap-ids rootfs 权限问题分析

本文记录在 K3s/containerd 环境中启用 `sysbox` proxy snapshotter `remap-ids` capability 后，`runtimeClassName: sysbox-runc`、`hostUsers: false` 的 `alpine:latest` Pod 启动失败问题的验证结论、根因分析和推荐解决方案。

## 结论摘要

问题根因不是某个 Pod 持续占用 `alpine:latest` 镜像，也不是非 `sysbox-runc` Pod 使用同一镜像导致 sysbox rootfs 权限异常。

根因是：`sysbox-snapshotter` 的 remap 模式可以在已有 snapshot 状态不兼容的情况下被切换。启用 `remap-ids` 后，containerd 不再创建 fallback `*-remap` snapshot，而是信任 `sysbox-snapshotter` 返回 idmapped mount；但 `sysbox-snapshotter` 会继续复用旧模式下已经 unpack 的 committed lower snapshots，并只给新 active snapshot 返回 `uidmap=` / `gidmap=`。这会形成“idmapped active mount + 非 idmapped 历史 lower layer”的混合 rootfs，最终在 runc 设置 rootfs bind mount 时触发权限错误。

推荐代码修复方向：不要允许不同 remap 语义的 snapshot chain 复用同一套 committed lower snapshots。应让 remap 模式或 remap 配置参与 snapshot chain identity，并在 snapshotter 启动或创建 snapshot 时检测不兼容的历史状态，必要时 fail fast 或重建对应 chain。

## 复现现象

测试对象：

- image: `alpine:latest`
- runtime: `runtimeClassName: sysbox-runc`
- user namespace: `hostUsers: false`
- snapshotter: `sysbox`

未启用 `remap-ids` 时，`alpine:latest` sysbox Pod 可以启动，并能在容器内执行读写验证：

```bash
id
cat /proc/self/uid_map
cat /proc/self/gid_map
cat /etc/resolv.conf >/dev/null
touch /alpine-base
ls -ln /alpine-base
rm /alpine-base
```

启用 `remap-ids` 后，同样的 `alpine:latest` sysbox Pod 启动失败，错误为：

```text
failed to create containerd task: failed to create shim task: OCI runtime create failed:
container_linux.go:439: starting container process caused:
process_linux.go:608: container init caused:
rootfs_linux.go:76: setting up rootfs mounts caused:
open etc/resolv.conf: permission denied
```

失败时的 active overlay mount 带有 idmapped mount options：

```text
uidmap=0:3263102976:65536
gidmap=0:3263102976:65536
upperdir=.../io.containerd.snapshotter.v1.sysbox/snapshots/1378/fs
workdir=.../io.containerd.snapshotter.v1.sysbox/snapshots/1378/work
lowerdir=.../io.containerd.snapshotter.v1.sysbox/snapshots/16/fs:.../snapshots/15/fs
```

但 lower layer 仍是旧模式下的 host-root ownership：

```text
.../snapshots/15     uid=0 gid=0 mode=drwx------
.../snapshots/15/fs  uid=0 gid=0 mode=drwxr-xr-x
.../snapshots/16     uid=0 gid=0 mode=drwx------
.../snapshots/16/fs  uid=0 gid=0 mode=drwxr-xr-x
```

## 已排除的假设

### 旧 sysbox alpine Pod 仍在运行

验证时确实发现过一个旧的 sysbox alpine Pod：

```text
default/copy-pasr-testhostusers-wwblioym-wmpd-867987d6f4-ptgc8
image=alpine:latest
runtimeClass=sysbox-runc
hostUsers=false
phase=Running
```

该 Pod 的 rootfs lowerdir 使用了同一组 sysbox lower snapshots：

```text
lowerdir=.../snapshots/1383/fs:.../snapshots/16/fs:.../snapshots/15/fs
upperdir=.../snapshots/1385/fs
```

但将该 Deployment 缩容到 0、释放旧 active snapshot 后，不清理 sysbox committed snapshots，重新启用 `remap-ids` 并创建新的 `alpine:latest` sysbox Pod，仍然失败：

```text
open etc/resolv.conf: permission denied
```

因此，“旧 sysbox alpine Pod 正在运行”不是必要条件。真正关键的是 sysbox snapshotter 下已经存在旧模式创建的 committed lower snapshots。

### 非 sysbox-runc Pod 使用同一 alpine 镜像

例如 `default/testhostusers-wwblioym-6cb9bbf6c4-4rbhd` 也使用 `alpine:latest`，但它不是 sysbox runtime，实际 snapshotter 为 `overlayfs`：

```text
snapshotter: overlayfs
Snapshotter: overlayfs
Image: docker.io/library/alpine:latest
```

其 rootfs mount 使用默认 overlayfs snapshotter 路径：

```text
upperdir=/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/100/fs
workdir=/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/100/work
```

它不会直接占用或修改：

```text
/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox/snapshots/...
```

非 sysbox Pod 可以共享 containerd image content/blob，但不会直接决定 sysbox snapshotter 的 lowerdir ownership，也不是本问题直接根因。

### busybox 通过不代表问题不存在

`busybox:1.36` 在无 `remap-ids` 和启用 `remap-ids` 两种情况下都能启动，并能在容器内执行读写验证。但这只能说明问题不对所有镜像 100% 复现。

`alpine:latest` 能稳定暴露问题，说明失败与镜像 layer 内容、lower chain 状态以及 rootfs setup 访问路径有关。不能用 busybox 成功来证明 remap-ids 路径正确。

## 代码路径分析

### remap mode 的来源

`sysbox-snapshotter` 启动时会根据 containerd proxy plugin capabilities 判断是否启用 idmapped mount。containerd 模板配置类似：

```toml
[proxy_plugins."sysbox"]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
  capabilities = ["remap-ids"]
```

启用后，containerd 插件状态显示：

```text
Type:             io.containerd.snapshotter.v1
ID:               sysbox
Capabilities:     remap-ids
```

如果只改 containerd 配置并重启 k3s，但不重启 `sysbox-snapshotter`，旧 `sysbox-snapshotter` 进程可能仍沿用旧 remapMode。因此切换该 capability 后必须同时重启 `sysbox-snapshotter`，否则会得到更混乱的测试结果。

### sysbox-snapshotter 当前行为

关键代码路径：

- `sysbox-snapshotter/snapshotter/remap.go`
- `sysbox-snapshotter/snapshotter/overlay.go`
- `sysbox-snapshotter/snapshotter/mounts.go`

`remapOptions()` 在 idmapped 模式下从 snapshot labels 返回 mount options：

```go
return []string{fmt.Sprintf("uidmap=%s", uidmap), fmt.Sprintf("gidmap=%s", gidmap)}, nil
```

`createSnapshot()` 会根据当前 snapshot labels 计算 mapped root owner，并对当前新建 snapshot 的 `fs` 和 `work` 做 chown：

```go
if mappedUID != -1 && mappedGID != -1 {
    os.Lchown(filepath.Join(path, "fs"), mappedUID, mappedGID)
    if kind == snapshots.KindActive {
        os.Lchown(filepath.Join(path, "work"), mappedUID, mappedGID)
    }
}
```

`mounts()` 则会把 parent snapshots 原样拼接为 lowerdir：

```go
parentPaths := make([]string, len(s.ParentIDs))
for i := range s.ParentIDs {
    parentPaths[i] = o.upperPath(s.ParentIDs[i])
}
options = append(options, fmt.Sprintf("lowerdir=%s", strings.Join(parentPaths, ":")))
```

当前缺失的是：

1. committed lower snapshots 没有记录创建时的 remap mode 或 remap identity。
2. active snapshot 创建时没有校验 parent chain 是否与当前 remap mode 兼容。
3. 启用 `remap-ids` 后没有重新 unpack、隔离或迁移旧 committed lower snapshots。
4. `mounts()` 直接复用 `ParentIDs`，使旧模式 lowerdir 被放入新的 idmapped active overlay mount。

## idmapped mount 为什么没有自动解决

`uidmap=0:3263102976:65536` 的含义是：

```text
容器视图 uid 0 <-> 宿主 uid 3263102976
容器视图 gid 0 <-> 宿主 gid 3263102976
```

这不会把磁盘上已有的 host uid 0 文件自动变成 host uid 3263102976 文件。idmapped mount 只是按给定映射解释 inode uid/gid，不会修改旧 lowerdir 的真实 ownership。

因此，在当前 idmapped 视图下，宿主 uid 3263102976 的文件才会呈现为容器 root。旧 lowerdir 仍是 host uid 0/gid 0，就不一定能在新的 userns/idmap 语义下被容器 root 正确访问。

这也是为什么问题不是“lowerdir 被隔离了所以互不影响”。lowerdir 的磁盘 ownership 确实没有被改动，但 overlayfs 组合 rootfs 时仍要把 lowerdir 纳入当前 mount 语义，旧 ownership 与新 idmap 不一致就会在 runc rootfs setup 阶段暴露为权限错误。

## 与 containerd fallback remap 的区别

未启用 `remap-ids` capability 时，containerd 不认为 proxy snapshotter 能直接返回 idmapped mount。它会走 fallback remap/chown 路径，创建类似 `*-remap` 的 snapshot，让 lower chain ownership 与目标 user namespace 对齐。

启用 `remap-ids` 后，containerd 看到 proxy plugin capability：

```text
Capabilities: remap-ids
```

于是 containerd 不再创建 fallback `*-remap` parent，而是把 remap 责任交给 snapshotter。此时如果 snapshotter 只给 active mount 返回 `uidmap/gidmap`，但继续复用旧 committed lower snapshots，就会形成混合 rootfs：

```text
containerd: 不再 chown fallback lower，因为 sysbox 声明支持 remap-ids
sysbox-snapshotter: active mount 返回 uidmap/gidmap，但 lowerdir 复用旧 chain
overlayfs/runc: rootfs 权限语义不一致，访问 etc/resolv.conf 等路径失败
```

这就是默认 overlayfs/fallback remap 路径没有同样问题，而当前 sysbox idmapped proxy snapshotter 路径会失败的原因。

## 运维规避方案

### 保持不启用 remap-ids

最稳妥的短期方案是不要声明：

```toml
capabilities = ["remap-ids"]
```

也就是让 containerd 不进入 idmapped proxy snapshotter 路径，继续使用 fallback 行为。

### 如必须启用 remap-ids

切换前必须避免旧 sysbox snapshotter 状态被继续复用：

1. 停止会自动重建 sysbox Pod 的 Deployment、StatefulSet、Job 等 workload。
2. 删除所有使用 `runtimeClassName: sysbox-runc` 的 Pod。
3. 清理 `sysbox` snapshotter 下的相关 active/committed snapshots，确保镜像在启用 `remap-ids` 后重新创建一致的 chain。
4. 修改 k3s containerd 模板，而不是只改生成后的 `config.toml`。
5. 重启 k3s/containerd。
6. 重启 `sysbox-snapshotter`，确认它读取到新的 remap mode。
7. 用全新 sysbox Pod 验证 `ctr plugins ls -d id==sysbox`、snapshot mounts 和容器内读写行为。

该方案仍属于运维规避，不是根治。只要代码仍允许新旧 remap 语义复用同一 lower chain，后续切换或状态残留仍可能复发。

## 推荐代码修复方案

### 方案一：remap identity 参与 snapshot chain identity

这是推荐的根治方向。

对同一个 image layer，不能只用原始 layer digest 作为 sysbox snapshotter committed snapshot 的唯一身份。snapshot identity 应包含 remap 语义，例如：

```text
layer digest
snapshotter name: sysbox
remap mode: disabled | idmapped
uidmap/gidmap identity: none | uidmap+gidmap digest
```

这样：

- 无 `remap-ids` 时创建的 committed snapshots 不会被启用 `remap-ids` 后的 active snapshot 复用。
- 不同 uidmap/gidmap 的 chain 不会互相复用。
- 切换配置后，新 Pod 会重新 unpack 或创建与当前 remap identity 匹配的 lower chain。

实现上可以通过 containerd metadata key、snapshot label 或 sysbox snapshotter 内部映射层完成，但核心原则是不允许不同 remap identity 共享同一个 committed lower snapshot。

### 方案二：创建 active snapshot 时校验 parent chain

在 `createSnapshot()` 或 `mounts()` 阶段检查所有 `ParentIDs` 的 remap metadata。如果当前 active snapshot 带有 `uidmap/gidmap`，但 parent committed snapshots 是旧的 disabled 模式，直接返回明确错误，例如：

```text
incompatible sysbox snapshot remap mode: active snapshot requires idmapped chain,
but parent sha256:... was created with remap mode disabled; clean or rebuild sysbox snapshots
```

这个方案不能自动修复，但能避免最终在 runc 阶段表现成难定位的：

```text
open etc/resolv.conf: permission denied
```

它适合作为方案一的防御性校验。

### 方案三：snapshotter 启动时 fail fast

`sysbox-snapshotter` 启动时读取当前 remap mode，并扫描已有 committed snapshots 的 remap metadata。如果发现历史状态与当前配置不兼容，直接拒绝启动或进入只读保护模式，并给出明确修复提示。

这可以防止“切换 capability 后服务正常启动，但新 Pod 随机失败”的隐性故障。

### 不推荐：在线 chown 或迁移已有 lowerdir

不建议对已有 committed lowerdir 做在线递归 chown 作为主要修复方案，原因：

1. committed lower snapshots 可能仍被运行中容器或其他 snapshot chain 引用。
2. 递归修改只读镜像层会破坏 layer cache 的不可变语义。
3. 大镜像递归 chown 成本高，容易影响 Pod 创建热路径。
4. 出错后会留下半迁移状态，故障更难恢复。

如果确实需要迁移，也应作为离线维护工具，并要求先停止所有 sysbox workload、冻结 containerd 状态、备份 metadata，再执行一次性转换。

## 建议验收矩阵

修复后至少覆盖以下场景：

| 场景 | 预期 |
|---|---|
| 无 `remap-ids` + 无历史 sysbox snapshots | Pod Running，容器内读写成功 |
| 无 `remap-ids` + 已有 sysbox snapshots | Pod Running，容器内读写成功 |
| 启用 `remap-ids` + 无历史 sysbox snapshots | Pod Running，mount options 含 `uidmap/gidmap`，容器内读写成功 |
| 启用 `remap-ids` + 已有 disabled 模式 sysbox snapshots | 要么自动创建新的兼容 chain，要么 fail fast 并给出明确错误；不能进入 runc `permission denied` |
| 从启用 `remap-ids` 回切到无 `remap-ids` | 不复用不兼容 chain，Pod 行为可预测 |
| 非 sysbox-runc Pod 使用同一镜像 | 不影响 sysbox snapshotter chain，默认 overlayfs Pod 正常 |

每个场景应记录三类证据：

1. containerd plugin capability：`ctr plugins ls -d id==sysbox`
2. snapshot mount options：`ctr -n k8s.io snapshots --snapshotter sysbox mounts <snapshot-key>`
3. 容器内实际行为：读 `/etc/resolv.conf`、查看 uid/gid map、rootfs 写入/删除

## 当前推荐行动

短期：生产或共享测试集群不要直接启用 `capabilities = ["remap-ids"]`，除非先清理所有 sysbox snapshotter 状态并完成完整回归验证。

中期：在 `sysbox-snapshotter` 中增加 remap metadata 和 parent chain 兼容性校验，避免继续生成混合 rootfs。

长期：让 remap identity 参与 sysbox committed snapshot key/chain identity，使 containerd 在不同 remap 语义下不会复用同一套 lower chain，从根上消除配置切换导致的状态污染。
