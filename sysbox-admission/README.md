# sysbox-admission

`sysbox-admission` is the mutating webhook backend used by the rootfs rw-layer
feature. It can be deployed by the `w7panel-sysbox` Helm chart and runs directly
from the `sysbox-deploy-k8s` image.

## Helm Deployment

Install the chart normally:

```bash
helm install w7panel-sysbox ./charts/w7panel-sysbox -n kube-system
```

By default the chart keeps the existing installer-only behavior. Enable the
admission backend explicitly:

```bash
helm upgrade --install w7panel-sysbox ./charts/w7panel-sysbox \
  -n kube-system \
  --set admission.enabled=true
```

The admission backend runs directly from the `sysbox-deploy-k8s` image. The
image build must include `sysbox-admission` under `/opt/sysbox/bin/generic`, and
the chart starts that binary inside the container. No host binary mount or
separate `sysbox-admission` image is required.

Relevant chart values:

| Value | Default | Description |
| --- | --- | --- |
| `admission.enabled` | `false` | Enables the backend Deployment and Service. |
| `admission.replicas` | `1` | Backend Deployment replica count. |
| `admission.binaryPath` | `/opt/sysbox/bin/generic/sysbox-admission` | Image-internal backend binary path. |
| `admission.image.repository` | `ghcr.io/w7panel/sysbox-deploy-k3s` | Backend image repository; defaults to the same repository as `daemonSet.image.repository`. |
| `admission.image.tag` | chart app version with `v` prefix | Backend image tag; defaults to the same tag resolution as `daemonSet.image.tag`. |
| `admission.image.pullPolicy` | `Always` | Backend image pull policy; defaults to the same pull policy as `daemonSet.image.pullPolicy`. |
| `admission.service.port` | `443` | Service port used by the webhook client config. |
| `admission.containerPort` | `9443` | HTTPS listen port passed to `sysbox-admission -addr`. |
| `admission.tls.caSecretName` | `sysbox-admission-webhook-ca` | Backend-owned CA Secret. |
| `admission.tls.certSecretName` | `sysbox-admission-webhook-tls` | Backend-owned TLS Secret. |
| `admission.lease.name` | `sysbox-admission-webhook-init` | Backend-owned initialization Lease. |
| `admission.webhook.name` | `sysbox-webhook-mutator` | Backend-owned MutatingWebhookConfiguration name. |

The chart does not require cert-manager and does not create the webhook TLS
Secret or `MutatingWebhookConfiguration`. The backend starts with
`-bootstrap-webhook=true`, creates or reuses the CA Secret, creates the serving
TLS Secret, coordinates initialization through a Lease, and upserts
`MutatingWebhookConfiguration/sysbox-webhook-mutator` with the CA bundle.

## MutatingWebhookConfiguration

The backend creates and maintains `MutatingWebhookConfiguration` named
`sysbox-webhook-mutator` by default.

It targets:

```yaml
- operations: ["CREATE", "UPDATE"]
  apiVersions: ["v1"]
  apiGroups: ["apps"]
  resources: ["deployments", "statefulsets", "daemonsets"]
- operations: ["CREATE", "UPDATE"]
  apiVersions: ["v1"]
  apiGroups: ["batch"]
  resources: ["jobs", "cronjobs"]
- operations: ["CREATE", "UPDATE"]
  apiVersions: ["v1"]
  apiGroups: [""]
  resources: ["pods"]
```

Kubernetes `admissionregistration.k8s.io/v1` supports `CREATE`, `UPDATE`,
`DELETE`, `CONNECT`, and `*` as webhook operations. It does not accept `PATCH`
as a webhook operation value; patched objects are evaluated through the update
admission path.

The backend always renders this match condition exactly:

```yaml
matchConditions:
  - name: has-rootfs-rw-layer
    expression: 'has(object.metadata.annotations) && "sysbox/rootfs-rw-layer" in object.metadata.annotations && object.metadata.annotations["sysbox/rootfs-rw-layer"] != ""'
```

`matchConditions` require a Kubernetes API server version that supports CEL
admission match conditions.

## Annotation Placement

For Pods, set `sysbox/rootfs-rw-layer` on the Pod metadata:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    sysbox/rootfs-rw-layer: '[{"name":"app","volumeName":"rootfs","path":"app"}]'
spec:
  runtimeClassName: sysbox-runc
```

For Deployments, StatefulSets, DaemonSets, Jobs, and CronJobs, set
`sysbox/rootfs-rw-layer` on the workload top-level metadata. The webhook selects
the admitted object by its own top-level metadata annotations, so
`spec.template.metadata.annotations` does not trigger workload admission by
itself.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  annotations:
    sysbox/rootfs-rw-layer: '[{"name":"app","volumeName":"rootfs","path":"app"}]'
spec:
  template:
    spec:
      runtimeClassName: sysbox-runc
```

The backend treats Pods and workload templates separately: Pods are patched at
`/metadata` and `/spec`; Deployments, StatefulSets, DaemonSets, and Jobs are
patched at `/spec/template`; CronJobs are patched at
`/spec/jobTemplate/spec/template`. The backend does not copy workload
annotations into `spec.template.metadata.annotations`.

## Verification

After installing with admission enabled:

```bash
kubectl get pods -n kube-system -l app.kubernetes.io/component=sysbox-admission
kubectl get svc -n kube-system
kubectl get mutatingwebhookconfiguration sysbox-webhook-mutator
kubectl get secret -n kube-system sysbox-admission-webhook-ca sysbox-admission-webhook-tls
```

The admission Deployment image values default to the same repository, tag
resolution, and pull policy as `daemonSet.image.*`, but can be configured
independently through `admission.image.*`. For domestic
registry access, set `admission.image.repository` to
`ghcr.registry.cdn.w7.cc/w7panel/sysbox-deploy-k3s`. There is no separate
`sysbox-admission` image build requirement.

If the backend pod fails to start, first confirm the deploy image contains
`admission.binaryPath`, then check the backend logs and RBAC for Secret, Lease,
and mutating webhook configuration access.
