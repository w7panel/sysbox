# w7panel-sysbox Helm Chart

This chart installs the Sysbox Kubernetes resources, including the runtime
installer and admission backend.

## Node Selector Configuration

By default, the chart does not restrict Sysbox resources to labeled nodes. To
install Sysbox only on nodes with a specific label, set the shared node selector:

```yaml
nodeSelector:
  sysbox-runtime: running
```

When enabled, the same label selector is applied to the installer DaemonSet, the
admission Deployment when admission is enabled, and the RuntimeClass scheduling
node selector. Label the target nodes before enabling this option:

```bash
kubectl label node <node-name> sysbox-runtime=running
```

To use a custom label, set `nodeSelector` to the desired key/value map.

## Image Configuration

The installer and admission backend use the same image configuration:

| Value | Default | Description |
| --- | --- | --- |
| `image.repository` | `ghcr.io/w7panel/sysbox-deploy-k3s` | Installer and admission image repository. |
| `image.tag` | `latest` | Installer and admission image tag. |

For domestic registry access, set the shared image repository to the CDN mirror:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n default \
  --set image.repository=ghcr.registry.cdn.w7.cc/w7panel/sysbox-deploy-k3s \
  --set image.tag=latest
```
