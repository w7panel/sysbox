# Sysbox 已知问题与修复方案

## 升级时覆盖 `sysbox-mgr` 报 `Text file busy`

### 现象

在已有 Sysbox 的节点上从 `v0.7.0-11` 升级到 `v0.7.0-12` 时，installer 第一次运行可能失败：

```text
Stopping Sysbox ...
Installing Sysbox on host ...
cp: cannot create regular file '/mnt/host/usr/bin/sysbox-mgr': Text file busy
```

对应代码位于 `sysbox-pkgr/k8s/scripts/sysbox-deploy-k8s.sh` 的
`copy_sysbox_to_host()`：它使用 `cp` 直接覆盖宿主现有可执行文件。

### 已验证结论

- Linux 在某个进程仍执行目标 inode 时，不允许以写方式直接覆盖该文件，返回 `ETXTBSY`。
- installer 已调用 `systemctl stop sysbox`，但报错时旧 `sysbox-mgr` inode 仍被执行；具体占用进程尚未捕获，不能仅凭日志断定是 systemd 主进程。
- 失败容器由 DaemonSet 自动重启，第二次运行安装成功，说明占用会在短时间内释放。
- 目标节点最终安装 `v0.7.0-12` 成功，`k3s`、`sysbox`、`sysbox-mgr`、`sysbox-fs` 均为 `active`，被重建的 Sysbox 工作负载均恢复 `Running`。

### 永久修复方案

保留安装前停止服务的流程，同时将所有 Sysbox 二进制改为同目录临时文件加原子替换，避免直接写入正在执行的 inode：

```bash
install_binary() {
	local src="$1"
	local dst="$2"
	local tmp="${dst}.new.$$"

	install -m 0755 "${src}" "${tmp}"
	mv -f "${tmp}" "${dst}"
}
```

`copy_sysbox_to_host()` 应通过该函数安装 `sysbox-mgr`、`sysbox-fs`、
`sysbox-runc` 和可选的 `sysbox-snapshotter`。临时文件必须与目标文件位于同一目录，确保 `mv` 使用同文件系统的原子 `rename`；旧进程可继续持有旧 inode，新启动的服务会读取新 inode。

服务停止也应显式覆盖所有组件：

```bash
for unit in sysbox.service sysbox-snapshotter.service \
	sysbox-fs.service sysbox-mgr.service; do
	if systemctl is-active --quiet "${unit}"; then
		systemctl stop "${unit}"
	fi
done
```

原子替换用于消除覆盖竞态，不能替代正常的服务停止流程。安装失败时还应删除本次生成的 `.new.<pid>` 临时文件。

### 验收标准

1. 在运行 Sysbox 工作负载的节点执行 `v0.7.0-11` 到新版本升级。
2. installer 首次运行完成且容器不重启，日志无 `Text file busy`。
3. 宿主 `sysbox`、`sysbox-mgr`、`sysbox-fs`、`sysbox-snapshotter` 与 K3s 均为 `active`，二进制版本为目标版本。
4. 安装器重建的 Sysbox 和 K3K Pod 全部恢复 `Running`。
5. 重复执行相同版本安装，确认流程幂等且不留下 `.new.<pid>` 文件。
