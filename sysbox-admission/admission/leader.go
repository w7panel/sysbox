package admission

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

type LeaderElectionConfig struct {
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

func DefaultLeaderElectionConfig() LeaderElectionConfig {
	return LeaderElectionConfig{
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	}
}

func NewLeaseLock(leases coordinationv1client.LeasesGetter, config LifecycleConfig, identity string) *resourcelock.LeaseLock {
	return &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Namespace: config.Namespace,
			Name:      config.LeaseName,
		},
		Client: leases,
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}
}

// RunLeaderElection releases leadership when ctx is cancelled; OnStartedLeading must return when its callback context is cancelled so protected work stops before the Lease is released.
func RunLeaderElection(ctx context.Context, lock resourcelock.Interface, config LeaderElectionConfig, callbacks leaderelection.LeaderCallbacks) error {
	if callbacks.OnStoppedLeading == nil {
		callbacks.OnStoppedLeading = func() {}
	}
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   config.LeaseDuration,
		RenewDeadline:   config.RenewDeadline,
		RetryPeriod:     config.RetryPeriod,
		Callbacks:       callbacks,
		ReleaseOnCancel: true,
		Name:            lock.Describe(),
	})
	if err != nil {
		return fmt.Errorf("create leader elector: %w", err)
	}
	elector.Run(ctx)
	return nil
}
