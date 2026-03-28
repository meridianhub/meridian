// Package notifier provides alerting integrations for external notification systems.
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/tenant/config"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Severity represents the severity level of an alert.
type Severity string

// Alert severity levels.
const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Slack notifier errors.
var (
	// ErrSlackWebhookFailed is returned when the Slack webhook request fails.
	ErrSlackWebhookFailed = errors.New("slack webhook request failed")

	// ErrSlackResponseError is returned when Slack returns an error response.
	ErrSlackResponseError = errors.New("slack returned error response")
)

// SlackNotifier sends alert notifications to Slack via incoming webhooks.
type SlackNotifier struct {
	webhookURL  string
	serviceName string
	httpClient  *http.Client
	logger      *slog.Logger
}

// SlackNotifierConfig contains configuration for creating a SlackNotifier.
type SlackNotifierConfig struct {
	// Config is the Slack configuration.
	Config config.SlackConfig

	// HTTPClient is an optional HTTP client (defaults to http.DefaultClient with timeout).
	HTTPClient *http.Client

	// Logger is an optional logger (defaults to slog.Default()).
	Logger *slog.Logger
}

// NewSlackNotifier creates a new SlackNotifier.
// Returns nil if Slack notifications are disabled.
func NewSlackNotifier(cfg SlackNotifierConfig) *SlackNotifier {
	if !cfg.Config.Enabled {
		return nil
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		// Use 30s timeout to match PagerDuty client - external services may have
		// variable latency, especially under load or during incidents.
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &SlackNotifier{
		webhookURL:  cfg.Config.WebhookURL,
		serviceName: cfg.Config.ServiceName,
		httpClient:  httpClient,
		logger:      logger,
	}
}

// Alert represents an alert to be sent to Slack.
type Alert struct {
	// TenantID is the ID of the affected tenant.
	TenantID tenant.TenantID

	// Severity is the alert severity (critical, warning, info).
	Severity Severity

	// Title is the alert title/summary.
	Title string

	// Message is the detailed alert message.
	Message string

	// Timestamp is when the alert was triggered.
	Timestamp time.Time

	// Metadata contains additional context fields.
	Metadata map[string]string
}

// SendAlert sends an alert notification to Slack.
func (s *SlackNotifier) SendAlert(ctx context.Context, alert Alert) error {
	alertID := uuid.New().String()

	// Build the Slack message payload
	payload := s.buildPayload(alert, alertID)

	// Marshal to JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal slack payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Error("slack webhook request failed",
			"error", err,
			"alert_id", alertID,
			"tenant_id", alert.TenantID)
		return fmt.Errorf("%w: %w", ErrSlackWebhookFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		s.logger.Error("slack webhook returned error",
			"status_code", resp.StatusCode,
			"response", string(respBody),
			"alert_id", alertID,
			"tenant_id", alert.TenantID)
		return fmt.Errorf("%w: status %d", ErrSlackResponseError, resp.StatusCode)
	}

	s.logger.Info("slack alert sent successfully",
		"alert_id", alertID,
		"tenant_id", alert.TenantID,
		"severity", alert.Severity)

	return nil
}

// NotifyProvisioningFailure sends a notification for a tenant provisioning failure.
func (s *SlackNotifier) NotifyProvisioningFailure(ctx context.Context, t *domain.Tenant) error {
	alert := Alert{
		TenantID:  t.ID,
		Severity:  SeverityCritical,
		Title:     "Tenant Provisioning Failed",
		Message:   t.ErrorMessage,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"display_name":     t.DisplayName,
			"settlement_asset": t.SettlementAsset,
			"status":           string(t.Status),
		},
	}

	return s.SendAlert(ctx, alert)
}

// slackPayload represents the Slack incoming webhook JSON payload.
type slackPayload struct {
	Text   string       `json:"text"`
	Blocks []slackBlock `json:"blocks"`
}

// slackBlock represents a Slack block element.
type slackBlock struct {
	Type   string         `json:"type"`
	Text   *slackTextObj  `json:"text,omitempty"`
	Fields []slackTextObj `json:"fields,omitempty"`
}

// slackTextObj represents a Slack text object.
type slackTextObj struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// severityEmoji returns the appropriate emoji for the given severity.
func severityEmoji(severity Severity) string {
	switch severity {
	case SeverityCritical:
		return "\U0001F534" // Red circle emoji
	case SeverityWarning:
		return "\u26A0\uFE0F" // Warning emoji
	case SeverityInfo:
		return "\u2139\uFE0F" // Info emoji
	default:
		return "\u2139\uFE0F" // Default to info emoji
	}
}

// buildPayload constructs the Slack message payload with rich formatting.
func (s *SlackNotifier) buildPayload(alert Alert, alertID string) slackPayload {
	emoji := severityEmoji(alert.Severity)
	headerText := fmt.Sprintf("%s *%s*", emoji, alert.Title)

	details := buildAlertDetails(alert, s.serviceName, alertID)

	blocks := []slackBlock{
		{Type: "section", Text: &slackTextObj{Type: "mrkdwn", Text: headerText}},
		{Type: "section", Text: &slackTextObj{Type: "mrkdwn", Text: details}},
	}

	// Add metadata section if there are fields
	if metadataFields := buildMetadataFields(alert.Metadata); len(metadataFields) > 0 {
		blocks = append(blocks, slackBlock{Type: "section", Fields: metadataFields})
	}

	// Add divider at the end
	blocks = append(blocks, slackBlock{Type: "divider"})

	fallbackText := fmt.Sprintf("%s %s - Tenant: %s", emoji, alert.Title, alert.TenantID)

	return slackPayload{
		Text:   fallbackText,
		Blocks: blocks,
	}
}

// buildAlertDetails formats the alert detail section with tenant, service, timestamp, and error.
func buildAlertDetails(alert Alert, serviceName string, alertID string) string {
	details := fmt.Sprintf(
		"*Tenant ID:* `%s`\n*Service:* %s\n*Timestamp:* %s\n*Alert ID:* `%s`",
		alert.TenantID,
		serviceName,
		alert.Timestamp.Format(time.RFC3339),
		alertID,
	)

	if alert.Message != "" {
		details += fmt.Sprintf("\n\n*Error:*\n```%s```", alert.Message)
	}

	return details
}

// buildMetadataFields converts alert metadata into Slack field objects, sorted by key.
func buildMetadataFields(metadata map[string]string) []slackTextObj {
	if len(metadata) == 0 {
		return nil
	}

	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var fields []slackTextObj
	for _, key := range keys {
		if value := metadata[key]; value != "" {
			fields = append(fields, slackTextObj{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*%s:* %s", key, value),
			})
		}
	}
	return fields
}

// formatProvisioningFailureMessage formats a tenant provisioning failure for Slack.
// This is kept unexported since slackPayload is unexported.
func formatProvisioningFailureMessage(t *domain.Tenant, serviceName string, alertID string, timestamp time.Time) slackPayload {
	notifier := &SlackNotifier{serviceName: serviceName}
	alert := Alert{
		TenantID:  t.ID,
		Severity:  SeverityCritical,
		Title:     "Tenant Provisioning Failed",
		Message:   t.ErrorMessage,
		Timestamp: timestamp,
		Metadata: map[string]string{
			"display_name":     t.DisplayName,
			"settlement_asset": t.SettlementAsset,
			"status":           string(t.Status),
		},
	}
	return notifier.buildPayload(alert, alertID)
}
