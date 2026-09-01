# User namespace 问题：具体解决步骤

## 根因

L1 CKM Pod 已在 Kubernetes 创建的 user namespace 中。传统 Sysbox 流程依赖宿主 `/etc/subuid` 和 `/etc/subgid` 分配 ID；L1 通常看不到或不能修改这些文件。L1 内 UID 0 也不等于宿主 root，直接加入 L1 userns 会破坏 L1/L2 边界。

## 实现顺序

1. `sysbox-mgr` 读取 `/proc/self/uid_map`、`/proc/self/gid_map` 判断是否为 initial user namespace；非完整映射即视为嵌套。
2. 解析 `--mapping-mode`：`standard-subid`、`nested-identity`、`auto`。nested 模式在 initial userns 中拒绝。
3. `auto` 先尝试普通 sub-ID；非 initial userns 分配失败时回退 nested identity。回退不读、不写、不依赖 L1 `/etc/subuid`/`subgid`。
4. nested identity 固定返回 `uid=0,gid=0,size=65536`，允许多个 L2 复用。注册和更新消息携带 `MappingMode`，manager/runtime 不一致即拒绝。
5. `sysbox-runc` 强制创建新的 child userns，禁止加入已有 L1 userns，并写入：

   ```text
   /proc/<L2-pid>/uid_map: 0 0 65536
   /proc/<L2-pid>/gid_map: 0 0 65536
   ```

6. nested 模式 rootfs/bind mount 全部 `NoShift`，禁止二次 chown、shiftfs、idmapped mount 和 rootfs cloning。
7. mount helper 使用 L1 userns 权限；mount、setns、cgroup、proc 查询只允许 L1 管理的 namespace、进程和路径。
8. `sysbox-fs` 在 L1 打开 `/dev/fuse`，每个 L2 独立 FUSE server，UID/GID 0 按 L1 坐标解释；删除“UID 0 是宿主 root”等缓存假设。

## 内部 K3s

L1 内复用已有 K3s。nested agent 启动内部 `sysbox-mgr`、`sysbox-fs`、snapshotter，并生成带 `--mapping-mode nested-identity` 的 `sysbox-runc-nested` wrapper；inner containerd 使用该 wrapper 与 sysbox snapshotter。

## 验证命令

```sh
kubectl exec -n default deploy/<name> -- cat /proc/1/uid_map
kubectl exec -n default deploy/<name> -- cat /proc/1/gid_map
kubectl exec -n default deploy/<name> -- sh -c 'touch /tmp/userns-ok && stat -c "%u:%g" /tmp/userns-ok'
```

两张 map 的首行必须为 `0 0 65536`；L2 可创建自身文件，但不能加入 L1 userns、修改 L1 cgroup 上限或访问未授权路径。重启后重复检查 rootfs，确认没有二次 ownership 偏移。

重点源码：`sysbox-mgr/utils.go`、`sysbox-mgr/mgr.go`、`sysbox-runc/libsysbox/syscont/spec.go`、`sysbox-runc/libsysbox/sysbox/mgr.go`、`sysbox-pkgr/k8s/scripts/sysbox-inner-k3s.sh`。

## 为什么不需要重新编译 containerd

K3s 的 containerd 虽然是内嵌进程，但 user namespace 逻辑在 `sysbox-mgr`/`sysbox-runc`，不在 containerd。containerd 只读取 CRI/OCI 配置，并通过 `io.containerd.runc.v2` 调用 `BinaryName` 指定的 `sysbox-runc-nested`；映射创建和 `uid_map/gid_map` 写入由 runc 完成。

- 只改 mgr/runc/fs 的 nested identity 逻辑、handler 名和配置不变：不编译或重启 containerd，更新 nested agent/二进制即可；已有容器按发布策略滚动重建。
- 首次给运行中的 L1 K3s 增加 `sysbox-runc` handler 或 `sysbox` snapshotter：不编译 containerd，但必须让 K3s 重建其内嵌 containerd 以读取 `config-v3.toml.tmpl`；执行受控重建 L1 Pod，不重启 L0 宿主。
- 只有修改 containerd 源码、插件 ABI 或 K3s 打包版本时，才需要重新构建 containerd/K3s。

当前模板声明 `runtime_type=io.containerd.runc.v2`、`snapshotter=sysbox` 和
`BinaryName=/var/lib/sysbox-inner/bin/sysbox-runc-nested`，属于外部 runtime 配置，不是
containerd 源码变更。

该方案只解决嵌套环境的身份映射和运行时启动，不提供 `/proc` noexec、独立 CPU/内存视图、跨 userns seccomp listener 或不可信多租户安全边界；这些必须单独标记为限制。
