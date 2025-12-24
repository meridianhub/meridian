package worker

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
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
