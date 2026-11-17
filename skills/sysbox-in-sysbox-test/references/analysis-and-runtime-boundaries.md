# Analysis and runtime boundaries

Use this reference for Sysbox-in-Sysbox branch comparisons, runtime-code
reviews, and evidence-based status reports. The current lightweight baseline
is `w7panel`; `w7panel-sysboxin` is a historical full-runtime experiment, not
an interchangeable baseline. See `w7panel-doc/sysbox-in-sysbox/HISTORY.md` for
historical nested-identity, manager/fs, Docker, and L3 work.

## Compare source and submodules

Identify the checked-out branch, exact baseline commit, merge base, and dirty
worktree before drawing conclusions. Compare the main repository and each
affected submodule independently: record changed gitlinks, old/new submodule
commits, and whether those commits are reachable from the submodule remotes.
If the requested ref is absent, report the missing ref and any explicit
fallback; never silently substitute a different branch. Useful read-only
commands include `git status --short --branch`, `git merge-base`,
`git diff --name-status <base>...HEAD`, `git submodule status`, and
`git -C <submodule> log <old>..<new>`.

Group findings by runtime handler, snapshotter/rootfs, PVC initialization,
special mounts, nested containerd configuration, chart, and tests. For each
material change report the baseline behavior, new behavior, runtime invariant,
and source or test evidence. Distinguish implemented, actually validated,
unverified, and known limitation; a code path or Ready Pod alone is not a
functional pass. Keep L0 host, L1 CKM/K3s, and L2 workload evidence separate.
Do not access clusters, mutate branches, or push as part of a read-only review.

## L2 proc views

`sysbox-runc-lite` neither launches nor registers with sysbox-fs or sysbox-mgr.
L2 `/proc` follows the existing mounts and default runtime behavior; it is not
a separate Sysbox virtualization target. Some reads happen to reach FUSE mounts
inherited from the full-runtime L1 CKM, but they do not create a lite FS/mgr
dependency or a separate L2 acceptance requirement.

Current L2 `sysbox-runc-lite` Pods omit `hostUsers: false`. Their cgroup and
PID namespace can still differ from L1. The existing host FS request handling
renders inherited FUSE reads for their caller; it must not replace that default
behavior with a complete L1 init snapshot. On 218, an L1 CKM read eight
processors and 16 GiB of memory, while a 100m CPU / 128 MiB L2 Pod read one
processor and 128 MiB. This is an observed inheritance effect, not a request
to add L2-specific virtualized `/proc` handling.

## Native runc versus lite

Read [the native-runc probe](native-runc-probe.md) for the dated 218 result.
Official `/bin/runc` with the `sysbox` snapshotter and admission passed basic
Ubuntu rootfs persistence, exec, and `/dev/null` access; this does not make
ordinary `runc/overlayfs` equivalent, nor prove that lite can be removed.
The native probe did not exercise lite's annotation-free empty-PVC image copy
or consumption of the snapshotter handoff for persistent special bind mounts.
Verify those separately before claiming functional replacement.

Native-runc success is evidence that the observed probe did not need lite's
former mount-error compatibility paths, not evidence that all `/dev` or `/proc`
code is redundant. Keep upstream runc's proc safety checks, ordinary proc
mounting, and device-node initialization. The custom `/dev` bind/userns
fallback, broad `ENOENT`/`EPERM` mount-error continuations, sysfs skip, and
bind/pivot tolerances were removed in a scoped A/B test; see
[the native-runc probe](native-runc-probe.md) for the dated local-binary result.
After any future scoped removal, cover ordinary Pods, rootfs PVC,
interactive TTY exec, character-device access, and persistent special mounts
on both L0 and L2 where affected. Report exact failures and the tested binary
and handler; do not infer success from a different runtime or stale image.
