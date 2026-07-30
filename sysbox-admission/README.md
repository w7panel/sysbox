# sysbox-admission

`sysbox-admission` is the Kubernetes mutating admission webhook for Sysbox rootfs rw-layer persistence. It validates the `sysbox/rootfs-rw-layer` Pod annotation and injects a canonical `sysbox-rootfs` sidecar so `sysbox-snapshotter` can resolve PVC-backed rootfs upper/work directories safely. When `sysbox/persistent-special-mounts` is explicitly `"true"`, it also gives each configured app container a reserved PVC mount used by `sysbox-runc` for persistent special directories.

The webhook handles Pods that meet all of these conditions:

- The request is a Pod `CREATE` admission request.
- `spec.runtimeClassName` is `sysbox-runc`.
- `metadata.annotations["sysbox/rootfs-rw-layer"]` exists and is non-empty.

It does not mutate workload resources such as Deployments directly. Put the annotation on the Pod template for controllers.

## Rootfs Rw-Layer Annotation

Put the annotation under a Pod's `metadata.annotations`. For Deployment, StatefulSet, and other controllers, put it under `spec.template.metadata.annotations`.

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    sysbox/rootfs-rw-layer: |
      [
        {
          "name": "c1",
          "volumeName": "rootfs",
          "path": "containers/c1"
        }
      ]
    sysbox/persistent-special-mounts: "true"
spec:
  runtimeClassName: sysbox-runc
  containers:
    - name: c1
      image: alpine:3.23.5
  volumes:
    - name: rootfs
      persistentVolumeClaim:
        claimName: sysbox-rootfs-pvc
```

Each annotation entry has this meaning:

| Field | Meaning |
| --- | --- |
| `name` | Target app container name. It must exist in `spec.containers`. |
| `volumeName` | Name of a PVC-backed `spec.volumes[]` entry. |
| `path` | Persistent rootfs rw-layer directory inside that PVC. It must be relative and must not escape the PVC root. |

Validation is fail-closed. The webhook denies the request when the annotation is invalid, a referenced container is missing, a referenced volume is not a PVC volume, a PVC claim name is empty, a container is configured more than once, or `path` is absolute or escapes through `..`.

## Sidecar Injection

When validation succeeds, `sysbox-admission` injects or replaces a container named `sysbox-rootfs`.

The injected sidecar is canonical:

- `name`: `sysbox-rootfs`
- `image`: the sandbox image detected from containerd config, or containerd's default sandbox image
- `volumeMounts`: one mount for each unique `volumeName` in the annotation
- `env`: empty
- `command`: empty

The sidecar is appended after all app containers. If the incoming Pod already contains a `sysbox-rootfs` container, it is removed and replaced with the canonical sidecar. This prevents users from tampering with sidecar image, command, environment, or mounts.

The sidecar does not carry rootfs rw-layer intent in environment variables. Rootfs configuration remains in the Pod annotation, which containerd forwards into each container OCI spec when the runtime is configured with:

```toml
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc]
  pod_annotations = ["sysbox/rootfs-rw-layer", "sysbox/persistent-special-mounts"]
```

`sysbox-snapshotter` reads that OCI annotation for container intent and reads the `sysbox-rootfs` sidecar OCI mounts only to resolve each PVC's node-side mount path.

## Persistent Special Directory Mount

When `sysbox/persistent-special-mounts` is exactly `"true"`, each app container configured by `sysbox/rootfs-rw-layer` also receives its selected PVC at:

```text
/var/lib/sysbox/rootfs-special-volume/<volumeName>
```

This is an internal hand-off mount, not an application data path. `sysbox-runc` validates that its node-side source belongs to the current Pod UID, resolves the configured rw-layer directory, initializes `<path>/special`, removes the hand-off mount from the final OCI spec, and replaces it with explicit mounts for all Sysbox special directories. A user mount under the reserved base path is rejected by admission.

Without the explicit opt-in annotation, admission still injects the sidecar required for persistent rootfs `upper/work`, but it does not inject this hand-off mount. Existing workloads therefore retain Sysbox's legacy node-local special-directory behavior after an upgrade.

The resulting PVC layout is:

```text
<PVC>/<path>/
├── upper/
├── work/
└── special/
    ├── meta.json
    ├── docker/
    ├── kubelet/
    ├── k0s/
    ├── k3s-agent/
    ├── rke2/
    ├── buildkit/
    └── containerd-overlay/
```

The separate `special/` tree is intentional. These directories are backed directly by the PVC filesystem rather than nested inside the FUSE overlay rootfs, while remaining part of the same PVC backup and restore boundary.

## Webhook Server

The server exposes:

- `GET /healthz`: readiness/liveness endpoint
- `POST /mutate`: Kubernetes AdmissionReview endpoint

Only Pod resources are supported. Other resources are denied with `unsupported resource <name>`.

Admission request bodies are limited to 1 MiB.

## TLS And Webhook Bootstrap

`sysbox-admission` can run with explicit TLS files or bootstrap its own Kubernetes webhook resources.

Important flags:

| Flag | Purpose |
| --- | --- |
| `--addr` | Listen address, default `:9443`. |
| `--tls-cert`, `--tls-key` | Explicit serving certificate and key. Must be set together. |
| `--allow-insecure-http` | Allow plaintext HTTP. Development only. |
| `--bootstrap-webhook` | Create/refresh CA Secret, TLS Secret, Lease, and MutatingWebhookConfiguration. |
| `--namespace` | Namespace for webhook-owned runtime resources. Defaults to `POD_NAMESPACE`, then `sysbox-system`. |
| `--service-name`, `--service-port` | Kubernetes Service used by the webhook client config. |
| `--webhook-name` | MutatingWebhookConfiguration name. Default `sysbox-webhook-mutator`. |
| `--ca-secret-name`, `--tls-secret-name` | Secret names used for bootstrap TLS. |
| `--lease-name` | Lease name for certificate/bootstrap coordination. |
| `--containerd-config-dir` | Directory used to detect the sandbox image from containerd config. |

When `--bootstrap-webhook=true`, one elected replica manages certificate resources and webhook configuration. All replicas wait for and refresh the serving TLS certificate from the configured Secret.

The generated webhook matches only Pod create requests whose `sysbox/rootfs-rw-layer` annotation exists and is non-empty. It uses `failurePolicy: Fail`, `sideEffects: None`, `reinvocationPolicy: IfNeeded`, and a 10 second timeout.

## Sandbox Image

The sidecar image must match the cluster sandbox image. `sysbox-admission` detects it from containerd config:

- With `--containerd-config-dir`, it reads from that directory.
- Without it, it uses the default containerd config lookup.
- If no sandbox image is configured, the containerd default is used.

## Build And Test

```bash
make sysbox-admission
make test
```

From the repository root:

```bash
make sysbox-admission
make test-admission-local
```
