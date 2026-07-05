//go:build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package overlay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/log"
)

// SnapshotterConfig is used to configure the overlay snapshotter instance
type SnapshotterConfig struct {
	asyncRemove  bool
	ms           MetaStore
	mountOptions []string
	remapMode    remapMode
	rootfsHooks  RootfsHooks
}

// Opt is an option to configure the overlay snapshotter
type Opt func(config *SnapshotterConfig) error

// AsynchronousRemove defers removal of filesystem content until
// the Cleanup method is called. Removals will make the snapshot
// referred to by the key unavailable and make the key immediately
// available for re-use.
func AsynchronousRemove(config *SnapshotterConfig) error {
	config.asyncRemove = true
	return nil
}

// WithMountOptions defines the default mount options used for the overlay mount.
// NOTE: Options are not applied to bind mounts.
func WithMountOptions(options []string) Opt {
	return func(config *SnapshotterConfig) error {
		config.mountOptions = append(config.mountOptions, options...)
		return nil
	}
}

type MetaStore interface {
	TransactionContext(ctx context.Context, writable bool) (context.Context, storage.Transactor, error)
	WithTransaction(ctx context.Context, writable bool, fn storage.TransactionCallback) error
	Close() error
}

func WithRemapIDs(config *SnapshotterConfig) error {
	config.remapMode = remapModeIDMapped
	return nil
}

func HasRemapIDs(sn snapshots.Snapshotter) bool {
	return remapModeOf(sn) == remapModeIDMapped
}

type snapshotter struct {
	root        string
	ms          MetaStore
	asyncRemove bool
	options     []string
	remapMode   remapMode
	rootfsHooks RootfsHooks
}

// NewSnapshotter returns a Snapshotter which uses overlayfs. The overlayfs
// diffs are stored under the provided root. A metadata file is stored under
// the root.
func NewSnapshotter(root string, opts ...Opt) (snapshots.Snapshotter, error) {
	var config SnapshotterConfig
	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	supportsDType, err := fs.SupportsDType(root)
	if err != nil {
		return nil, err
	}
	if !supportsDType {
		return nil, fmt.Errorf("%s does not support d_type. If the backing filesystem is xfs, please reformat with ftype=1 to enable d_type support", root)
	}
	if config.ms == nil {
		config.ms, err = storage.NewMetaStore(filepath.Join(root, "metadata.db"))
		if err != nil {
			return nil, err
		}
	}

	if err := os.Mkdir(filepath.Join(root, "snapshots"), 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	if !hasOption(config.mountOptions, "userxattr", false) {
		// figure out whether "userxattr" option is recognized by the kernel && needed
		userxattr, err := overlayutils.NeedsUserXAttr(root)
		if err != nil {
			log.L.WithError(err).Warnf("cannot detect whether \"userxattr\" option needs to be used, assuming to be %v", userxattr)
		}
		if userxattr {
			config.mountOptions = append(config.mountOptions, "userxattr")
		}
	}

	if !hasOption(config.mountOptions, "index", false) && supportsIndex() {
		config.mountOptions = append(config.mountOptions, "index=off")
	}

	return &snapshotter{
		root:        root,
		ms:          config.ms,
		asyncRemove: config.asyncRemove,
		options:     config.mountOptions,
		remapMode:   config.remapMode,
		rootfsHooks: config.rootfsHooks,
	}, nil
}

func hasOption(options []string, key string, hasValue bool) bool {
	for _, option := range options {
		if hasValue {
			if strings.HasPrefix(option, key) && len(option) > len(key) && option[len(key)] == '=' {
				return true
			}
		} else if option == key {
			return true
		}
	}
	return false
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (o *snapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	}); err != nil {
		return info, err
	}

	return info, nil
}

func (o *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (newInfo snapshots.Info, err error) {
	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		newInfo, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		if err != nil {
			return err
		}

		return nil
	})
	return newInfo, err
}

// Usage returns the resources taken by the snapshot identified by key.
//
// For active snapshots, this will scan the usage of the overlay "diff" (aka
// "upper") directory and may take some time.
//
// For committed snapshots, the value is returned from the metadata database.
func (o *snapshotter) Usage(ctx context.Context, key string) (_ snapshots.Usage, err error) {
	var (
		usage snapshots.Usage
		info  snapshots.Info
		id    string
	)
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, usage, err = storage.GetInfo(ctx, key)
		return err
	}); err != nil {
		return usage, err
	}

	if info.Kind == snapshots.KindActive {
		upperPath := o.upperPath(id)
		du, err := fs.DiskUsage(ctx, upperPath)
		if err != nil {
			// TODO(stevvooe): Consider not reporting an error in this case.
			return snapshots.Usage{}, err
		}
		usage = snapshots.Usage(du)
	}
	return usage, nil
}

func (o *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	logSnapshotLifecycle(ctx, "prepare_start", logSnapshotRequest(snapshots.KindActive, key, parent))
	mounts, err := o.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
	logSnapshotLifecycle(ctx, "prepare_done", logSnapshotResult(snapshots.KindActive, key, parent, mounts, err))
	return mounts, err
}

func (o *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	logSnapshotLifecycle(ctx, "view_start", logSnapshotRequest(snapshots.KindView, key, parent))
	mounts, err := o.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
	logSnapshotLifecycle(ctx, "view_done", logSnapshotResult(snapshots.KindView, key, parent, mounts, err))
	return mounts, err
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (o *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	var s storage.Snapshot
	var info snapshots.Info
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		s, err = storage.GetSnapshot(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get active mount: %w", err)
		}

		_, info, _, err = storage.GetInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot info: %w", err)
		}
		return nil
	}); err != nil {
		logSnapshotLifecycle(ctx, "mounts_error", logSnapshotError(key, err))
		return nil, err
	}
	mounts, err := o.mounts(s, info)
	if err != nil {
		return nil, fmt.Errorf("failed to build snapshot mounts: %w", err)
	}
	logSnapshotLifecycle(ctx, "mounts_base", logMounts(key, s, info, mounts))
	if info.Kind != snapshots.KindActive {
		logSnapshotLifecycle(ctx, "mounts_done", logMountResult(key, mounts, nil))
		return mounts, nil
	}
	rewritten, err := applyRootfsHook(ctx, o.rootfsHooks, key, info.Labels, mounts)
	logSnapshotLifecycle(ctx, "mounts_done", logMountResult(key, rewritten, err))
	return rewritten, err
}

func (o *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	logSnapshotLifecycle(ctx, "commit_start", logCommitRequest(name, key))
	err := o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		// grab the existing id
		id, _, _, err := storage.GetInfo(ctx, key)
		if err != nil {
			return err
		}

		usage, err := fs.DiskUsage(ctx, o.upperPath(id))
		if err != nil {
			return err
		}

		if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot %s: %w", key, err)
		}
		return nil
	})
	logSnapshotLifecycle(ctx, "commit_done", logCommitResult(name, key, err))
	return err
}

// Remove abandons the snapshot identified by key. The snapshot will
// immediately become unavailable and unrecoverable. Disk space will
// be freed up on the next call to `Cleanup`.
func (o *snapshotter) Remove(ctx context.Context, key string) (err error) {
	var removals []string
	// Remove directories after the transaction is closed, failures must not
	// return error since the transaction is committed with the removal
	// key no longer available.
	defer func() {
		if err == nil {
			for _, dir := range removals {
				if err := os.RemoveAll(dir); err != nil {
					log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
				}
			}
		}
	}()
	logSnapshotLifecycle(ctx, "remove_start", logSnapshotKey(key))
	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, _, err = storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove snapshot %s: %w", key, err)
		}

		if !o.asyncRemove {
			removals, err = o.getCleanupDirectories(ctx)
			if err != nil {
				return fmt.Errorf("unable to get directories for removal: %w", err)
			}
		}
		return nil
	})
	logSnapshotLifecycle(ctx, "remove_done", logRemoveResult(key, removals, err))
	return err
}

// Walk the snapshots.
func (o *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

// Cleanup cleans up disk resources from removed or abandoned snapshots
func (o *snapshotter) Cleanup(ctx context.Context) error {
	cleanup, err := o.cleanupDirectories(ctx)
	if err != nil {
		return err
	}

	for _, dir := range cleanup {
		if err := os.RemoveAll(dir); err != nil {
			log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
		}
	}

	return nil
}

func (o *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var (
		s        storage.Snapshot
		td, path string
		info     snapshots.Info
	)

	defer func() {
		if err != nil {
			if td != "" {
				if err1 := os.RemoveAll(td); err1 != nil {
					log.G(ctx).WithError(err1).Warn("failed to cleanup temp snapshot directory")
				}
			}
			if path != "" {
				if err1 := os.RemoveAll(path); err1 != nil {
					log.G(ctx).WithError(err1).WithField("path", path).Error("failed to reclaim snapshot directory, directory may need removal")
					err = fmt.Errorf("failed to remove path: %v: %w", err1, err)
				}
			}
		}
	}()

	if err := o.ms.WithTransaction(ctx, true, func(ctx context.Context) (err error) {
		snapshotDir := filepath.Join(o.root, "snapshots")
		td, err = o.prepareDirectory(snapshotDir, kind)
		if err != nil {
			return fmt.Errorf("failed to create prepare snapshot dir: %w", err)
		}

		s, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		_, info, _, err = storage.GetInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot info: %w", err)
		}
		if _, err := remapOptions(o.remapMode, info.Labels); err != nil {
			return fmt.Errorf("failed to validate snapshot remap labels: %w", err)
		}

		mappedUID, mappedGID := -1, -1
		if owner, ok, err := fallbackChownOwner(o.remapMode, info.Labels); err != nil {
			return err
		} else if ok {
			mappedUID, mappedGID = owner.uid, owner.gid
		} else if owner, ok, err := idmappedSnapshotOwner(o.remapMode, info.Labels); err != nil {
			return err
		} else if ok {
			mappedUID, mappedGID = owner.uid, owner.gid
		}

		if mappedUID == -1 || mappedGID == -1 {
			if len(s.ParentIDs) > 0 {
				st, err := os.Stat(o.upperPath(s.ParentIDs[0]))
				if err != nil {
					return fmt.Errorf("failed to stat parent: %w", err)
				}
				stat, ok := st.Sys().(*syscall.Stat_t)
				if !ok {
					return fmt.Errorf("incompatible types after stat call: *syscall.Stat_t expected")
				}
				mappedUID = int(stat.Uid)
				mappedGID = int(stat.Gid)
			}
		}

		if mappedUID != -1 && mappedGID != -1 {
			if err := os.Lchown(filepath.Join(td, "fs"), mappedUID, mappedGID); err != nil {
				return fmt.Errorf("failed to chown: %w", err)
			}
			if kind == snapshots.KindActive {
				if err := os.Lchown(filepath.Join(td, "work"), mappedUID, mappedGID); err != nil {
					return fmt.Errorf("failed to chown workdir: %w", err)
				}
			}
		}

		path = filepath.Join(snapshotDir, s.ID)
		if err = os.Rename(td, path); err != nil {
			return fmt.Errorf("failed to rename: %w", err)
		}
		if mappedUID != -1 && mappedGID != -1 {
			if err := os.Lchown(filepath.Join(path, "fs"), mappedUID, mappedGID); err != nil {
				return fmt.Errorf("failed to chown final fs: %w", err)
			}
			if kind == snapshots.KindActive {
				if err := os.Lchown(filepath.Join(path, "work"), mappedUID, mappedGID); err != nil {
					return fmt.Errorf("failed to chown final workdir: %w", err)
				}
			}
		}
		td = ""

		return nil
	}); err != nil {
		logSnapshotLifecycle(ctx, "create_error", logCreateSnapshotError(kind, key, parent, err))
		return nil, err
	}
	mounts, err := o.mounts(s, info)
	if err != nil {
		logSnapshotLifecycle(ctx, "create_error", logCreateSnapshotError(kind, key, parent, err))
		return nil, fmt.Errorf("failed to build snapshot mounts: %w", err)
	}
	if kind == snapshots.KindActive {
		mounts, err = applyRootfsHook(ctx, o.rootfsHooks, key, info.Labels, mounts)
		if err != nil {
			logSnapshotLifecycle(ctx, "create_error", logCreateSnapshotError(kind, key, parent, err))
			if removeErr := o.Remove(ctx, key); removeErr != nil {
				return nil, fmt.Errorf("failed to remove snapshot after rootfs hook error: %v: %w", removeErr, err)
			}
			return nil, fmt.Errorf("failed to apply rootfs hook: %w", err)
		}
	}
	logSnapshotLifecycle(ctx, "create_done", logCreateSnapshotDone(kind, key, parent, s, info, path, mounts))
	return mounts, nil
}

func (o *snapshotter) prepareDirectory(snapshotDir string, kind snapshots.Kind) (string, error) {
	td, err := os.MkdirTemp(snapshotDir, "new-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	if err := os.Mkdir(filepath.Join(td, "fs"), 0755); err != nil {
		return td, err
	}

	if kind == snapshots.KindActive {
		if err := os.Mkdir(filepath.Join(td, "work"), 0711); err != nil {
			return td, err
		}
	}

	return td, nil
}

// Close closes the snapshotter
func (o *snapshotter) Close() error {
	return o.ms.Close()
}

// supportsIndex checks whether the "index=off" option is supported by the kernel.
func supportsIndex() bool {
	if _, err := os.Stat("/sys/module/overlay/parameters/index"); err == nil {
		return true
	}
	return false
}
