package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const tokenRepoTestTenantID = "test_tenant_token_repo"

func setupTokenRepoTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	return testdb.SetupTestDB(t,
		testdb.WithModels(
			&IdentityEntity{},
			&EmailVerificationTokenEntity{},
			&PasswordResetTokenEntity{},
		),
		testdb.WithTenant(tokenRepoTestTenantID),
	)
}

func createTestIdentityForTokens(t *testing.T, repo *Repository, ctx context.Context) *domain.Identity {
	t.Helper()
	identity, err := domain.NewIdentity(tokenRepoTestTenantID, "test-"+uuid.New().String()+"@example.com")
	require.NoError(t, err)
	err = identity.SetPassword("$2a$10$somehash")
	require.NoError(t, err)
	err = repo.Save(ctx, identity)
	require.NoError(t, err)
	return identity
}

// --- Verification Token Repository Tests ---

func TestRepository_SaveVerificationToken(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	vt, _, err := domain.NewVerificationToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)

	err = repo.SaveVerificationToken(ctx, vt)
	require.NoError(t, err)
}

func TestRepository_FindVerificationTokenByHash(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	vt, _, err := domain.NewVerificationToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)
	err = repo.SaveVerificationToken(ctx, vt)
	require.NoError(t, err)

	found, err := repo.FindVerificationTokenByHash(ctx, vt.TokenHash())
	require.NoError(t, err)
	assert.Equal(t, vt.ID(), found.ID())
	assert.Equal(t, vt.TenantID(), found.TenantID())
	assert.Equal(t, vt.IdentityID(), found.IdentityID())
	assert.Equal(t, vt.TokenHash(), found.TokenHash())
	assert.Nil(t, found.ConsumedAt())
}

func TestRepository_FindVerificationTokenByHash_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	_, err := repo.FindVerificationTokenByHash(ctx, "nonexistent")
	assert.ErrorIs(t, err, domain.ErrVerificationTokenNotFound)
}

func TestRepository_CountVerificationTokensInWindow(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	for i := 0; i < 3; i++ {
		vt, _, err := domain.NewVerificationToken(tokenRepoTestTenantID, identity.ID())
		require.NoError(t, err)
		err = repo.SaveVerificationToken(ctx, vt)
		require.NoError(t, err)
	}

	count, err := repo.CountVerificationTokensInWindow(ctx, identity.ID(), 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestRepository_CountVerificationTokensInWindow_ExcludesConsumed(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	// Create and consume one token
	vt1, _, err := domain.NewVerificationToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)
	err = vt1.Consume()
	require.NoError(t, err)
	err = repo.SaveVerificationToken(ctx, vt1)
	require.NoError(t, err)

	// Create one unconsumed token
	vt2, _, err := domain.NewVerificationToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)
	err = repo.SaveVerificationToken(ctx, vt2)
	require.NoError(t, err)

	count, err := repo.CountVerificationTokensInWindow(ctx, identity.ID(), 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestRepository_CountVerificationTokensInWindow_DifferentIdentity(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity1 := createTestIdentityForTokens(t, repo, ctx)
	identity2 := createTestIdentityForTokens(t, repo, ctx)

	vt, _, err := domain.NewVerificationToken(tokenRepoTestTenantID, identity1.ID())
	require.NoError(t, err)
	err = repo.SaveVerificationToken(ctx, vt)
	require.NoError(t, err)

	count, err := repo.CountVerificationTokensInWindow(ctx, identity2.ID(), 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// --- Password Reset Token Repository Tests ---

func TestRepository_SavePasswordResetToken(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	prt, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)

	err = repo.SavePasswordResetToken(ctx, prt)
	require.NoError(t, err)
}

func TestRepository_FindPasswordResetTokenByHash(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	prt, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)
	err = repo.SavePasswordResetToken(ctx, prt)
	require.NoError(t, err)

	found, err := repo.FindPasswordResetTokenByHash(ctx, prt.TokenHash())
	require.NoError(t, err)
	assert.Equal(t, prt.ID(), found.ID())
	assert.Equal(t, prt.TenantID(), found.TenantID())
	assert.Equal(t, prt.IdentityID(), found.IdentityID())
	assert.Equal(t, prt.TokenHash(), found.TokenHash())
	assert.Nil(t, found.ConsumedAt())
}

func TestRepository_FindPasswordResetTokenByHash_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	_, err := repo.FindPasswordResetTokenByHash(ctx, "nonexistent")
	assert.ErrorIs(t, err, domain.ErrPasswordResetTokenNotFound)
}

func TestRepository_CountPasswordResetTokensInWindow(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	for i := 0; i < 3; i++ {
		prt, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity.ID())
		require.NoError(t, err)
		err = repo.SavePasswordResetToken(ctx, prt)
		require.NoError(t, err)
	}

	count, err := repo.CountPasswordResetTokensInWindow(ctx, identity.ID(), 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestRepository_CountPasswordResetTokensInWindow_ExcludesConsumed(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	// Create and consume one token
	prt1, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)
	err = prt1.Consume()
	require.NoError(t, err)
	err = repo.SavePasswordResetToken(ctx, prt1)
	require.NoError(t, err)

	// Create one unconsumed token
	prt2, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity.ID())
	require.NoError(t, err)
	err = repo.SavePasswordResetToken(ctx, prt2)
	require.NoError(t, err)

	count, err := repo.CountPasswordResetTokensInWindow(ctx, identity.ID(), 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestRepository_MarkPasswordResetTokensConsumedForIdentity(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity := createTestIdentityForTokens(t, repo, ctx)

	var hashes []string
	for i := 0; i < 3; i++ {
		prt, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity.ID())
		require.NoError(t, err)
		err = repo.SavePasswordResetToken(ctx, prt)
		require.NoError(t, err)
		hashes = append(hashes, prt.TokenHash())
	}

	err := repo.MarkPasswordResetTokensConsumedForIdentity(ctx, identity.ID())
	require.NoError(t, err)

	for _, hash := range hashes {
		found, err := repo.FindPasswordResetTokenByHash(ctx, hash)
		require.NoError(t, err)
		assert.NotNil(t, found.ConsumedAt(), "token %s should be consumed", hash)
	}

	count, err := repo.CountPasswordResetTokensInWindow(ctx, identity.ID(), 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestRepository_MarkPasswordResetTokensConsumedForIdentity_DoesNotAffectOther(t *testing.T) {
	db, ctx, cleanup := setupTokenRepoTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	identity1 := createTestIdentityForTokens(t, repo, ctx)
	identity2 := createTestIdentityForTokens(t, repo, ctx)

	prt1, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity1.ID())
	require.NoError(t, err)
	err = repo.SavePasswordResetToken(ctx, prt1)
	require.NoError(t, err)

	prt2, _, err := domain.NewPasswordResetToken(tokenRepoTestTenantID, identity2.ID())
	require.NoError(t, err)
	err = repo.SavePasswordResetToken(ctx, prt2)
	require.NoError(t, err)

	err = repo.MarkPasswordResetTokensConsumedForIdentity(ctx, identity1.ID())
	require.NoError(t, err)

	found1, err := repo.FindPasswordResetTokenByHash(ctx, prt1.TokenHash())
	require.NoError(t, err)
	assert.NotNil(t, found1.ConsumedAt())

	found2, err := repo.FindPasswordResetTokenByHash(ctx, prt2.TokenHash())
	require.NoError(t, err)
	assert.Nil(t, found2.ConsumedAt())
}
