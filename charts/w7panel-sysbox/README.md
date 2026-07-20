# w7panel-sysbox Helm Chart

This chart installs the Sysbox Kubernetes resources, including the runtime
installer and admission backend.

## Values

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
| `installer.image.repository` | `ghcr.registry.cdn.w7.cc/w7panel/sysbox-deploy-k3s` | Installer image repository. |
| `installer.image.tag` | `""` | Installer image tag; defaults to `v{{ .Chart.AppVersion }}`. |
| `admission.image.repository` | `""` | Optional admission image repository override; defaults to `installer.image.repository`. |
| `admission.image.tag` | `""` | Optional admission image tag override; defaults to `installer.image.tag`. |
| `admission.image.pullPolicy` | `""` | Optional admission pull policy override; defaults to `installer.image.pullPolicy`. |

For direct GHCR access, set the shared image repository:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n default \
  --set installer.image.repository=ghcr.io/w7panel/sysbox-deploy-k3s
```

### Installer

| Value | Default | Description |
| --- | --- | --- |
| `installer.podLabels` | `{}` | Extra labels for installer pods. |
| `installer.podAnnotations` | `{}` | Extra annotations for installer pods. |

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
  --set admission.enabled=true \
  --set admission.containerdConfigPath=/etc/containerd
```

### Snapshotter

The installer passes `snapshotter.enabled` to the deploy script inside the shared
image. Set it to `false` to install Sysbox without the host-side
`sysbox-snapshotter` service and containerd proxy snapshotter configuration.

| Value | Default | Description |
| --- | --- | --- |
| `snapshotter.enabled` | `false` | Install and configure the host-side sysbox-snapshotter service. |
