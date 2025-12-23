// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
)

// AlertManager monitors and alerts on tenant provisioning failures.
// It identifies tenants stuck in provisioning_failed state and logs alerts
// for integration with external alerting systems (PagerDuty, Slack, etc.).
type AlertManager struct {
	repo   *persistence.Repository
	logger *slog.Logger
}

// NewAlertManager creates a new AlertManager.
func NewAlertManager(repo *persistence.Repository, logger *slog.Logger) *AlertManager {
	return &AlertManager{
		repo:   repo,
		logger: logger,
	}
}

// CheckFailedProvisioningAlerts queries for tenants in provisioning_failed state
// older than the specified threshold and logs alerts with structured fields.
// The alerts include tenant_id, error_message, failed_at timestamp, and an alert label
// for downstream alerting system integration.
//
// The threshold parameter determines how old a failed tenant must be before alerting.
// Typically set to 1 hour to avoid alerting on transient failures that may self-recover.
func (a *AlertManager) CheckFailedProvisioningAlerts(ctx context.Context, threshold time.Duration) error {
	// Calculate cutoff time for failed tenants
	cutoffTime := time.Now().Add(-threshold)

	// Query for tenants in provisioning_failed state older than threshold
	failedTenants, err := a.repo.ListByStatusOlderThan(ctx, domain.StatusProvisioningFailed, cutoffTime)
	if err != nil {
		a.logger.Error("failed to query provisioning_failed tenants",
			"error", err,
			"threshold", threshold)
		return err
	}

	// Log alert for each failed tenant
	for _, tenant := range failedTenants {
		a.logger.Error("tenant provisioning failure alert",
			"alert", "tenant_provisioning_failed",
			"tenant_id", tenant.ID,
			"error_message", tenant.ErrorMessage,
			// Note: Using created_at as a proxy for failure timestamp. In typical workflows,
			// tenants transition to provisioning_failed within seconds of creation, making
			// created_at a reasonable approximation. A dedicated failed_at field would require
			// schema changes and is deferred to future work.
			"failed_at", tenant.CreatedAt,
			"status", tenant.Status,
			"threshold_hours", threshold.Hours())

		// TODO: Integrate with PagerDuty API
		// Example: pagerduty.TriggerIncident(ctx, tenant.ID, tenant.ErrorMessage)

		// TODO: Integrate with Slack webhook
		// Example: slack.PostMessage(ctx, alertChannel, formatAlertMessage(tenant))
	}

	if len(failedTenants) > 0 {
		a.logger.Warn("found tenants with persistent provisioning failures",
			"count", len(failedTenants),
			"threshold", threshold)
	}

	return nil
}
