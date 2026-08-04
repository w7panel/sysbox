# sysbox-admission

`sysbox-admission` is the Kubernetes mutating admission webhook for Sysbox. It validates the `sysbox/rootfs-rw-layer` Pod annotation and injects a canonical `sysbox-rootfs` sidecar so `sysbox-snapshotter` can resolve PVC-backed rootfs upper/work directories safely. For every `sysbox-runc` Pod it also generates trusted `sysbox/volume-init` metadata for writable PVC mounts, allowing sysbox-runc to initialize an empty PVC from the container image without an init container. Application containers never receive the rootfs PVC mount directly.

`sysbox/volume-init` is generated from ordinary application containers in the Pod spec and must not be configured by users. The webhook replaces any user-provided value. Init containers, read-only PVC mounts, block devices, and non-PVC volumes are excluded; file mounts are passed through unchanged by runc. For an empty directory PVC (allowing `lost+found`), runc copies the matching image directory once before mounting it. Existing data is never overwritten; when application containers share an empty PVC, the first one to initialize it supplies the data.

The webhook handles Pods that meet both of these conditions:

- The request is a Pod `CREATE` admission request.
- `spec.runtimeClassName` is `sysbox-runc`.

The PVC initialization metadata is generated without requiring `sysbox/rootfs-rw-layer`. The rootfs sidecar is injected only when that annotation exists and is non-empty.

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
          "path": "containers/c1",
          "persistentSpecialMounts": true,
          "specialPath": ["/srv/data"]
        }
      ]
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
| `persistentSpecialMounts` | Optional per-container switch for PVC-backed special directories. Defaults to `false`. |
| `specialPath` | Optional extra absolute, non-root paths. Requires `persistentSpecialMounts: true` and must not overlap built-in or custom paths. |

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
  pod_annotations = ["sysbox/rootfs-rw-layer"]
```

`sysbox-snapshotter` reads that OCI annotation for container intent and reads the `sysbox-rootfs` sidecar OCI mounts only to resolve each PVC's node-side mount path.

## Persistent Special Directories

When the matching rootfs entry has `persistentSpecialMounts: true`, `sysbox-snapshotter` writes the resolved PVC source to a root-only handoff under:

```text
/run/sysbox/rootfs-pvc-handoff/<container-id-hash>.json
```

`sysbox-runc` validates the handoff against the container ID, Pod UID, container name, volume name, and kubelet volume path, resolves the configured rw-layer directory, and bind-mounts raw special directories back onto the corresponding container paths. When a special directory is first created, runc seeds it once from the matching directory in the merged image rootfs before installing the bind mount. The handoff is removed with the container snapshot and is never part of the Pod spec or container mount namespace.

Without per-entry opt-in, that container still supports persistent rootfs `upper/work`, while Sysbox retains its legacy node-local special-directory behavior. Another container or sidecar cannot inherit the setting.

The resulting PVC layout is:

```text
<PVC>/<path>/
├── upper/
├── work/
└── special/
    ├── var/lib/docker/
    ├── var/lib/kubelet/
    ├── var/lib/k0s/
    ├── var/lib/rancher/k3s/
    ├── var/lib/rancher/rke2/
    ├── var/lib/buildkit/
    ├── var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/
    └── srv/data/
```

There is no `meta.json`. The `special/` directories are bind-mounted explicitly so inner Docker and K3s see the PVC's real filesystem rather than overlay-on-FUSE. A missing directory is initialized once from the image directory (or created empty if absent), then atomically published. Existing PVC ownership, ACLs, and xattrs are never rewritten. Persistent special mounts bypass `sysbox-mgr` recursive ownership shifting and require idmapped mounts; startup fails if the source filesystem cannot provide them.

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
