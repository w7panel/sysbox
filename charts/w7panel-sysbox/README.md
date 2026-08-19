# w7panel-sysbox Helm Chart

This chart installs Sysbox in either the physical host cluster or a K3s cluster
running inside a Sysbox Pod. Select the target explicitly; the chart never
guesses from the current user namespace.

## Install mode

The same chart and deploy image are used at both levels:

| Value | Installation target | Agent |
| --- | --- | --- |
| `installMode=host` | L0 cluster nodes, to provide Sysbox for L1 Pods | Host installer DaemonSet and systemd services |
| `installMode=nested` | L1 K3s nodes, to provide Sysbox for L2 Pods | Nested agent DaemonSet using `nested-identity` |

Both modes create the same `RuntimeClass/sysbox-runc`, so workload manifests do
not differ between L1 and L2.

`installMode` has no operational default. Helm rejects an install that omits it,
preventing an inner-cluster install from accidentally running the host installer
and restarting the L1 K3s service. As a second guard, the host installer refuses
to run outside the initial user namespace.

Install on the outer cluster:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n sysbox-system --create-namespace \
  --set installMode=host
```

Install on the K3s cluster inside the L1 Sysbox Pod:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n sysbox-system --create-namespace \
  --set installMode=nested
```

The nested mode never restarts K3s or the physical host. The host mode does
restart the target node's K3s service after changing its containerd template.
When migrating an already running L1 for the first time, the nested agent writes
the K3s containerd template and stays NotReady because containerd cannot reload
runtime handlers. Roll-recreate only that L1 K3s Pod from its L0 controller; do
not reboot the L0 host and do not kill containerd alone. New L1 Pods should
prepare this template before starting K3s and do not require the migration
restart. Then wait for the agent:

```bash
kubectl -n sysbox-system rollout status daemonset/w7panel-sysbox-nested-agent
kubectl get node -l sysbox.w7panel.io/nested-runtime=ready
```

Then use the identical workload setting at either level:

```yaml
spec:
  runtimeClassName: sysbox-runc
```

## Values

| Value | Default | Description |
| --- | --- | --- |
| `installMode` | required | Explicit installation target: `host` for L0 or `nested` for L1. |

### RuntimeClass

The chart always creates the `sysbox-runc` RuntimeClass. This name is fixed
because Sysbox admission only mutates Pods that use `runtimeClassName:
sysbox-runc`.

### Node Selector

By default, the chart does not restrict Sysbox resources to labeled nodes. To
install Sysbox only on nodes with a specific label, set the shared node selector:

```yaml
nodeSelector:
  sysbox-runtime: running
```

When set, the same label selector is applied to the installer DaemonSet, the
admission Deployment, and the RuntimeClass scheduling node selector. Label the
target nodes before using this option:

```bash
kubectl label node <node-name> sysbox-runtime=running
```

To use a custom label, set `nodeSelector` to the desired key/value map.

| Value | Default | Description |
| --- | --- | --- |
| `nodeSelector` | `{}` | Shared node selector for the installer DaemonSet, admission Deployment, and RuntimeClass scheduling. |

### Images

The installer and admission backend use the same image by default. When the
installer tag is empty, the chart defaults it to `v{{ .Chart.AppVersion }}`.

| Value | Default | Description |
| --- | --- | --- |
| `installer.image.repository` | `docker.cnb.cool/i0358/zpk/sysbox-deploy-k3s` | Shared host-installer and nested-agent image repository. |
| `installer.image.tag` | `""` | Installer image tag; defaults to `v{{ .Chart.AppVersion }}`. |
| `installer.image.digest` | `sha256:54999e25d3362101caf71ae17d7e95d70a581e7a5ae7af63921cf548a35946ae` | Immutable installer image digest; takes precedence over `tag`. |
| `admission.image.repository` | `""` | Optional admission image repository override; defaults to `installer.image.repository`. |
| `admission.image.tag` | `""` | Optional admission image tag override; defaults to `installer.image.tag`. |
| `admission.image.digest` | `""` | Optional immutable admission image digest; defaults to `installer.image.digest`. |
| `admission.image.pullPolicy` | `""` | Optional admission pull policy override; defaults to `installer.image.pullPolicy`. |

To use a different registry, set the shared image repository:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n default \
  --set installMode=host \
  --set installer.image.repository=ghcr.io/w7panel/sysbox-deploy-k3s
```

### Installer

| Value | Default | Description |
| --- | --- | --- |
| `installer.podLabels` | `{}` | Extra labels for installer pods. |
| `installer.podAnnotations` | `{}` | Extra annotations for installer pods. |

### Nested agent

The nested agent copies the image binaries to `/var/lib/sysbox-inner/bin`, uses
only L1-visible state under `/var/lib/sysbox-inner`, `/var/lib/sysboxfs-inner`,
`/run/sysbox`, and the K3s data directory, and starts the manager with explicit
`nested-identity` mapping. It requires delegated cgroup v2, `/dev/fuse`, and
bidirectional mount propagation from the L1 Sysbox Pod.

| Value | Default | Description |
| --- | --- | --- |
| `nested.pauseImage` | `rancher/mirrored-pause:3.6` | Sandbox image written to the inner K3s containerd template. |

### Admission

The admission backend is rendered by this chart and reuses the installer image
unless `admission.image` overrides are set. Set `admission.enabled=false` to
skip the admission Deployment, Service, and admission-only RBAC.

| Value | Default | Description |
| --- | --- | --- |
| `admission.enabled` | `false` | Render the admission backend Deployment, Service, and admission-only RBAC. |
| `admission.replicas` | `1` | Admission backend replica count. |
| `admission.containerdConfigPath` | `/var/lib/rancher/k3s/agent/etc/containerd` | Host containerd config directory mounted read-only into the admission backend for sandbox image detection. |
| `admission.podLabels` | `{}` | Extra labels for admission pods. |
| `admission.podAnnotations` | `{}` | Extra annotations for admission pods. |

For non-k3s clusters, set `admission.containerdConfigPath` to the host
containerd config directory containing `config.toml`:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  --set installMode=host \
  --set admission.enabled=true \
  --set admission.containerdConfigPath=/etc/containerd
```

### Snapshotter

In `host` mode, the installer passes `snapshotter.enabled` to the deploy script
inside the shared image. Set it to `false` to install Sysbox without the host-side
`sysbox-snapshotter` service and containerd proxy snapshotter configuration.
Nested mode always starts its dedicated snapshotter.

| Value | Default | Description |
| --- | --- | --- |
| `snapshotter.enabled` | `false` | Install and configure the host-side sysbox-snapshotter service. |
