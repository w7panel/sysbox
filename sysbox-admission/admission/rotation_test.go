package admission

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRotationLoop_run_ensuresImmediatelyAndReloadsCertificate(t *testing.T) {
	// Given: a rotation loop with one successful lifecycle result and no ticks.
	ctx, cancel := context.WithCancel(context.Background())
	initialCertificate := mustGenerateTLSCertificate(t)
	refreshedCertificate := mustGenerateTLSCertificate(t)
	reloader := NewCertificateReloader(initialCertificate)
	manager := &recordingLifecycleManager{
		results: []LifecycleResult{{TLSCertificate: refreshedCertificate}},
		calls:   make(chan struct{}, 8),
	}
	ticks := make(chan time.Time)
	loop := newCertificateRotationLoop(manager, reloader, time.Hour)

	// When: the loop starts and the first Ensure completes.
	done := make(chan error, 1)
	go func() {
		done <- loop.run(ctx, ticks)
	}()
	manager.waitForCalls(t, 1)
	cancel()

	// Then: the first certificate is published before the loop exits on cancellation.
	require.NoError(t, waitForRotationDone(t, done))
	require.Equal(t, 1, manager.callCount())
	got, err := reloader.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	require.Equal(t, refreshedCertificate.Certificate, got.Certificate)
}

func TestRotationLoop_Run_returnsError_whenIntervalIsNotPositive(t *testing.T) {
	// Given: a rotation loop configured with a non-positive interval.
	loop := newCertificateRotationLoop(&recordingLifecycleManager{}, NewEmptyCertificateReloader(), 0)

	// When: the loop starts.
	err := loop.Run(context.Background())

	// Then: the loop rejects the interval instead of panicking in time.NewTicker.
	require.ErrorContains(t, err, "certificate rotation interval must be positive")
}

func TestRotationLoop_run_ensuresAndReloadsCertificateOnEachTick(t *testing.T) {
	// Given: a rotation loop with deterministic ticks and two lifecycle results.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initialCertificate := mustGenerateTLSCertificate(t)
	firstCertificate := mustGenerateTLSCertificate(t)
	secondCertificate := mustGenerateTLSCertificate(t)
	reloader := NewCertificateReloader(initialCertificate)
	manager := &recordingLifecycleManager{
		results: []LifecycleResult{
			{TLSCertificate: firstCertificate},
			{TLSCertificate: secondCertificate},
		},
		calls: make(chan struct{}, 8),
	}
	ticks := make(chan time.Time)
	loop := newCertificateRotationLoop(manager, reloader, time.Hour)
	done := make(chan error, 1)

	// When: the loop starts and receives one tick.
	go func() {
		done <- loop.run(ctx, ticks)
	}()
	manager.waitForCalls(t, 1)
	ticks <- time.Unix(1, 0)
	manager.waitForCalls(t, 2)
	cancel()

	// Then: the certificate from the tick reconciliation is published.
	require.NoError(t, waitForRotationDone(t, done))
	require.Equal(t, 2, manager.callCount())
	got, err := reloader.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	require.Equal(t, secondCertificate.Certificate, got.Certificate)
}

func TestRotationLoop_run_retriesAfterInitialEnsureError(t *testing.T) {
	// Given: the initial lifecycle reconciliation fails before a later tick succeeds.
	ctx, cancel := context.WithCancel(context.Background())
	wantErr := errors.New("ensure failed")
	initialCertificate := mustGenerateTLSCertificate(t)
	refreshedCertificate := mustGenerateTLSCertificate(t)
	reloader := NewCertificateReloader(initialCertificate)
	manager := &recordingLifecycleManager{
		results: []LifecycleResult{
			{},
			{TLSCertificate: refreshedCertificate},
		},
		errors: []error{wantErr},
		calls:  make(chan struct{}, 8),
	}
	ticks := make(chan time.Time)
	loop := newCertificateRotationLoop(manager, reloader, time.Hour)
	errorsSeen := make(chan error, 1)
	loop.SetErrorHandler(func(err error) { errorsSeen <- err })
	done := make(chan error, 1)

	// When: the loop starts, reports the first error, then receives a retry tick.
	go func() {
		done <- loop.run(ctx, ticks)
	}()
	manager.waitForCalls(t, 1)
	require.ErrorIs(t, receiveRotationError(t, errorsSeen), wantErr)
	ticks <- time.Unix(2, 0)
	manager.waitForCalls(t, 2)
	cancel()

	// Then: the loop exits only after cancellation and publishes the later certificate.
	require.NoError(t, waitForRotationDone(t, done))
	got, err := reloader.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	require.Equal(t, refreshedCertificate.Certificate, got.Certificate)
}

func TestRotationLoop_run_retriesAfterTickEnsureError(t *testing.T) {
	// Given: the initial lifecycle reconciliation succeeds, one tick fails, and a later tick succeeds.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wantErr := errors.New("tick ensure failed")
	initialCertificate := mustGenerateTLSCertificate(t)
	firstCertificate := mustGenerateTLSCertificate(t)
	secondCertificate := mustGenerateTLSCertificate(t)
	reloader := NewCertificateReloader(initialCertificate)
	manager := &recordingLifecycleManager{
		results: []LifecycleResult{
			{TLSCertificate: firstCertificate},
			{},
			{TLSCertificate: secondCertificate},
		},
		errors: []error{nil, wantErr},
		calls:  make(chan struct{}, 8),
	}
	ticks := make(chan time.Time)
	loop := newCertificateRotationLoop(manager, reloader, time.Hour)
	errorsSeen := make(chan error, 1)
	loop.SetErrorHandler(func(err error) { errorsSeen <- err })
	done := make(chan error, 1)

	// When: the loop receives one failing tick followed by one successful tick.
	go func() {
		done <- loop.run(ctx, ticks)
	}()
	manager.waitForCalls(t, 1)
	ticks <- time.Unix(3, 0)
	manager.waitForCalls(t, 2)
	require.ErrorIs(t, receiveRotationError(t, errorsSeen), wantErr)
	ticks <- time.Unix(4, 0)
	manager.waitForCalls(t, 3)
	cancel()

	// Then: the loop keeps running after the tick error and publishes the later certificate.
	require.NoError(t, waitForRotationDone(t, done))
	got, err := reloader.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	require.Equal(t, secondCertificate.Certificate, got.Certificate)
}

type recordingLifecycleManager struct {
	mu      sync.Mutex
	results []LifecycleResult
	errors  []error
	count   int
	calls   chan struct{}
}

func (m *recordingLifecycleManager) Ensure(ctx context.Context) (LifecycleResult, error) {
	select {
	case <-ctx.Done():
		return LifecycleResult{}, ctx.Err()
	default:
	}
	if m.calls == nil {
		m.calls = make(chan struct{}, 8)
	}
	m.mu.Lock()
	callIndex := m.count
	m.count++
	m.mu.Unlock()
	m.calls <- struct{}{}
	if callIndex < len(m.errors) && m.errors[callIndex] != nil {
		return LifecycleResult{}, m.errors[callIndex]
	}
	if callIndex < len(m.results) {
		return m.results[callIndex], nil
	}
	return LifecycleResult{}, nil
}

func (m *recordingLifecycleManager) waitForCalls(t *testing.T, want int) {
	t.Helper()
	for m.callCount() < want {
		select {
		case <-m.calls:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for %d Ensure calls", want)
		}
	}
}

func (m *recordingLifecycleManager) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

func waitForRotationDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for rotation loop to stop")
		return context.DeadlineExceeded
	}
}

func receiveRotationError(t *testing.T, errorsSeen <-chan error) error {
	t.Helper()
	select {
	case err := <-errorsSeen:
		return err
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for rotation error")
		return context.DeadlineExceeded
	}
}
