package email_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresAuditRepository_Record(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresAuditRepository(db)
	now := time.Now().UTC()
	providerID := "ses-msg-123"

	entry := &email.AuditEntry{
		TenantID:     testTenantID,
		OutboxID:     uuid.New(),
		ProviderID:   &providerID,
		ToAddresses:  []string{"user@example.com"},
		FromAddress:  "noreply@meridianhub.cloud",
		Subject:      "Test Email",
		TemplateName: "welcome",
		Status:       email.AuditStatusSent,
		SentAt:       &now,
		ProviderResponse: map[string]any{
			"messageId": "ses-msg-123",
		},
	}

	err := repo.Record(ctx, entry)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, entry.ID)
}

func TestPostgresAuditRepository_FindByOutboxID(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresAuditRepository(db)
	outboxID := uuid.New()
	now := time.Now().UTC()
	providerID := "ses-msg-456"

	// Record two audit entries for the same outbox ID
	for _, status := range []email.AuditStatus{email.AuditStatusSent, email.AuditStatusDelivered} {
		entry := &email.AuditEntry{
			TenantID:     testTenantID,
			OutboxID:     outboxID,
			ProviderID:   &providerID,
			ToAddresses:  []string{"user@example.com"},
			FromAddress:  "noreply@meridianhub.cloud",
			Subject:      "Test Email",
			TemplateName: "welcome",
			Status:       status,
			SentAt:       &now,
		}
		require.NoError(t, repo.Record(ctx, entry))
	}

	entries, err := repo.FindByOutboxID(ctx, outboxID)
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	// Most recent first
	assert.Equal(t, outboxID, entries[0].OutboxID)
}

func TestPostgresAuditRepository_FindByOutboxID_Empty(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresAuditRepository(db)
	entries, err := repo.FindByOutboxID(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPostgresAuditRepository_Record_ProviderResponseRoundTrip(t *testing.T) {
	db, ctx, cleanup := setupOutboxTestDB(t)
	defer cleanup()

	repo := email.NewPostgresAuditRepository(db)
	outboxID := uuid.New()
	now := time.Now().UTC()
	providerID := "ses-msg-789"

	entry := &email.AuditEntry{
		TenantID:     testTenantID,
		OutboxID:     outboxID,
		ProviderID:   &providerID,
		ToAddresses:  []string{"user@example.com"},
		FromAddress:  "noreply@meridianhub.cloud",
		Subject:      "Test",
		TemplateName: "invoice",
		Status:       email.AuditStatusSent,
		SentAt:       &now,
		ProviderResponse: map[string]any{
			"messageId":  "ses-msg-789",
			"requestId":  "req-abc",
			"statusCode": float64(200),
		},
	}

	require.NoError(t, repo.Record(ctx, entry))

	entries, err := repo.FindByOutboxID(ctx, outboxID)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "ses-msg-789", entries[0].ProviderResponse["messageId"])
	assert.Equal(t, float64(200), entries[0].ProviderResponse["statusCode"])
}
