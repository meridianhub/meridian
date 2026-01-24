// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/env"
)

// Default lease renewal configuration.
const (
	// DefaultRenewalInterval is the default interval between lease renewals.
	// Must be less than the lease duration (default 5 minutes) to prevent expiry.
	DefaultRenewalInterval = 2 * time.Minute
)

// LeaseRenewalConfig holds configuration for lease renewal.
type LeaseRenewalConfig struct {
	// RenewalInterval is how often to renew the lease.
	// Default: 2 minutes (SAGA_LEASE_RENEWAL_INTERVAL)
	RenewalInterval time.Duration
}

// NewLeaseRenewalConfig creates a LeaseRenewalConfig populated from environment variables.
// Environment variables:
//   - SAGA_LEASE_RENEWAL_INTERVAL: Duration string (e.g., "2m", "90s"). Default: "2m"
func NewLeaseRenewalConfig() *LeaseRenewalConfig {
	return &LeaseRenewalConfig{
		RenewalInterval: env.GetEnvAsDuration("SAGA_LEASE_RENEWAL_INTERVAL", DefaultRenewalInterval),
	}
}

// LeaseRenewer periodically renews the lease on a saga to prevent it from being
// claimed by another pod while execution is in progress.
//
// The renewer runs a background goroutine that calls RenewLease on the ClaimService
// at regular intervals. It supports graceful shutdown via context cancellation or
// explicit Stop() call.
//
// Example usage:
//
//	renewer := saga.NewLeaseRenewer(sagaID, claimService, logger)
//	renewer.Start(ctx)
//	defer renewer.Stop()
//	// ... saga execution ...
type LeaseRenewer struct {
	sagaID       uuid.UUID
	claimService *ClaimService
	logger       *slog.Logger

	renewalInterval time.Duration
	callback        func() // optional callback after each renewal attempt (for testing)

	mu       sync.Mutex
	running  bool
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// LeaseRenewerOption is a functional option for configuring a LeaseRenewer.
type LeaseRenewerOption func(*LeaseRenewer)

// WithRenewalInterval sets the interval between lease renewals.
// Default: 2 minutes (must be less than lease duration to prevent expiry)
func WithRenewalInterval(interval time.Duration) LeaseRenewerOption {
	return func(r *LeaseRenewer) {
		if interval > 0 {
			r.renewalInterval = interval
		}
	}
}

// WithRenewalCallback sets a callback function that is called after each renewal attempt.
// Useful for testing to track renewal frequency.
func WithRenewalCallback(callback func()) LeaseRenewerOption {
	return func(r *LeaseRenewer) {
		r.callback = callback
	}
}

// NewLeaseRenewer creates a new LeaseRenewer for the specified saga.
//
// Parameters:
//   - sagaID: The UUID of the saga whose lease should be renewed
//   - claimService: The ClaimService to use for renewal (must have correct PodID)
//   - logger: Structured logger (uses slog.Default() if nil)
//   - opts: Optional functional options for configuration
func NewLeaseRenewer(sagaID uuid.UUID, claimService *ClaimService, logger *slog.Logger, opts ...LeaseRenewerOption) *LeaseRenewer {
	if logger == nil {
		logger = slog.Default()
	}

	r := &LeaseRenewer{
		sagaID:          sagaID,
		claimService:    claimService,
		logger:          logger.With("saga_id", sagaID.String()),
		renewalInterval: DefaultRenewalInterval,
		done:            make(chan struct{}),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Start begins the background lease renewal goroutine.
// The renewer will continue until the context is cancelled or Stop() is called.
//
// It is safe to call Start multiple times, but only the first call will have effect.
// Subsequent calls while the renewer is running will be ignored.
func (r *LeaseRenewer) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		r.logger.Debug("lease renewer already running, ignoring Start() call")
		return
	}

	r.running = true
	r.wg.Add(1)

	go r.run(ctx)

	r.logger.Info("lease renewer started",
		"renewal_interval", r.renewalInterval)
}

// Stop signals the renewer to stop and waits for the goroutine to exit.
// It is safe to call Stop multiple times; subsequent calls will block until
// the first Stop completes.
func (r *LeaseRenewer) Stop() {
	r.stopOnce.Do(func() {
		r.logger.Debug("stopping lease renewer")
		close(r.done)
	})

	r.wg.Wait()

	r.mu.Lock()
	r.running = false
	r.mu.Unlock()

	r.logger.Info("lease renewer stopped")
}

// run is the main renewal loop that periodically calls RenewLease.
func (r *LeaseRenewer) run(ctx context.Context) {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		r.wg.Done()
	}()

	ticker := time.NewTicker(r.renewalInterval)
	defer ticker.Stop()

	r.logger.Debug("lease renewer loop started")

	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("lease renewer stopping due to context cancellation")
			return
		case <-r.done:
			r.logger.Debug("lease renewer stopping due to Stop() call")
			return
		case <-ticker.C:
			r.renewLease(ctx)
		}
	}
}

// renewLease performs a single lease renewal operation.
func (r *LeaseRenewer) renewLease(ctx context.Context) {
	// Create a child context with a reasonable timeout for the database operation
	renewCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := r.claimService.RenewLease(renewCtx, r.sagaID)
	if err != nil {
		// Log the error but don't stop - renewal failures are non-fatal.
		// The saga will eventually be claimed by another pod if renewals
		// continue to fail and the lease expires.
		r.logger.Warn("failed to renew saga lease",
			"error", err)
	} else {
		r.logger.Debug("saga lease renewed successfully")
	}

	// Call the optional callback (for testing)
	if r.callback != nil {
		r.callback()
	}
}
