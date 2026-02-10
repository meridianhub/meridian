package stripe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Preflight check errors.
var (
	ErrPreflightFailed = errors.New("stripe preflight check failed")
	ErrTenantNotFound  = errors.New("meridian-ops tenant not found")
	ErrNostroNotFound  = errors.New("stripe_nostro account not found")
	ErrRevenueNotFound = errors.New("revenue account not found")
)

// PreflightChecker verifies that required infrastructure exists before the
// Stripe webhook handler starts accepting traffic.
type PreflightChecker struct {
	tenantVerifier  TenantVerifier
	accountVerifier AccountVerifier
	logger          *slog.Logger
}

// TenantVerifier checks that a tenant exists.
type TenantVerifier interface {
	TenantExists(ctx context.Context, tenantID string) (bool, error)
}

// AccountVerifier checks that an account exists within a tenant.
type AccountVerifier interface {
	AccountExists(ctx context.Context, tenantID, accountID string) (bool, error)
}

// PreflightConfig contains configuration for the preflight checker.
type PreflightConfig struct {
	// TenantVerifier checks tenant existence.
	TenantVerifier TenantVerifier
	// AccountVerifier checks account existence.
	AccountVerifier AccountVerifier
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// NewPreflightChecker creates a new PreflightChecker.
func NewPreflightChecker(cfg PreflightConfig) *PreflightChecker {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &PreflightChecker{
		tenantVerifier:  cfg.TenantVerifier,
		accountVerifier: cfg.AccountVerifier,
		logger:          logger,
	}
}

// Check verifies that the meridian-ops tenant and required accounts exist.
// Returns an error wrapping ErrPreflightFailed if any precondition is not met.
// This should be called at startup; the service should panic if it fails.
func (p *PreflightChecker) Check(ctx context.Context) error {
	p.logger.Info("running stripe preflight checks")

	// Verify meridian-ops tenant exists
	if p.tenantVerifier != nil {
		exists, err := p.tenantVerifier.TenantExists(ctx, "meridian-ops")
		if err != nil {
			return fmt.Errorf("%w: failed to verify meridian-ops tenant: %w", ErrPreflightFailed, err)
		}
		if !exists {
			return fmt.Errorf("%w: %w", ErrPreflightFailed, ErrTenantNotFound)
		}
		p.logger.Info("preflight: meridian-ops tenant verified")
	}

	// Verify stripe_nostro account exists
	if p.accountVerifier != nil {
		exists, err := p.accountVerifier.AccountExists(ctx, "meridian-ops", "stripe_nostro")
		if err != nil {
			return fmt.Errorf("%w: failed to verify stripe_nostro account: %w", ErrPreflightFailed, err)
		}
		if !exists {
			return fmt.Errorf("%w: %w", ErrPreflightFailed, ErrNostroNotFound)
		}
		p.logger.Info("preflight: stripe_nostro account verified")

		// Verify revenue account exists
		exists, err = p.accountVerifier.AccountExists(ctx, "meridian-ops", "revenue")
		if err != nil {
			return fmt.Errorf("%w: failed to verify revenue account: %w", ErrPreflightFailed, err)
		}
		if !exists {
			return fmt.Errorf("%w: %w", ErrPreflightFailed, ErrRevenueNotFound)
		}
		p.logger.Info("preflight: revenue account verified")
	}

	p.logger.Info("stripe preflight checks passed")
	return nil
}
