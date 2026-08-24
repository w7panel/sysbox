# Sysbox 分支审查记录

## 审查范围

- 仓库：`sysbox`
- 基线：`w7panel`
- 被审查分支：`w7panel-sysboxin`
- 比较范围：`w7panel...w7panel-sysboxin`
- 审查日期：2026-08-24
- 不包含：`w7panel-ckm`、`w7panel-server`、`w7panel-ui` 等其他项目

当前分支相对 `w7panel` 领先 34 个提交，涉及主仓库、Helm Chart、snapshotter 以及
`sysbox-runc`、`sysbox-mgr`、`sysbox-fs`、`sysbox-pkgr` 等子模块。

## Findings

### [P1] 普通 Sysbox 容器的 `/proc noexec` 被无条件移除

位置：

- `sysbox-runc/libsysbox/syscont/spec.go:656-659`
- `sysbox-runc/libsysbox/syscont/spec.go:686-694`
- `sysbox-runc/libcontainer/specconv/spec_linux.go:391-397`

`cfgMounts()` 和 OCI 转换阶段都会删除 `/proc` mount 的 `noexec`，并且
`cfgProcExec(spec, true)` 传入了恒为 `true` 的参数，因此 annotation/env opt-in
不会再起控制作用。该行为影响普通标准 Sysbox workload，不仅影响 nested-identity，
会使普通容器失去原有的 `/proc noexec` 隔离。

### [P1] nested 模式静默丢弃用户提供的 OCI seccomp profile

位置：`sysbox-runc/libcontainer/specconv/spec_linux.go:457-465`

代码先把 OCI `spec.Linux.Seccomp` 转换到 `config.Seccomp`，随后在
`SkipSpecialMounts` 或 `NestedIdentity` 下直接将其置为 `nil`。这会使
`RuntimeDefault` 或 `Localhost` seccomp 配置静默失效。Sysbox 自己的 seccomp
通知配置位于独立的 `SeccompNotif` 字段，避免重复 listener 不需要清空普通
seccomp filter。

### [P1] 迁移已有 L1 时整体覆盖 containerd 模板且无法恢复

位置：`sysbox-pkgr/k8s/scripts/sysbox-inner-k3s.sh:307-366`

脚本使用 `cat > config-v3.toml.tmpl` 写入完整模板，没有备份、合并或卸载恢复逻辑。
README 明确支持迁移已经运行的 L1；这种迁移会丢失原有 containerd runtime、插件和
用户自定义配置，Helm 卸载后也不会恢复原模板。

### [P1] 安装过程无条件写入测试专用 kubelet 驱逐配置

位置：`sysbox-pkgr/k8s/scripts/sysbox-inner-k3s.sh:269-283`

每次 nested runtime prepare 或 fallback 都会写入
`99-sysbox-inner-eviction.conf`，把 `nodefs.available` 和 `imagefs.available`
固定为 `1Gi`。这会改变已有 L1 kubelet 的磁盘保护策略，可能让节点在磁盘接近耗尽时
才触发驱逐。Chart 没有卸载 hook；虽然仓库增加了清理脚本，但
`sysbox-pkgr/k8s/Dockerfile.sysbox-k3s:114-118` 没有把该脚本复制到发布镜像，卸载后
配置可能残留。

### [P1] nested agent readiness probe 进入错误的 namespace

位置：

- `charts/w7panel-sysbox/templates/daemonset/sysbox-nested-agent.yaml:70-73`
- `sysbox-pkgr/k8s/scripts/sysbox-nested-agent.sh:47-78`

agent 已识别 `hostPID` 场景下 PID 1 是 L1 server，并动态寻找真正的 K3s 子进程；但
readiness probe 仍固定执行 `nsenter -m -n -t 1`。因此健康 runtime 可能因为 probe 检查
了错误 namespace 而长期保持 NotReady，也可能误读外层同名 socket。

### [P2] admission 独立镜像覆盖会组合错误 digest

位置：`charts/w7panel-sysbox/templates/admission/deployment.yaml:42`

当设置独立的 `admission.image.repository` 或 `admission.image.tag` 时，模板仍默认继承
非空的 `installer.image.digest`。例如设置 repository 为 `example.com/custom/admission`、
tag 为 `v9`，渲染结果仍是：

```text
example.com/custom/admission@sha256:e10b0f...
```

tag 被忽略，且 installer 镜像 digest 通常不适用于 admission 镜像，容易导致拉取失败
或拉取错误制品。

### [P2] rootfs smoke test 复用旧 PVC，可能掩盖初始化回归

位置：`w7panel-doc/tests/nested-chart-smoke.sh:166-224`

测试只删除 Deployment，随后对固定名称 PVC 执行 `apply`。重复运行时已有 PVC 以及
`upper/work` 目录会被复用，即使全新 rootfs 层的初始化逻辑退化，测试也可能仅验证旧
层重新挂载和持久化。

## 验证结果

通过：

- `git diff --check w7panel...w7panel-sysboxin`
- `helm lint charts/w7panel-sysbox --set installMode=host`
- `helm lint charts/w7panel-sysbox --set installMode=nested`
- `go test ./libsysbox/syscont ./libcontainer/specconv`（`sysbox-runc`）
- `go test ./...`（`sysbox-fs`）
- `go test ./rootfs`（`sysbox-snapshotter`）
- nested agent 相关 shell 脚本 `sh -n`

环境限制：

- `sysbox-mgr` 全量测试中的既有 `shiftfsMgr` 用例依赖 `/mnt/scratch`，当前环境缺失该
  fixture；该失败未归因于本次分支差异。
- `sysbox-ipc` 测试要求更新 `go.mod`，审查期间未修改依赖文件。

本文件只记录审查发现；实现修复前，不应将上述问题标记为已解决。
