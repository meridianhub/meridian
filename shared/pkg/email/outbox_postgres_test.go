package email_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

func setupOutboxTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(&email.OutboxEntity{}, &email.AuditLogEntity{}),
		testdb.WithTenant(testTenantID),
	)
}

func newTestOutboxEntry() *email.OutboxEntry {
	return &email.OutboxEntry{
		TenantID:       testTenantID,
		IdempotencyKey: uuid.NewString(),
		ToAddresses:    []string{"user@example.com"},
		FromAddress:    "noreply@meridianhub.cloud",
		Subject:        "Test Email",
		TemplateName:   "welcome",
		TemplateData:   map[string]any{"name": "Alice"},
		MaxAttempts:    5,
	}
}

// enqueueAndClaim is a helper that enqueues an entry and transitions it to
// SENDING via FetchDispatchable, returning the claimed entry.
func enqueueAndClaim(t *testing.T, ctx context.Context, repo *email.PostgresOutboxRepository, entry *email.OutboxEntry) email.OutboxEntry {
	t.Helper()
	require.NoError(t, repo.Enqueue(ctx, entry))
	entries, err := repo.FetchDispatchable(ctx, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, entry.ID, entries[0].ID)
	return entries[0]
}

func TestPostgresOutboxRepository_Enqueue(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()

	err := repo.Enqueue(ctx, entry)
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, entry.ID)
	assert.Equal(t, email.StatusPending, entry.Status)
	assert.False(t, entry.CreatedAt.IsZero())
}

func TestPostgresOutboxRepository_Enqueue_DuplicateIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()

	err := repo.Enqueue(ctx, entry)
	require.NoError(t, err)

	duplicate := newTestOutboxEntry()
	duplicate.IdempotencyKey = entry.IdempotencyKey
	err = repo.Enqueue(ctx, duplicate)
	require.ErrorIs(t, err, email.ErrDuplicateIdempotency)
}

func TestPostgresOutboxRepository_Enqueue_NegativeMaxAttempts(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()
	entry.MaxAttempts = -1

	err := repo.Enqueue(ctx, entry)
	require.ErrorIs(t, err, email.ErrInvalidMaxAttempts)
}

func TestPostgresOutboxRepository_FetchDispatchable(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)

	// Enqueue 3 entries
	for i := 0; i < 3; i++ {
		err := repo.Enqueue(ctx, newTestOutboxEntry())
		require.NoError(t, err)
	}

	entries, err := repo.FetchDispatchable(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 3)

	for _, e := range entries {
		assert.Equal(t, email.StatusSending, e.Status)
	}
}

func TestPostgresOutboxRepository_MarkSent(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()
	claimed := enqueueAndClaim(t, ctx, repo, entry)

	err := repo.MarkSent(ctx, claimed.ID)
	require.NoError(t, err)

	// Should no longer be dispatchable
	entries, err := repo.FetchDispatchable(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPostgresOutboxRepository_MarkSent_NotFound(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	err := repo.MarkSent(ctx, uuid.New())
	require.ErrorIs(t, err, email.ErrOutboxNotFound)
}

func TestPostgresOutboxRepository_MarkFailed(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()
	claimed := enqueueAndClaim(t, ctx, repo, entry)

	err := repo.MarkFailed(ctx, claimed.ID, "provider timeout")
	require.NoError(t, err)

	// Entry should not be immediately dispatchable (backoff)
	entries, err := repo.FetchDispatchable(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPostgresOutboxRepository_MarkFailed_NotFound(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	err := repo.MarkFailed(ctx, uuid.New(), "error")
	require.ErrorIs(t, err, email.ErrOutboxNotFound)
}

func TestPostgresOutboxRepository_Cancel(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()
	require.NoError(t, repo.Enqueue(ctx, entry))

	err := repo.Cancel(ctx, entry.ID)
	require.NoError(t, err)

	// Should no longer be dispatchable
	entries, err := repo.FetchDispatchable(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPostgresOutboxRepository_Cancel_NotFound(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	err := repo.Cancel(ctx, uuid.New())
	require.ErrorIs(t, err, email.ErrOutboxNotFound)
}

func TestPostgresOutboxRepository_Cancel_AlreadySent(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()
	claimed := enqueueAndClaim(t, ctx, repo, entry)
	require.NoError(t, repo.MarkSent(ctx, claimed.ID))

	// Cannot cancel a sent entry
	err := repo.Cancel(ctx, claimed.ID)
	require.ErrorIs(t, err, email.ErrOutboxNotFound)
}

func TestPostgresOutboxRepository_FetchDispatchable_RespectsNextAttemptAt(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)

	// Enqueue, claim, and fail an entry to push next_attempt_at into the future
	entry := newTestOutboxEntry()
	enqueueAndClaim(t, ctx, repo, entry)
	require.NoError(t, repo.MarkFailed(ctx, entry.ID, "timeout"))

	// Fresh entry should still be fetchable
	fresh := newTestOutboxEntry()
	require.NoError(t, repo.Enqueue(ctx, fresh))

	entries, err := repo.FetchDispatchable(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, fresh.ID, entries[0].ID)
}

func TestPostgresOutboxRepository_FetchDispatchable_LimitRespected(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)

	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Enqueue(ctx, newTestOutboxEntry()))
	}

	entries, err := repo.FetchDispatchable(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestPostgresOutboxRepository_Enqueue_TemplateDataRoundTrip(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()
	entry.TemplateData = map[string]any{
		"name":   "Alice",
		"amount": float64(99.99),
		"items":  []any{"widget", "gadget"},
	}

	require.NoError(t, repo.Enqueue(ctx, entry))

	entries, err := repo.FetchDispatchable(ctx, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "Alice", entries[0].TemplateData["name"])
	assert.Equal(t, float64(99.99), entries[0].TemplateData["amount"])

	items, ok := entries[0].TemplateData["items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 2)
}

func TestPostgresOutboxRepository_MissingTenantContext(t *testing.T) {
	db, _, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresOutboxRepository(db)
	entry := newTestOutboxEntry()

	// Use background context (no tenant)
	err := repo.Enqueue(context.Background(), entry)
	require.Error(t, err)
}
