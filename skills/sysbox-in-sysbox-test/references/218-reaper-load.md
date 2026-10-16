# sysbox-fs reaper load on 218 (2026-10-15)

This records the investigation and the 2026-10-16 test on 218. The
`threads-max` read optimization is deployed on that node; the sysfs network
namespace proposal failed the fresh-CKM test and was reverted. Recheck the
node and installed image before using this result on another cluster.

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

## Optimization status

1. Reverted after 218 test: joining the target CKM net namespace in the
   `sysfs` helper made the native `/sys/class/net` count match `ip link`, but
   Sysbox-FS returned `ENOENT` for `/sys/devices/virtual/net`. Every class/net
   symlink was broken. Restoring the original runc and recreating the same CKM
   restored the directory and symlinks; the source change was removed.
2. Deployed on 218: `ProcSysKernel` opens `threads-max` without
   PassThrough for read-only access and reads the host's global value directly
   on every Read. Opens with write access and Write retain the existing
   PassThrough path. This targets the three-`nsenter` read sequence without
   caching a potentially changing value.
3. Deferred by user request: changing the `reaper: nothing to reap` Info log
   level. Keep reaper changes out of builds and commits for this optimization.

## 218 test and recovery

- The installed host binary SHA256 values are `70755db4...` for `sysbox-fs`
  and baseline `ad7fcbe3...` for `sysbox-runc`. The final FS-only installer
  image is `ccr.ccs.tencentyun.com/afan-public/sysbox:fs-threadsmax-218-20261016`
  (registry digest `sha256:9d9dcee9cb7a078060f1fe4dee66831226f5404133c9897653ed0fb2311c5736`).
  The installer DaemonSet uses this image with `OnDelete` and a checksum guard
  against reinstalling identical binaries. This is a direct DaemonSet patch;
  a future Helm upgrade can overwrite it.
- The fresh CKM `k3k-console-164315/loadopt-218-1016` passed the skill's
  preflight, L0 RuntimeClass smoke, full-runc local-path and Longhorn CSI,
  lite local-path, nested Chart installation, and L2 nginx rootfs/PVC
  persistence checks. A later controlled run read `threads-max` 100 times in
  that CKM and 100 times in an existing CKM. Both returned the host value, and
  the journal had zero matching `comm=cat` nsenter requests. The Sysbox-FS Info
  trace level was retained.
- Restarting `sysbox-fs` detached old Pods' FUSE mounts, producing `Socket not
  connected` and `unknown container` warnings. Existing CKMs and ordinary
  Sysbox Pods were recreated sequentially. The remaining CKMs, node, and test
  workload became Ready; no pre-restart `sysbox-runc` Pods remained, and the
  last-minute unknown-container/error count was zero. Replacing the FS binary
  on a live node requires a Pod remount plan.
- Comparable ten-minute logs: 08:49–08:59 UTC had 8,772 nsenter requests,
  1,875 lookups, 3,465 `k3s` requests, and 2,011 reaper lines. 09:00–09:10
  had 3,056 / 479 / 362 / 609, plus 1,082 unknown-container warnings. After
  recovery began, 09:11–09:21 had 7,733 / 1,443 / 1,340 / 1,317. These are
  not a steady-state optimization percentage: old mounts were disconnected,
  Pods were rebuilt, and several CKMs disappeared during the test.
- After recovery, 09:27–09:32 and 09:32–09:37 UTC were stable five-minute
  windows with four Ready CKMs and no new outer `sysbox-runc` Pods. They had
  1,475 and 1,497 nsenter requests, 411 and 422 lookups, and 497 and 510
  reaper lines respectively; both had zero unknown-container and error lines.
  The pre-deployment baseline had nine CKMs, so these totals are not an
  equal-population performance comparison. No measured CPU reduction can be
  assigned to the optimization from these samples.

## Size of the targeted saving

The earlier trace showed one `openFileRequest` plus two `readFileRequest`
events for a CKM `k3s` read of `threads-max` about every ten seconds. Avoiding
those three requests saves approximately 18 nsenter requests per minute, or
1,080 per hour, for each CKM that retains that polling cadence. With four
currently Ready CKMs, the workload-based estimate is 72 per minute and 4,320
per hour. The controlled 200-read run produced zero matching nsenter requests.

As a descriptive cross-check, `comm=k3s` nsenter requests were 3,465 over ten
minutes with nine CKMs before deployment (385 per CKM) and 832 over ten
minutes with four CKMs after recovery (208 per CKM). The difference of 177 per
CKM is close to the 180 predicted from the ten-second read cadence. These are
different CKM populations and workloads, so 46% is an observed per-CKM log
ratio, not a causal CPU or fleet-wide performance measurement.

For another rollout, compare CPU, child creation, and path-specific requests
over at least two five-minute bursts with equal CKM populations. Confirm
`/sys/class/net` symlinks resolve and `threads-max` reads match the host.
