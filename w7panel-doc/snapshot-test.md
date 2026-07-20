# Sysbox rootfs 持久化部署与测试

本文说明如何在 K3s 集群部署 `sysbox-snapshotter`，以及如何验证容器根目录 `/` 的写入在 Pod 重建后仍然存在。

## 一键测试

部署完成后可直接运行完整端到端测试：

```bash
cd sysbox
KUBECONFIG=/home/.kubeconfig ./w7panel-doc/snapshot-test.sh
```

可通过环境变量覆盖测试参数：

```bash
NAMESPACE=default \
STORAGE_CLASS=longhorn \
TEST_IMAGE=ubuntu:24.04 \
TIMEOUT=300s \
HOST_DEBUG_NAMESPACE=default \
HOST_DEBUG_POD=sysbox-debug-tools \
KUBECONFIG=/home/.kubeconfig \
./w7panel-doc/snapshot-test.sh
```

脚本会检查宿主服务与 containerd plugin，创建临时 PVC/Deployment，完成 rootfs 写入、修改、删除和 Pod 重建验证，检查服务日志，最后自动删除临时资源。成功时退出码为 `0` 并输出 `PASS`。

## 工作原理

该功能不会把 PVC 直接挂载到容器 `/`。直接挂载 `/` 会遮盖镜像 rootfs，并不是正确的持久化方式。

启用后，`sysbox-snapshotter` 使用 `fuse-overlayfs` 组装容器 rootfs：

```text
lowerdir = containerd 管理的镜像只读层
upperdir = PVC 中的 upper/
workdir  = PVC 中的 work/
merged   = 容器的 /
```

`sysbox-admission` 负责校验 `sysbox/rootfs-rw-layer` annotation，并注入 `sysbox-rootfs` sidecar。sidecar 只用于把 PVC 暴露给 kubelet/CSI；业务容器的 `/` 由 snapshotter 在首次 mount 前改写。

## 前置条件

- K3s 节点运行 Linux amd64。
- 集群已安装可用的 CSI StorageClass，例如 Longhorn。
- 节点可以拉取部署镜像。
- 所有集群命令显式使用测试 kubeconfig：

```bash
export KUBECONFIG=/home/.kubeconfig
```

当前验证通过的镜像：

```text
docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s:rootfs-20260713075329-5
docker.cnb.cool/i0358/zpk/sysbox-admission:local-20260713065909
```

这是合并镜像前的历史验收记录；当前 release 默认由
`sysbox-deploy-k3s` 同时提供 installer 和 admission。

installer 镜像 `-5` 包含以下兼容修复：

- 自动安装宿主 `fuse-overlayfs`。
- 避免 K3s containerd 2.2.3 处理 UID/GID mapping options 时 panic。
- 避免 `sysbox-rootfs` sidecar 首次创建时出现 OCI spec 自引用竞态。

## 部署

先给目标节点添加 Sysbox 调度标签：

```bash
kubectl --kubeconfig "$KUBECONFIG" label node <node-name> sysbox-runtime=running --overwrite
```

部署或升级 Helm chart：

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  --kubeconfig "$KUBECONFIG" \
  --namespace kube-system \
  --create-namespace \
  --set nodeSelector.sysbox-runtime=running \
  --set snapshotter.enabled=true \
  --set installer.image.repository=docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s \
  --set installer.image.tag=rootfs-20260713075329-5 \
  --set installer.image.pullPolicy=IfNotPresent \
  --set admission.enabled=true \
  --set admission.image.repository=docker.cnb.cool/i0358/zpk/sysbox-admission \
  --set admission.image.tag=local-20260713065909 \
  --set admission.image.pullPolicy=IfNotPresent \
  --wait \
  --timeout 10m
```

installer 更新宿主服务和 K3s containerd 配置时会短暂重启 K3s。单节点测试集群在此期间出现 API 暂时不可用属于预期现象。

检查 Helm 与 Pod：

```bash
helm --kubeconfig "$KUBECONFIG" get values w7panel-sysbox -n kube-system -a

kubectl --kubeconfig "$KUBECONFIG" get pods -n kube-system \
  -l app.kubernetes.io/instance=w7panel-sysbox -o wide
```

installer 日志最后应出现 `Done.`：

```bash
kubectl --kubeconfig "$KUBECONFIG" logs -n kube-system \
  -l app.kubernetes.io/name=w7panel-sysbox \
  -c w7panel-sysbox --tail=200 --prefix
```

## 检查宿主服务

通过调试 Pod 进入宿主 namespace：

```bash
kubectl --kubeconfig "$KUBECONFIG" exec -n default sysbox-debug-tools -- \
  nsenter -t 1 -m -u -i -n -p -- \
  systemctl is-active k3s sysbox-snapshotter sysbox-fs sysbox-mgr
```

四项均应返回 `active`。

确认 snapshotter socket 和 containerd proxy plugin：

```bash
kubectl --kubeconfig "$KUBECONFIG" exec -n default sysbox-debug-tools -- \
  nsenter -t 1 -m -u -i -n -p -- \
  k3s ctr plugins ls -d id==sysbox
```

预期输出包含：

```text
Type:             io.containerd.snapshotter.v1
ID:               sysbox
address           /run/sysbox-snapshotter.sock
Capabilities:     remap-ids
```

containerd 配置应满足：

```toml
[proxy_plugins."sysbox"]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
  capabilities = ["remap-ids"]

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.'sysbox-runc']
  snapshotter = "sysbox"
  pod_annotations = ["sysbox/rootfs-rw-layer"]
```

## 创建测试工作负载

以下示例假设集群存在 `longhorn` StorageClass：

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: sysbox-rootfs-test
spec:
  accessModes:
  - ReadWriteOnce
  storageClassName: longhorn
  resources:
    requests:
      storage: 2Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sysbox-rootfs-test
spec:
  replicas: 1
  selector:
    matchLabels:
      app: sysbox-rootfs-test
  template:
    metadata:
      labels:
        app: sysbox-rootfs-test
      annotations:
        sysbox/rootfs-rw-layer: |-
          [
            {
              "name": "system",
              "volumeName": "rootfs",
              "path": "system"
            }
          ]
    spec:
      runtimeClassName: sysbox-runc
      hostUsers: false
      containers:
      - name: system
        image: ubuntu:24.04
        command: ["sleep", "infinity"]
      volumes:
      - name: rootfs
        persistentVolumeClaim:
          claimName: sysbox-rootfs-test
```

保存为 `/tmp/sysbox-rootfs-test.yaml` 后执行：

```bash
kubectl --kubeconfig "$KUBECONFIG" apply -f /tmp/sysbox-rootfs-test.yaml
kubectl --kubeconfig "$KUBECONFIG" rollout status deployment/sysbox-rootfs-test --timeout=5m
```

Pod 应为 `2/2 Running`：一个业务容器和一个 admission 自动注入的 `sysbox-rootfs` sidecar。

```bash
kubectl --kubeconfig "$KUBECONFIG" get pod -l app=sysbox-rootfs-test
```

## 验证根挂载

获取 Pod 名称：

```bash
POD=$(kubectl --kubeconfig "$KUBECONFIG" get pod \
  -l app=sysbox-rootfs-test -o jsonpath='{.items[0].metadata.name}')
```

检查容器 `/`：

```bash
kubectl --kubeconfig "$KUBECONFIG" exec "$POD" -c system -- \
  sh -c 'grep " / / " /proc/self/mountinfo'
```

预期文件系统类型为：

```text
fuse.fuse-overlayfs
```

容器内的 mountinfo 不会展示 FUSE helper 的完整 `upperdir/workdir` 参数。需要从宿主检查：

```bash
kubectl --kubeconfig "$KUBECONFIG" exec -n default sysbox-debug-tools -- \
  nsenter -t 1 -m -u -i -n -p -- \
  pgrep -af fuse-overlayfs
```

目标业务容器对应进程的参数应类似：

```text
workdir=/var/lib/kubelet/pods/<pod-uid>/volumes/kubernetes.io~csi/<pvc>/mount/system/work
upperdir=/var/lib/kubelet/pods/<pod-uid>/volumes/kubernetes.io~csi/<pvc>/mount/system/upper
lowerdir=/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox/snapshots/<id>/fs
```

只有 `upperdir/workdir` 应位于 PVC；`lowerdir` 继续位于 snapshotter 的镜像 snapshot 路径。

## 验证 Pod 重建持久化

在根目录写入唯一内容：

```bash
TOKEN="sysbox-rootfs-$(date +%s)"

kubectl --kubeconfig "$KUBECONFIG" exec "$POD" -c system -- \
  sh -c "printf '%s\\n' '$TOKEN' > /a.txt && sync && cat /a.txt"
```

记录旧 Pod UID，然后删除 Pod：

```bash
OLD_UID=$(kubectl --kubeconfig "$KUBECONFIG" get pod "$POD" -o jsonpath='{.metadata.uid}')
kubectl --kubeconfig "$KUBECONFIG" delete pod "$POD" --wait=true
kubectl --kubeconfig "$KUBECONFIG" rollout status deployment/sysbox-rootfs-test --timeout=5m
```

获取新 Pod 并确认 UID 已变化：

```bash
NEW_POD=$(kubectl --kubeconfig "$KUBECONFIG" get pod \
  -l app=sysbox-rootfs-test -o jsonpath='{.items[0].metadata.name}')
NEW_UID=$(kubectl --kubeconfig "$KUBECONFIG" get pod "$NEW_POD" -o jsonpath='{.metadata.uid}')

test "$OLD_UID" != "$NEW_UID"
```

验证 `/a.txt`：

```bash
ACTUAL=$(kubectl --kubeconfig "$KUBECONFIG" exec "$NEW_POD" -c system -- cat /a.txt)
test "$ACTUAL" = "$TOKEN"
printf 'persist test passed: %s\n' "$ACTUAL"
```

## CKM 验证

CKM 生成的 Server Pod 应同时满足：

```yaml
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
metadata:
  annotations:
    sysbox/rootfs-rw-layer: '[{"name":"<server-container>","path":"","volumeName":"varlibrancherk3s"}]'
```

检查实际 Pod：

```bash
kubectl --kubeconfig "$KUBECONFIG" get pod -n <ckm-namespace> <ckm-pod> \
  -o jsonpath='{.spec.runtimeClassName}{"\n"}{.spec.hostUsers}{"\n"}{.metadata.annotations.sysbox/rootfs-rw-layer}{"\n"}'
```

持久化测试与上节相同：在 CKM Server 容器内写入 `/a.txt`，删除 Server Pod，等待 Deployment 创建新的 `2/2 Running` Pod，再读取 `/a.txt`。

## 常见问题

### `fuse-overlayfs not installed`

确认 installer 使用 `rootfs-20260713075329-5` 或更新版本。检查宿主：

```bash
kubectl --kubeconfig "$KUBECONFIG" exec -n default sysbox-debug-tools -- \
  nsenter -t 1 -m -u -i -n -p -- command -v fuse-overlayfs
```

### containerd 出现 `slice bounds out of range`

如果堆栈指向 `mount.RemoveIDMapOption`，说明使用了未兼容 K3s containerd 2.2.3 的旧 snapshotter。升级到 installer `-5`，并重建受影响的 Sysbox Pod。

### `sysbox sidecar oci spec unavailable`

旧 snapshotter 在 sidecar 自身创建时可能发生 spec 自引用竞态。升级到 installer `-5`，确认 `sysbox-snapshotter` 已被替换，然后重建 Pod。

### `content digest ... not found`

这是 containerd image metadata 存在但 blob 缺失。以 pause 镜像为例，可从可用镜像站强制补齐 content：

```bash
kubectl --kubeconfig "$KUBECONFIG" exec -n default sysbox-debug-tools -- \
  nsenter -t 1 -m -u -i -n -p -- \
  k3s ctr -n k8s.io content fetch --platform linux/amd64 \
  docker.m.daocloud.io/rancher/mirrored-pause:3.6
```

### Pod 重建后 `/a.txt` 丢失

依次确认：

1. 新 Pod 仍使用相同 PVC、`volumeName` 和 `path`。
2. Pod annotation 位于 controller 的 `spec.template.metadata.annotations`。
3. Pod 是 `2/2 Running`，且存在 `sysbox-rootfs` sidecar。
4. `/` 的文件系统类型是 `fuse.fuse-overlayfs`。
5. 宿主 `fuse-overlayfs` 参数中的 `upperdir/workdir` 指向 PVC，而不是 `io.containerd.snapshotter.v1.sysbox/snapshots/...`。

### 切换 `remap-ids` 后启动失败

不要混用不同 remap 语义生成的历史 sysbox snapshot chain。详细根因和清理原则见 [sysbox-snapshotter-remap-ids-root-cause.md](./sysbox-snapshotter-remap-ids-root-cause.md)。

## 使用限制

- 同一 `PVC + path` 只能由一个运行中的业务容器使用，不能多副本并发共享同一个 overlay `upperdir/workdir`。
- `path` 必须是相对路径，不能包含 `..`。
- 更换 PVC 或 `path` 等价于使用新的 rootfs 读写层。
- PVC 只保存 rootfs 写层，不保存 `/proc`、`/sys`、`/dev`、tmpfs、EmptyDir 或其他独立 Kubernetes Volume。
- 验收完成后删除临时 Deployment；是否删除 PVC 取决于是否需要保留测试数据。

```bash
kubectl --kubeconfig "$KUBECONFIG" delete deployment sysbox-rootfs-test
kubectl --kubeconfig "$KUBECONFIG" delete pvc sysbox-rootfs-test
```
