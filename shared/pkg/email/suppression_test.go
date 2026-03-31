package email_test

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSuppressionTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(&email.SuppressedAddressEntity{}),
		testdb.WithTenant(testTenantID),
	)
}

func TestPostgresSuppressionRepository_IsSuppressed_NotFound(t *testing.T) {
	db, _, cleanup := setupSuppressionTestDB(t)
	defer cleanup()

	repo := email.NewPostgresSuppressionRepository(db)

	suppressed, err := repo.IsSuppressed(context.Background(), "unknown@example.com")
	require.NoError(t, err)
	assert.False(t, suppressed)
}

func TestPostgresSuppressionRepository_AddAndCheck(t *testing.T) {
	db, _, cleanup := setupSuppressionTestDB(t)
	defer cleanup()

	repo := email.NewPostgresSuppressionRepository(db)

	entry := &email.SuppressionEntry{
		EmailAddress:    "bounced@example.com",
		SuppressionType: email.SuppressionBounce,
		ProviderID:      "resend-123",
		Reason:          "hard bounce",
		TenantID:        testTenantID,
	}
	err := repo.AddSuppression(context.Background(), entry)
	require.NoError(t, err)

	suppressed, err := repo.IsSuppressed(context.Background(), "bounced@example.com")
	require.NoError(t, err)
	assert.True(t, suppressed)
}

func TestPostgresSuppressionRepository_CaseInsensitive(t *testing.T) {
	db, _, cleanup := setupSuppressionTestDB(t)
	defer cleanup()

	repo := email.NewPostgresSuppressionRepository(db)

	err := repo.AddSuppression(context.Background(), &email.SuppressionEntry{
		EmailAddress:    "User@Example.COM",
		SuppressionType: email.SuppressionComplaint,
		TenantID:        testTenantID,
	})
	require.NoError(t, err)

	suppressed, err := repo.IsSuppressed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.True(t, suppressed)
}

func TestPostgresSuppressionRepository_UpsertOnConflict(t *testing.T) {
	db, _, cleanup := setupSuppressionTestDB(t)
	defer cleanup()

	repo := email.NewPostgresSuppressionRepository(db)

	// First insert: bounce
	err := repo.AddSuppression(context.Background(), &email.SuppressionEntry{
		EmailAddress:    "user@example.com",
		SuppressionType: email.SuppressionBounce,
		ProviderID:      "id-1",
		TenantID:        testTenantID,
	})
	require.NoError(t, err)

	// Second insert: complaint (same tenant+email) - should upsert, not error
	err = repo.AddSuppression(context.Background(), &email.SuppressionEntry{
		EmailAddress:    "user@example.com",
		SuppressionType: email.SuppressionComplaint,
		ProviderID:      "id-2",
		TenantID:        testTenantID,
	})
	require.NoError(t, err)

	// Should still be suppressed
	suppressed, err := repo.IsSuppressed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.True(t, suppressed)
}

func TestPostgresSuppressionRepository_CrossTenantCheck(t *testing.T) {
	db, _, cleanup := setupSuppressionTestDB(t)
	defer cleanup()

	repo := email.NewPostgresSuppressionRepository(db)

	// Suppress in one tenant
	err := repo.AddSuppression(context.Background(), &email.SuppressionEntry{
		EmailAddress:    "shared@example.com",
		SuppressionType: email.SuppressionBounce,
		TenantID:        "tenant-A",
	})
	require.NoError(t, err)

	// IsSuppressed is cross-tenant - should find it regardless of tenant context
	suppressed, err := repo.IsSuppressed(context.Background(), "shared@example.com")
	require.NoError(t, err)
	assert.True(t, suppressed)
}

func TestPostgresSuppressionRepository_NilEntry(t *testing.T) {
	db, _, cleanup := setupSuppressionTestDB(t)
	defer cleanup()

	repo := email.NewPostgresSuppressionRepository(db)

	err := repo.AddSuppression(context.Background(), nil)
	require.Error(t, err)
}
