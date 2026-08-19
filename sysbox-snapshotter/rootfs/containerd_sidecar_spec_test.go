package rootfs

import (
	"errors"
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestUniqueSidecarSpec(t *testing.T) {
	spec := &runtimespec.Spec{}
	got, err := uniqueSidecarSpec([]*runtimespec.Spec{spec})
	if err != nil {
		t.Fatalf("uniqueSidecarSpec() error = %v", err)
	}
	if got != spec {
		t.Fatalf("uniqueSidecarSpec() returned a different spec")
	}
}

func TestUniqueSidecarSpecRejectsAmbiguousCandidates(t *testing.T) {
	_, err := uniqueSidecarSpec([]*runtimespec.Spec{{}, {}})
	if !errors.Is(err, ErrSidecarSpecAmbiguous) {
		t.Fatalf("uniqueSidecarSpec() error = %v, want %v", err, ErrSidecarSpecAmbiguous)
	}
}

func TestSelectSidecarSpecPrefersRunningCandidate(t *testing.T) {
	stale := &runtimespec.Spec{}
	running := &runtimespec.Spec{}
	got, err := selectSidecarSpec(
		[]*runtimespec.Spec{stale, running},
		[]*runtimespec.Spec{running},
	)
	if err != nil {
		t.Fatalf("selectSidecarSpec() error = %v", err)
	}
	if got != running {
		t.Fatalf("selectSidecarSpec() did not select the running spec")
	}
}
