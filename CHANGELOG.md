# Changelog

## Unreleased

- 记录 2026-08-12 嵌套 L2 复测：因 L1 缺少可用 `fusermount3`/`/dev/fuse`，容器在 sysbox-fs 预注册阶段失败；已保存日志、清理临时容器并恢复宿主 sysbox-fs，避免将环境故障误判为嵌套 seccomp 或 namespace 回归。
- 修复 nested-identity 专用 PoC 在 `SkipSpecialMounts` 已启用时仍强制挂载 proc/sysfs 导致 L2 以 `EPERM` 启动失败；该模式恢复跳过特殊挂载，以便先验证 L2 user namespace 与容器启动链。
All notable changes to this project will be documented in this file.

## [Unreleased] - unreleased
### Added
	* nested Sysbox identity mapping: add an explicit, fail-closed `STANDARD_SUBID` / `NESTED_IDENTITY` contract across sysbox-runc, sysbox-mgr, and sysbox-fs. The dedicated inner runtime now creates a separate child user namespace with `0:0:65536`, uses NoShift without touching L1 `/etc/subuid` or `/etc/subgid`, keeps mount helpers in the L1 user namespace, binds seccomp sessions to registered container processes, and delegates cgroup v2 through a hidden limit parent plus `sysbox.delegate`. This replaces the no-third-userns workaround while preserving standard Sysbox's rejection of host ID 0.
	* nested Sysbox cgroup devices: do not load an L2 cgroup-device BPF program from the non-initial user namespace; the kernel does not delegate this operation, so L2 inherits L1's device policy while CPU, memory, IO, and PID limits remain on the hidden L2 limit cgroup. Real L1 Docker testing also confirmed that a second Sysbox seccomp user-notification listener fails with `EBUSY` when the L1 process tree already inherited L0's listener; full nested sysbox-fs syscall proxying therefore requires a single-listener routing design and is not yet complete.
	* sysbox-inner-k3s: start the real `sysbox-snapshotter` before inner K3s and bind the dedicated `sysbox-runc-inner` handler to it, including rootfs rw-layer annotation forwarding. Minimal K3s images now receive a local `mount.fuse3` adapter for their existing `fuse-overlayfs`, allowing nested integration tests to exercise the PVC-backed snapshot path instead of silently remaining on containerd's default overlayfs snapshotter.
	* sysbox-runc / command-mode inner K3s: run the dedicated `sysbox-runc-inner` handler in the outer Sysbox user namespace, while retaining its other container namespaces. A third user namespace cannot mount procfs through the outer Sysbox boundary (`EPERM`), so the prior pause Pod either had an empty `/proc` or could not start. The specialized path skips ID mappings and rootfs UID shifting, handles the resulting empty Sysbox-FS registration mapping, and restores working procfs for `free`, `top`, and `ps`.
	* sysbox-fs: when an experimental nested Sysbox process mounts procfs from a child user namespace, allow that specific request past the tracer's CAP_SYS_ADMIN precheck and proxy it into its target namespaces while retaining the outer user namespace. The inner runc intentionally lacks that capability, so the generic precheck returned `EPERM` before the helper could run; both the former all-namespace proxy (`EINVAL`) and direct kernel execution (`EPERM`) fail. The new path prevents inner Pods from receiving an empty `/proc`, which makes `free`, `top`, and `ps` unusable.
	* sysbox-in-sysbox 218 test: wait for the fresh inner K3s Node and default ServiceAccount before creating the runtime test Pod. A running outer Pod alone races K3s bootstrap and otherwise fails with `serviceaccount "default" not found`.
	* sysbox-runc / command-mode inner K3s: add the dedicated `sysbox-runc-inner` handler. Its wrapper serializes `SYSBOX_SKIP_SPECIAL_MOUNTS` into `initConfig`, avoiding reliance on CRI forwarding arbitrary Pod annotations. In this explicitly experimental nested mode, skip unavailable proc/sys mounts, Sysbox-FS emulated-source bind mounts, sysctls, and read-only mount inspection; each otherwise fails under the outer user namespace.
	* sysbox-deploy-k8s: include a statically linked `fusermount3` in the K3s deploy artifact and require it before command-mode inner Sysbox starts. This fixes Sysbox-in-Sysbox pod pre-registration failing because `sysbox-fs` could not launch its FUSE server when the binary was absent; static linking is required because the K3s image has no dynamic loader.
	* sysbox-runc / sysbox-mgr: add the initial command-mode nested allocator experiment with `1:65535` subuid/subgid ranges. This path is superseded by the explicit `NESTED_IDENTITY` mode above because the reduced mapping could not preserve a complete 65536-ID child user namespace.
	* sysbox-runc: make the dedicated nested handler skip implicit Sysbox special-directory backing mounts when its wrapper environment is active. CRI may drop the matching OCI annotation; without this check sysbox-mgr attempted chowns for `/var/lib/docker` and K3s paths against the parent user namespace and failed with `EINVAL`.
	* sysbox-runc: preserve the nested `exec.fifo` writer briefly after its byte is sent. The specialized no-proc fd path otherwise writes before `runc start` opens its reader, losing the byte and leaving the Pod permanently in `ContainerCreating`.
	* sysbox-runc: for experimental `sysbox/skip-special-mounts: "true"` nesting, omit the inner procfs and sysfs mounts. Both pseudo-filesystem mount paths are rejected with `EPERM` under the nested user namespace; the PoC pause workload does not require either mount.
	* sysbox-runc: skip the parent bind-mount helper's procfs remount for the same experimental nesting annotation. That remount is also denied in the nested user namespace before any bind mount can run.
	* sysbox-runc: skip Sysbox-FS emulated-path overmounts for the nesting annotation because their `/sys` targets are intentionally absent in the pause-only PoC.
	* sysbox-runc: add experimental `sysbox/allow-proc-exec: "true"` opt-in for nested K3s. It removes `noexec` only from that outer Sysbox container's `/proc`, allowing the inner CNI namespace helper to execute `/proc/self/exe`; the default `/proc noexec` isolation remains unchanged. sysbox-deploy-k8s permits the key for both Pod sandbox and workload-container OCI specs.
	* sysbox-runc: add experimental `sysbox/skip-special-mounts: "true"` opt-in for nested Sysbox tests, skipping implicit special-directory backing mounts when ownership cannot be shifted inside a parent user namespace; normal Sysbox containers keep the default mounts.
  * sysbox-deploy-k8s: add experimental inner-K3s command launchers for starting `sysbox-mgr` and `sysbox-fs` directly inside a non-systemd K3s container. The POSIX `sysbox-inner-k3s.sh` launcher is usable by K3s images without Bash, while `--inner-k3s-prepare` and `--inner-k3s-run` retain deploy-image compatibility. The deploy image now includes static `rsync`, which `sysbox-mgr` requires in a minimal K3s image. This lets CKM test an inner Sysbox runtime without installing Sysbox on the outer node.
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
