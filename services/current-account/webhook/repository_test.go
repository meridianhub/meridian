package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupWebhookTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(&DeliveryEntity{}),
	)
}

func TestNewRepository(t *testing.T) {
	db, _, cleanup := setupWebhookTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	assert.NotNil(t, repo)
}

func TestRepository_RecordDelivery(t *testing.T) {
	db, ctx, cleanup := setupWebhookTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	record := &DeliveryRecord{
		ID:         uuid.New(),
		EventID:    "event-001",
		EventType:  EventTypeAccountFrozen,
		TenantID:   "tenant-1",
		AccountID:  "account-1",
		WebhookURL: "https://example.com/hook",
		Status:     DeliveryStatusPending,
		Attempts:   0,
		CreatedAt:  time.Now().Truncate(time.Microsecond),
	}

	err := repo.RecordDelivery(ctx, record)
	require.NoError(t, err)

	// Verify it was saved by retrieving it
	retrieved, err := repo.GetByID(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, record.ID, retrieved.ID)
	assert.Equal(t, record.EventID, retrieved.EventID)
	assert.Equal(t, record.EventType, retrieved.EventType)
	assert.Equal(t, record.TenantID, retrieved.TenantID)
	assert.Equal(t, record.Status, retrieved.Status)
}

func TestRepository_RecordDelivery_Update(t *testing.T) {
	db, ctx, cleanup := setupWebhookTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	id := uuid.New()
	record := &DeliveryRecord{
		ID:         id,
		EventID:    "event-001",
		EventType:  EventTypeAccountFrozen,
		TenantID:   "tenant-1",
		AccountID:  "account-1",
		WebhookURL: "https://example.com/hook",
		Status:     DeliveryStatusPending,
		Attempts:   0,
		CreatedAt:  time.Now().Truncate(time.Microsecond),
	}

	require.NoError(t, repo.RecordDelivery(ctx, record))

	// Update the record
	record.Status = DeliveryStatusSuccess
	record.Attempts = 1
	now := time.Now().Truncate(time.Microsecond)
	record.CompletedAt = &now

	require.NoError(t, repo.RecordDelivery(ctx, record))

	retrieved, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, retrieved.Status)
	assert.Equal(t, 1, retrieved.Attempts)
	assert.NotNil(t, retrieved.CompletedAt)
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	db, _, cleanup := setupWebhookTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	_, err := repo.GetByID(context.Background(), uuid.New())
	assert.Error(t, err)
}

func TestRepository_ListByTenant(t *testing.T) {
	db, ctx, cleanup := setupWebhookTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	// Create records for two tenants
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.RecordDelivery(ctx, &DeliveryRecord{
			ID:         uuid.New(),
			EventID:    "event-" + uuid.New().String()[:8],
			EventType:  EventTypeAccountFrozen,
			TenantID:   "tenant-A",
			AccountID:  "account-1",
			WebhookURL: "https://example.com/hook",
			Status:     DeliveryStatusPending,
			CreatedAt:  time.Now().Truncate(time.Microsecond),
		}))
	}
	require.NoError(t, repo.RecordDelivery(ctx, &DeliveryRecord{
		ID:         uuid.New(),
		EventID:    "event-other",
		EventType:  EventTypeAccountClosed,
		TenantID:   "tenant-B",
		AccountID:  "account-2",
		WebhookURL: "https://example.com/hook",
		Status:     DeliveryStatusSuccess,
		CreatedAt:  time.Now().Truncate(time.Microsecond),
	}))

	records, err := repo.ListByTenant(ctx, "tenant-A", 10)
	require.NoError(t, err)
	assert.Len(t, records, 3)

	// Verify tenant isolation
	records, err = repo.ListByTenant(ctx, "tenant-B", 10)
	require.NoError(t, err)
	assert.Len(t, records, 1)
}

func TestRepository_ListByAccount(t *testing.T) {
	db, ctx, cleanup := setupWebhookTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	require.NoError(t, repo.RecordDelivery(ctx, &DeliveryRecord{
		ID:         uuid.New(),
		EventID:    "event-1",
		EventType:  EventTypeAccountFrozen,
		TenantID:   "tenant-1",
		AccountID:  "account-A",
		WebhookURL: "https://example.com/hook",
		Status:     DeliveryStatusPending,
		CreatedAt:  time.Now().Truncate(time.Microsecond),
	}))
	require.NoError(t, repo.RecordDelivery(ctx, &DeliveryRecord{
		ID:         uuid.New(),
		EventID:    "event-2",
		EventType:  EventTypeAccountClosed,
		TenantID:   "tenant-1",
		AccountID:  "account-B",
		WebhookURL: "https://example.com/hook",
		Status:     DeliveryStatusSuccess,
		CreatedAt:  time.Now().Truncate(time.Microsecond),
	}))

	records, err := repo.ListByAccount(ctx, "account-A", 10)
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, "account-A", records[0].AccountID)
}

func TestRepository_CountByStatus(t *testing.T) {
	db, ctx, cleanup := setupWebhookTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	// Create mixed-status records
	for _, status := range []DeliveryStatus{DeliveryStatusPending, DeliveryStatusPending, DeliveryStatusSuccess} {
		require.NoError(t, repo.RecordDelivery(ctx, &DeliveryRecord{
			ID:         uuid.New(),
			EventID:    "event-" + uuid.New().String()[:8],
			EventType:  EventTypeAccountFrozen,
			TenantID:   "tenant-1",
			AccountID:  "account-1",
			WebhookURL: "https://example.com/hook",
			Status:     status,
			CreatedAt:  time.Now().Truncate(time.Microsecond),
		}))
	}

	count, err := repo.CountByStatus(ctx, "tenant-1", DeliveryStatusPending)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = repo.CountByStatus(ctx, "tenant-1", DeliveryStatusSuccess)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = repo.CountByStatus(ctx, "tenant-1", DeliveryStatusFailed)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestEntityDomainConversion_RoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Microsecond)
	attemptAt := now.Add(-time.Minute)
	completedAt := now
	errMsg := "connection refused"
	respCode := 503

	record := &DeliveryRecord{
		ID:            uuid.New(),
		EventID:       "event-123",
		EventType:     EventTypeAccountClosed,
		TenantID:      "tenant-conv",
		AccountID:     "account-conv",
		WebhookURL:    "https://example.com/hook",
		Status:        DeliveryStatusFailed,
		Attempts:      3,
		LastAttemptAt: &attemptAt,
		LastError:     &errMsg,
		ResponseCode:  &respCode,
		CreatedAt:     now,
		CompletedAt:   &completedAt,
	}

	// Convert to entity
	entity := toEntity(record)
	assert.Equal(t, record.ID, entity.ID)
	assert.Equal(t, record.EventID, entity.EventID)
	assert.Equal(t, string(record.EventType), entity.EventType)
	assert.Equal(t, record.TenantID, entity.TenantID)
	assert.Equal(t, record.AccountID, entity.AccountID)
	assert.Equal(t, record.WebhookURL, entity.WebhookURL)
	assert.Equal(t, string(record.Status), entity.Status)
	assert.Equal(t, record.Attempts, entity.Attempts)
	assert.Equal(t, record.LastError, entity.LastError)
	assert.Equal(t, record.ResponseCode, entity.ResponseCode)
	assert.Equal(t, record.CreatedAt, entity.CreatedAt)
	assert.Equal(t, record.LastAttemptAt, entity.LastAttemptAt)
	assert.Equal(t, record.CompletedAt, entity.CompletedAt)

	// Convert back to domain
	domain := toDomain(entity)
	assert.Equal(t, record.ID, domain.ID)
	assert.Equal(t, record.EventID, domain.EventID)
	assert.Equal(t, record.EventType, domain.EventType)
	assert.Equal(t, record.TenantID, domain.TenantID)
	assert.Equal(t, record.AccountID, domain.AccountID)
	assert.Equal(t, record.WebhookURL, domain.WebhookURL)
	assert.Equal(t, record.Status, domain.Status)
	assert.Equal(t, record.Attempts, domain.Attempts)
	assert.Equal(t, record.LastError, domain.LastError)
	assert.Equal(t, record.ResponseCode, domain.ResponseCode)
	assert.Equal(t, record.CreatedAt, domain.CreatedAt)
	assert.Equal(t, record.LastAttemptAt, domain.LastAttemptAt)
	assert.Equal(t, record.CompletedAt, domain.CompletedAt)
}

func TestToEntity_NilOptionalFields(t *testing.T) {
	record := &DeliveryRecord{
		ID:         uuid.New(),
		EventID:    "event-456",
		EventType:  EventTypeAccountFrozen,
		TenantID:   "tenant-test",
		AccountID:  "account-test",
		WebhookURL: "https://example.com/hook",
		Status:     DeliveryStatusPending,
		Attempts:   0,
		CreatedAt:  time.Now(),
	}

	entity := toEntity(record)
	assert.Nil(t, entity.LastAttemptAt)
	assert.Nil(t, entity.LastError)
	assert.Nil(t, entity.ResponseCode)
	assert.Nil(t, entity.CompletedAt)
}

func TestToDomain_NilOptionalFields(t *testing.T) {
	entity := &DeliveryEntity{
		ID:         uuid.New(),
		EventID:    "event-789",
		EventType:  string(EventTypeAccountClosed),
		TenantID:   "tenant-test",
		AccountID:  "account-test",
		WebhookURL: "https://example.com/hook",
		Status:     string(DeliveryStatusSuccess),
		Attempts:   1,
		CreatedAt:  time.Now(),
	}

	domain := toDomain(entity)
	assert.Nil(t, domain.LastAttemptAt)
	assert.Nil(t, domain.LastError)
	assert.Nil(t, domain.ResponseCode)
	assert.Nil(t, domain.CompletedAt)
}

func TestDeliveryEntityTableName(t *testing.T) {
	entity := DeliveryEntity{}
	assert.Equal(t, "webhook_deliveries", entity.TableName())
}

func TestDeliveryStatusConstants(t *testing.T) {
	assert.Equal(t, DeliveryStatus("pending"), DeliveryStatusPending)
	assert.Equal(t, DeliveryStatus("success"), DeliveryStatusSuccess)
	assert.Equal(t, DeliveryStatus("failed"), DeliveryStatusFailed)
}

func TestEventTypeConstants(t *testing.T) {
	assert.Equal(t, EventType("account.frozen"), EventTypeAccountFrozen)
	assert.Equal(t, EventType("account.closed"), EventTypeAccountClosed)
}
