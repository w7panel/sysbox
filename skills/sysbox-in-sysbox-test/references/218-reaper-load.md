# sysbox-fs reaper load on 218 (2026-10-15)

This records an investigation and a proposed optimization. The runtime fixes
below have **not** been implemented or validated. Recheck paths and behavior
against the deployed revision before using this result on another cluster.

## Observed behavior

- `reaper: nothing to reap` comes from `sysbox-fs/nsenter/reaper.go`: after
  waiting for a child, the reaper performs an additional `Wait4(WNOHANG)`
  sweep. Its log count is not a request or child-process count.
- The inner agent is not the original source. The `5b6j9` inner agent Pod was
  created at 2026-10-09 09:44 UTC; the preceding roughly 19 hours already had
  114,501 such lines. The hour 2026-10-08 15:00–16:00 UTC had 5,541 lines.
- A long-running `k3s` process in CKM opens `/proc/sys/kernel/threads-max`
  roughly every 10 seconds. Matching eBPF `openat` and journald monotonic
  timestamps showed one `openFileRequest` and two `readFileRequest` events on
  the same thread about 0.4 ms after the open. `ProcSysKernel` has no special
  case for this node, so these operations use PassThrough and start `nsenter`.
- CKM `k3s` also emits bursts of about 97–98 `lookupRequest` events roughly
  every five minutes. In one burst, 98 requests matched 98 `lstat` calls under
  `/dev/.sysbox-sysfs-*/devices/virtual/net/<interface>`; examples included
  `cilium_host` and `lxc...` interfaces.
- At observation time, CKM `/sys/class/net` listed 111 entries, while
  `ip -o link show` listed 18 interfaces. The host listed 108 entries. CKM
  `/sys/class/net` is on native sysfs, but `/sys/devices/virtual` is a Sysbox
  FUSE mount. Many links from the former resolve to absent targets in the
  CKM network namespace. `SysDevicesVirtual` sends `net` lookups through
  PassThrough; failed lookups return `ENOENT` and do not enter the FUSE nodeDB,
  so a later scan repeats the work.

The current `sysbox-runc/libcontainer/container_linux.go` adds mount and PID
namespaces to the helper for a `sysfs` mount, but adds the target net namespace
only for `switchDockerDns`. This code is consistent with the host-like
`/sys/class/net` view; confirm the mount's network namespace during a runtime
test before treating that code path as the fully proven cause. The two observed
`k3s` paths explain load independent of the inner agent, but historical reaper
lines cannot be individually assigned to either path. Do not extrapolate a
one-minute sample containing a five-minute burst into a steady rate.

## Proposed optimization — not implemented

1. For the `sysfs` helper only, also join the target CKM network namespace
   before mounting sysfs. Keep the namespace choice of other helper operations
   unchanged. This should make `/sys/class/net` reflect CKM interfaces and
   remove the burst of lookups for host-only links. It affects newly created
   containers; test one disposable CKM before considering existing instances.
2. In `ProcSysKernel`, handle `threads-max` Open without PassThrough and read
   the host's global `/proc/sys/kernel/threads-max` directly on each Read.
   Preserve the existing Write path and offset/error behavior. This targets
   the repeated three-`nsenter` read sequence without caching a potentially
   changing value.
3. Change only the routine `reaper: nothing to reap` message from Info to
   Debug. Preserve the reap sweep and error/actual-reap logs. This reduces
   log I/O, not the underlying `nsenter` process load.

Before and after a test build, compare `sysbox-fs` CPU, child creation count,
and request counts by path over intervals containing at least two five-minute
bursts. In the test CKM, compare `/sys/class/net` to `ip link`, resolve its
interface links, verify network workloads, and confirm `threads-max` reads
match the host without spawning `nsenter`. Check container startup and reaper
errors. Do not restart production `sysbox-fs` or CKM merely to investigate;
a prior restart on 218 produced `unknown container` warnings.
