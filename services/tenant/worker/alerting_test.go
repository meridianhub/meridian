package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/clients"
	"github.com/meridianhub/meridian/services/tenant/config"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/notifier"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAlertManager(t *testing.T) {
	_, repo := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	am := NewAlertManager(repo, logger)

	require.NotNil(t, am)
	assert.Equal(t, repo, am.repo)
	assert.Equal(t, logger, am.logger)
}

func TestCheckFailedProvisioningAlerts_NoFailedTenants(t *testing.T) {
	_, repo := setupTestDB(t)
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	am := NewAlertManager(repo, logger)

	ctx := context.Background()
	threshold := 1 * time.Hour

	err := am.CheckFailedProvisioningAlerts(ctx, threshold)

	// Should succeed with no failed tenants
	require.NoError(t, err)

	// Should not log any alerts
	logs := logBuf.String()
	assert.NotContains(t, logs, "tenant provisioning failure alert")
	assert.NotContains(t, logs, "found tenants with persistent provisioning failures")
}

func TestCheckFailedProvisioningAlerts_WithFailedTenants(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create a buffer to capture logs
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	am := NewAlertManager(repo, logger)

	ctx := context.Background()

	// Create two failed tenants
	failedTenant1 := &domain.Tenant{
		ID:              tenant.TenantID("failed_tenant_1"),
		DisplayName:     "Failed Tenant 1",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "database connection timeout",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, failedTenant1)
	require.NoError(t, err)
	// Set updated_at to match created_at for test purposes (GORM sets updated_at to NOW())
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant1.ID.String()).Error
	require.NoError(t, err)

	failedTenant2 := &domain.Tenant{
		ID:              tenant.TenantID("failed_tenant_2"),
		DisplayName:     "Failed Tenant 2",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "schema migration failed: constraint violation",
		CreatedAt:       time.Now().Add(-3 * time.Hour),
		Version:         1,
	}
	err = repo.Create(ctx, failedTenant2)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant2.ID.String()).Error
	require.NoError(t, err)

	// Create an active tenant (should not be included in alerts)
	activeTenant := &domain.Tenant{
		ID:              tenant.TenantID("active_tenant"),
		DisplayName:     "Active Tenant",
		SettlementAsset: "EUR",
		Status:          domain.StatusActive,
		CreatedAt:       time.Now().Add(-5 * time.Hour),
		Version:         1,
	}
	err = repo.Create(ctx, activeTenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", activeTenant.ID.String()).Error
	require.NoError(t, err)

	threshold := 1 * time.Hour

	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	logs := logBuf.String()

	// Should contain alerts for both failed tenants
	assert.Contains(t, logs, "tenant provisioning failure alert")
	assert.Contains(t, logs, "failed_tenant_1")
	assert.Contains(t, logs, "failed_tenant_2")
	assert.Contains(t, logs, "database connection timeout")
	assert.Contains(t, logs, "schema migration failed: constraint violation")
	assert.Contains(t, logs, "alert=tenant_provisioning_failed")

	// Should contain warning about failed tenants
	assert.Contains(t, logs, "found tenants with persistent provisioning failures")
	assert.Contains(t, logs, "count=2")

	// Should NOT contain active tenant
	assert.NotContains(t, logs, "active_tenant")
}

func TestCheckFailedProvisioningAlerts_RepositoryError(t *testing.T) {
	_, repo := setupTestDB(t)
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	am := NewAlertManager(repo, logger)

	// Use a cancelled context to force an error
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately to force repository error
	threshold := 1 * time.Hour

	// Note: This test verifies error handling when repository query fails
	err := am.CheckFailedProvisioningAlerts(ctx, threshold)

	// Should return an error (context canceled)
	require.Error(t, err)

	// Should log the error
	logs := logBuf.String()
	assert.Contains(t, logs, "failed to query provisioning_failed tenants")
	assert.Contains(t, logs, "error=")
}

func TestCheckFailedProvisioningAlerts_AlertStructuredFields(t *testing.T) {
	db, repo := setupTestDB(t)
	var logBuf safeBuffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	am := NewAlertManager(repo, logger)

	ctx := context.Background()

	// Create a failed tenant
	failedTenant := &domain.Tenant{
		ID:              tenant.TenantID("test_tenant"),
		DisplayName:     "Test Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "test error message",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, failedTenant)
	require.NoError(t, err)
	// Set updated_at to match created_at for test purposes (GORM sets updated_at to NOW())
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant.ID.String()).Error
	require.NoError(t, err)

	threshold := 1 * time.Hour

	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Verify structured logging fields are present (JSON format)
	logs := logBuf.String()
	assert.Contains(t, logs, `"alert":"tenant_provisioning_failed"`)
	assert.Contains(t, logs, `"tenant_id":"test_tenant"`)
	assert.Contains(t, logs, `"error_message":"test error message"`)
	assert.Contains(t, logs, `"status":"provisioning_failed"`)
	assert.Contains(t, logs, `"threshold_hours":1`)
}

// repositoryWithMock wraps a real repository and allows mocking specific methods
type repositoryWithMock struct {
	base                      *persistence.Repository
	listByStatusOlderThanFunc func(ctx context.Context, status domain.Status, cutoff time.Time) ([]*domain.Tenant, error)
}

func (m *repositoryWithMock) ListByStatusOlderThan(ctx context.Context, status domain.Status, cutoff time.Time) ([]*domain.Tenant, error) {
	if m.listByStatusOlderThanFunc != nil {
		return m.listByStatusOlderThanFunc(ctx, status, cutoff)
	}
	return m.base.ListByStatusOlderThan(ctx, status, cutoff)
}

func TestCheckFailedProvisioningAlerts_WithMockedRepository(t *testing.T) {
	t.Skip("Skipping - requires repository interface refactor for proper mocking")
	// This test demonstrates how the feature will work once ListByStatusOlderThan is implemented
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Mock repository that returns failed tenants
	mockRepo := &repositoryWithMock{
		listByStatusOlderThanFunc: func(_ context.Context, status domain.Status, _ time.Time) ([]*domain.Tenant, error) {
			assert.Equal(t, domain.StatusProvisioningFailed, status)
			return []*domain.Tenant{
				{
					ID:           tenant.TenantID("mock_failed_1"),
					DisplayName:  "Mock Failed 1",
					Status:       domain.StatusProvisioningFailed,
					ErrorMessage: "mock error 1",
					CreatedAt:    time.Now().Add(-2 * time.Hour),
				},
			}, nil
		},
	}

	am := &AlertManager{
		repo:   mockRepo.base, // Use base repository from mock
		logger: logger,
	}

	ctx := context.Background()
	threshold := 1 * time.Hour

	err := am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Verify alert was logged with correct fields
	logs := logBuf.String()
	assert.Contains(t, logs, "tenant provisioning failure alert")
	assert.Contains(t, logs, "alert=tenant_provisioning_failed")
	assert.Contains(t, logs, "tenant_id=mock_failed_1")
	assert.Contains(t, logs, "error_message=\"mock error 1\"")
}

func TestCheckFailedProvisioningAlerts_ThresholdRespected(t *testing.T) {
	t.Skip("Skipping - requires repository interface refactor for proper mocking")
	// This test verifies that the cutoff time calculation is correct
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var capturedCutoff time.Time
	mockRepo := &repositoryWithMock{
		listByStatusOlderThanFunc: func(_ context.Context, _ domain.Status, cutoff time.Time) ([]*domain.Tenant, error) {
			capturedCutoff = cutoff
			return []*domain.Tenant{}, nil
		},
	}

	am := &AlertManager{
		repo:   mockRepo.base,
		logger: logger,
	}

	ctx := context.Background()
	threshold := 2 * time.Hour
	beforeCall := time.Now().Add(-threshold)

	err := am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Verify cutoff time is approximately threshold ago (within 1 second tolerance)
	expectedCutoff := time.Now().Add(-threshold)
	assert.WithinDuration(t, expectedCutoff, capturedCutoff, 1*time.Second)
	assert.True(t, capturedCutoff.After(beforeCall) || capturedCutoff.Equal(beforeCall))
}

func TestCheckFailedProvisioningAlerts_LogsContainAlertLabel(t *testing.T) {
	t.Skip("Skipping - requires repository interface refactor for proper mocking")
	// Verify that logs contain the "alert" label for easy grep/parsing by monitoring systems
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockRepo := &repositoryWithMock{
		listByStatusOlderThanFunc: func(_ context.Context, _ domain.Status, _ time.Time) ([]*domain.Tenant, error) {
			return []*domain.Tenant{
				{
					ID:           tenant.TenantID("test"),
					DisplayName:  "Test",
					Status:       domain.StatusProvisioningFailed,
					ErrorMessage: "test error",
					CreatedAt:    time.Now().Add(-2 * time.Hour),
				},
			}, nil
		},
	}

	am := &AlertManager{
		repo:   mockRepo.base,
		logger: logger,
	}

	ctx := context.Background()
	err := am.CheckFailedProvisioningAlerts(ctx, 1*time.Hour)
	require.NoError(t, err)

	// The alert label makes it easy for monitoring tools to identify alerts
	logs := logBuf.String()

	// Check for the alert label in the log output
	// Looking for pattern: alert=tenant_provisioning_failed
	assert.True(t,
		strings.Contains(logs, "alert=tenant_provisioning_failed") ||
			strings.Contains(logs, `alert="tenant_provisioning_failed"`),
		"logs should contain alert label")
}

func TestNewAlertManager_WithSlackNotifier(t *testing.T) {
	_, repo := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	slackNotifier := notifier.NewSlackNotifier(notifier.SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled:     true,
			WebhookURL:  "https://hooks.slack.com/services/test",
			ServiceName: "test-service",
		},
	})

	am := NewAlertManager(repo, logger, WithSlackNotifier(slackNotifier))

	require.NotNil(t, am)
	assert.NotNil(t, am.slackNotifier)
}

func TestCheckFailedProvisioningAlerts_WithSlackNotifier(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create a mock Slack server
	var receivedPayloads []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		_ = json.Unmarshal(body, &payload)
		receivedPayloads = append(receivedPayloads, payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	slackNotifier := notifier.NewSlackNotifier(notifier.SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled:     true,
			WebhookURL:  server.URL,
			ServiceName: "tenant-service",
		},
		HTTPClient: server.Client(),
		Logger:     logger,
	})

	am := NewAlertManager(repo, logger, WithSlackNotifier(slackNotifier))

	ctx := context.Background()

	// Create a failed tenant
	failedTenant := &domain.Tenant{
		ID:              tenant.TenantID("slack_test_tenant"),
		DisplayName:     "Slack Test Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "database connection timeout",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, failedTenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant.ID.String()).Error
	require.NoError(t, err)

	threshold := 1 * time.Hour

	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Verify Slack received the alert
	require.Len(t, receivedPayloads, 1, "Slack should receive exactly one alert")

	payload := receivedPayloads[0]
	assert.Contains(t, payload["text"].(string), "slack_test_tenant")
	assert.Contains(t, payload["text"].(string), "Tenant Provisioning Failed")

	// Verify blocks structure
	blocks, ok := payload["blocks"].([]interface{})
	require.True(t, ok, "blocks should be an array")
	assert.GreaterOrEqual(t, len(blocks), 2, "should have at least header and details blocks")

	// Verify success log
	logs := logBuf.String()
	assert.Contains(t, logs, "slack alert sent successfully")
}

func TestCheckFailedProvisioningAlerts_SlackErrorHandling(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create a mock Slack server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	slackNotifier := notifier.NewSlackNotifier(notifier.SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled:     true,
			WebhookURL:  server.URL,
			ServiceName: "tenant-service",
		},
		HTTPClient: server.Client(),
		Logger:     logger,
	})

	// Use no-retry config to preserve original test behavior (fast test)
	retryConfig := config.AlertRetryConfig{
		MaxRetries:     0, // No retries - just initial attempt
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	}

	am := NewAlertManager(repo, logger,
		WithSlackNotifier(slackNotifier),
		WithRetryConfig(retryConfig),
	)

	ctx := context.Background()

	// Create a failed tenant
	failedTenant := &domain.Tenant{
		ID:              tenant.TenantID("slack_error_tenant"),
		DisplayName:     "Slack Error Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "test error",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, failedTenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant.ID.String()).Error
	require.NoError(t, err)

	threshold := 1 * time.Hour

	// CheckFailedProvisioningAlerts should NOT return error even if Slack fails
	// (Slack failures are logged but don't block the alert loop)
	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Verify error was logged
	logs := logBuf.String()
	assert.Contains(t, logs, "failed to send Slack alert")
	assert.Contains(t, logs, "slack_error_tenant")
}

// =============================================================================
// PagerDuty Integration Tests
// =============================================================================

func TestNewAlertManager_WithPagerDutyClient(t *testing.T) {
	_, repo := setupTestDB(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pdClient := clients.NewPagerDutyClient(config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-key",
		Source:     "test-source",
	})

	am := NewAlertManager(repo, logger, WithPagerDutyClient(pdClient))

	require.NotNil(t, am)
	assert.Equal(t, pdClient, am.pagerdutyClient)
}

func TestCheckFailedProvisioningAlerts_SendsPagerDutyAlert(t *testing.T) {
	db, repo := setupTestDB(t)

	// Track received PagerDuty events
	var receivedEvents []clients.PagerDutyEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var event clients.PagerDutyEvent
		err = json.Unmarshal(body, &event)
		require.NoError(t, err)
		receivedEvents = append(receivedEvents, event)

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"success","message":"Event processed"}`))
	}))
	defer server.Close()

	// Create PagerDuty client with test server
	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-routing-key",
			Source:     "tenant-service-test",
		},
		clients.WithEventsURL(server.URL),
	)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	am := NewAlertManager(repo, logger, WithPagerDutyClient(pdClient))

	ctx := context.Background()

	// Create a failed tenant
	failedTenant := &domain.Tenant{
		ID:              tenant.TenantID("pd_test_tenant"),
		DisplayName:     "PagerDuty Test Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "database schema creation failed",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, failedTenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant.ID.String()).Error
	require.NoError(t, err)

	threshold := 1 * time.Hour

	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Verify PagerDuty was called
	require.Len(t, receivedEvents, 1)

	event := receivedEvents[0]
	assert.Equal(t, "test-routing-key", event.RoutingKey)
	assert.Equal(t, clients.EventActionTrigger, event.EventAction)
	assert.Contains(t, event.DedupKey, "tenant-provisioning-failed-pd_test_tenant")
	assert.Contains(t, event.Payload.Summary, "pd_test_tenant")
	assert.Contains(t, event.Payload.Summary, "database schema creation failed")
	assert.Equal(t, "critical", event.Payload.Severity)
	assert.Equal(t, "tenant-service-test", event.Payload.Source)

	// Verify custom details
	customDetails := event.Payload.CustomDetails
	assert.Equal(t, "pd_test_tenant", customDetails["tenant_id"])
	assert.Equal(t, "PagerDuty Test Tenant", customDetails["display_name"])
	assert.Equal(t, "provisioning_failed", customDetails["status"])
	assert.Equal(t, "database schema creation failed", customDetails["error_message"])
}

func TestCheckFailedProvisioningAlerts_PagerDutyError_ContinuesProcessing(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create a server that returns errors
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","message":"Internal server error"}`))
	}))
	defer server.Close()

	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-key",
			Source:     "test-source",
		},
		clients.WithEventsURL(server.URL),
	)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Use no-retry config to preserve original test behavior
	retryConfig := config.AlertRetryConfig{
		MaxRetries:     0, // No retries - just initial attempt
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	}

	am := NewAlertManager(repo, logger,
		WithPagerDutyClient(pdClient),
		WithRetryConfig(retryConfig),
	)

	ctx := context.Background()

	// Create two failed tenants
	for i := 1; i <= 2; i++ {
		tenantObj := &domain.Tenant{
			ID:              tenant.TenantID("pd_tenant_" + string(rune('a'+i-1))),
			DisplayName:     "Test Tenant",
			SettlementAsset: "USD",
			Status:          domain.StatusProvisioningFailed,
			ErrorMessage:    "error",
			CreatedAt:       time.Now().Add(-2 * time.Hour),
			Version:         1,
		}
		err := repo.Create(ctx, tenantObj)
		require.NoError(t, err)
		err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenantObj.ID.String()).Error
		require.NoError(t, err)
	}

	threshold := 1 * time.Hour

	// Should not return an error even when PagerDuty fails
	err := am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Should have attempted to send alerts for both tenants (1 attempt each, no retries)
	assert.Equal(t, 2, callCount)

	// Should have logged the errors
	logs := logBuf.String()
	assert.Contains(t, logs, "failed to send PagerDuty alert")
}

func TestCheckFailedProvisioningAlerts_NoPagerDutyClient_LogsOnly(t *testing.T) {
	db, repo := setupTestDB(t)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create AlertManager without PagerDuty client
	am := NewAlertManager(repo, logger)

	ctx := context.Background()

	// Create a failed tenant
	failedTenant := &domain.Tenant{
		ID:              tenant.TenantID("log_only_tenant"),
		DisplayName:     "Log Only Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "test error",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, failedTenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant.ID.String()).Error
	require.NoError(t, err)

	threshold := 1 * time.Hour

	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Should log the alert
	logs := logBuf.String()
	assert.Contains(t, logs, "tenant provisioning failure alert")
	assert.Contains(t, logs, "log_only_tenant")

	// Should NOT attempt to send PagerDuty alert (no error logs about PagerDuty)
	assert.NotContains(t, logs, "failed to send PagerDuty alert")
}

func TestCheckFailedProvisioningAlerts_TruncatesLongErrorMessage(t *testing.T) {
	db, repo := setupTestDB(t)

	var receivedEvent clients.PagerDutyEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		err = json.Unmarshal(body, &receivedEvent)
		require.NoError(t, err)

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-key",
			Source:     "test-source",
		},
		clients.WithEventsURL(server.URL),
	)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	am := NewAlertManager(repo, logger, WithPagerDutyClient(pdClient))

	ctx := context.Background()

	// Create a tenant with a very long error message (>200 chars)
	longErrorMsg := strings.Repeat("x", 300)
	failedTenant := &domain.Tenant{
		ID:              tenant.TenantID("long_error_tenant"),
		DisplayName:     "Long Error Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    longErrorMsg,
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, failedTenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", failedTenant.ID.String()).Error
	require.NoError(t, err)

	threshold := 1 * time.Hour

	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Verify the summary was truncated (should end with "...")
	assert.LessOrEqual(t, len(receivedEvent.Payload.Summary), 300, "summary should be truncated")
	assert.Contains(t, receivedEvent.Payload.Summary, "...")

	// Verify the full error message is still in custom_details
	assert.Equal(t, longErrorMsg, receivedEvent.Payload.CustomDetails["error_message"])
}

// =============================================================================
// Rate Limiting Integration Tests
// =============================================================================

func TestCheckFailedProvisioningAlerts_RateLimiting(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create 15 failed tenants
	ctx := context.Background()
	for i := 1; i <= 15; i++ {
		tenant := &domain.Tenant{
			ID:              tenant.TenantID(fmt.Sprintf("rate_limit_tenant_%d", i)),
			DisplayName:     fmt.Sprintf("Tenant %d", i),
			SettlementAsset: "USD",
			Status:          domain.StatusProvisioningFailed,
			ErrorMessage:    "test error",
			CreatedAt:       time.Now().Add(-2 * time.Hour),
			Version:         1,
		}
		err := repo.Create(ctx, tenant)
		require.NoError(t, err)
		err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
		require.NoError(t, err)
	}

	// Track received PagerDuty events
	var receivedCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		receivedCount++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-key",
			Source:     "test-source",
		},
		clients.WithEventsURL(server.URL),
	)

	// Create rate limiter with max 10 alerts per minute
	rateLimiter := NewAlertRateLimiter(10, 10)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	am := NewAlertManager(repo, logger,
		WithPagerDutyClient(pdClient),
		WithRateLimiter(rateLimiter),
	)

	threshold := 1 * time.Hour
	err := am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Should have sent exactly 10 alerts (rate limited the rest)
	assert.Equal(t, 10, receivedCount)

	// Should have rate limit warnings in logs
	logs := logBuf.String()
	assert.Contains(t, logs, "rate limited")
}

func TestCheckFailedProvisioningAlerts_RateLimitMetric(t *testing.T) {
	db, repo := setupTestDB(t)

	// Create 5 failed tenants
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		tenant := &domain.Tenant{
			ID:              tenant.TenantID(fmt.Sprintf("metric_tenant_%d", i)),
			DisplayName:     fmt.Sprintf("Tenant %d", i),
			SettlementAsset: "USD",
			Status:          domain.StatusProvisioningFailed,
			ErrorMessage:    "test error",
			CreatedAt:       time.Now().Add(-2 * time.Hour),
			Version:         1,
		}
		err := repo.Create(ctx, tenant)
		require.NoError(t, err)
		err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
		require.NoError(t, err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-key",
			Source:     "test-source",
		},
		clients.WithEventsURL(server.URL),
	)

	// Track rate limit hits
	var rateLimitHits int
	rateLimiter := NewAlertRateLimiter(3, 3, WithRateLimitCallback(func(_ string) {
		rateLimitHits++
	}))

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	am := NewAlertManager(repo, logger,
		WithPagerDutyClient(pdClient),
		WithRateLimiter(rateLimiter),
	)

	threshold := 1 * time.Hour
	err := am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Should have 2 rate limit hits (5 tenants - 3 allowed = 2 blocked)
	assert.Equal(t, 2, rateLimitHits)
}

// =============================================================================
// Retry Logic Integration Tests
// =============================================================================

func TestCheckFailedProvisioningAlerts_RetryOnFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timing-sensitive test in short mode")
	}

	db, repo := setupTestDB(t)

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:              tenant.TenantID("retry_test_tenant"),
		DisplayName:     "Retry Test Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "test error",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
	require.NoError(t, err)

	// Create a server that fails twice then succeeds
	var attemptCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attemptCount++
		if attemptCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error","message":"server error"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-key",
			Source:     "test-source",
		},
		clients.WithEventsURL(server.URL),
	)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Use fast retry config for tests
	retryConfig := config.AlertRetryConfig{
		MaxRetries:     4,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	}

	am := NewAlertManager(repo, logger,
		WithPagerDutyClient(pdClient),
		WithRetryConfig(retryConfig),
	)

	threshold := 1 * time.Hour
	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Server should have been called 3 times (2 failures + 1 success)
	assert.Equal(t, 3, attemptCount)

	// Logs should show retry attempts
	logs := logBuf.String()
	assert.Contains(t, logs, "attempt failed")
}

func TestCheckFailedProvisioningAlerts_RetryExhausted_SendsToDLQ(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timing-sensitive test in short mode")
	}

	db, repo := setupTestDB(t)

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:              tenant.TenantID("dlq_test_tenant"),
		DisplayName:     "DLQ Test Tenant",
		SettlementAsset: "GBP",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "test error for DLQ",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
	require.NoError(t, err)

	// Create a server that always fails
	var attemptCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attemptCount++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","message":"persistent failure"}`))
	}))
	defer server.Close()

	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-key",
			Source:     "test-source",
		},
		clients.WithEventsURL(server.URL),
	)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Use fast retry config with 4 retries
	retryConfig := config.AlertRetryConfig{
		MaxRetries:     4,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	}

	dlq := NewAlertDeadLetterQueue()

	am := NewAlertManager(repo, logger,
		WithPagerDutyClient(pdClient),
		WithRetryConfig(retryConfig),
		WithDeadLetterQueue(dlq),
	)

	threshold := 1 * time.Hour
	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Server should have been called 5 times (initial + 4 retries)
	assert.Equal(t, 5, attemptCount)

	// Alert should be in DLQ
	assert.Equal(t, 1, dlq.Len())

	alerts := dlq.List()
	assert.Equal(t, AlertTypePagerDuty, alerts[0].AlertType)
	assert.Equal(t, "dlq_test_tenant", alerts[0].TenantID)
	assert.Contains(t, alerts[0].ErrorMessage, "error")
	assert.Equal(t, 5, alerts[0].AttemptCount)

	// Logs should indicate DLQ storage
	logs := logBuf.String()
	assert.Contains(t, logs, "sent to DLQ")
}

// =============================================================================
// DLQ Integration Tests
// =============================================================================

func TestCheckFailedProvisioningAlerts_NoDLQ_OnlyLogsError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timing-sensitive test in short mode")
	}

	db, repo := setupTestDB(t)

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:              tenant.TenantID("no_dlq_tenant"),
		DisplayName:     "No DLQ Tenant",
		SettlementAsset: "EUR",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "error without dlq",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
	require.NoError(t, err)

	// Always-failing server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error"}`))
	}))
	defer server.Close()

	pdClient := clients.NewPagerDutyClient(
		config.PagerDutyConfig{
			Enabled:    true,
			RoutingKey: "test-key",
			Source:     "test-source",
		},
		clients.WithEventsURL(server.URL),
	)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	retryConfig := config.AlertRetryConfig{
		MaxRetries:     1,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	}

	// No DLQ configured
	am := NewAlertManager(repo, logger,
		WithPagerDutyClient(pdClient),
		WithRetryConfig(retryConfig),
	)

	threshold := 1 * time.Hour
	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Should log the final error but not crash
	logs := logBuf.String()
	assert.Contains(t, logs, "failed to send PagerDuty alert after retries")
	// Should NOT contain DLQ message since DLQ is not configured
	assert.NotContains(t, logs, "sent to DLQ")
}

func TestAlertManager_DeadLetterQueue_Accessor(t *testing.T) {
	_, repo := setupTestDB(t)
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	t.Run("returns nil when not configured", func(t *testing.T) {
		am := NewAlertManager(repo, logger)
		assert.Nil(t, am.DeadLetterQueue())
	})

	t.Run("returns DLQ when configured", func(t *testing.T) {
		dlq := NewAlertDeadLetterQueue()
		am := NewAlertManager(repo, logger, WithDeadLetterQueue(dlq))
		assert.Equal(t, dlq, am.DeadLetterQueue())
	})
}

func TestCheckFailedProvisioningAlerts_SlackWithRetryAndDLQ(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timing-sensitive test in short mode")
	}

	db, repo := setupTestDB(t)

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:              tenant.TenantID("slack_dlq_tenant"),
		DisplayName:     "Slack DLQ Tenant",
		SettlementAsset: "USD",
		Status:          domain.StatusProvisioningFailed,
		ErrorMessage:    "slack test error",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		Version:         1,
	}
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
	require.NoError(t, err)

	// Always-failing Slack server
	var slackAttempts int
	slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		slackAttempts++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer slackServer.Close()

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	slackNotifier := notifier.NewSlackNotifier(notifier.SlackNotifierConfig{
		Config: config.SlackConfig{
			Enabled:     true,
			WebhookURL:  slackServer.URL,
			ServiceName: "tenant-service",
		},
		HTTPClient: slackServer.Client(),
		Logger:     logger,
	})

	retryConfig := config.AlertRetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	}

	dlq := NewAlertDeadLetterQueue()

	am := NewAlertManager(repo, logger,
		WithSlackNotifier(slackNotifier),
		WithRetryConfig(retryConfig),
		WithDeadLetterQueue(dlq),
	)

	threshold := 1 * time.Hour
	err = am.CheckFailedProvisioningAlerts(ctx, threshold)
	require.NoError(t, err)

	// Should have attempted 3 times (initial + 2 retries)
	assert.Equal(t, 3, slackAttempts)

	// Should be in DLQ
	assert.Equal(t, 1, dlq.Len())
	alerts := dlq.List()
	assert.Equal(t, AlertTypeSlack, alerts[0].AlertType)
	assert.Equal(t, "slack_dlq_tenant", alerts[0].TenantID)
}
