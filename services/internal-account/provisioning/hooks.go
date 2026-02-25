// Package provisioning provides hooks for integrating with tenant provisioning workflows.
package provisioning

import (
	"context"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// DefaultAccountHookTimeout is the maximum time allowed for default account provisioning.
const DefaultAccountHookTimeout = 30 * time.Second

// CreateDefaultAccountHook creates a post-provisioning hook function that provisions
// default internal accounts for newly created tenants.
//
// Usage with the provisioning worker:
//
//	provisioner := provisioning.NewProvisioner(ibaService, logger)
//	worker.RegisterPostProvisioningHook("default-internal-accounts",
//	    provisioning.CreateDefaultAccountHook(provisioner, logger))
//
// The returned hook is:
// - Non-blocking: Errors are returned but the worker will continue
// - Idempotent: Safe to call multiple times for the same tenant
// - Timeout-protected: Uses DefaultAccountHookTimeout (30 seconds)
func CreateDefaultAccountHook(provisioner *Provisioner, logger *slog.Logger) func(ctx context.Context, tenantID tenant.TenantID) error {
	if logger == nil {
		logger = slog.Default()
	}

	return func(ctx context.Context, tenantID tenant.TenantID) error {
		// Apply timeout to prevent hanging
		ctx, cancel := context.WithTimeout(ctx, DefaultAccountHookTimeout)
		defer cancel()

		logger.Info("provisioning default internal accounts",
			"tenant_id", tenantID)

		result, err := provisioner.ProvisionDefaultAccounts(ctx, tenantID)
		if err != nil {
			return err
		}

		// Log summary
		logger.Info("default internal accounts provisioned",
			"tenant_id", tenantID,
			"created", result.Created,
			"skipped", result.Skipped,
			"failed", result.Failed)

		// Return first error if any accounts failed (for logging by the worker)
		if len(result.Errors) > 0 {
			return result.Errors[0]
		}

		return nil
	}
}
