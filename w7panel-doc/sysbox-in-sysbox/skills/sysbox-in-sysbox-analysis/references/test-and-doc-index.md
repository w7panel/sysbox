# Test and documentation index

| Capability | Entrypoint | Acceptance evidence |
|---|---|---|
| Setup/chart | `00-check-prereqs.sh`, `01-create-ckm.sh`, `04-install-ckm-chart.sh` | Existing L1 K3s reused; chart uses `sysbox-runc`. |
| Identity | `05-test-ckm-k3s.sh`, `08-check-isolation.sh` | L2 child userns maps `0 0 65536` for UID/GID. |
| CNI/HTTP | nested chart smoke and nginx deployment | L2 netns and HTTP path work. |
| Rootfs/Docker | `09-test-docker-rootfs.sh` | Files survive restart; overlay2 works. |
| Cgroups | `10-test-cgroup-delegation.sh` | L2 cannot raise L1 limits. |
| Lifecycle/exec | `11-test-nested-agent-lifecycle.sh`, `12-test-interactive-exec.sh` | Daemons recover; inner and outer exec finish. |
| Cleanup | `99-cleanup.sh` | Only test resources removed. |

Classify results as validated, implemented-but-unverified, limitation/abandoned, or environment-blocked. Current documented limitations include `/proc` noexec isolation, independent CPU/memory views, seccomp listener visibility, and untrusted multi-tenant isolation.
