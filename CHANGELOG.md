# Changelog
All notable changes to this project will be documented in this file.

## [Unreleased] - unreleased
### Changed
  * Record a controlled v47 FS CPU experiment on the isolated 218 VM. Resolving PID namespaces only for `/proc/loadavg` kept function tests passing but measured 55.16/56.60 CPU seconds against a same-session 56.76-second v47 baseline for 16,000 reads; the small difference was not stable, so the experimental code was withdrawn instead of adding complexity without a proven gain. Update the current TAPD documents with this result and the delivered WeCom alert path.
  * Record an isolated v47 minute-fallback integration check: one aggregate Sysbox restart kept the same two-container Pod and the minute scan recovered three mounts, while one eBPF fault event leaves a pure no-event fallback unproven. This narrows TAPD 1016310's remaining validation gap and documents why stopping only FS during profiling invalidated an earlier test attempt.
  * Record the isolated v40→v47→v40 FS CPU A/B and per-file results. The v47 candidate used about 2.2 times the FS CPU for the same successful `/proc` reads, so TAPD 1016311's CPU target remains open; also clarify the legacy-unit migration limit when an 8-replica Deployment continually recreates Pods.
  * Stop waiting for every deleted runtime Pod's finalizer during installer upgrades. On 218, a replaced K3k Pod stayed Failed with an etcd finalizer while its new CKM was Ready, leaving the installer and node label stuck at `installing`; `kubectl delete --wait=false` lets the controller finish cleanup independently and avoids granting unnecessary Pod watch access.
  * Preserve K3s-provided CNI binary and config directories when creating or updating the host containerd v3 template. A clean K3s v1.32 VM became NotReady after the v40 installer replaced the template because containerd fell back to `/opt/cni/bin` and `/etc/cni/net.d`; the installer now renders K3s node paths in both fresh and existing templates. It uses `bin_dir` for containerd 2.0 and `bin_dirs` for newer releases, including the 2.3 series on 218.
  * Keep `/proc/sys/fs/binfmt_misc` resolvable in a fresh sysbox-fs FUSE view. During an 8-Pod, 16-container FS restart fault burst on 218, five old child mounts could not be staged because the replacement view returned `ENOENT` for this mountpoint; the affected containers later restarted. The handler now provides the directory needed by sysbox-mgr's binfmt_misc mount, while recovery still preserves and verifies the existing child mount. A repeat burst recovered all 16 original containers without restarts or recovery failures.
  * Migrate safely recreatable full-runtime Pods before replacing a legacy sysbox-fs unit that cannot retain live FUSE sessions. The installer verifies each controller UID with narrowly scoped read permissions, temporarily cordons the node, waits for the old Pod UIDs and every Sysbox FUSE mount to disappear, and preserves a stale checkpoint before installing the new unit. It rechecks immediately before stopping the old service; API failures, unmanaged or DaemonSet Pods, and residual mounts abort while that service is still running. DaemonSets can bypass a cordon and require manual migration. Previously the installer rejected this first upgrade, or could disconnect sessions by stopping the old daemon before deleting Pods.
  * Verify the published v0.7.1-44 Chart, lite binary, and image on 218 after mirroring the exact GHCR manifest to CCR. The L0 and L2 persistence matrix passed; a live FS restart preserved the selected CKM Pod/container, with 22 successful recoveries and no failures. This closes the formal-release gap for the eBPF concurrency fix while leaving CPU A/B and production alert delivery unverified.
  * Let the GitHub-hosted release job fetch GitHub sources directly and retry image pushes up to three times. The v0.7.1-43 release built successfully but GHCR returned `unknown blob` on push; its rerun then hit a gh-proxy.org 429 while cloning shiftfs sources. Both failures prevented formal assets from being published, so the release workflow now avoids the proxy rate limit and retries the observed transient registry failure.
  * Atomically claim each namespace/device eBPF FUSE fault cooldown before publishing its event. Concurrent first faults and faults after cooldown expiry could previously emit duplicates because lookup, ring-buffer output, and map update were separate. A private-namespace kernel test now covers 64 simultaneous first faults, expiry, and one batch repair of two disconnected FUSE devices; three local privileged race-mode runs and three isolated 218 kernel runs passed. The 218 run did not replace the installed FS.
  * Exercise 64 concurrent reads against a disconnected FUSE mount in the isolated privileged eBPF recovery test, replacing the sequential fault loop to cover a real same-mount fault burst. Three race-mode runs passed; record the formal v0.7.1-42 minute-scan health and why rolling back v41 on 218 business sessions is unsafe for CPU A/B.
  * Bring the current TAPD 1016310/1016311 project documents up to date after the published v0.7.1-42 218 functional and FS-restart checks; retain the explicit migration, CPU A/B, fault-injection, and notification gaps so readers do not treat historical candidate-only status as current.
  * Validate one published v0.7.1-42 host FS restart with preserved FD store on 218: the selected CKM retained its Pod and container IDs, virtual `/proc` and inner K3s stayed available, all six CKMs were Ready, and recovery reported 22 successes with zero failures. This checks the formal image's live-session recovery path after the unit migration.
  * Verify the published v0.7.1-42 Chart, lite binary, and CCR image on 218: the L0 RuntimeClasses, local-path and Longhorn PVC persistence, and selected CKM L2 lite workload passed after a full host upgrade. Record the remaining CPU A/B, fault-injection, and Alertmanager validation gaps so this functional release pass is not mistaken for complete TAPD acceptance.
  * Block installer replacement of a running legacy sysbox-fs while any process mount namespace still contains a Sysbox FUSE mount. The previous upgrade path stopped the old daemon before recreating its Pods, disconnecting their virtual `/proc`; the check now runs before host changes and also requires the recovery flag and preserved, populated systemd FD store for live sessions. Empty nodes remain upgradeable, while disconnected mounts or a stopped daemon with a checkpoint require manual recovery.
  * Record the 218 rc2 source-candidate installation and restart results, including the manual migration needed for stale legacy FUSE checkpoints; this prevents the tested recovery path from being mistaken for an unattended first upgrade or a formal release pass.
  * Let the CKM Chart test script pass an existing image pull Secret when validating a private CCR image; otherwise the inner installer could not start despite the image and Chart being correct.
  * Allow installer and admission Pods to use an existing image pull Secret, so the private CCR release image can be installed without changing the cluster's default ServiceAccount.
  * Replace the obsolete TAPD 1016310/1016311 design descriptions with current project documents: record the minute FUSE fallback, first-upgrade unit migration, live requester-view cache boundaries, and unverified CPU/release status so historical targets are not mistaken for implemented behavior.
  * Add a preflight check to the project Sysbox-in-Sysbox test skill: FS-only restart tests must confirm the installed systemd unit enables session recovery and FD preservation. This prevents a binary-only first upgrade from disconnecting legacy FUSE mounts, as observed on 218.
  * Record the failed 218 v0.7.1-40-to-source FS-only test and recovery. The installed old systemd unit lacked `--session-recovery` and the FD store, so restarting only the new binary disconnected existing FUSE mounts; rollback plus sequential recreation restored all 19 full-runtime Pods and six CKMs. This first-upgrade boundary requires a full unit installation and Pod migration before testing preserved-Pod restarts or comparing CPU.
  * Cover a silently disconnected FUSE mount with a privileged local recovery test: replacement succeeds without first reading the stale file, and a repeated repair finds no remaining old mount. Clarify that the recovery guard rejects a shared parent mount and unsupported child propagation, not every shared target. This does not yet exercise the full one-minute cgroup scan end to end.
  * Build cgroup v2 resource-view keys without the v1 controller-list path. Every virtual proc request still resolves its live cgroup and PID namespace; the local key benchmark fell from about 32 ns to 21 ns per operation without changing cache isolation. This microbenchmark does not establish a whole-service CPU saving.
  * On 218/server1, persist the containerd default AppArmor profile with an additional signal rule for its `cri-containerd.apparmor.d//&unconfined` stacked label. The default same-profile rule rejected nginx `SIGHUP` during `nginx -s reload`; the host parser accepted the updated profile, reload succeeded, and the Pod stayed Ready without a restart. The original template is retained under `/var/lib/sysbox-test/apparmor-nginx-20261118/` for rollback.
  * Remove per-container `sysbox/resource-cache-ttl` annotation handling and its registration/restart state; Sysbox-FS now uses only its resource defaults or daemon flag, avoiding a policy path that was not needed. FUSE recovery now rejects shared propagation on the target's parent mount, where a replacement can reach peers, instead of rejecting a shared target itself; isolated namespace tests cover both cases.
  * Document CPU seconds per fixed `/proc` workload as the primary cache-performance comparison, because request throughput and load average alone did not show whether the work saved Sysbox-FS CPU on 218.
  * Refine the 218 CPU comparison to use one long-lived reader that reopens `/proc` files, avoiding repeated `cat` process creation. The old-version measurements varied substantially with host load and CKM population changed during the candidate switch, so the existing data cannot establish a CPU improvement.
  * Record two single-process, per-file CPU passes on 218: `/proc/swaps` consistently costs the most FS CPU of the four tested files, while the available perf samples point mainly to Go runtime GC and do not yet justify a handler change.
  * Avoid allocating an unused cgroup v1 controller map when resolving a cgroup v2 request. This keeps live per-request identity checks while reducing allocation pressure in the sysbox-fs cache path (4 to 3 allocations in the local benchmark). The 218 source-image test passed fixed `/proc` reads, but varying host load and CKM population did not establish a server CPU gain.
  * Render host and nested installer DaemonSets from one Pod template and start both through the same installer entrypoint. The two previously separate templates and commands could drift; mode-specific host mounts, nested K3s mounts, readiness, and installation logic remain distinct where required.
  * Keep the seccomp listener open when Linux reports a transient `POLLERR` while polling its notification lock, and log terminal poll events with their flags. Previously this event closed the listener of a running CKM; inherited `openat2` and `umount2` notifications then returned ENOSYS, blocking cgroup access and inner container deletion. A local regression test covers the transient error and terminal HUP/invalid-FD events. On 218, the published v0.7.1-41 FS binary plus a rebuild of the CKM with an already-lost listener restored native Pod create/delete; another FS restart preserved it. The historical listener-loss trigger remains unproven, and the full Chart was not upgraded in this fast test.
  * Prune FUSE recovery's repaired mount keys after a complete minute scan of surviving checkpoint owners. Short-lived nested mount namespaces and removed owners previously left keys in memory indefinitely; shared devices remain marked while any live owner still uses the namespace, and an incomplete scan preserves all keys for retry.
  * Fail closed when a real FUSE reader's cgroup or PID namespace cannot be resolved for virtual `/proc` resource files. A short-lived nested reader could otherwise fall back to its outer container init cgroup or host values; successful reads still use the requester's live view, while synthetic requests without a PID retain their existing test behavior.
  * Prune cached silent-recovery mount namespaces when nested workloads exit and clear them when the checkpointed container is removed. The minute fallback previously retained every namespace ever seen under a long-lived CKM, causing recovery bookkeeping to grow with Pod churn; live repaired namespaces remain cached to avoid repeat work.
  * Limit full-table cleanup of short-lived `/proc` resource snapshots to once per minute. Under many active views, the 100–200 ms output TTL previously triggered repeated map scans; each view still refreshes at its own TTL, and container teardown still removes its snapshots immediately. The 1000-view local microbenchmark improved from about 192–193 ns/op to about 160 ns/op in steady runs. A temporary 218 FS candidate kept the selected CKM Ready with zero recovery failures; a 24-Pod, 24,000-open-per-round old→new→old comparison showed overlapping timing ranges, so high-density end-to-end throughput gain remains unproven.
  * Amortize `/proc/stat` stale-view cleanup to one sweep per minute instead of traversing every view on every cache refresh. Container teardown still removes its states immediately, and the five-minute stale threshold is unchanged. A 1000-view lookup benchmark fell from about 11.5 µs to 13 ns per lookup; related race tests passed. A temporary 218 FS candidate kept the selected CKM and services healthy, with zero recovery failures, while the same L1 read workload showed no measurable end-to-end throughput gain.
  * Correct the FUSE recovery documentation to describe the actual minute fallback scope: registered containers and mount namespaces under their cgroup subtrees, rather than only outer mounts. A non-publishing local release build from the current source produced a deb, lite binary, deploy image, and Chart with all listed SHA256 checks passing; this does not verify a published tag or cluster installation.
  * Prevent the host `sysbox-fs` nsenter reaper from claiming a FUSE mount-recovery helper child while that helper is being waited for. An older-binary 218 A/B run hit `waitid: no child processes` during recovery; the scoped wait lock removes that race. A temporary 24-Pod full-runtime test on server1 completed with zero recovery failures, and the minute-scan candidate count returned from 114 to 66 after cleanup. L2 lite keeps its default `/proc` behavior and has no FS/mgr acceptance target.
  * Prune checkpoint-only FUSE recovery records after their restored container unregisters or its original PID exits. A 24-Pod churn test on 218 left the minute scan at 114 candidates after all test Pods were deleted because the in-memory checkpoint map never shrank; live candidates now determine the scan metric, and event readers take a safe owner copy while pruning. Local race tests and a 4-Pod 218 rerun passed: candidates returned from 74 to 66 after deletion, with no recovery failures. This changes only the host FS recovery bookkeeping, not the lite runtime or its service dependencies.
  * Install the Sysbox recovery metrics collection in the existing 218 monitoring release from `w7panel-metrics` Chart 1.0.39. Legacy Helm values initially caused nil-pointer rendering in newly optional templates; the monitoring repository now defaults missing groups safely. Node Exporter textfile collection and vmagent AppArmor configuration are managed by Helm, and VMSingle receives the server1 recovery series with zero textfile scrape errors. Alert evaluation and notification remain off because no Alertmanager URL exists; this source-Chart installation is not a published release acceptance.
  * Reuse the 30-second shared host-input cache for CPU online/present fallback reads. When a cgroup cpuset is unavailable, repeated resource-view requests previously reread `/sys/devices/system/cpu/online`; CPU limits and output remain derived per requester. Handler race tests and a temporary 218 FS installation passed; a short L1 read comparison showed no measurable throughput gain.
  * Clarify the nested runtime boundary: `sysbox-runc-lite` neither launches nor registers with FS/mgr; L2 `/proc` probes only exercise FUSE file mounts inherited from the L1 CKM. This keeps recovery and cache acceptance focused on the host FS and full-runtime L1 containers instead of treating lite as a separate FS/mgr integration.
  * Record the accepted recovery boundary: keep the minute fallback for silent FUSE failures, so recovery is not described as zero-polling. A same-cgroup, different-PID-namespace probe on 218 confirmed separate inherited FUSE loadavg views; this closes the missing live isolation check without treating it as a throughput benchmark. Formal alert delivery remains pending because 218 has no Alertmanager address.
  * Record that the validated Sysbox recovery monitoring Chart changes are now on `w7panel-metrics/dev-v1`; the packaged alert resources also pass the 218 API's server-side dry-run. The initial Chart remained undeployed at that stage; the later 1.0.39 installation is recorded above. A real Alertmanager URL and published release acceptance are still required for operational alerts.
  * Retry FUSE mount-policy refresh after a replacement if parsing mountinfo temporarily fails. Once the old mount has been replaced, later scans cannot rediscover its path; retaining those paths until refresh succeeds prevents the immutable policy from keeping stale mount IDs. Fault-injection, related race tests, and the privileged FUSE/eBPF recovery test pass.
  * Keep FUSE fault reads responsive during a slow mount repair by draining eBPF events into a bounded, deduplicated repair queue. A single worker still serializes repairs; queue overflow is counted, and shutdown waits for recovery workers before checkpointing. Recovery metrics now use a separate short-held lock so a blocked mount helper cannot stall scraping.
  * Separate `/proc/loadavg` samplers by the requester's cgroup and PID namespace, and filter sampled tasks by both boundaries. Previously two PID namespaces in one cgroup could share a sampler and expose each other's process counts; the namespace init remains the stable sampler lifetime anchor while live requests select the view.
  * Verify the latest FS source candidate on 218: the target CKM kept its identity and K3s readiness after an FS restart, and a new 100m/128MiB L2 Pod read its own inherited FUSE proc view with a nonzero first-read loadavg lastPID. The new fallback metrics reached Node Exporter and VMSingle; 66 checkpointed candidates took about 55 seconds on the recovery-heavy first scan and under 200 ms on each of two subsequent scans. Temporary Pods were deleted. This local-image test does not establish an equal-load performance gain or release acceptance.
  * Document that an L2 Pod without a child user namespace may still have its own cgroup and PID namespace. Its inherited Sysbox FUSE `/proc` reads must use the requester view; the 218 L1/L2 CPU and memory measurements rule out reusing a complete L1 output snapshot, while shared raw host inputs remain safe to cache.
  * Measure each minute FUSE recovery fallback's total scan time and deduplicated checkpoint-container count. Repair duration excluded cgroup and nested-namespace discovery, so it could not show scanning cost or compare cost at different container densities; the new metrics share the existing local socket and textfile exporters.
  * Seed a new loadavg view's `lastPID` from its own PID namespace before the first five-second sample. A newly accessed L2 view previously returned `0` on its first read; the seed avoids using the outer init's PID while preserving periodic EWMA updates.
  * Verify inherited L2 FUSE proc views on 218 with a disposable 100m CPU Pod: `/proc/cpuinfo` exposes one processor versus eight in L1, and `/proc/loadavg` uses a distinct PID namespace count and lastPID. `/proc/uptime` keeps the L1 system uptime while its idle column reflects the requester. This confirms that complete L1 registration snapshots cannot seed L2 output; the Pod was deleted.
  * Validate optional Sysbox recovery alerts on 218 with a temporary VMAlert, VMRule, and HTTP receiver: a synthetic rule reached `/api/v2/alerts` with HTTP 200. The monitoring Chart now configures VMAlert's AppArmor exception because rule reload otherwise triggers a denied self-SIGHUP and restart. The temporary resources were removed; a production Alertmanager URL and release validation are still pending.
  * Verify the nested `/proc/meminfo` boundary on 218: a disposable 128 MiB `sysbox-runc-lite` Pod inherited the outer Sysbox FUSE file mount and read its own 131072 kB limit, while the L1 server read 16 GiB. Correct the earlier blanket statement that L2 lacks a per-Pod proc view; this test proves request-scoped meminfo, not independent virtualization of every `/proc` file.
  * Verify recovery metrics ingestion on 218 after repairing vmagent's AppArmor self-SIGHUP denial: Node Exporter exposes the textfile, vmagent stays Running, and VMSingle returns the Sysbox recovery series. The monitoring Chart fix is pushed on `w7panel-metrics/sysbox-fs-recovery-metrics` but is not merged or released; alert evaluation and notification remain absent.
  * Verify the recovery metrics textfile on 218: a non-root Node Exporter reads the atomic file and exposes the Sysbox series with zero textfile scrape errors, while the selected CKM remains Ready. The live exporter argument is a temporary DaemonSet patch; its durable Chart change is proposed separately.
  * Publish the existing FUSE recovery counters and latency histogram atomically to a per-node textfile every 15 seconds. `/run/sysbox` is root-only, so the node's non-root exporter could not read the protected metrics socket; the separate read-only textfile path enables platform collection without a TCP listener or sidecar.
  * Complete the local FUSE recovery latency metric as a Prometheus histogram with buckets and a completed-attempt count, so monitoring can calculate percentiles as well as the mean; the earlier sum-only metric could not yield p95.
  * Verify the recovery-duration metric on 218 with a temporary static FS: 309 successful repairs had p50 176 ms, p95 268 ms and maximum 491 ms; the target CKM kept its identity and K3s readiness. Record the installed hash and rollback binary in KNOWN-ISSUES.md. The existing vmagent is unhealthy, so platform ingestion and release validation remain unverified.
  * Export cumulative FUSE recovery duration alongside attempt counts, so a scraper can calculate average repair latency; per-repair millisecond logs alone could not provide a time-series latency measure. Platform collection and alert rules remain pending.
  * Reuse the shared raw `/proc/cpuinfo` cache when CPU-count fallback is needed. The fallback previously reread the same host file on each request, even though CPU-info rendering already cached it; per-request cgroup limits still determine the visible CPU count.
  * Expose FUSE recovery attempts, outcomes, stale-FD events, and frequent-fault process counts through a local `/run/sysbox/recovery-metrics.sock` HTTP `/metrics` endpoint. Previously these counters were only in minute logs, so monitoring could not scrape recovery success and persistent old-FD alerts independently. Local race tests and a temporary 218 binary installation verified the socket, stable recovery counters, unchanged CKM identity, and working L1 `/proc`/K3s; platform scraping and release validation remain pending.
  * Cache each `/proc/swaps` virtual view through the existing short-lived proc snapshot lifecycle. Its size and usage come from the requester's cgroup, so a global output cache would mix tenants; the per-view snapshot also keeps split reads consistent and releases with the FUSE owner.
  * Stop retrying nested mount namespaces whose Sysbox FUSE mounts use shared propagation during the minute recovery scan. The recovery helper still rejects unsafe replacement; the scan now records a one-time unsupported warning instead of increasing failure counts and logging the same error every minute.
  * Extend the minute recovery fallback to nested mount namespaces found only within each registered container's cgroup, and check each namespace once after successful repair or confirmation. Previously a silent nested namespace with no FUSE read error could remain disconnected indefinitely. Warn once per minute when the same process repeatedly reads a stale Sysbox FUSE mount, and log each mount repair's duration in milliseconds; these expose long-lived old descriptors and provide timing evidence without flooding logs.
  * Track the W7Panel `sysbox-ipc` fork on its `w7panel` branch so the recovery and resource-identity protocol changes remain fetchable through the main repository's submodule URL.
  * Exclude local `.cache` from the deb source archive so release builds neither duplicate Go/image caches nor package cached Docker login material.
  * Implement the safe same-boot recovery core of TAPD 1016310: preserve FS/mgr state and seccomp listeners in systemd, detect stale FUSE access with CO-RE eBPF, and replace matching namespace mounts without exposing host proc data. Drain requests before checkpointing, retain original immutable-mount policy and mount flags, and reject unsupported or dirty state. Old open descriptors still fail and must be reopened; legacy installation requires initial Pod migration. After a host reboot, archive only mgr-owned previous-boot work directories before fresh initialization, preserving data and unrelated snapshotter storage instead of preventing startup. Pin host procfs fdinfo when validating mount FDs after joining a container namespace, and resolve only tracked recovery helpers through the registered init so nested child mounts survive replacement without adding work to normal FUSE lookups. Stage child mount trees in a private copy of the host namespace: real container namespaces may lack usable `/tmp`, which caused recovery to fail until the staging location was fixed. Route reused historical FUSE device numbers by fault namespace, prefer long-lived nested processes, and stop retrying repaired namespaces; the 218 checkpoint otherwise rejected a later restart and repeated stale-FD work. Local real OCI repeated-restart, FUSE/eBPF, systemd listener handoff and race tests passed; 218 source verification confirmed CKM UID/init PID unchanged and FUSE mounts recovered after repeated restarts. Host `/proc` passthrough fallback is deliberately excluded because it breaks container isolation.
  * Classify recovery helper exits caused by a short-lived target process as stale events (`ESRCH`), preserving retries without reporting a mount failure; rapid nested readers previously produced repeated `FUSE recovery incomplete` errors during the first minute after FS restart.
  * Implement the P0/P1 core of TAPD 1016311: independent per-view loadavg sampling, bounded global raw-data caches, per-request identity memoization and runc registration metadata. Add a global transient-cache TTL and per-container shortening annotation, retain mixed-TTL snapshots until their own expiry, and stop samplers when their owner exits. This replaces read-triggered sampling while preserving cgroup/PID-namespace isolation. Registration-time identity is only a seed: live requests still resolve their own cgroup/PID namespace, and full initial snapshot seeds and a passthrough/simulation list are not implemented.
  * Add `fs` to the cached test-build components: build a static sysbox-fs binary and patch only that executable into an explicit baseline deploy image, enabling focused FS regression without rebuilding unrelated runtime components. Document the required host restart and affected-Pod recreation.
  * sysbox-fs: isolate proc snapshots by shared-state lifetime and requester cgroup/PID namespace, merge concurrent cache misses, and release snapshots/loadavg/stat state when the final FUSE user exits. Nested resource views could reuse an outer-container snapshot, concurrent reads could duplicate collection or advance loadavg twice, and unregistered containers retained sampling state; the initial cache-only change preserved TTLs, EWMA constants, lazy sampling, and five-minute idle expiry while preventing closed lifetimes from repopulating caches (the later TAPD implementation above replaces the sampling lifecycle). Validation: local race tests and 218 source-image regression passed, including 800 cgroup-isolation checks, L0 full/lite local-path and full CSI persistence, and L2 persistence/CRI stats. Restarting the host FS required affected Pod recreation; baseline business failures and the temporary installation limits are recorded in KNOWN-ISSUES.md.
  * Record v0.7.1-39 release validation in TEST.MD: verify nested node labels are no longer rewritten, node permissions and NODE_NAME injection are absent, and install/reinstall plus local-path nginx persistence and CRI stats pass on ckm-snapshot-1109.
  * Remove the unused nested-runtime node label reconciliation from the inner agent after RuntimeClass scheduling selectors were removed; drop the nested agent/uninstall NODE_NAME injection and keep node get/patch permissions only in host mode. Runtime health probes remain unchanged.
  * Verify v0.7.1-38 on 218 ckm-snapshot-1109: release checksums and installed lite binary match, nested RuntimeClass and nginx Pod have no node selector, install/reinstall and local-path persistence/CRI stats pass. Recover from image-download stalls by importing the digest-verified release image; retain test PVCs and scale the test deployment to zero.
  * Remove scheduling node selectors from both host and nested RuntimeClasses so workload scheduling no longer depends on the shared installation selector or the `sysbox.w7panel.io/nested-runtime: ready` label; prohibit these selectors in AGENTS.md and align the Chart documentation.
  * Record ckm-2lmw8 PHP recovery: an existing rootfs-persistent Pod lacked its sysbox-rootfs sidecar and could not start. Current admission dry-run injected it correctly; recreating only the failed Pod restored PHP-FPM with its original PVC retained.
  * Verify the official v0.7.1-37 release on 218: host/nested auto detection, release image and binary checksums, full/lite local-path and Longhorn regression, nested cold-image startup, cancellation cleanup, snapshotter recovery and idempotent Helm upgrade passed. Record transient image digest/download and Pod-capacity issues separately from runtime results.
  * Lint both explicit host and nested Chart modes during release packaging; offline CI cannot resolve the default auto mode against a target cluster.
  * Default Chart installMode to auto (also for an empty value): detect CKM's child-agent DaemonSet via k3k-agent-pod=true and IS_CHILD=true, matching w7panel-ckm/createAgent and w7panel-server. Preserve explicit host/nested overrides and reject offline auto rendering rather than silently installing the host runtime in a child cluster. Verified connected rendering against the 218 host and its independent CKM.
  * Give the nested agent readiness command 10 seconds instead of Kubernetes' implicit 1-second timeout, allowing its 3-second snapshotter RPC and subsequent CRI check to complete under load without false readiness failures.
  * Pull Chart images directly from `ghcr.io/w7panel/sysbox-deploy-k3s` by default instead of the CDN proxy, including the host installer, nested agent, uninstall hook and admission backend.
  * Document the manual fusermount3 AppArmor repair procedure: enter the affected host, preserve and back up local policy, add scoped Sysbox rules, compile and reload the profile, verify workload recovery and logs, and restore the backup if needed.
  * Record the authorized manual fusermount3 policy repair on the 60 cluster: Sysbox mount/unmount rules are present and reload succeeds with enforcement retained; the affected tradition-tool Deployment is now 1/1 Available. This is a targeted live recovery, not full v0.7.1-34 release validation.
  * Fix nested snapshotter cold-image timeouts: keep extraction snapshots layered using containerd's snapshot reference label and materialize only container writable roots. Make rootfs copying cancellable, stage data before atomic publication, serialize retries, and require successful unmount before directory deletion. Preserve existing PVC contents on failure and recognize completed publication across retries using the upper inode marker. Nested materialization now requires GNU cp and filesystem RENAME_EXCHANGE support; unsupported environments fail explicitly.
  * Fix inner-agent health checks accepting a dead snapshotter's stale socket: require a listening Unix socket and query the snapshotter service directly with an RPC deadline instead of containerd's cached metadata. Add shell/socket and snapshotter cancellation, metadata preservation, cleanup and interrupted-publication regressions; 218 cluster validation is recorded in KNOWN-ISSUES.md.
  * Configure the host fusermount3 AppArmor local policy after dependency installation and before starting Sysbox, including same-version installs. Distributions that confine fusermount3 rejected `/var/lib/sysboxfs/<container-id>/` mounts and left Pods in ContainerCreating; allow only Sysbox FUSE mounts/unmounts, preserve existing local rules, and fail installation if policy reload fails. Disabled or absent profiles are left untouched.
  * Diagnose console-145382's v33 lite timeout: nested snapshotter materializes every image extraction layer, amplifying copies beyond the 120-second create deadline; copies continue after cancellation. Record the native-runc comparison and successful cached retry (nginx HTTP 200), while retaining the unresolved cleanup and stale-socket defects; no runtime code was changed.
  * Record console-145382's official v0.7.1-33 nested upgrade: the CNI path fix works, but a stale snapshotter socket falsely satisfies agent health checks and a subsequent lite nginx container creation times out. Document the socket recovery, stopped smoke test, and unresolved functional validation without claiming a release pass.
  * Clarify the CKM listener investigation against Linux v7.0: interrupted seccomp poll locking can return POLLERR without invalidating the listener, while Sysbox's non-POLLIN exit condition closes it. Record this concrete error-handling defect separately from the unrecorded historical trigger; no runtime change was deployed.
  * Document the 218 CKM agent deletion failure: BPF confirmed missing seccomp listeners on both live K3s process trees, while containerd TaskExit cleanup failed with rootfs unmount ENOSYS. Record why recreating the outer Pod can restore the listener and distinguish the verified failure mechanism from the still-unidentified listener release trigger; no runtime behavior was changed.
  * Reduce CKM `k3s` nsenter load for read-only `/proc/sys/kernel/threads-max`: open without PassThrough and read the current host value directly. On 218, 200 controlled reads succeeded without a `comm=cat` nsenter request; L0 rootfs/PVC and L1/L2 tests passed. The proposed sysfs net namespace change broke `/sys/devices/virtual/net` lookups in a fresh CKM, so it was reverted from the runtime and source. Restarting `sysbox-fs` disconnected existing FUSE mounts; affected Pods were recreated sequentially and verified. The global log reduction cannot be attributed to this change because the Pod and CKM populations changed during recovery.
  * sysbox-fs: log empty nsenter reaper sweeps at debug level because frequent INFO messages obscure useful service logs; report unexpected wait errors separately instead of mislabeling them as empty sweeps.
  * Document the isolated 218 Longhorn throttle test and its exact cgroup v2 `io.max` setup and removal commands: at 1 MiB/s and 100 IOPS, CKM startup's preflight `cluster-reset` hit 7–8 second etcd fsyncs and timed out after about 46 seconds; removing the limit restored CKM readiness. This records the trigger behind a prolonged `Starting single node setup` message and makes the test repeatable without affecting other volumes.
  * Record the 218 `31 → 15-k3s137 → 31` CKM startup comparison and the confirmed image-layer download stall that caused the first Helm timeout. A further `15-k3s137` attempt hit `ErrImageNeverPull` for the imported image and rolled back; document the final healthy `31` state for follow-up diagnosis.
  * Record the 218 release check for `v0.7.1-15`: its installer rejects K3s v1.37 before host installation, so a new CKM startup result is unavailable. The Helm release was restored to `0.7.1-31`, with the node and seven existing CKMs Ready.
  * Make the host `sysbox-runc` bind-mount `/dev/fuse` and grant device access for every Sysbox container, so CKM Server no longer depends on a Pod hostPath. Preserve an existing explicit device mount and fail clearly when the host device is unavailable. Add a no-volume L0 open check to the Sysbox-in-Sysbox skill before CKM template migration.
  * Document the 218 CKM test showing that removing `/dev/fuse` leaves inner K3s running but prevents the inner Sysbox agent and snapshotter-backed workloads from starting; all seven CKMs were restored to the original controller image and FUSE mounts.
  * Document the follow-up 218 test: the CKM template now owns the `/dev/fuse` mount, while `mknod /dev/fuse c 10 229` inside a `sysbox-runc` Pod with `hostUsers: false` fails with `Operation not permitted`.
  * Removed obsolete bootstrap-image environment variables from the release workflow. The release script now builds and pushes only the deploy image; leaving the unused variables suggested that CI still produced a bootstrap image.
  * Restore the nested `runtime_platforms.sysbox-runc-lite` mapping so a CKM created without the legacy bootstrap registers the Sysbox snapshotter filesystem in containerd CRI stats. Without it, fresh inner K3s nodes returned an empty writable-layer mountpoint and `/stats/summary` failed; the new `ckm-inner-chart-1009` passed the full nested workload regression after the Chart upgrade.
  * Nested Chart uninstall now lets its hook finish template and payload cleanup before restarting the CKM Server through the uninstall script. Terminating K3s inside the hook interrupted Helm and left the release in `uninstalling`; the two-stage flow completes Helm first, then reloads containerd. The agent also detects K3s' rewritten argv and uses snapshotter RPC for recovery after agent recreation.
  * Updated Sysbox-in-Sysbox skill and L1 installation guide for Chart-managed inner runtime: the deploy image now supplies the agent and payload, while CKM no longer injects a bootstrap initContainer or launcher. Installation may restart inner K3s; the test-cluster migration checks every existing CKM sequentially, including pinned templates, so stale startup wiring and unloaded CRI handlers are detected. Historical bootstrap results remain in HISTORY and KNOWN-ISSUES.
  * Inner K3s bootstrap: advertise `remap-ids` on the Sysbox proxy snapshotter, matching the host template so containerd can delegate snapshot UID/GID remapping when an inner Pod uses a user namespace. A single-layer bootstrap test image was applied to `ckm-disk-ubuntu-0930`; containerd reported `Capabilities: remap-ids`, and the existing no-`hostUsers` lite nginx rootfs, empty-PVC, special-bind, and CRI-stats regression passed. Inner userns remapping was attempted but blocked before snapshotter preparation; see `KNOWN-ISSUES.md`.
  * Inner K3s bootstrap: remove explicit `enable_unprivileged_ports`, `enable_unprivileged_icmp`, and `device_ownership_from_security_context` overrides from the containerd runtime template, leaving these settings to containerd defaults.
  * sysbox-runc-lite: restore upstream runc's `/dev/null` stat precheck before stdio ownership setup, so a missing device fails explicitly instead of continuing with divergent runtime behavior.
  * sysbox-runc-lite: restore upstream gocapability behavior when `/proc/self/status` is missing instead of silently accepting an incomplete capability load. This removes a Sysbox-only proc compatibility bypass; the same binary passed host `local-path`/`disk-default` and nested CKM rootfs, empty-PVC, special-bind, CRI-stats, and TTY/proc regressions. A single-layer bootstrap image containing the tested binary was pushed and passed a CKM Server rebuild plus nested functional regression; no tagged release artifact has been verified.
  * Inner K3s bootstrap: remove the `runtime_platforms.sysbox-runc-lite` image-service mapping and the lite handler's `container_annotations`, retaining its `snapshotter = "sysbox"` and rootfs Pod annotation. A new `disk-default` CKM passed the nested rootfs, empty-PVC copy, special-bind, CRI writable-layer, and `/stats/summary` checks after a live script swap; a rebuilt bootstrap image remains unverified.
  * sysbox-runc-lite: remove custom `/dev`, `/proc`, sysfs, mount-error, recursive-bind and pivot fallbacks so the runtime follows standard runc mount/device behavior while retaining empty-PVC initialization and persistent special mounts. A locally built static binary passed L2 nested and L0 local-path/Longhorn functional regressions plus TTY exec; no new image or release artifact has been built.
  * sysbox-runc-lite: remove an unrelated `/dev/null` precheck left in `fixStdioPermissions`, keeping the requested lite delta limited to PVC initialization and special-directory binds. Unit tests pass; this final source variant still needs same-version live regression.
  * Restore strict admission rejection for rootfs-annotated Pods lacking a Sysbox RuntimeClass. The source guard is retained; a test admission image and webhook match-condition update were validated on 218 and two CKMs, while a formal chart/release build remains pending.
  * Build and push a single-layer CKM bootstrap test image containing the final lite source and inner K3s script without redundant image-service mapping or container annotations. Image contents passed smoke checks; the shared CKM controller reverted a CKM-local image override, so nested functional validation remains pending.
  * After switching the shared CKM controller's bootstrap image, `ckm-disk-ubuntu-0930` rebuilt with the flattened image and passed the nested rootfs, empty-PVC initialization, special-bind, and CRI-stats regression. The test Deployment and PVCs remain in place.
  * Consolidate Sysbox-in-Sysbox guidance into the project skill and require it for related work through `AGENTS.md`, so branch analysis and live regression use one maintained entry point with explicit authorization boundaries.
  * sysbox-runc-lite: remove the extra exec-time `chroot` and `/dev/ptmx` fallback. The standard runc setns path and device setup handle lite workloads; newly created host and CKM nginx Pods pass non-TTY/TTY exec, device, persisted rootfs, UID/GID 65534, and initContainer shared-volume checks. Unspecified RuntimeClass Pods continue to use native runc.
  * sysbox-runc-lite: preserve OCI UID/GID mappings as supplied instead of rewriting nonzero host IDs inside nested user namespaces; remove the custom `user_namespaces` and `mountExtensions.idmap` feature claims, which were not required by the current L2 rootfs workflow and could misrepresent runtime capabilities. The nested regression now validates the workload directly rather than requiring the removed feature flag.

### Added
  * Inner K3s bootstrap no longer creates the retired `sysbox-runc-nested` wrapper or advertises a full `sysbox-runc` containerd handler. The lightweight nested chart exposes only `sysbox-runc-lite`; ordinary Pods keep native `runc`. Existing wrapper files are removed on launcher startup so an upgraded CKM cannot accidentally use the abandoned full runtime.
  * Nested CKM chart installation now creates a real Helm release inside the selected CKM K3s. The test flow copies Helm and the selected chart into the Server temporarily, while bootstrap remains the sole owner of the lite binary and containerd config; skills and deployment docs now enforce this topology and distinguish the historical full nested runtime from current lite acceptance.
  * Preserved the GitHub runner user's Docker login when `release.sh` switches to its build cache config; the release had built and verified both images but GHCR push failed with `unauthorized` because it previously copied credentials only from `/root/.docker`.
  * Isolated the root-run deb builder's Go caches from the host Go caches in `release.sh`; GitHub Actions previously failed to build `sysbox-runc-lite` with `permission denied` after the deb container wrote root-owned cache files.
  * Inner K3s now leaves its default `runc` handler on the native runtime and overlayfs; the webhook no longer chooses lite from a rootfs annotation. Set `runtimeClassName: sysbox-runc-lite` explicitly for Sysbox rootfs workloads. Previously the bootstrap routed ordinary Pods through lite and the webhook could select lite without a RuntimeClass.
  * Added cached fast-path builds for Sysbox-in-Sysbox regressions. Changed Go components are patched into a known-good deploy or bootstrap image, avoiding a full package/image rebuild for each test while retaining the complete release path for final acceptance.
  * Added the `06-build-and-test.sh` nested-runtime acceptance entry point and documented the required L0/L1/L2 regression sequence.
  * Prevented the CKM test helper from reusing a retained Server PVC under the same CKM name; local-path does not support the resize attempted by the controller, so the helper now requires a unique name without modifying existing data.
  * Add `skills/sysbox-in-sysbox-test`, with build/release and staged-test wrappers plus a reusable L0/L1/L2 regression workflow covering release artifacts, RuntimeClass handlers, local-path and CSI PVC initialization, rootfs persistence, special binds, CKM deployment, and IP-aware test cleanup.
  * sysbox-admission: stop injecting `/dev/fuse` into outer `sysbox-runc` Pods; L1 CKM bootstrap remains responsible for the fuse device required by its snapshotter.
  * w7panel-sysbox: omit the host installer DaemonSet in nested mode; the CKM bootstrap and nested chart runtime configuration provide the required lightweight runtime instead.
  * Release packaging excludes local Git metadata and generated `dist/` artifacts from the source archive so deployment-image builds package only source files.
  * sysbox-runc-lite initializes eligible empty CSI PVC mounts without the removed `sysbox/volume-init` Pod annotation; the nested nginx regression now verifies initial copy and persistence after Pod recreation.
  * Allow nested runc-lite test workloads without `hostUsers:false` so validation can bypass the CKM inner-containerd sandbox userns blocker.
  * sysbox-deploy-k8s: stop managing the `sysbox-runtime` node taint during installation and cleanup so deployment does not alter node scheduling; existing installer tolerations remain in place.
  * submodules: track the `w7panel` branch for the W7Panel-maintained `sysbox-fs`, `sysbox-runc`, and `sysbox-mgr` repositories; upstream-only modules remain on their default branch.
  * sysbox-deploy-k8s: run host service operations through the host PID 1 on all distributions; Ubuntu 24.04 installers previously treated the helper unit cleanup as a chroot operation and exited with code 1.
  * sysbox-deploy-k8s: support installation on CentOS Stream 9 hosts; the deploy script now normalizes the quoted `ID=centos` value to recognize `centos-9`, uses the generic Sysbox artifacts, and builds the K3s installer from a CentOS Stream 9 base instead of the retired CentOS 7 base.
  * sysbox-deploy-k8s: mount the host DBus socket at both `/var/run/dbus` and `/run/dbus` so systemd control commands work inside CentOS Stream 9 installer containers.
  * sysbox-deploy-k8s: set the DBus system bus address explicitly for CentOS Stream 9 installer containers.
  * sysbox-deploy-k8s: execute systemd service operations in the host namespace on CentOS Stream 9 because container-local systemctl cannot connect to the host bus.
  * sysbox-deploy-k8s: mount the host `/bin` directory for chrooted systemctl execution on CentOS Stream 9.
  * sysbox-deploy-k8s: run installer Pods with the host PID namespace so CentOS Stream 9 service operations target host PID 1.
  * sysbox-installer-helper: use DNF rather than Debian package commands when provisioning dependencies on CentOS Stream 9.
  * sysbox-installer-helper: skip Ubuntu-specific Shiftfs DKMS installation on CentOS Stream 9, whose repositories do not provide DKMS.
  * sysbox-deploy-k3s: install `procps-ng` in the CentOS Stream 9 installer image so host sysctl configuration succeeds.
  * sysbox-deploy-k8s: continue sysctl configuration when a distro omits an optional kernel sysctl entry.
  * sysbox-runc: skip proc pressure bind mounts only on CentOS Stream 9 hosts, where legacy Ubuntu Bionic rootfses lack `/proc/pressure`; preserve the three virtualized PSI paths on Ubuntu and other hosts.
  * sysbox-runc: tolerate K3s writing its `net.ipv4.ip_unprivileged_port_start` sandbox sysctl when it is read-only inside a Sysbox container namespace.
  * Document the nested K3s sandbox failure seen with a PVC-backed `fuse-overlayfs` Sysbox rootfs on CentOS Stream 9, distinguishing the base `/proc` mount failure from proc-pressure virtualization; testing confirmed the inner K3s `native` snapshotter does not avoid it.
  * sysbox-fs: when proxying nested procfs mounts, bind only proc submounts that exist in the Sysbox container; this prevents missing CentOS Stream 9 PSI paths from rolling back the entire inner `/proc` mount while preserving them on Ubuntu.
  * sysbox-deploy-k8s: add support for Kubernetes v1.36; the deployment script previously rejected this version through its supported-version whitelist.
  * Fix Debian release packaging by keeping the unreleased changelog header in the format required by the package converter.
  * Fix PVC subPath validation when kubelet uses a generated PVC directory name and CRI uses the logical volume name.

## [0.7.1] - 2026-07-28
### Added
* Support Sysbox on Ubuntu 24.04 with kernel 6.8+ under containerd 2.x; fixes sysfs mount failures ("mount through procfd: operation not permitted") during container/pod creation (issue #1013).
* Fix bug where a container's requested `--ulimit nofile` soft value silently failed to apply when the hard limit exceeded Go's internal rlimit cache threshold (issue #1014).
* Fix nil-pointer crash in sysbox-fs triggered when a container shares another container's network namespace via `docker run --network container:<id>`.
* Fix deadlock in sysbox-fs that could occur when many Docker containers are started/stopped concurrently inside a Sysbox container (issue #998).
* Fix sysbox-mgr serializing container registration behind a global lock unnecessarily, slowing down concurrent container restores when Docker is slow to respond.
* Fix bind mounts failing (or silently landing in the wrong place) when a mount's destination is nested inside another pending mount via a symlink in the container image; affected K8s pods mounting a volume at `/run` on images with a `/var/run -> /run` symlink (issue #1026).
* Update grpc dependency to pick up an HTTP/2 flood-protection fix.
* Remove usage of deprecated Go standard library packages (e.g., `io/ioutil`) across Sysbox components.

## [0.7.0] - 2025-03-02
### Added
* Port runc security patches for CVE-2025-3133, CVE-2025-52881, CVE-52565.
* Support volatile overlayfs remounting.
* Add features command to sysbox-runc.
* Enable newer versions of runc to run inside Sysbox containers properly by trapping openat2 system call to allow access to sysbox-fs mounts under /proc and /sys.
* sysbox-deploy-k8s: add support for k8s v1.33, v1.34, and v1.35. Deprecate support for v1.29 to v1.31.
* sysbox-deploy-k8s: enable compatibility with K8s user-namespaces (requires containerd v2.0.5+ or CRI-O).
* sysbox-deploy-k8s: don't install CRI-O when K8s cluster has containerd 2.0.5+.
* Update docs to indicate support for K8s user-namespaces (requires `hostUsers: false` directive in pod spec).

## [0.6.7] - 2025-05-09
### Added
  * Add support for binfmt_misc namespacing inside Sysbox containers (kernel 6.7+); issue #592.
  * Add edition and version info in sysbox-mgr and sysbox-fs logs.
  * Fix bug when starting/stopping containers with large number of layers, on kernels without idmapping or shiftfs.
  * Fix bug when unmounting fuse device inside Sysbox container (issue #854).
  * Fix bug causing failure to open /proc/sys/kernel/shm inside Sysbox container (issues #903, #909).
  * Fix bug causing false-negatives when checking for shiftfs support in hosts (issue #922)
  * Fix bug causing sysbox-mgr to consume CPU cycles when idle (issue #915).
  * sysbox-deploy-k8s: add support for K8s v1.32 and deprecated support for K8s v1.28.

## [0.6.6] - 2025-01-03
### Added
  * Add support for k8s v1.31 and deprecate k8s v1.27.
  * Ensure latest OCI runc works inside Sysbox containers (issue #879).
  * Retry fix for bug in sysbox-fs that resulted in left-over nsenter mounts of procfs and sysfs (issue #829).
  * Fix bug in parsing of subid limits in /etc/login.defs (issue #870).
  * Fix bug in Sysbox shiftfs checker that was causing false negatives.

## [0.6.5] - 2024-11-08
### Added
  * Update to Golang 1.22.
  * Implement 'relaxed-read-only' mode to ease the bind-mount requirements of read-only containers.
  * Enhance sysbox-fs emulation to allow writes to '/proc/sys/kernel/shm*' paths.
  * Fix emulation of /proc/sys/kernel/random (issue #785).
  * Fix bug in sysbox-fs that resulted in left-over nsenter mounts of procfs and sysfs (issue #829).
  * Fix issue causing Sysbox to slow down due to the accumulation of large numbers of mountpoints when '--allow-immutable-unmounts' knob is set to 'false'.
  * Skip dns change when default route doesn't exists (issue #834).
  * Fix issue breaking read() operations over '/sys/kernel' nodes with non-zero offsets.
  * Return error for disallowed unmount of sysbox-fs managed mountpoint (issue #808).
  * sysbox-deploy-k8s: add support for K8s v1.29 and v1.30.
  * sysbox-deploy-k8s: deprecate support of K8s v1.26.
  * sysbox-deploy-k8s: add support for incremental Sysbox upgrades.
  * sysbox-deploy-k8s: introduce logic to config Sysbox's daemons through a configMap.
  * sysbox-deploy-k8s: increase 'vm.max_map_count' to satisfy mmap-demanding apps (Elastic).
  * sysbox-deploy-k8s: enhance logic to detect Sysbox's config-environment changes (sysctl vars).
  * sysbox-deploy-k8s: fix issue preventing Sysbox installation from concluding during upgrades.
  * sysbox-deploy-k8s: fix for race-condition in GKE clusters.
  * sysbox-deploy-k8s: fix for 'sysbox-installer-helper' to workaround dpkg error.
  * sysbox-deploy-k8s: check for existence of 'unprivileged_userns_clone' procfs node before attempting to write().
  * sysbox-deploy-k8s: delete preexisting sysbox pods during upgrade or re-installation process.

## [0.6.4] - 2024-04-06
### Added
  * Fix to allowing running x86 apps in Sysbox containers (issue #350).
  * Fix sysbox-fs nsenter mount leak.
  * Fix sysbox emulation of /proc and /sys in containers for kernels 6.5+.
  * Add hardening against CVE-2024-21626.
  * Fix ordering of mounts under /run for containers with systemd (issue #767).

## [0.6.3] - 2024-01-08
### Added
  * Fix to ensure "docker --net=host" works inside Sysbox containers (issue #712).
  * Fix bug when mounting host kernel headers into containers (issue #727).
  * Fix emulation of /sys/devices/virtual/* inside containers (issue #719).
  * Don't intercept *xattr syscalls by default (improves performance).
  * Add feature to skip shiftfs and idmapping on specific container files/dirs (via `SYSBOX_SKIP_UID_SHIFT` container env var).
  * Fix bug with fsuid-map-fail-on-error config option.
  * Fix bug with pivot-root inside Sysbox containers (ensures docker:24-dind image can run inside Sysbox containers).
  * sysbox-deploy-k8s: add support for Kubernetes v1.27 and v1.28.
  * sysbox-deploy-k8s: automatically detect installation on GKE clusters and set up configs accordingly.
  * sysbox-deploy-k8s: support installation on Debian-based K8s nodes.
  * sysbox-deploy-k8s: don't install shiftfs on K8s nodes with kernel >= 5.19.
### Removed
  * sysbox-deploy-k8s: deprecated support for K8s v1.24 and v1.25 (EOL'd).

## [0.6.2] - 2023-06-09
### Added
  * Fix bug in Sysbox's checking of host support for idmapping and shiftfs.
  * Fix storage leak in /var/lib/sysbox when using Sysbox on K8s clusters.
  * Fix bug in Sysbox's handling of "docker run -w" flag.
  * Change disable-inner-image-preload flag to allow running (but not committing) sysbox containers with preloaded inner images.
  * Set disable-inner-image-preload flag in Sysbox K8s deployments to improve performance when stopping pods.

## [0.6.1] - 2023-04-07
### Added
  * Added support for ID-mapped overlayfs lower layers; eliminates need for shiftfs and Sysbox rootfs chown; requires kernel 5.19+.
  * Have Sysbox perform shiftfs and ID-mapping functional checks during init (issue #596).
  * Fixed rootfs cloning to prevent inode leakage (for hosts with kernel < 5.19 and no shiftfs) (issue #570).
  * Added support for Kubernetes v1.24 to v1.26.
  * Added --disable-inner-image-preload flag to sysbox-mgr (speeds up Sysbox container startup).
  * Added --syscont-mode flag to sysbox-mgr; allows Sysbox to work in system container mode (default) or regular container mode; the latter is meant for running microservices with stronger isolation.
  * Added --disable-shiftfs-on-fuse flag to sysbox-mgr; prevents Sysbox from mounting shiftfs on top of FUSE-backed filesystems (some of which don't work with shiftfs).
  * Added few optimizations to expedite I/O operations in procfs/sysfs emulated resources.
  * Enhanced life-cycle management of Sysbox daemons in Systemd-free scenarios.
  * Prevented concurrent execution of Sysbox daemons (multi-instance problem).
  * Improved the handling of ungraceful shutdown scenarios.
  * Eliminated Sysbox dependencies on configfs kernel module presence.
  * Fixed emulation of /sys/module/nf_conntrack/parameters inside containers.
  * Added emulation of /sys/devices/virtual/dmi branch inside containers (for hosts where this or inner resources is not present).
  * Hide /sys/kernel/security inside containers (issue #662)
  * Don't assign more capabilities to the container than those given to Sysbox itself.
  * Don't fail in kernel distros without /lib/modules/<kernel-release>.
  * Increased the pods-per-node limit from 16 to 4K (Sysbox-CE now matches Sysbox-EE on this regard).
  * Extended kubelet config-detection process to multiple drop-in files in sysbox-deploy-k8s daemon-set.
  * Incorporated taints during sysbox-deploy-k8s installation process.
  * Fixed issue preventing sysbox-deploy-k8s installation in rke2 environments (issue #614).
  * Fixed issue preventing proper sysbox-deploy-k8s installation in Azure (issue #612).

## [0.5.2] - 2022-05-18
### Added
  * Fixed issue #544 preventing containers initialization within sysbox containers when running latest oci-runc releases (1.1.0-rc.1+).

## [0.5.1] - 2022-04-06
### Added
  * Added support to allow CIFS mounts within Sysbox containers (Sysbox-EE only).
  * Fixed issue to allow shiftfs mounts over files that are themselves bind-mounts.

## [0.5.0] - 2022-03-22
### Added
  * Added support for Linux ID-mapped mounts (shiftfs alternative in kernels >= 5.12).
  * Added support for ARM64 hosts.
  * Added support for running buildx/buildkit inside Sysbox containers.
  * Added support for running Rancher RKE2 and Mirantis K0s inside Sysbox containers.
  * Added configs to disable trapping chown and xattr* syscalls (improves performance but may reduce functionality).
  * Added config to strictly honor container capabilities from higher-level container manager.
  * Added support for per-container configs via `SYSBOX_*` env vars.
  * Improved performance of Sysbox's syscall interception code.
  * Improved the way Sysbox releases the seccomp-fd handles for intercept syscalls (kernels >= 5.8).
  * Improved Sysbox's cross-compilation support (artifacts can now be generated from/to either AMD64 or ARM64 hosts).
  * Update to golang 1.16.
  * Replaced the per-distro *.deb installation packages with a single deb bundle package.
  * Allow alternative Docker data-root inside a Sysbox container (if Docker is pre-installed in the Sysbox container image).
  * Fixed segfault when building Docker image inside Sysbox container (issue #484).
  * Fixed segfault when running python pip install inside nested sysbox container (issue #485).
  * Fixed issue with running KinD inside a Sysbox container (issue #415).
  * Fixed problem with shiftfs mounts on Kubernetes persistent volumes (issue #431).

### Removed
  * None.

## [0.4.1] - 2021-09-30
### Added
  * Added important optimization to expedite the container creation cycle.
  * Enhanced uid-shifting logic to perform shifting operations of Sysbox's special dirs on a need basis.
  * Added support for Kinvolk's Flatcar Linux distribution (Sysbox-EE only).
  * Added basic building-blocks to allow Sysbox support on ARM platforms.
  * Fixed issue preventing Sysbox folders from being eliminated from HDD when Sysbox is shutdown.
  * Enable sys container processes to set 'trusted.overlay.opaque' xattr on files (issue #254).
  * Fixed bug resulting in the failure of "mount" operation within a sys container.
  * Made various enhancements to Sysbox's kubernetes installer to simplify its operation.
  * Extend Sysbox's kubernetes installer to support Rancher's RKE k8s distribution.

## [0.4.0] - 2021-07-13
### Added
  * Added support to create secure Kubernetes PODs with Sysbox (sysbox-pods).
  * Added support for Cgroups-v2 systems.
  * Added support to allow K3s execution within Sysbox containers.
  * Extended Sysbox support to Fedora-33 and Fedora-34 releases.
  * Extended Sysbox support to Flatcar Linux distribution.
  * Modified Sysbox binaries' installation path ("/usr/local/sbin" -> "/usr/bin").
  * Enhanced generation and handling of logging output by relying on systemd (journald) subsystem.
  * Multiple enhancements in /proc & /sys file-system's emulation logic.
  * Extended installer to allow it to deploy Sysbox in non-strictly-supported distros / releases.
  * Improved security of shiftfs mounts.
  * Fixed issue impacting sysbox-fs stability in scaling scenarios (issue #266).
  * Fixed issue preventing sys-container initialization due a recent change in oci-runc (issue #291).
  * Fixed issue with "--mountpoint" cli knob being ignored (sysbox issue #310).
  * Fixed issue causing sysbox-fs handlers to stall upon access to a procfs node (issue #306).
  * Fixed issue preventing write access to 'domainname' procfs node (issue #287).
  * Fixed issue preventing systemd-based containers from being able to initialize (issue #273).
  * Made changes to allow Docker network sharing between containers.
  * Ensure that Sysbox mounts in read-only containers are mounted as read only.
### Removed
  * Deprecated EOL'd Fedora-31 and Fedora-32 releases.

## [0.3.0] - 2021-03-26
### Added
  * Secured system container initial mounts (mount/remount/unmounts on these from within the container are now restricted). See [here](docs/user-guide/security.md#initial-mount-immutability) for details.
  * Improved Sysbox systemd service unit files (dependencies, open-file limits).
  * Improved logging by sysbox-mgr and sysbox-fs (json logging, more succint logs).
  * Added support for systemd-managed cgroups v1 on the host (cgroups v2 still not supported).
  * Added support for read-only Docker containers.
  * Synced-up sysbox-runc to include the latest changes from the OCI runc.
  * Added support for Debian distribution (Buster and Bullseye).
  * Added ground-work to support Sysbox on RedHat, Fedora, and CentOS (next step is creating a package manager for these).
  * Added config option to configure the Sysbox work directory (defaults to /var/lib/sysbox).
  * Added support and required automation for Sysbox-in-Docker deployments.
  * Fixed sporadic session stalling issue during syscall interception handling.
  * Fixed sysbox-mgr file descriptor leak (sysbox issue #195).
  * Fixed problem with "docker --restart" on Sysbox containers (sysbox issue #184).
  * Fixed race condition in sysbox-fs procfs & sysfs emulation.
  * Fixed problem preventing kernel-headers from being properly imported within sys containers.
  * Fixed inappropriate handling of mount instructions in chroot jail environments.
### Removed
  * None.

## [0.2.1] - 2020-08-25
### Added
  * Created debian packages for first community-edition release.
  * Fixed package installer bug preventing 'shiftfs' feature from being properly utilized.
  * Enhanced package installer to prevent network overlaps between inner and outer containers.
### Removed
  * Deprecated support of Ubuntu's EOL release: Eoan (19.10).

## [0.2.0] - 2020-07-03
### Added
  * Added initial Kubernetes-in-Docker support to enable secure, flexible and portable K8s clusters.
  * Added support for running privileged-containers within secure system containers.
  * Added support for containerd to run within system containers.
  * Made multiple performance improvements to expedite container initialization and i/o operations.
  * Added support for Ubuntu-Eoan (19.10) and Ubuntu-Focal (20.04).
  * Extended support for Ubuntu-Cloud releases (Bionic, Eoan, Focal).
  * Enhanced Sysbox documentation.
### Removed
  * Deprecated support of Ubuntu's EOL releases: Ubuntu-Disco (19.04) and Ubuntu-Cosmic (18.10).

## [0.1.2] - 2019-11-11
### Added
  * Created Sysbox Quick Start Guide document (with several examples on how to use system containers).
  * Added support for running Systemd in a system container.
  * Added support for the Ubuntu shiftfs filesytem (replaces the Nestybox shiftfs).
  * Using `docker build` to create a system container image that includes inner container images.
  * Using `docker commit` to create a system container image that includes inner container images.
  * Added support for mounts over a system container's `/var/lib/docker` (for persistency of inner container images).
  * Made multiple improvements to the Sysbox User's Guide and Design Guide docs.
  * Rebranded 'sysboxd' to 'sysbox'.
### Removed
  * Deprecated Nestybox shiftfs module.

## [0.1.1] - 2019-09-04
### Added
  * Extend installer support to latest Ubuntu kernel (5.0.0-27).

## [0.1.0] - 2019-08-28
### Added
  * Initial public release.
  * Added external documentation: README, user-guide, design-guide, etc.
  * Extend support to Ubuntu-Bionic (+5.x kernel) with userns-remap disabled.
  * Added consistent versioning to all sysboxd components.
  * Increased list of kernels supported by nbox-shiftfs module (refer to nbox-shiftfs module documentation).
  * Add changelog info to the debian package installer.

## [0.0.1] - unreleased
### Added
  * Internal release (non-public).
  * Supports launching system containers with Docker.
  * Supports running Docker inside a system container.
  * Supports exclusive uid(gid) mappings per system container.
  * Supports partially virtualized procfs.
  * Supports docker with or without userns-remap.
  * Supports Ubuntu Disco (with userns-remap disabled).
  * Supports Ubuntu Disco, Cosmic, and Bionic (with userns-remap enabled).
  * Includes the Nestybox shiftfs kernel module for uid(gid) shifting.

## 2026-11-09 — 内层 Sysbox 保留 K3s CNI 路径

- 修复内层安装脚本将 CNI 目录固定为 `/var/lib/rancher/k3s/data/cni` 的问题：使用 `/bin` 插件布局的 K3s 容器安装 Sysbox 后，会因找不到 flannel、loopback 而无法创建或清理 Pod 网络。
- containerd v3 模板改用 K3s 的 `CNIBinDir` 和 `CNIConfDir`，按上游条件生成 `bin_dirs` 与 `conf_dir`，保留实际路径并正确转义；不改变 agent 网络模式、重启流程或其他 runtime 配置。
- 新增实际 shell heredoc 生成及 Go template 渲染回归测试，覆盖 `/bin`、`data/cni`、自定义路径、仅插件目录、仅配置目录及空配置，并检查 Sysbox runtime 配置仍保留。验证：在 `sysbox-pkgr/k8s/scripts` 执行 `go test -v sysbox-inner-k3s_test.go`，6 个用例通过；Shell 语法检查、nested 模式 Helm lint 和模板渲染通过。未构建发布镜像、未部署或重启线上资源，现场恢复尚未验证。
