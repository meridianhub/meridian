package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockErr creates a test error by wrapping a message.
// This avoids err113 linter warnings about dynamic error creation in tests.
func mockErr(msg string) error {
	return fmt.Errorf("%s", msg)
}

// =============================================================================
// InMemoryAlertStore Tests
// =============================================================================

func TestInMemoryAlertStore_Record(t *testing.T) {
	store := NewInMemoryAlertStore()

	record := AlertRecord{
		AlertType: AlertTypePagerDuty,
		TenantID:  "test-tenant-1",
		Payload: AlertPayload{
			Summary:  "Test alert",
			Severity: "critical",
		},
		Timestamp: time.Now(),
		Success:   true,
	}

	store.Record(record)

	records := store.Records()
	require.Len(t, records, 1)
	assert.Equal(t, "test-tenant-1", records[0].TenantID)
	assert.Equal(t, AlertTypePagerDuty, records[0].AlertType)
}

func TestInMemoryAlertStore_Count(t *testing.T) {
	store := NewInMemoryAlertStore()

	assert.Equal(t, 0, store.Count())

	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t1"})
	store.Record(AlertRecord{AlertType: AlertTypeSlack, TenantID: "t2"})
	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t3"})

	assert.Equal(t, 3, store.Count())
}

func TestInMemoryAlertStore_CountByType(t *testing.T) {
	store := NewInMemoryAlertStore()

	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t1"})
	store.Record(AlertRecord{AlertType: AlertTypeSlack, TenantID: "t2"})
	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t3"})

	assert.Equal(t, 2, store.CountByType(AlertTypePagerDuty))
	assert.Equal(t, 1, store.CountByType(AlertTypeSlack))
}

func TestInMemoryAlertStore_CountSuccessful(t *testing.T) {
	store := NewInMemoryAlertStore()

	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t1", Success: true})
	store.Record(AlertRecord{AlertType: AlertTypeSlack, TenantID: "t2", Success: false})
	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t3", Success: true})

	assert.Equal(t, 2, store.CountSuccessful())
}

func TestInMemoryAlertStore_Clear(t *testing.T) {
	store := NewInMemoryAlertStore()

	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t1"})
	store.Record(AlertRecord{AlertType: AlertTypeSlack, TenantID: "t2"})

	assert.Equal(t, 2, store.Count())

	store.Clear()

	assert.Equal(t, 0, store.Count())
}

func TestInMemoryAlertStore_FindByTenantID(t *testing.T) {
	store := NewInMemoryAlertStore()

	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "tenant-a"})
	store.Record(AlertRecord{AlertType: AlertTypeSlack, TenantID: "tenant-b"})
	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "tenant-a"})

	records := store.FindByTenantID("tenant-a")
	require.Len(t, records, 2)
	for _, r := range records {
		assert.Equal(t, "tenant-a", r.TenantID)
	}

	records = store.FindByTenantID("tenant-b")
	require.Len(t, records, 1)
	assert.Equal(t, "tenant-b", records[0].TenantID)

	records = store.FindByTenantID("nonexistent")
	assert.Empty(t, records)
}

func TestInMemoryAlertStore_RecordsCopy(t *testing.T) {
	store := NewInMemoryAlertStore()

	store.Record(AlertRecord{AlertType: AlertTypePagerDuty, TenantID: "t1"})

	// Get records
	records := store.Records()
	require.Len(t, records, 1)

	// Modify the returned slice
	records[0].TenantID = "modified"

	// Original should be unchanged
	originalRecords := store.Records()
	assert.Equal(t, "t1", originalRecords[0].TenantID)
}

// =============================================================================
// MockPagerDutyClient Tests
// =============================================================================

func TestMockPagerDutyClient_TriggerAlert(t *testing.T) {
	mock := NewMockPagerDutyClient(true)

	ctx := context.Background()
	customDetails := map[string]any{
		"tenant_id":    "test-tenant-123",
		"display_name": "Test Tenant",
	}

	err := mock.TriggerAlert(ctx, "Test summary", "dedup-key", "critical", customDetails)
	require.NoError(t, err)

	assert.Equal(t, 1, mock.Calls())
	records := mock.Store().Records()
	require.Len(t, records, 1)
	assert.Equal(t, "test-tenant-123", records[0].TenantID)
	assert.Equal(t, "Test summary", records[0].Payload.Summary)
	assert.Equal(t, "dedup-key", records[0].Payload.DedupKey)
	assert.Equal(t, "critical", records[0].Payload.Severity)
	assert.True(t, records[0].Success)
}

func TestMockPagerDutyClient_SetFailNext(t *testing.T) {
	mock := NewMockPagerDutyClient(true)
	testErr := mockErr("simulated PagerDuty error")

	mock.SetFailNext(testErr)

	ctx := context.Background()
	err := mock.TriggerAlert(ctx, "Test", "key", "warning", nil)
	require.Error(t, err)
	assert.Equal(t, testErr, err)

	// Next call should succeed
	err = mock.TriggerAlert(ctx, "Test2", "key2", "info", nil)
	require.NoError(t, err)

	assert.Equal(t, 2, mock.Calls())
	// Only one successful record
	assert.Equal(t, 1, mock.Store().Count())
}

func TestMockPagerDutyClient_IsEnabled(t *testing.T) {
	enabledMock := NewMockPagerDutyClient(true)
	disabledMock := NewMockPagerDutyClient(false)

	assert.True(t, enabledMock.IsEnabled())
	assert.False(t, disabledMock.IsEnabled())
}

func TestMockPagerDutyClient_Reset(t *testing.T) {
	mock := NewMockPagerDutyClient(true)

	ctx := context.Background()
	_ = mock.TriggerAlert(ctx, "Test", "key", "info", nil)
	mock.SetFailNext(mockErr("error"))

	assert.Equal(t, 1, mock.Calls())
	assert.Equal(t, 1, mock.Store().Count())

	mock.Reset()

	assert.Equal(t, 0, mock.Calls())
	assert.Equal(t, 0, mock.Store().Count())
}

func TestMockPagerDutyClient_WithSharedStore(t *testing.T) {
	sharedStore := NewInMemoryAlertStore()

	mock1 := NewMockPagerDutyClient(true, WithMockPagerDutyStore(sharedStore))
	mock2 := NewMockPagerDutyClient(true, WithMockPagerDutyStore(sharedStore))

	ctx := context.Background()
	_ = mock1.TriggerAlert(ctx, "Alert 1", "key1", "critical", nil)
	_ = mock2.TriggerAlert(ctx, "Alert 2", "key2", "warning", nil)

	// Both should share the same store
	assert.Equal(t, 2, sharedStore.Count())
	assert.Equal(t, 2, mock1.Store().Count())
	assert.Equal(t, 2, mock2.Store().Count())
}

// =============================================================================
// MockSlackNotifier Tests
// =============================================================================

func TestMockSlackNotifier_NotifyProvisioningFailure(t *testing.T) {
	mock := NewMockSlackNotifier(true)

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:           tenant.TenantID("slack-test-tenant"),
		DisplayName:  "Slack Test Tenant",
		Status:       domain.StatusProvisioningFailed,
		ErrorMessage: "Test error message",
	}

	err := mock.NotifyProvisioningFailure(ctx, tenant)
	require.NoError(t, err)

	assert.Equal(t, 1, mock.Calls())
	records := mock.Store().Records()
	require.Len(t, records, 1)
	assert.Equal(t, AlertTypeSlack, records[0].AlertType)
	assert.Equal(t, "slack-test-tenant", records[0].TenantID)
	assert.True(t, records[0].Success)
}

func TestMockSlackNotifier_SetFailNext(t *testing.T) {
	mock := NewMockSlackNotifier(true)
	testErr := mockErr("simulated Slack error")

	mock.SetFailNext(testErr)

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:          tenant.TenantID("test-tenant"),
		DisplayName: "Test",
	}

	err := mock.NotifyProvisioningFailure(ctx, tenant)
	require.Error(t, err)
	assert.Equal(t, testErr, err)

	// Next call should succeed
	err = mock.NotifyProvisioningFailure(ctx, tenant)
	require.NoError(t, err)

	assert.Equal(t, 2, mock.Calls())
	// Only one successful record
	assert.Equal(t, 1, mock.Store().Count())
}

func TestMockSlackNotifier_IsEnabled(t *testing.T) {
	enabledMock := NewMockSlackNotifier(true)
	disabledMock := NewMockSlackNotifier(false)

	assert.True(t, enabledMock.IsEnabled())
	assert.False(t, disabledMock.IsEnabled())
}

func TestMockSlackNotifier_Reset(t *testing.T) {
	mock := NewMockSlackNotifier(true)

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:          tenant.TenantID("test"),
		DisplayName: "Test",
	}
	_ = mock.NotifyProvisioningFailure(ctx, tenant)
	mock.SetFailNext(mockErr("error"))

	assert.Equal(t, 1, mock.Calls())
	assert.Equal(t, 1, mock.Store().Count())

	mock.Reset()

	assert.Equal(t, 0, mock.Calls())
	assert.Equal(t, 0, mock.Store().Count())
}

func TestMockSlackNotifier_WithSharedStore(t *testing.T) {
	sharedStore := NewInMemoryAlertStore()

	mock1 := NewMockSlackNotifier(true, WithMockSlackStore(sharedStore))
	mock2 := NewMockSlackNotifier(true, WithMockSlackStore(sharedStore))

	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:          tenant.TenantID("test"),
		DisplayName: "Test",
	}
	_ = mock1.NotifyProvisioningFailure(ctx, tenant)
	_ = mock2.NotifyProvisioningFailure(ctx, tenant)

	// Both should share the same store
	assert.Equal(t, 2, sharedStore.Count())
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestInMemoryAlertStore_ConcurrentAccess(t *testing.T) {
	store := NewInMemoryAlertStore()
	const goroutines = 100

	done := make(chan bool)

	for i := 0; i < goroutines; i++ {
		go func() {
			store.Record(AlertRecord{
				AlertType: AlertTypePagerDuty,
				TenantID:  "tenant",
			})
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	assert.Equal(t, goroutines, store.Count())
}

func TestMockPagerDutyClient_ConcurrentTriggerAlert(t *testing.T) {
	mock := NewMockPagerDutyClient(true)
	const goroutines = 50

	done := make(chan bool)
	ctx := context.Background()

	for i := 0; i < goroutines; i++ {
		go func() {
			_ = mock.TriggerAlert(ctx, "Summary", "key", "info", nil)
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	assert.Equal(t, goroutines, mock.Calls())
	assert.Equal(t, goroutines, mock.Store().Count())
}

func TestMockSlackNotifier_ConcurrentNotify(t *testing.T) {
	mock := NewMockSlackNotifier(true)
	const goroutines = 50

	done := make(chan bool)
	ctx := context.Background()
	tenant := &domain.Tenant{
		ID:          tenant.TenantID("test"),
		DisplayName: "Test",
	}

	for i := 0; i < goroutines; i++ {
		go func() {
			_ = mock.NotifyProvisioningFailure(ctx, tenant)
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	assert.Equal(t, goroutines, mock.Calls())
	assert.Equal(t, goroutines, mock.Store().Count())
}
