package admission

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/leaderelection"
)

func TestNewLeaseLock_usesLifecycleNamespaceLeaseNameAndIdentity(t *testing.T) {
	// Given: lifecycle ownership names and a process identity.
	client := fake.NewSimpleClientset()
	config := LifecycleConfig{Namespace: "sysbox-system", LeaseName: "sysbox-admission-lifecycle"}

	// When: a LeaseLock is created for leader election.
	lock := NewLeaseLock(client.CoordinationV1(), config, "pod-a")

	// Then: client-go will coordinate through that lifecycle Lease and holder identity.
	require.Equal(t, "sysbox-system", lock.LeaseMeta.Namespace)
	require.Equal(t, "sysbox-admission-lifecycle", lock.LeaseMeta.Name)
	require.Equal(t, "pod-a", lock.LockConfig.Identity)
}

func TestRunLeaderElection_runsLeaderCallbackForSingleCandidateAndReturnsOnContextCancel(t *testing.T) {
	// Given: a single leader-election candidate with bounded test lifetime.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := fake.NewSimpleClientset()
	lock := NewLeaseLock(client.CoordinationV1(), LifecycleConfig{Namespace: "sysbox-system", LeaseName: "sysbox-admission-lifecycle"}, "pod-a")
	started := make(chan struct{})
	done := make(chan error, 1)
	leaderConfig := LeaderElectionConfig{
		LeaseDuration: 200 * time.Millisecond,
		RenewDeadline: 100 * time.Millisecond,
		RetryPeriod:   20 * time.Millisecond,
	}
	callbacks := leaderelection.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			close(started)
			<-ctx.Done()
		},
	}

	// When: leader election runs and the test cancels after leadership starts.
	go func() {
		done <- RunLeaderElection(ctx, lock, leaderConfig, callbacks)
	}()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Fatalf("leader callback did not run: %v", ctx.Err())
	}

	// Then: RunLeaderElection returns after context cancellation.
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("leader election did not return after context cancellation")
	}
}

func TestRunLeaderElection_cancelsLeaderCallbackBeforeReturningOnContextCancel(t *testing.T) {
	// Given: a leader callback that reports when its callback context is cancelled and exited.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := fake.NewSimpleClientset()
	lock := NewLeaseLock(client.CoordinationV1(), LifecycleConfig{Namespace: "sysbox-system", LeaseName: "sysbox-admission-lifecycle"}, "pod-a")
	leaderConfig := LeaderElectionConfig{
		LeaseDuration: 200 * time.Millisecond,
		RenewDeadline: 100 * time.Millisecond,
		RetryPeriod:   20 * time.Millisecond,
	}
	started := make(chan struct{})
	callbackCancelled := make(chan struct{})
	callbackExited := make(chan struct{})
	done := make(chan error, 1)
	callbacks := leaderelection.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			close(callbackCancelled)
			close(callbackExited)
		},
	}

	// When: the parent context is cancelled after leadership starts.
	go func() {
		done <- RunLeaderElection(ctx, lock, leaderConfig, callbacks)
	}()
	waitForSignal(t, ctx, started, "leader callback to start")
	cancel()

	// Then: callback context cancellation is observed and callback exits before RunLeaderElection returns.
	waitForSignalWithin(t, callbackCancelled, "callback context cancellation")
	waitForSignalWithin(t, callbackExited, "leader callback to exit")
	require.NoError(t, waitForElectionDoneWithin(t, done, "leader election to stop"))
}

func waitForSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %s: %v", description, ctx.Err())
	}
}

func waitForSignalWithin(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForElectionDoneWithin(t *testing.T, done <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for %s", description)
		return context.DeadlineExceeded
	}
}
