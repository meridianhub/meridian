// Package webhook provides webhook notification support for account lifecycle events.
// Webhooks are sent asynchronously for regulatory compliance events (Freeze, Close)
// to tenant-configured HTTP endpoints.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Notifier errors.
var (
	// ErrNoWebhookURL is returned when the tenant has no webhook URL configured.
	ErrNoWebhookURL = errors.New("no webhook URL configured for tenant")

	// ErrWebhookDeliveryFailed is returned when webhook delivery fails after all retries.
	ErrWebhookDeliveryFailed = errors.New("webhook delivery failed")

	// ErrInvalidWebhookResponse is returned when the webhook endpoint returns a non-2xx status.
	ErrInvalidWebhookResponse = errors.New("webhook endpoint returned non-2xx status")

	// ErrInsecureWebhookURL is returned when a webhook URL uses HTTP instead of HTTPS.
	ErrInsecureWebhookURL = errors.New("webhook URL must use HTTPS")
)

// EventType represents the type of account lifecycle event.
type EventType string

const (
	// EventTypeAccountFrozen represents an account freeze event.
	EventTypeAccountFrozen EventType = "account.frozen"

	// EventTypeAccountClosed represents an account closure event.
	EventTypeAccountClosed EventType = "account.closed"
)

// Payload is the JSON payload sent to webhook endpoints.
type Payload struct {
	// EventID is a unique identifier for this webhook event.
	EventID string `json:"event_id"`

	// EventType is the type of event (e.g., "account.frozen", "account.closed").
	EventType EventType `json:"event_type"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// AccountID is the ID of the affected account.
	AccountID string `json:"account_id"`

	// TenantID is the tenant that owns the account.
	TenantID string `json:"tenant_id"`

	// Reason is the reason provided for the action (if any).
	Reason string `json:"reason,omitempty"`

	// FinalBalance is the account balance at the time of the event (for closures).
	FinalBalance *BalanceInfo `json:"final_balance,omitempty"`
}

// BalanceInfo contains account balance details.
type BalanceInfo struct {
	// Amount is the balance amount in minor units (e.g., cents).
	Amount int64 `json:"amount"`

	// CurrencyCode is the ISO 4217 currency code.
	CurrencyCode string `json:"currency_code"`
}

// DeliveryStatus represents the status of a webhook delivery attempt.
type DeliveryStatus string

const (
	// DeliveryStatusPending means the delivery is queued.
	DeliveryStatusPending DeliveryStatus = "pending"

	// DeliveryStatusSuccess means the delivery succeeded.
	DeliveryStatusSuccess DeliveryStatus = "success"

	// DeliveryStatusFailed means the delivery failed after all retries.
	DeliveryStatusFailed DeliveryStatus = "failed"
)

// DeliveryRecord represents a webhook delivery attempt for audit trail.
type DeliveryRecord struct {
	// ID is the unique identifier for this delivery record.
	ID uuid.UUID

	// EventID is the event that triggered this delivery.
	EventID string

	// EventType is the type of event.
	EventType EventType

	// TenantID is the tenant that owns the account.
	TenantID string

	// AccountID is the affected account.
	AccountID string

	// WebhookURL is the URL the webhook was sent to.
	WebhookURL string

	// Status is the current delivery status.
	Status DeliveryStatus

	// Attempts is the number of delivery attempts made.
	Attempts int

	// LastAttemptAt is when the last delivery attempt was made.
	LastAttemptAt *time.Time

	// LastError is the error message from the last failed attempt.
	LastError *string

	// ResponseCode is the HTTP status code from the last attempt.
	ResponseCode *int

	// CreatedAt is when the delivery was queued.
	CreatedAt time.Time

	// CompletedAt is when the delivery succeeded or finally failed.
	CompletedAt *time.Time
}

// Notifier defines the interface for sending webhook notifications.
// Implementations should handle delivery, retries, and audit logging.
type Notifier interface {
	// NotifyAccountFrozen sends a webhook notification for an account freeze event.
	// Returns nil if the tenant has no webhook URL configured (not an error case).
	NotifyAccountFrozen(ctx context.Context, tenantID, accountID, reason string, timestamp time.Time) error

	// NotifyAccountClosed sends a webhook notification for an account closure event.
	// Returns nil if the tenant has no webhook URL configured (not an error case).
	NotifyAccountClosed(ctx context.Context, tenantID, accountID, reason string, balance *BalanceInfo, timestamp time.Time) error
}

// URLProvider retrieves webhook URLs for tenants.
type URLProvider interface {
	// GetWebhookURL returns the webhook URL for a tenant.
	// Returns empty string if no webhook is configured.
	GetWebhookURL(ctx context.Context, tenantID string) (string, error)
}

// DeliveryRecorder records webhook delivery attempts for audit trail.
type DeliveryRecorder interface {
	// RecordDelivery creates or updates a delivery record.
	RecordDelivery(ctx context.Context, record *DeliveryRecord) error
}

// Config contains configuration for the HTTP webhook notifier.
type Config struct {
	// HTTPClient is the HTTP client to use for webhook requests.
	// If nil, a default client with reasonable timeouts is created.
	HTTPClient *http.Client

	// URLProvider retrieves webhook URLs for tenants.
	URLProvider URLProvider

	// DeliveryRecorder records delivery attempts (optional - for audit trail).
	// If nil, delivery attempts are not recorded to the database.
	DeliveryRecorder DeliveryRecorder

	// Logger is the structured logger to use.
	Logger *slog.Logger

	// MaxRetries is the maximum number of retry attempts.
	// Default: 3
	MaxRetries int

	// RetryDelays are the delays between retry attempts.
	// Default: [1s, 2s, 4s] (exponential backoff)
	RetryDelays []time.Duration

	// RequestTimeout is the timeout for individual HTTP requests.
	// Default: 5s
	RequestTimeout time.Duration
}

// HTTPNotifier implements Notifier using HTTP POST requests with retry logic.
type HTTPNotifier struct {
	httpClient       *http.Client
	urlProvider      URLProvider
	deliveryRecorder DeliveryRecorder
	logger           *slog.Logger
	maxRetries       int
	retryDelays      []time.Duration
}

// NewHTTPNotifier creates a new HTTP webhook notifier.
func NewHTTPNotifier(cfg Config) *HTTPNotifier {
	// Apply defaults
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		timeout := cfg.RequestTimeout
		if timeout == 0 {
			timeout = 5 * time.Second
		}
		httpClient = &http.Client{
			Timeout: timeout,
		}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	maxRetries := cfg.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}

	retryDelays := cfg.RetryDelays
	if len(retryDelays) == 0 {
		// Exponential backoff: 1s, 2s, 4s
		retryDelays = []time.Duration{
			1 * time.Second,
			2 * time.Second,
			4 * time.Second,
		}
	}

	return &HTTPNotifier{
		httpClient:       httpClient,
		urlProvider:      cfg.URLProvider,
		deliveryRecorder: cfg.DeliveryRecorder,
		logger:           logger,
		maxRetries:       maxRetries,
		retryDelays:      retryDelays,
	}
}

// NotifyAccountFrozen sends a webhook notification for an account freeze event.
func (n *HTTPNotifier) NotifyAccountFrozen(ctx context.Context, tenantID, accountID, reason string, timestamp time.Time) error {
	payload := Payload{
		EventID:   uuid.New().String(),
		EventType: EventTypeAccountFrozen,
		Timestamp: timestamp,
		AccountID: accountID,
		TenantID:  tenantID,
		Reason:    reason,
	}
	return n.sendWebhook(ctx, payload)
}

// NotifyAccountClosed sends a webhook notification for an account closure event.
func (n *HTTPNotifier) NotifyAccountClosed(ctx context.Context, tenantID, accountID, reason string, balance *BalanceInfo, timestamp time.Time) error {
	payload := Payload{
		EventID:      uuid.New().String(),
		EventType:    EventTypeAccountClosed,
		Timestamp:    timestamp,
		AccountID:    accountID,
		TenantID:     tenantID,
		Reason:       reason,
		FinalBalance: balance,
	}
	return n.sendWebhook(ctx, payload)
}

// sendWebhook sends a webhook with retry logic.
func (n *HTTPNotifier) sendWebhook(ctx context.Context, payload Payload) error {
	webhookURL, err := n.getWebhookURL(ctx, payload)
	if err != nil {
		return err
	}
	if webhookURL == "" {
		return nil // No webhook configured, skip silently
	}

	record := n.createDeliveryRecord(payload, webhookURL)
	n.recordDelivery(ctx, record, payload.EventID)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		n.logger.Error("failed to serialize webhook payload", "event_id", payload.EventID, "error", err)
		return fmt.Errorf("failed to serialize payload: %w", err)
	}

	lastErr := n.executeWithRetries(ctx, webhookURL, payloadBytes, record, payload)
	if lastErr == nil {
		return nil
	}

	n.markDeliveryFailed(ctx, record, payload, lastErr)
	return fmt.Errorf("%w: %w", ErrWebhookDeliveryFailed, lastErr)
}

// getWebhookURL retrieves the webhook URL for the tenant.
func (n *HTTPNotifier) getWebhookURL(ctx context.Context, payload Payload) (string, error) {
	webhookURL, err := n.urlProvider.GetWebhookURL(ctx, payload.TenantID)
	if err != nil {
		n.logger.Error("failed to get webhook URL", "tenant_id", payload.TenantID, "error", err)
		return "", fmt.Errorf("failed to get webhook URL: %w", err)
	}
	if webhookURL == "" {
		n.logger.Debug("no webhook URL configured, skipping notification",
			"tenant_id", payload.TenantID, "event_type", payload.EventType)
		return "", nil
	}

	// Enforce HTTPS for security - HTTP URLs are rejected per security policy
	if !strings.HasPrefix(webhookURL, "https://") {
		n.logger.Error("webhook URL must use HTTPS",
			"tenant_id", payload.TenantID,
			"url", webhookURL)
		return "", ErrInsecureWebhookURL
	}

	return webhookURL, nil
}

// createDeliveryRecord creates a new delivery record for audit trail.
func (n *HTTPNotifier) createDeliveryRecord(payload Payload, webhookURL string) *DeliveryRecord {
	return &DeliveryRecord{
		ID:         uuid.New(),
		EventID:    payload.EventID,
		EventType:  payload.EventType,
		TenantID:   payload.TenantID,
		AccountID:  payload.AccountID,
		WebhookURL: webhookURL,
		Status:     DeliveryStatusPending,
		Attempts:   0,
		CreatedAt:  time.Now(),
	}
}

// recordDelivery records a delivery attempt to the audit trail.
func (n *HTTPNotifier) recordDelivery(ctx context.Context, record *DeliveryRecord, eventID string) {
	if n.deliveryRecorder != nil {
		if err := n.deliveryRecorder.RecordDelivery(ctx, record); err != nil {
			n.logger.Error("failed to record webhook delivery", "event_id", eventID, "error", err)
		}
	}
}

// executeWithRetries attempts webhook delivery with retry logic.
// Returns nil on success, or the last error on failure.
func (n *HTTPNotifier) executeWithRetries(
	ctx context.Context,
	webhookURL string,
	payloadBytes []byte,
	record *DeliveryRecord,
	payload Payload,
) error {
	var lastErr error

	for attempt := 0; attempt <= n.maxRetries; attempt++ {
		if err := n.waitForRetry(ctx, attempt, payload.EventID); err != nil {
			return err
		}

		record.Attempts = attempt + 1
		now := time.Now()
		record.LastAttemptAt = &now

		statusCode, err := n.doHTTPRequest(ctx, webhookURL, payloadBytes)
		record.ResponseCode = &statusCode

		if err == nil && statusCode >= 200 && statusCode < 300 {
			n.markDeliverySuccess(ctx, record, payload)
			return nil
		}

		lastErr = n.handleAttemptFailure(ctx, err, statusCode, record, payload, attempt)
	}

	return lastErr
}

// waitForRetry waits before a retry attempt (skips for first attempt).
func (n *HTTPNotifier) waitForRetry(ctx context.Context, attempt int, eventID string) error {
	if attempt == 0 {
		return nil
	}

	delayIdx := attempt - 1
	if delayIdx >= len(n.retryDelays) {
		delayIdx = len(n.retryDelays) - 1
	}
	delay := n.retryDelays[delayIdx]

	n.logger.Info("retrying webhook delivery", "event_id", eventID, "attempt", attempt+1, "delay", delay)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

// markDeliverySuccess marks a delivery as successful.
func (n *HTTPNotifier) markDeliverySuccess(ctx context.Context, record *DeliveryRecord, payload Payload) {
	record.Status = DeliveryStatusSuccess
	completedAt := time.Now()
	record.CompletedAt = &completedAt

	if n.deliveryRecorder != nil {
		_ = n.deliveryRecorder.RecordDelivery(ctx, record)
	}

	n.logger.Info("webhook delivered successfully",
		"event_id", payload.EventID,
		"tenant_id", payload.TenantID,
		"account_id", payload.AccountID,
		"event_type", payload.EventType,
		"attempts", record.Attempts)
}

// handleAttemptFailure handles a failed delivery attempt.
func (n *HTTPNotifier) handleAttemptFailure(
	ctx context.Context,
	err error,
	statusCode int,
	record *DeliveryRecord,
	payload Payload,
	attempt int,
) error {
	var lastErr error
	if err != nil {
		errMsg := err.Error()
		record.LastError = &errMsg
		lastErr = err
	} else {
		errMsg := fmt.Sprintf("HTTP %d", statusCode)
		record.LastError = &errMsg
		lastErr = fmt.Errorf("%w: HTTP %d", ErrInvalidWebhookResponse, statusCode)
	}

	n.logger.Warn("webhook delivery attempt failed",
		"event_id", payload.EventID,
		"attempt", attempt+1,
		"status_code", statusCode,
		"error", lastErr)

	if n.deliveryRecorder != nil {
		_ = n.deliveryRecorder.RecordDelivery(ctx, record)
	}

	return lastErr
}

// markDeliveryFailed marks a delivery as failed after all retries exhausted.
func (n *HTTPNotifier) markDeliveryFailed(ctx context.Context, record *DeliveryRecord, payload Payload, lastErr error) {
	record.Status = DeliveryStatusFailed
	completedAt := time.Now()
	record.CompletedAt = &completedAt

	if n.deliveryRecorder != nil {
		_ = n.deliveryRecorder.RecordDelivery(ctx, record)
	}

	n.logger.Error("webhook delivery failed after all retries",
		"event_id", payload.EventID,
		"tenant_id", payload.TenantID,
		"account_id", payload.AccountID,
		"event_type", payload.EventType,
		"attempts", record.Attempts,
		"last_error", lastErr)
}

// doHTTPRequest performs a single HTTP POST request.
// Returns the HTTP status code and any error.
func (n *HTTPNotifier) doHTTPRequest(ctx context.Context, url string, payload []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Meridian-Webhook/1.0")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode, nil
}

// NoOpNotifier is a no-operation implementation of Notifier.
// Useful for testing and scenarios where webhooks are not configured.
type NoOpNotifier struct{}

// NotifyAccountFrozen does nothing and always returns nil.
func (n *NoOpNotifier) NotifyAccountFrozen(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

// NotifyAccountClosed does nothing and always returns nil.
func (n *NoOpNotifier) NotifyAccountClosed(_ context.Context, _, _, _ string, _ *BalanceInfo, _ time.Time) error {
	return nil
}
