// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/config"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/notifier"
	"github.com/meridianhub/meridian/services/tenant/observability"
	"github.com/meridianhub/meridian/services/tenant/pagerduty"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// Alert type constants for rate limiting.
const (
	AlertTypePagerDuty = "pagerduty"
	AlertTypeSlack     = "slack"
)

// Error message truncation constants.
// PagerDuty Events API v2 has a 1024 character limit for the summary field.
// We use a conservative limit to leave room for the tenant ID prefix.
const (
	// maxErrorMessageLength is the maximum length for error messages in alert summaries.
	maxErrorMessageLength = 200
	// truncatedErrorMessageLength is the length to truncate to (leaving room for "...").
	truncatedErrorMessageLength = 197
)

// ErrRateLimited is returned when an alert is blocked by rate limiting.
var ErrRateLimited = errors.New("alert rate limited")

// AlertManager monitors and alerts on tenant provisioning failures.
// It identifies tenants stuck in provisioning_failed state and logs alerts
// for integration with external alerting systems (PagerDuty, Slack, etc.).
//
// Features:
// - Rate limiting: Token bucket algorithm to prevent alert storms
// - Retry logic: Exponential backoff with configurable retries
// - Dead-letter queue: Failed alerts are stored for manual review
type AlertManager struct {
	repo            *persistence.Repository
	logger          *slog.Logger
	pagerdutyClient *pagerduty.Client
	slackNotifier   *notifier.SlackNotifier
	rateLimiter     *AlertRateLimiter
	dlq             *AlertDeadLetterQueue
	retryConfig     config.AlertRetryConfig
}

// AlertManagerOption configures the AlertManager.
type AlertManagerOption func(*AlertManager)

// WithPagerDutyClient configures the AlertManager to send alerts to PagerDuty.
func WithPagerDutyClient(client *pagerduty.Client) AlertManagerOption {
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

// WithRateLimiter configures alert rate limiting.
func WithRateLimiter(limiter *AlertRateLimiter) AlertManagerOption {
	return func(a *AlertManager) {
		a.rateLimiter = limiter
	}
}

// WithDeadLetterQueue configures the dead-letter queue for failed alerts.
func WithDeadLetterQueue(dlq *AlertDeadLetterQueue) AlertManagerOption {
	return func(a *AlertManager) {
		a.dlq = dlq
	}
}

// WithRetryConfig configures the retry behavior for failed alerts.
func WithRetryConfig(cfg config.AlertRetryConfig) AlertManagerOption {
	return func(a *AlertManager) {
		a.retryConfig = cfg
	}
}

// NewAlertManager creates a new AlertManager.
// By default, rate limiting and DLQ are disabled. Use WithRateLimiter and
// WithDeadLetterQueue options to enable them.
func NewAlertManager(repo *persistence.Repository, logger *slog.Logger, opts ...AlertManagerOption) *AlertManager {
	am := &AlertManager{
		repo:        repo,
		logger:      logger,
		retryConfig: config.DefaultAlertRetryConfig(),
	}

	for _, opt := range opts {
		opt(am)
	}

	return am
}

// DeadLetterQueue returns the DLQ for external inspection/processing.
// Returns nil if DLQ is not configured.
func (a *AlertManager) DeadLetterQueue() *AlertDeadLetterQueue {
	return a.dlq
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
//
// Rate limiting: Alerts are rate limited per alert type (PagerDuty, Slack) to prevent
// alert storms. When rate limited, alerts are logged but not sent.
//
// Retry: Failed alerts are retried with exponential backoff (1s, 2s, 4s, 8s).
// After max retries, alerts are sent to the dead-letter queue for manual review.
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
		// Check for context cancellation to enable graceful shutdown
		if ctx.Err() != nil {
			return ctx.Err()
		}

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
			a.sendPagerDutyAlertWithRetry(ctx, tenant)
		}

		// Send alert to Slack if configured
		if a.slackNotifier != nil {
			a.sendSlackAlertWithRetry(ctx, tenant)
		}
	}

	if len(failedTenants) > 0 {
		a.logger.Warn("found tenants with persistent provisioning failures",
			"count", len(failedTenants),
			"threshold", threshold)
	}

	return nil
}

// sendPagerDutyAlertWithRetry sends a PagerDuty alert with rate limiting, retry, and DLQ.
func (a *AlertManager) sendPagerDutyAlertWithRetry(ctx context.Context, tenant *domain.Tenant) {
	a.sendAlertWithRetry(ctx, tenant, AlertTypePagerDuty, observability.AlertProviderPagerDuty,
		func() error { return a.sendPagerDutyAlert(ctx, tenant) })
}

// sendSlackAlertWithRetry sends a Slack alert with rate limiting, retry, and DLQ.
func (a *AlertManager) sendSlackAlertWithRetry(ctx context.Context, tenant *domain.Tenant) {
	a.sendAlertWithRetry(ctx, tenant, AlertTypeSlack, observability.AlertProviderSlack,
		func() error { return a.slackNotifier.NotifyProvisioningFailure(ctx, tenant) })
}

// sendAlertWithRetry is the shared implementation for sending alerts with rate limiting, retry, and DLQ.
func (a *AlertManager) sendAlertWithRetry(ctx context.Context, tenant *domain.Tenant, alertType string, provider string, sendFn func() error) {
	severity := observability.AlertSeverityCritical

	// Check rate limit
	if a.rateLimiter != nil && !a.rateLimiter.Allow(alertType) {
		a.logger.Warn("alert rate limited",
			"tenant_id", tenant.ID,
			"alert_type", alertType)
		observability.RecordAlertSent(provider, severity, observability.AlertStatusRateLimited)
		return
	}

	payload := a.buildAlertPayload(tenant)
	firstAttempt := time.Now()

	a.logger.Debug("sending alert",
		"tenant_id", tenant.ID,
		"alert_type", alertType,
		"summary", payload.Summary)

	attemptCount, lastErr := a.executeAlertWithRetry(ctx, tenant.ID, alertType, sendFn)

	if lastErr != nil {
		a.logger.Error("failed to send alert after retries",
			"tenant_id", tenant.ID,
			"alert_type", alertType,
			"attempts", attemptCount,
			"error", lastErr)
		observability.RecordAlertSent(provider, severity, observability.AlertStatusError)
		a.enqueueToDeadLetterQueue(alertType, tenant.ID.String(), payload, lastErr, firstAttempt, attemptCount)
	} else {
		observability.RecordAlertSent(provider, severity, observability.AlertStatusSuccess)
	}
}

// executeAlertWithRetry runs the send function with exponential backoff retry.
// Returns the attempt count and last error (nil on success).
func (a *AlertManager) executeAlertWithRetry(ctx context.Context, tenantID fmt.Stringer, alertType string, sendFn func() error) (int, error) {
	retryConfig := sharedclients.RetryConfig{
		MaxRetries:          a.retryConfig.MaxRetries,
		InitialInterval:     a.retryConfig.InitialBackoff,
		MaxInterval:         a.retryConfig.MaxBackoff,
		Multiplier:          2.0,
		RandomizationFactor: 0.1,
	}

	attemptCount := 0
	var lastErr error

	err := sharedclients.Retry(ctx, retryConfig, func() error {
		attemptCount++
		lastErr = sendFn()
		if lastErr != nil {
			a.logger.Warn("alert attempt failed",
				"tenant_id", tenantID,
				"alert_type", alertType,
				"attempt", attemptCount,
				"error", lastErr)
			return a.wrapAsRetryable(lastErr)
		}
		return nil
	})
	if err != nil {
		return attemptCount, lastErr
	}
	return attemptCount, nil
}

// enqueueToDeadLetterQueue stores a failed alert in the DLQ for manual review.
func (a *AlertManager) enqueueToDeadLetterQueue(alertType, tenantID string, payload AlertPayload, lastErr error, firstAttempt time.Time, attemptCount int) {
	if a.dlq == nil {
		return
	}
	a.dlq.Enqueue(FailedAlert{
		AlertType:      alertType,
		TenantID:       tenantID,
		Payload:        payload,
		ErrorMessage:   lastErr.Error(),
		FirstAttemptAt: firstAttempt,
		LastAttemptAt:  time.Now(),
		AttemptCount:   attemptCount,
	})
	a.logger.Info("alert sent to DLQ",
		"tenant_id", tenantID,
		"alert_type", alertType,
		"attempts", attemptCount)
	observability.SetAlertDLQDepth(a.dlq.Len())
}

// buildAlertPayload creates an AlertPayload from a tenant for DLQ storage.
func (a *AlertManager) buildAlertPayload(tenant *domain.Tenant) AlertPayload {
	summary := fmt.Sprintf("Tenant provisioning failed: %s", tenant.ID)
	if tenant.ErrorMessage != "" {
		errMsg := tenant.ErrorMessage
		if len(errMsg) > maxErrorMessageLength {
			errMsg = errMsg[:truncatedErrorMessageLength] + "..."
		}
		summary = fmt.Sprintf("Tenant provisioning failed: %s - %s", tenant.ID, errMsg)
	}

	return AlertPayload{
		Summary:  summary,
		DedupKey: fmt.Sprintf("tenant-provisioning-failed-%s", tenant.ID),
		Severity: string(pagerduty.SeverityCritical),
		CustomDetails: map[string]any{
			"tenant_id":     tenant.ID.String(),
			"display_name":  tenant.DisplayName,
			"status":        string(tenant.Status),
			"error_message": tenant.ErrorMessage,
			"created_at":    tenant.CreatedAt.Format(time.RFC3339),
		},
	}
}

// wrapAsRetryable wraps an error to make it retryable by the shared retry logic.
// The shared retry.go uses gRPC status codes to determine retryability.
// For HTTP-based alerts (PagerDuty, Slack), we wrap the error with gRPC Unavailable status.
func (a *AlertManager) wrapAsRetryable(err error) error {
	// The shared retry package uses gRPC codes to determine retryability.
	// For HTTP errors, we wrap them as gRPC Unavailable to trigger retries.
	// This allows us to reuse the existing retry logic without modification.
	return grpcUnavailableError{underlying: err}
}

// grpcUnavailableError wraps an HTTP error as a gRPC Unavailable error
// so that the shared retry logic treats it as retryable.
type grpcUnavailableError struct {
	underlying error
}

func (e grpcUnavailableError) Error() string {
	return e.underlying.Error()
}

func (e grpcUnavailableError) Unwrap() error {
	return e.underlying
}

// GRPCStatus implements the interface expected by status.FromError
// to return a gRPC status that indicates the error is retryable.
func (e grpcUnavailableError) GRPCStatus() *grpcstatus.Status {
	return grpcstatus.New(codes.Unavailable, e.underlying.Error())
}

// sendPagerDutyAlert sends a provisioning failure alert to PagerDuty.
func (a *AlertManager) sendPagerDutyAlert(ctx context.Context, tenant *domain.Tenant) error {
	// Build alert summary
	summary := fmt.Sprintf("Tenant provisioning failed: %s", tenant.ID)
	if tenant.ErrorMessage != "" {
		// Truncate error message if too long (PagerDuty has summary limits)
		errMsg := tenant.ErrorMessage
		if len(errMsg) > maxErrorMessageLength {
			errMsg = errMsg[:truncatedErrorMessageLength] + "..."
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
	return a.pagerdutyClient.TriggerAlert(ctx, summary, dedupKey, pagerduty.SeverityCritical, customDetails)
}
