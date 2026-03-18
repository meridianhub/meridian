package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/config"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSlackNotifier_Disabled(t *testing.T) {
	cfg := SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled: false,
		},
	}

	notifier := NewSlackNotifier(cfg)

	assert.Nil(t, notifier, "should return nil when Slack is disabled")
}

func TestNewSlackNotifier_Enabled(t *testing.T) {
	cfg := SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled:     true,
			WebhookURL:  "https://hooks.slack.com/services/test",
			ServiceName: "test-service",
		},
	}

	notifier := NewSlackNotifier(cfg)

	require.NotNil(t, notifier, "should return notifier when enabled")
	assert.Equal(t, "https://hooks.slack.com/services/test", notifier.webhookURL)
	assert.Equal(t, "test-service", notifier.serviceName)
	assert.NotNil(t, notifier.httpClient)
	assert.NotNil(t, notifier.logger)
}

func TestNewSlackNotifier_WithCustomHTTPClient(t *testing.T) {
	customClient := &http.Client{Timeout: 30 * time.Second}
	cfg := SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled:    true,
			WebhookURL: "https://hooks.slack.com/services/test",
		},
		HTTPClient: customClient,
	}

	notifier := NewSlackNotifier(cfg)

	require.NotNil(t, notifier)
	assert.Equal(t, customClient, notifier.httpClient)
}

func TestNewSlackNotifier_WithCustomLogger(t *testing.T) {
	var logBuf bytes.Buffer
	customLogger := slog.New(slog.NewTextHandler(&logBuf, nil))
	cfg := SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled:    true,
			WebhookURL: "https://hooks.slack.com/services/test",
		},
		Logger: customLogger,
	}

	notifier := NewSlackNotifier(cfg)

	require.NotNil(t, notifier)
	assert.Equal(t, customLogger, notifier.logger)
}

func TestSeverityEmoji_Critical(t *testing.T) {
	emoji := severityEmoji(SeverityCritical)
	assert.Contains(t, emoji, "\U0001F534") // Red circle
}

func TestSeverityEmoji_Warning(t *testing.T) {
	emoji := severityEmoji(SeverityWarning)
	assert.Contains(t, emoji, "\u26A0") // Warning sign
}

func TestSeverityEmoji_Info(t *testing.T) {
	emoji := severityEmoji(SeverityInfo)
	assert.Contains(t, emoji, "\u2139") // Info sign
}

func TestSeverityEmoji_Unknown(t *testing.T) {
	emoji := severityEmoji(Severity("unknown"))
	assert.Contains(t, emoji, "\u2139") // Default to info
}

func TestBuildPayload_BasicStructure(t *testing.T) {
	notifier := &SlackNotifier{serviceName: "test-service"}
	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant-123"),
		Severity:  SeverityCritical,
		Title:     "Test Alert",
		Message:   "This is a test error message",
		Timestamp: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Metadata:  map[string]string{"key": "value"},
	}

	payload := notifier.buildPayload(alert, "alert-id-123")

	// Check fallback text
	assert.Contains(t, payload.Text, "Test Alert")
	assert.Contains(t, payload.Text, "test-tenant-123")

	// Check blocks structure
	require.GreaterOrEqual(t, len(payload.Blocks), 3)

	// First block should be header with emoji
	headerBlock := payload.Blocks[0]
	assert.Equal(t, "section", headerBlock.Type)
	require.NotNil(t, headerBlock.Text)
	assert.Equal(t, "mrkdwn", headerBlock.Text.Type)
	assert.Contains(t, headerBlock.Text.Text, "Test Alert")
	assert.Contains(t, headerBlock.Text.Text, "\U0001F534") // Red circle for critical

	// Second block should be details
	detailsBlock := payload.Blocks[1]
	assert.Equal(t, "section", detailsBlock.Type)
	require.NotNil(t, detailsBlock.Text)
	assert.Contains(t, detailsBlock.Text.Text, "test-tenant-123")
	assert.Contains(t, detailsBlock.Text.Text, "test-service")
	assert.Contains(t, detailsBlock.Text.Text, "alert-id-123")
	assert.Contains(t, detailsBlock.Text.Text, "This is a test error message")
}

func TestBuildPayload_WithMetadata(t *testing.T) {
	notifier := &SlackNotifier{serviceName: "test-service"}
	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityWarning,
		Title:     "Warning Alert",
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"display_name": "Acme Corp",
			"status":       "provisioning_failed",
		},
	}

	payload := notifier.buildPayload(alert, "alert-123")

	// Find the fields block
	var foundMetadata bool
	for _, block := range payload.Blocks {
		if len(block.Fields) > 0 {
			foundMetadata = true
			// Check metadata fields are present
			fieldTexts := make([]string, 0, len(block.Fields))
			for _, field := range block.Fields {
				fieldTexts = append(fieldTexts, field.Text)
			}
			assert.True(t, containsAny(fieldTexts, "Acme Corp"), "should contain display_name")
			assert.True(t, containsAny(fieldTexts, "provisioning_failed"), "should contain status")
		}
	}
	assert.True(t, foundMetadata, "should have metadata block")
}

func TestBuildPayload_EmptyMessage(t *testing.T) {
	notifier := &SlackNotifier{serviceName: "test-service"}
	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityInfo,
		Title:     "Info Alert",
		Message:   "", // Empty message
		Timestamp: time.Now(),
	}

	payload := notifier.buildPayload(alert, "alert-123")

	// Details block should not contain error code block
	detailsBlock := payload.Blocks[1]
	assert.NotContains(t, detailsBlock.Text.Text, "```")
}

func TestBuildPayload_DividerAtEnd(t *testing.T) {
	notifier := &SlackNotifier{serviceName: "test-service"}
	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityCritical,
		Title:     "Test Alert",
		Timestamp: time.Now(),
	}

	payload := notifier.buildPayload(alert, "alert-123")

	// Last block should be divider
	lastBlock := payload.Blocks[len(payload.Blocks)-1]
	assert.Equal(t, "divider", lastBlock.Type)
}

func TestFormatProvisioningFailureMessage(t *testing.T) {
	tenant := &domain.Tenant{
		ID:              tenant.TenantID("failed-tenant-456"),
		DisplayName:     "Failed Corp",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "database connection timeout",
	}

	timestamp := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	payload := formatProvisioningFailureMessage(tenant, "tenant-service", "alert-789", timestamp)

	// Verify critical emoji for provisioning failures
	assert.Contains(t, payload.Text, "\U0001F534") // Red circle

	// Verify tenant info
	assert.Contains(t, payload.Text, "failed-tenant-456")
	assert.Contains(t, payload.Text, "Tenant Provisioning Failed")

	// Verify error message in details
	detailsBlock := payload.Blocks[1]
	assert.Contains(t, detailsBlock.Text.Text, "database connection timeout")
}

func TestSendAlert_Success(t *testing.T) {
	var receivedPayload slackPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		err = json.Unmarshal(body, &receivedPayload)
		require.NoError(t, err)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	notifier := &SlackNotifier{
		webhookURL:  server.URL,
		serviceName: "test-service",
		httpClient:  server.Client(),
		logger:      logger,
	}

	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityCritical,
		Title:     "Test Alert",
		Message:   "Test error",
		Timestamp: time.Now(),
	}

	err := notifier.SendAlert(context.Background(), alert)

	require.NoError(t, err)
	assert.Contains(t, receivedPayload.Text, "test-tenant")
	assert.Contains(t, logBuf.String(), "slack alert sent successfully")
}

func TestSendAlert_WebhookError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	notifier := &SlackNotifier{
		webhookURL:  server.URL,
		serviceName: "test-service",
		httpClient:  server.Client(),
		logger:      logger,
	}

	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityCritical,
		Title:     "Test Alert",
		Timestamp: time.Now(),
	}

	err := notifier.SendAlert(context.Background(), alert)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSlackResponseError)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, logBuf.String(), "slack webhook returned error")
}

func TestSendAlert_ConnectionError(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	notifier := &SlackNotifier{
		webhookURL:  "https://invalid.host.example.com/webhook",
		serviceName: "test-service",
		httpClient:  &http.Client{Timeout: 100 * time.Millisecond},
		logger:      logger,
	}

	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityCritical,
		Title:     "Test Alert",
		Timestamp: time.Now(),
	}

	err := notifier.SendAlert(context.Background(), alert)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSlackWebhookFailed)
	assert.Contains(t, logBuf.String(), "slack webhook request failed")
}

func TestSendAlert_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second) //nolint:forbidigo // simulates slow server response to test context cancellation
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	notifier := &SlackNotifier{
		webhookURL:  server.URL,
		serviceName: "test-service",
		httpClient:  server.Client(),
		logger:      logger,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityCritical,
		Title:     "Test Alert",
		Timestamp: time.Now(),
	}

	err := notifier.SendAlert(ctx, alert)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSlackWebhookFailed)
}

func TestNotifyProvisioningFailure(t *testing.T) {
	var receivedPayload slackPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &SlackNotifier{
		webhookURL:  server.URL,
		serviceName: "tenant-service",
		httpClient:  server.Client(),
		logger:      slog.Default(),
	}

	tenant := &domain.Tenant{
		ID:              tenant.TenantID("provisioning-failed-tenant"),
		DisplayName:     "Failed Inc",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "schema migration failed: constraint violation",
	}

	err := notifier.NotifyProvisioningFailure(context.Background(), tenant)

	require.NoError(t, err)
	assert.Contains(t, receivedPayload.Text, "provisioning-failed-tenant")
	assert.Contains(t, receivedPayload.Text, "Tenant Provisioning Failed")

	// Verify it's marked as critical
	headerBlock := receivedPayload.Blocks[0]
	assert.Contains(t, headerBlock.Text.Text, "\U0001F534") // Red circle
}

func TestSendAlert_PayloadStructure(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &SlackNotifier{
		webhookURL:  server.URL,
		serviceName: "test-service",
		httpClient:  server.Client(),
		logger:      slog.Default(),
	}

	alert := Alert{
		TenantID:  tenant.TenantID("test-tenant"),
		Severity:  SeverityWarning,
		Title:     "Warning Alert",
		Message:   "Warning message",
		Timestamp: time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		Metadata: map[string]string{
			"display_name": "Test Corp",
		},
	}

	err := notifier.SendAlert(context.Background(), alert)
	require.NoError(t, err)

	// Verify JSON structure
	var payload map[string]interface{}
	err = json.Unmarshal(receivedBody, &payload)
	require.NoError(t, err)

	// Must have text field (fallback)
	assert.NotNil(t, payload["text"])

	// Must have blocks array
	blocks, ok := payload["blocks"].([]interface{})
	require.True(t, ok, "blocks should be an array")
	require.GreaterOrEqual(t, len(blocks), 2, "should have at least header and details blocks")

	// First block should be section with text
	firstBlock := blocks[0].(map[string]interface{})
	assert.Equal(t, "section", firstBlock["type"])
	textObj := firstBlock["text"].(map[string]interface{})
	assert.Equal(t, "mrkdwn", textObj["type"])
	assert.Contains(t, textObj["text"].(string), "\u26A0") // Warning emoji
}

// Helper function to check if any string in slice contains substring
func containsAny(strs []string, substr string) bool {
	for _, s := range strs {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
