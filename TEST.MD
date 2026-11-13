# v0.7.1-39 子集群验收

## 结论与范围

2026-11-13（Asia/Hong_Kong），正式发布产物在 218 的
`k3k-console-164315/ckm-snapshot-1109` 安装及功能验收通过。
本轮验证删除 inner-agent 节点标签维护逻辑、内层 `NODE_NAME` 注入与节点权限后的行为。

- 主仓库：`cf32fbdb091ee9cec6a85e9e4570d74fbb726d00`，tag `v0.7.1-39`。
- sysbox-pkgr：`bf726ba552646e3dbb8cfd450525837766550af7`。
- [Release 工作流](https://github.com/w7panel/sysbox/actions/runs/43413234239) 全部成功后才安装。
- kubeconfig：用户指定 `/root/.kube/218.config`。
- Helm：`default/w7panel-sysbox`，升级 revision 14，重复安装后 revision 15，均为 `deployed`。
- 安装模式：显式 `nested`；存储：`local-path`；nginx：`ccr.ccs.tencentyun.com/afan-public/nginx:latest`。

未执行 L0 部署、其他 CKM、真实 CSI、全新 CKM、实际卸载流程或性能压力验收。
`sysbox-install` / `sysbox-runtime` 仅检查用途，本轮未修改。

## 发布产物核验

下载正式 Chart、lite 二进制、`image-metadata.txt` 和 `SHA256SUMS`，执行
`sha256sum -c SHA256SUMS --ignore-missing`，所有已下载条目通过。

| 产物 | SHA256 |
| --- | --- |
| `w7panel-sysbox-0.7.1-39.tgz` | `90834024161a68d30605f68789ed2fb4f88a96c0e272c09afc8a9f2c25af51b2` |
| `sysbox-runc-lite-v0.7.1-39-amd64` | `7f66bb40ca473aedd2b048880ee2012c2d1d5919499be3b8ee33490ff92ed195` |
| 镜像 manifest | `96c5a23d19888c180f46801a632f03d83b8d8c33b6eb74b8aba536104be17291` |
| 运行中 `sysbox-inner-agent.sh` | `8669480c740470b2f577eb7a7044f4ae2f7f9baad2271732880f3d006e7d539e` |

镜像为 `ghcr.io/w7panel/sysbox-deploy-k3s:v0.7.1-39`。实际安装的 lite 哈希与正式二进制一致；
agent 脚本哈希与提交中的文件一致。

上轮 v0.7.1-38 曾因镜像下载断流导致安装等待超时。本轮提前通过 IPv4 Range 分片下载镜像，
逐层校验后导入目标子集群 containerd，再升级 Chart；没有修改镜像源配置，也未把网络直拉能力计为通过。

## 验证结果

| 检查 | 结果与证据 |
| --- | --- |
| Shell 静态检查 | `sh -n` 通过；ShellCheck warning/error 级别通过。完整输出仅有既存嵌套 awk 单引号的 SC2016 info 提示。 |
| 健康探针回归 | `go test -v sysbox-inner-agent_test.go` 的 9 个子用例全部通过，包括缺失/残留 socket、RPC 失败、空存储、缺失 handler 与正常状态。 |
| Chart 静态检查 | host/nested Helm lint 通过；正式发布包渲染断言通过：两种 RuntimeClass 无 scheduling；节点 get/patch 仅 host 保留；nested agent/卸载 Job 无 NODE_NAME。 |
| 安装 | `04-install-ckm-chart.sh` 返回 PASS；agent/admission 均 `1/1 Running`。 |
| 运行时配置 | 安装/功能脚本验证 native runc 与 sysbox-runc-lite 分离、lite 使用 sysbox snapshotter、CRI 文件系统映射有效，无 full inner sysbox-runc handler。 |
| 实际 RuntimeClass | `sysbox-runc-lite.scheduling` 为空；新 nginx Pod 的 nodeSelector 为空。 |
| 实际内层依赖 | ClusterRole `w7panel-sysbox-installer` 无 nodes 规则；agent DaemonSet 无 NODE_NAME。 |
| 标签不再写回 | 新 agent Ready 后手动移除节点 `sysbox.w7panel.io/nested-runtime` 旧标签；经过多个 10 秒调和周期和一次重复安装仍为空，agent 持续 Ready。新脚本不负责自动清理已有旧标签。 |
| nginx HTTP | Pod IP 的 HTTP 请求成功返回 `Welcome to nginx!` 页面。 |
| 空 PVC 初始化 | 新 webroot PVC 包含镜像的 index.html、50x.html，Pod 无 sysbox/volume-init 注解。 |
| 重建持久化 | 删除并重建 nginx Pod 后，webroot 标记与 `/srv/data` 特殊目录标记均保留。 |
| CRI / kubelet stats | nginx writableLayer.fsId.mountpoint 非空；节点 `/stats/summary` 请求成功。 |
| 重复安装 | 再次运行 04 返回 PASS；Server restartCount 保持 4，startedAt 保持 `2026-11-13T11:15:10Z`，没有额外重启。 |
| 服务日志 | 本轮恢复后的 agent、admission、snapshotter 日志未见 panic、fatal、OOM、ENOMEM 或标签权限错误；snapshotter 报告版本 0.7.1-39。 |

首次升级更换运行时版本时，内层 K3s 按安装流程重启，agent/admission 随之各重启一次；恢复后验收通过。
客户端访问外层集群有 kubectl 1.32 / server 1.37 版本差警告，命令执行成功，本轮未调整客户端版本。

## 执行入口

在仓库根目录执行；第一次安装前已导入上述 digest 的正式镜像。

```bash
export KUBECONFIG_218=/root/.kube/218.config
export CKM_NAMESPACE=k3k-console-164315 CKM_NAME=ckm-snapshot-1109
export OUTER_NAMESPACE=k3k-console-164315 ROOTFS_STORAGE_CLASS=local-path
export SYSBOX_IMAGE_REPO=ghcr.io/w7panel/sysbox-deploy-k3s SYSBOX_IMAGE_TAG=v0.7.1-39
export NESTED_CHART_FILE=/tmp/sysbox-release-v0.7.1-39/w7panel-sysbox-0.7.1-39.tgz
bash w7panel-doc/sysbox-in-sysbox/04-install-ckm-chart.sh

CKM_TEST_DEPLOYMENT=sysbox-release-39-check \
CKM_TEST_ROOTFS_PVC=sysbox-release-39-rootfs \
CKM_TEST_VOLUME_INIT_PVC=sysbox-release-39-webroot \
bash w7panel-doc/sysbox-in-sysbox/05-test-ckm-k3s.sh
```

05 最终返回 `FUNCTIONAL PASS`。该脚本会重建指定测试 Deployment，并删除重建指定 webroot PVC；
后续复测应使用新的测试名称，避免覆盖本轮保留的数据。

## 测试现场

- `sysbox-release-39-check` 已缩容至 0，测试 Pod 已删除。
- 保留 Bound PVC：`sysbox-release-39-rootfs`（2 GiB）、`sysbox-release-39-webroot`（256 MiB）。
- 正式 v0.7.1-39 Helm release 保留运行；未卸载或修改其他子集群。
