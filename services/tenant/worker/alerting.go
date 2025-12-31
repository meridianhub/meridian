// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/clients"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/notifier"
)

// AlertManager monitors and alerts on tenant provisioning failures.
// It identifies tenants stuck in provisioning_failed state and logs alerts
// for integration with external alerting systems (PagerDuty, Slack, etc.).
type AlertManager struct {
	repo            *persistence.Repository
	logger          *slog.Logger
	pagerdutyClient *clients.PagerDutyClient
	slackNotifier   *notifier.SlackNotifier
}

// AlertManagerOption configures the AlertManager.
type AlertManagerOption func(*AlertManager)

// WithPagerDutyClient configures the AlertManager to send alerts to PagerDuty.
func WithPagerDutyClient(client *clients.PagerDutyClient) AlertManagerOption {
	return func(a *AlertManager) {
		a.pagerdutyClient = client
	}
}

// WithSlackNotifier configures the AlertManager to send alerts to Slack.
func WithSlackNotifier(slack *notifier.SlackNotifier) AlertManagerOption {
	return func(a *AlertManager) {
		a.slackNotifier = slack
	}
}

// NewAlertManager creates a new AlertManager.
func NewAlertManager(repo *persistence.Repository, logger *slog.Logger, opts ...AlertManagerOption) *AlertManager {
	am := &AlertManager{
		repo:   repo,
		logger: logger,
	}

	for _, opt := range opts {
		opt(am)
	}

	return am
}

// CheckFailedProvisioningAlerts queries for tenants in provisioning_failed state
// older than the specified threshold and logs alerts with structured fields.
// The alerts include tenant_id, error_message, failed_at timestamp, and an alert label
// for downstream alerting system integration.
//
// The threshold parameter determines how old a failed tenant must be before alerting.
// Typically set to 1 hour to avoid alerting on transient failures that may self-recover.
//
// Note: Alerts will repeat every 15 minutes (default alert interval) for the same tenant
// until the provisioning issue is resolved. PagerDuty deduplication is based on tenant_id
// to avoid alert fatigue (the dedup_key ensures repeated calls for the same tenant
// update the existing incident rather than creating new ones).
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

	// Process alerts for each failed tenant
	for _, tenant := range failedTenants {
		a.logger.Warn("tenant provisioning failure alert",
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

		// Send alert to PagerDuty if configured
		if a.pagerdutyClient != nil && a.pagerdutyClient.IsEnabled() {
			if err := a.sendPagerDutyAlert(ctx, tenant); err != nil {
				// Log error but continue processing other tenants
				// PagerDuty failures should not block the alert loop
				a.logger.Error("failed to send PagerDuty alert",
					"tenant_id", tenant.ID,
					"error", err)
			}
		}

		// Send alert to Slack if configured
		if a.slackNotifier != nil {
			if err := a.slackNotifier.NotifyProvisioningFailure(ctx, tenant); err != nil {
				// Log error but continue processing other tenants
				// Slack failures should not block the alert loop
				a.logger.Error("failed to send Slack alert",
					"tenant_id", tenant.ID,
					"error", err)
			}
		}
	}

	if len(failedTenants) > 0 {
		a.logger.Warn("found tenants with persistent provisioning failures",
			"count", len(failedTenants),
			"threshold", threshold)
	}

	return nil
}

// sendPagerDutyAlert sends a provisioning failure alert to PagerDuty.
func (a *AlertManager) sendPagerDutyAlert(ctx context.Context, tenant *domain.Tenant) error {
	// Build alert summary
	summary := fmt.Sprintf("Tenant provisioning failed: %s", tenant.ID)
	if tenant.ErrorMessage != "" {
		// Truncate error message if too long (PagerDuty has summary limits)
		errMsg := tenant.ErrorMessage
		if len(errMsg) > 200 {
			errMsg = errMsg[:197] + "..."
		}
		summary = fmt.Sprintf("Tenant provisioning failed: %s - %s", tenant.ID, errMsg)
	}

	// Use tenant ID as dedup key to group repeated alerts for the same tenant
	dedupKey := fmt.Sprintf("tenant-provisioning-failed-%s", tenant.ID)

	// Build custom details for the alert payload
	customDetails := map[string]any{
		"tenant_id":     tenant.ID.String(),
		"display_name":  tenant.DisplayName,
		"status":        string(tenant.Status),
		"error_message": tenant.ErrorMessage,
		"created_at":    tenant.CreatedAt.Format(time.RFC3339),
	}

	// Provisioning failures are critical - they block tenant onboarding
	return a.pagerdutyClient.TriggerAlert(ctx, summary, dedupKey, clients.SeverityCritical, customDetails)
}
