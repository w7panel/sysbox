# Sysbox + K3s Swap 配置指南

如何在 K3s 中启用 swap 支持，并在 sysbox-runc 系统容器内验证 swap 视图。本文同时记录 `w7panel` 分支中 `/proc/swaps`、`/proc/meminfo` 的回归测试方法。

---

## 背景

Kubernetes 默认禁用 swap。从 v1.28 开始，通过 `NodeSwap` feature gate 和 `memorySwap.swapBehavior` 可以启用 pod 级别的 swap 支持。

Sysbox 系统容器运行 systemd + Docker/containerd 时，swap 隔离是重要的资源管理能力。w7panel 分支的 sysbox-fs 已经支持从 cgroup v2 读取 `memory.swap.*` 并虚拟化到容器内的 `/proc/swaps` 和 `/proc/meminfo`。

---

## 环境要求

- K3s v1.28+（本文使用 v1.35.5+k3s1）
- 宿主机已启用 swap（`swapon --show` 可以看到 swap 设备）
- sysbox-runc 已安装并配置为 K3s containerd runtime

---

## 第一步：宿主机启用 swap

```bash
# 查看当前 swap 状态
swapon --show
free -h

# 如果没有 swap，创建一个 swap 文件（4GB 示例）
fallocate -l 4G /swapfile
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile

# 持久化（写入 /etc/fstab）
echo '/swapfile none swap sw 0 0' >> /etc/fstab

# 验证
free -h | grep Swap
# 预期输出: Swap:          4.0Gi       0Ki       4.0Gi
```

---

## 第二步：配置 K3s kubelet swap 支持

K3s 通过 kubelet 配置文件启用 swap。有两种方式：

### 方式 A：通过 kubelet-config drop-in（推荐）

```bash
# 写入 kubelet drop-in 配置
cat > /var/lib/rancher/k3s/agent/etc/kubelet.conf.d/99-swap.conf << 'EOF'
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
failSwapOn: false
memorySwap:
  swapBehavior: LimitedSwap
EOF

# 重启 K3s
systemctl restart k3s
```

### 方式 B：通过 config.yaml CLI 参数

```yaml
# /etc/rancher/k3s/config.yaml
kubelet-arg:
  - "fail-swap-on=false"
```

> **注意：** `k3s v1.35.x` 不支持 `--memory-swap-behavior` CLI 标志，需要通过方式 A 的 KubeletConfiguration 配置。

---

## 第三步：验证 kubelet 配置生效

```bash
# 确认 k3s 已启动
systemctl status k3s --no-pager

# 查看 kubelet 启动参数中包含 swap 相关配置
journalctl -u k3s --no-pager -n 50 | grep -i "swap\|memorySwap"

# 查看节点状态
k3s kubectl get nodes
```

---

## 第四步：创建测试 Pod 验证 swap

### 创建带 swap 限制的 Pod

```yaml
# test-swap-pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-swap
spec:
  runtimeClassName: sysbox-runc
  hostUsers: false
  containers:
  - name: ubuntu
    image: docker.cnb.cool/i0358/docker-images-chrom/nestybox-ubuntu-bionic-systemd-docker
    command: ["/sbin/init"]
    resources:
      limits:
        memory: "512Mi"
      requests:
        memory: "256Mi"
  restartPolicy: Always
```

```bash
k3s kubectl apply -f test-swap-pod.yaml
```

### 验证 swap 视图

```bash
# 等待 pod 就绪
k3s kubectl wait --for=condition=Ready pod/test-swap --timeout=120s

# 查看 cgroup swap 限制（宿主机视角）
PID=$(k3s crictl inspect $(k3s crictl ps --pod $(k3s crictl pods --name test-swap -q) -q) 2>/dev/null | python3 -c "import sys,json;print(json.load(sys.stdin).get('info',{}).get('pid',''))" 2>/dev/null)
CONTAINER_CG=$(cat /proc/$PID/cgroup 2>/dev/null | grep "^0::" | cut -d: -f3 | sed 's/\/init.scope//')
echo "Container cgroup: $CONTAINER_CG"
cat "/sys/fs/cgroup${CONTAINER_CG}/memory.swap.max" 2>/dev/null

# 容器内查看 swap
k3s kubectl exec test-swap -- free -h
k3s kubectl exec test-swap -- cat /proc/swaps
k3s kubectl exec test-swap -- cat /proc/meminfo | grep -E "^Swap"
k3s kubectl exec test-swap -- sh -c 'cat /sys/fs/cgroup/memory.swap.max 2>/dev/null; cat /sys/fs/cgroup/memory.swap.current 2>/dev/null'
```

---

## 第五步：运行 swap 专项测试

使用 `w7panel-doc/swaps.sh`：

```bash
# 开启 K3s swap 支持并重启 k3s
bash w7panel-doc/swaps.sh --enable-k3s

# 创建 / 重建测试 pod
bash w7panel-doc/swaps.sh --create

# 验证 K3s 配置、pod cgroup swap、/proc/swaps、/proc/meminfo
bash w7panel-doc/swaps.sh --verify

# 一次性执行：开启 K3s swap → 重建 pod → 验证
bash w7panel-doc/swaps.sh --all

# 宿主临时 swapon 测试；会创建临时 64MiB swapfile，测试后自动 swapoff 并删除
bash w7panel-doc/swaps.sh --host-swapon-test
```

成功时，pod 内应看到一条虚拟 swap：

```text
/proc/swaps:
Filename                                Type            Size    Used    Priority
none                                    virtual         135108  0       0

/proc/meminfo:
SwapTotal:         135108 kB
SwapFree:          135108 kB

free -m:
Swap:              131        0       131
```

`135108 kB` 来自 cgroup v2 `memory.swap.max=138350592`，即 `138350592 / 1024`。

---

## 第六步：运行完整资源视图测试

使用 `test-pod.sh` 脚本（位于 `w7panel-doc/test-pod.sh`）：

```bash
# 完整流程：创建 pod → 安装工具 → 验证所有 proc/sys 文件
bash w7panel-doc/test-pod.sh

# 仅验证（pod 须已运行）
bash w7panel-doc/test-pod.sh --verify
```

---

## Sysbox-fs swap 代码说明

Sysbox-fs 的 swap 虚拟化逻辑在 `handler/implementations/procResources.go` 的 `swapInfoV2()` 函数中：

```go
func swapInfoV2(cg cgroupView, hostSwapTotalKB uint64) (swapInfo, bool) {
    if max, ok := cg.readV2Effective("memory.swap.max", func(s string) bool {
        return s != "" && s != "max"
    }); ok {
        usedBytes, _ := parseUintValueFromV2(cg, "memory.swap.current")
        return swapInfoV2FromMax(max, usedBytes, hostSwapTotalKB)
    }
    return swapInfo{}, false  // 无 swap limit → 隐藏 swap
}
```

**行为规则：**

| cgroup `memory.swap.max` | 容器内 `/proc/swaps` | 容器内 `SwapTotal` | 说明 |
|---|---|---|---|
| 不存在 / `max` / `0` | 仅表头 | `0` | swap 被禁用（默认） |
| `536870912` (512Mi) | 显示一行 swap 条目 | `524288 kB` | swap 已配置并生效 |

**关键点：**
- 要使 swap 在容器内可见，需要 kubelet/containerd 把 swap limit 写入容器 cgroup。
- K8s + Sysbox pod 会注册 infra/pause 容器和业务容器；同 netns 容器会共享 sysbox-fs state。
- 资源视图必须优先使用 FUSE 请求进程的 cgroup，而不是共享 state 的第一个容器 init pid。否则会读到 pause 容器的 `memory.swap.max=max`，导致业务容器内 `/proc/swaps` 被错误隐藏。
- `w7panel` 分支已将 `cgroupForReq()` 改成优先使用 `req.Pid`，读不到时才回退到 `Container.InitPid()`。

---

## 故障排查

### cgroup 有 swap.max 但容器内 SwapTotal=0

先确认业务容器 cgroup 是否真的有数值型 `memory.swap.max`：

```bash
# 容器内
cat /sys/fs/cgroup/memory.swap.max
cat /sys/fs/cgroup/memory.swap.current

# 检查 kubelet 配置是否生效
cat /var/lib/rancher/k3s/agent/etc/kubelet.conf.d/*.conf | grep -A2 swap
```

如果容器内 `memory.swap.max` 是数值，但 `/proc/swaps` 仍只有 header，检查 sysbox-fs 是否包含 `cgroupForReq()` 的请求进程 cgroup 修复，并重建安装 `/usr/bin/sysbox-fs` 后重建 pod。

K8s pod 中可用下面的方法比较 pause 容器和业务容器 cgroup：

```bash
journalctl -u sysbox-fs --no-pager | grep 'Container registration completed' | tail
cat /proc/<pause-init-pid>/cgroup
cat /proc/<app-init-pid>/cgroup
cat /sys/fs/cgroup/<app-cgroup-without-init.scope>/memory.swap.max
```

### `--memory-swap-behavior` flag 错误

```
Error: failed to parse kubelet flag: unknown flag: --memory-swap-behavior
```

**原因：** 此 CLI 标志在部分 k3s 版本中不可用。使用 KubeletConfiguration drop-in（方式 A）代替。

### swap 限制不生效

确认宿主机 swap 已启用：

```bash
swapon --show
# 如果没有任何输出，说明宿主 swap 未激活
```
