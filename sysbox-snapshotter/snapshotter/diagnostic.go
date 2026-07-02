package overlay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/log"
	"github.com/sirupsen/logrus"
)

const snapshotDiagnosticMessage = "sysbox snapshotter diagnostic"

func logSnapshotLifecycle(ctx context.Context, event string, fields logrus.Fields) {
	fields["event"] = event
	log.G(ctx).WithFields(fields).Info(snapshotDiagnosticMessage)
}

func logSnapshotRequest(kind snapshots.Kind, key string, parent string) logrus.Fields {
	return logrus.Fields{
		"kind":   kind.String(),
		"key":    key,
		"parent": parent,
	}
}

func logSnapshotResult(kind snapshots.Kind, key string, parent string, mounts []mount.Mount, err error) logrus.Fields {
	fields := logSnapshotRequest(kind, key, parent)
	fields["mounts"] = summarizeMounts(mounts)
	if err != nil {
		fields["err"] = err.Error()
	}
	return fields
}

func logSnapshotError(key string, err error) logrus.Fields {
	fields := logSnapshotKey(key)
	fields["err"] = err.Error()
	return fields
}

func logSnapshotKey(key string) logrus.Fields {
	return logrus.Fields{"key": key}
}

func logMounts(key string, snapshot storage.Snapshot, info snapshots.Info, mounts []mount.Mount) logrus.Fields {
	return logrus.Fields{
		"key":        key,
		"id":         snapshot.ID,
		"kind":       snapshot.Kind.String(),
		"parent_ids": snapshot.ParentIDs,
		"labels":     info.Labels,
		"mounts":     summarizeMounts(mounts),
	}
}

func logMountResult(key string, mounts []mount.Mount, err error) logrus.Fields {
	fields := logrus.Fields{
		"key":    key,
		"mounts": summarizeMounts(mounts),
	}
	if err != nil {
		fields["err"] = err.Error()
	}
	return fields
}

func logCommitRequest(name string, key string) logrus.Fields {
	return logrus.Fields{
		"name": name,
		"key":  key,
	}
}

func logCommitResult(name string, key string, err error) logrus.Fields {
	fields := logCommitRequest(name, key)
	if err != nil {
		fields["err"] = err.Error()
	}
	return fields
}

func logRemoveResult(key string, removals []string, err error) logrus.Fields {
	fields := logSnapshotKey(key)
	fields["removals"] = removals
	if err != nil {
		fields["err"] = err.Error()
	}
	return fields
}

func logCreateSnapshotError(kind snapshots.Kind, key string, parent string, err error) logrus.Fields {
	fields := logSnapshotRequest(kind, key, parent)
	fields["err"] = err.Error()
	return fields
}

func logCreateSnapshotDone(kind snapshots.Kind, key string, parent string, snapshot storage.Snapshot, info snapshots.Info, path string, mounts []mount.Mount) logrus.Fields {
	return logrus.Fields{
		"kind":       kind.String(),
		"key":        key,
		"parent":     parent,
		"id":         snapshot.ID,
		"parent_ids": snapshot.ParentIDs,
		"labels":     info.Labels,
		"path":       path,
		"fs_stat":    summarizePath(filepath.Join(path, "fs")),
		"work_stat":  summarizePath(filepath.Join(path, "work")),
		"mounts":     summarizeMounts(mounts),
	}
}

func summarizeMounts(mounts []mount.Mount) []string {
	summaries := make([]string, 0, len(mounts))
	for _, item := range mounts {
		summaries = append(summaries, fmt.Sprintf("type=%s source=%s target=%s options=%v", item.Type, item.Source, item.Target, item.Options))
	}
	return summaries
}

func summarizePath(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("%s stat_error=%v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Sprintf("%s mode=%s", path, info.Mode())
	}
	return fmt.Sprintf("%s uid=%d gid=%d mode=%s", path, stat.Uid, stat.Gid, info.Mode())
}
