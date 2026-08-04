# Sysbox PVC 首次目录初始化（volume-init）

`runtimeClassName: sysbox-runc` 的 Pod 将可写 PVC 挂载到业务容器目录时，Sysbox 会在该 PVC **首次为空**时，把镜像中同一路径的目录内容复制到 PVC。之后 PVC 已有内容始终优先，不会因 Pod 重建或镜像更新而覆盖。

这相当于 Docker 将镜像目录初始化到空 named volume 的语义，可省去仅用于复制初始文件的 initContainer。

## 使用方式

用户无需配置 annotation。只需使用 Sysbox runtime，并将 PVC 挂载到普通业务容器：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-with-webroot
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx-with-webroot
  template:
    metadata:
      labels:
        app: nginx-with-webroot
    spec:
      runtimeClassName: sysbox-runc
      hostUsers: false
      containers:
      - name: nginx
        image: nginx:latest
        volumeMounts:
        - name: webroot
          mountPath: /usr/share/nginx/html
      volumes:
      - name: webroot
        persistentVolumeClaim:
          claimName: nginx-webroot
```

首次创建时，`/usr/share/nginx/html` 中的默认 `index.html`、`50x.html` 被复制到 `nginx-webroot`。业务随后写入该目录的数据保留在 PVC；重建 Pod 后仍可见，镜像中的同路径新文件不会合并或覆盖。

## 生效条件和边界

- 仅处理 `runtimeClassName: sysbox-runc` 的 Pod。
- 仅处理 `spec.containers` 中可写的 PVC `volumeMount`；`initContainers`、只读挂载、文件或 `subPath` 挂载不参与初始化。
- Sysbox admission 会从 Pod 生成内部 `sysbox/volume-init` annotation；不要手写它，用户提供的同名 annotation 会被覆盖。
- runc 仅在 PVC 根目录为空时复制，允许文件系统自动创建的 `lost+found`。已有任意业务内容即视为已初始化。
- 复制使用保留属性的 `rsync`，同一空卷的并发首次启动通过文件锁串行化；先成功初始化的容器内容成为该卷初始内容。
- 目标路径在镜像中不存在时，PVC 保持空目录。该能力不创建 PVC/PV，也不负责多 Pod 共享卷的读写协调。

部署脚本会将 `sysbox/volume-init` 加入 sysbox-runc 的 containerd `pod_annotations` 白名单。使用旧部署时需要重新执行 `sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh`，否则 admission 写入的内部 annotation 不会传递给 OCI spec。

## 验收

测试脚本创建临时 PVC 和 Deployment，检查默认 Nginx 文件已复制，写入标记后删除 Pod 并验证标记未被覆盖；无论成功或失败都会清理临时资源。

```bash
cd sysbox
KUBECONFIG=/root/.kube/218.config \
STORAGE_CLASS=longhorn \
NODE_NAME=server1 \
bash w7panel-doc/tests/volume-init-test.sh
```

常用参数：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `KUBECONFIG` | `/home/.kubeconfig` | 集群 kubeconfig。 |
| `NAMESPACE` | `default` | 临时资源所在命名空间。 |
| `STORAGE_CLASS` | `longhorn` | 用于动态创建测试 PVC 的 StorageClass。 |
| `TEST_IMAGE` | `nginx:latest` | 必须在 `/usr/share/nginx/html` 提供可验证初始文件的镜像。 |
| `NODE_NAME` | 空 | 可选，指定测试 Pod 节点。 |
| `TIMEOUT_SECONDS` | `300` | 每个等待阶段的超时秒数。 |

若集群没有可用的动态 StorageClass，请先提供可绑定的 PVC，并按实际存储环境调整或复用测试脚本中的资源定义。
