//go:build linux

package overlay

import (
	"reflect"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestRewriteOverlayMounts_rewrites_only_overlay_upper_and_work_dirs(t *testing.T) {
	// Given
	mounts := []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: []string{"lowerdir=/lower", "upperdir=/old-upper", "workdir=/old-work", "uidmap=0:100000:65536"},
		},
		{
			Type:    "bind",
			Source:  "/bind-source",
			Options: []string{"upperdir=/bind-upper", "workdir=/bind-work", "rbind"},
		},
	}

	// When
	got := rewriteOverlayMounts(mounts, "/pvc/upper", "/pvc/work")

	// Then
	want := []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: []string{"lowerdir=/lower", "upperdir=/pvc/upper", "workdir=/pvc/work", "uidmap=0:100000:65536"},
		},
		{
			Type:    "bind",
			Source:  "/bind-source",
			Options: []string{"upperdir=/bind-upper", "workdir=/bind-work", "rbind"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected rewritten mounts %#v, got %#v", want, got)
	}
	if reflect.DeepEqual(mounts[0].Options, got[0].Options) {
		t.Fatalf("expected rewritten mount options to differ from original")
	}
}

func TestRewriteOverlayMounts_preserves_options_that_only_prefix_match(t *testing.T) {
	// Given
	mounts := []mount.Mount{{
		Type:    "overlay",
		Source:  "overlay",
		Options: []string{"upperdir", "workdir", "upperdirx=/old-upper", "workdirx=/old-work"},
	}}

	// When
	got := rewriteOverlayMounts(mounts, "/pvc/upper", "/pvc/work")

	// Then
	if !reflect.DeepEqual(got, mounts) {
		t.Fatalf("expected prefix-only options to be preserved, got %#v", got)
	}
}
