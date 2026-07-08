package admission

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type certificateLifecycleManager interface {
	Ensure(ctx context.Context) (LifecycleResult, error)
}

type CertificateRotationLoop struct {
	manager      certificateLifecycleManager
	reloader     *CertificateReloader
	interval     time.Duration
	errorHandler func(error)
}

func NewCertificateRotationLoop(manager *LifecycleManager, reloader *CertificateReloader, interval time.Duration) *CertificateRotationLoop {
	return newCertificateRotationLoop(manager, reloader, interval)
}

func newCertificateRotationLoop(manager certificateLifecycleManager, reloader *CertificateReloader, interval time.Duration) *CertificateRotationLoop {
	return &CertificateRotationLoop{
		manager:      manager,
		reloader:     reloader,
		interval:     interval,
		errorHandler: func(error) {},
	}
}

func (l *CertificateRotationLoop) SetErrorHandler(handler func(error)) {
	l.errorHandler = handler
}

func (l *CertificateRotationLoop) Run(ctx context.Context) error {
	if l.interval <= 0 {
		return fmt.Errorf("certificate rotation interval must be positive")
	}
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	return l.run(ctx, ticker.C)
}

func (l *CertificateRotationLoop) run(ctx context.Context, ticks <-chan time.Time) error {
	l.ensureAndReload(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			l.ensureAndReload(ctx)
		}
	}
}

func (l *CertificateRotationLoop) ensureAndReload(ctx context.Context) {
	result, err := l.manager.Ensure(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			l.errorHandler(fmt.Errorf("ensure admission certificate lifecycle: %w", err))
		}
		return
	}
	l.reloader.SetCertificate(result.TLSCertificate)
}
