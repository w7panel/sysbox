# Implementation map

| Area | Paths to inspect | Verification target |
|---|---|---|
| Nested identity | `sysbox-mgr/{mgr.go,utils.go}`, `sysbox-runc/libsysbox/syscont`, `libcontainer/specconv` | Non-initial userns fallback, explicit `MappingMode`, `0 0 65536`, child userns, NoShift. |
| Runtime/FUSE | `sysbox-runc/libsysbox/sysbox`, `sysbox-fs` | Mode agreement, L1 `/dev/fuse`, one FUSE server per L2, L1-coordinate ownership. |
| Inner K3s | `sysbox-pkgr/k8s/scripts/sysbox-inner-k3s.sh`, `sysbox-nested-agent.sh` | Nested daemons, wrapper, handler/snapshotter, ready label, stale cleanup. |
| Cgroup/CNI | runtime/manager networking and cgroup code | Delegated subtree, inherited L1 limits, child-owned netns and restored routes. |
| Rootfs/Helm | snapshotter, `charts/w7panel-sysbox` | PVC rootfs, persistence, `installMode`, fixed `sysbox-runc` RuntimeClass. |
| Tests/docs | `w7panel-doc/sysbox-in-sysbox`, `w7panel-doc/tests` | Reproducible scripts and explicit limitations. |

```text
L0 host Kubernetes
└─ L1 CKM Pod (sysbox-runc, hostUsers=false)
   └─ L1 K3s
      └─ L2 Sysbox workload
```
