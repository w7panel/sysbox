# w7panel-sysbox Helm Chart

This chart installs the Sysbox Kubernetes resources. By default, the admission
backend is disabled and the chart keeps the existing runtime installer behavior.

## Admission Image Configuration

The runtime installer DaemonSet image is controlled by:

| Value | Default | Description |
| --- | --- | --- |
| `daemonSet.image.repository` | `ghcr.io/w7panel/sysbox-deploy-k3s` | Runtime installer image repository. |
| `daemonSet.image.tag` | chart app version with `v` prefix | Runtime installer image tag. |
| `daemonSet.image.pullPolicy` | `Always` | Runtime installer image pull policy. |

The admission backend can use a separate image source:

| Value | Default | Description |
| --- | --- | --- |
| `admission.image.repository` | `ghcr.io/w7panel/sysbox-deploy-k3s` | Admission backend image repository. Defaults to the same repository as `daemonSet.image.repository`. |
| `admission.image.tag` | chart app version with `v` prefix | Admission backend image tag. Defaults to the same tag resolution as `daemonSet.image.tag`. |
| `admission.image.pullPolicy` | `Always` | Admission backend image pull policy. Defaults to the same pull policy as `daemonSet.image.pullPolicy`. |

For domestic registry access, set the admission image repository to the CDN
mirror without changing the runtime installer DaemonSet image:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n default \
  --set admission.enabled=true \
  --set admission.image.repository=ghcr.registry.cdn.w7.cc/w7panel/sysbox-deploy-k3s \
  --set admission.image.tag=v0.7.0-1 \
  --set admission.image.pullPolicy=IfNotPresent
```

For admission-only testing, do not run a full chart upgrade unless you intend to
reconcile the Sysbox runtime installer DaemonSet too. Render and apply only the
admission Service, admission Deployment, and required RBAC templates for isolated
admission backend tests.
