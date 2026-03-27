package persistence

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var tokenModels = []interface{}{
	&IdentityEntity{},
	&EmailVerificationTokenEntity{},
	&PasswordResetTokenEntity{},
}

func setupTokenTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupCockroachDB(t, tokenModels)
}

// TestEmailVerificationToken_InsertAndRetrieve verifies round-trip persistence.
func TestEmailVerificationToken_InsertAndRetrieve(t *testing.T) {
	db, cleanup := setupTokenTestDB(t)
	defer cleanup()

	identityID := uuid.New()
	identity := &IdentityEntity{
		ID:           identityID,
		TenantID:     testTenantIDStr,
		Email:        "verify@example.com",
		Status:       "PENDING_INVITE",
		PasswordHash: "",
	}
	require.NoError(t, db.Create(identity).Error)

	token := &EmailVerificationTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  "abc123hashvalue0000000000000000000000000000000000000000000000000",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.Create(token).Error)
	assert.NotEqual(t, uuid.Nil, token.ID)

	var retrieved EmailVerificationTokenEntity
	err := db.Where("token_hash = ?", token.TokenHash).First(&retrieved).Error
	require.NoError(t, err)
	assert.Equal(t, token.ID, retrieved.ID)
	assert.Equal(t, identityID, retrieved.IdentityID)
	assert.Equal(t, testTenantIDStr, retrieved.TenantID)
	assert.Nil(t, retrieved.ConsumedAt)
}

// TestEmailVerificationToken_UniqueHashConstraint verifies duplicate token_hash is rejected.
func TestEmailVerificationToken_UniqueHashConstraint(t *testing.T) {
	db, cleanup := setupTokenTestDB(t)
	defer cleanup()

	identityID := uuid.New()
	identity := &IdentityEntity{
		ID:           identityID,
		TenantID:     testTenantIDStr,
		Email:        "dupe@example.com",
		Status:       "PENDING_INVITE",
		PasswordHash: "",
	}
	require.NoError(t, db.Create(identity).Error)

	hash := "dupehash000000000000000000000000000000000000000000000000000000000"
	token1 := &EmailVerificationTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  hash,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.Create(token1).Error)

	token2 := &EmailVerificationTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  hash,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	err := db.Create(token2).Error
	assert.Error(t, err, "expected unique constraint violation on token_hash")
}

// TestEmailVerificationToken_CascadeDelete verifies tokens are removed when identity is deleted.
func TestEmailVerificationToken_CascadeDelete(t *testing.T) {
	db, cleanup := setupTokenTestDB(t)
	defer cleanup()

	identityID := uuid.New()
	identity := &IdentityEntity{
		ID:           identityID,
		TenantID:     testTenantIDStr,
		Email:        "cascade@example.com",
		Status:       "PENDING_INVITE",
		PasswordHash: "",
	}
	require.NoError(t, db.Create(identity).Error)

	token := &EmailVerificationTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  "cascadehash0000000000000000000000000000000000000000000000000000000",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.Create(token).Error)

	require.NoError(t, db.Delete(identity).Error)

	var count int64
	db.Model(&EmailVerificationTokenEntity{}).Where("identity_id = ?", identityID).Count(&count)
	assert.Equal(t, int64(0), count, "tokens should be cascade deleted with identity")
}

// TestPasswordResetToken_InsertAndRetrieve verifies round-trip persistence.
func TestPasswordResetToken_InsertAndRetrieve(t *testing.T) {
	db, cleanup := setupTokenTestDB(t)
	defer cleanup()

	identityID := uuid.New()
	identity := &IdentityEntity{
		ID:           identityID,
		TenantID:     testTenantIDStr,
		Email:        "reset@example.com",
		Status:       "ACTIVE",
		PasswordHash: "hashed",
	}
	require.NoError(t, db.Create(identity).Error)

	token := &PasswordResetTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  "resethash00000000000000000000000000000000000000000000000000000000",
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}
	require.NoError(t, db.Create(token).Error)
	assert.NotEqual(t, uuid.Nil, token.ID)

	var retrieved PasswordResetTokenEntity
	err := db.Where("token_hash = ?", token.TokenHash).First(&retrieved).Error
	require.NoError(t, err)
	assert.Equal(t, token.ID, retrieved.ID)
	assert.Equal(t, identityID, retrieved.IdentityID)
	assert.Equal(t, testTenantIDStr, retrieved.TenantID)
	assert.Nil(t, retrieved.ConsumedAt)
}

// TestPasswordResetToken_UniqueHashConstraint verifies duplicate token_hash is rejected.
func TestPasswordResetToken_UniqueHashConstraint(t *testing.T) {
	db, cleanup := setupTokenTestDB(t)
	defer cleanup()

	identityID := uuid.New()
	identity := &IdentityEntity{
		ID:           identityID,
		TenantID:     testTenantIDStr,
		Email:        "dupepr@example.com",
		Status:       "ACTIVE",
		PasswordHash: "hashed",
	}
	require.NoError(t, db.Create(identity).Error)

	hash := "dupeprhash0000000000000000000000000000000000000000000000000000000"
	token1 := &PasswordResetTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  hash,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}
	require.NoError(t, db.Create(token1).Error)

	token2 := &PasswordResetTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  hash,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}
	err := db.Create(token2).Error
	assert.Error(t, err, "expected unique constraint violation on token_hash")
}

// TestPasswordResetToken_CascadeDelete verifies tokens are removed when identity is deleted.
func TestPasswordResetToken_CascadeDelete(t *testing.T) {
	db, cleanup := setupTokenTestDB(t)
	defer cleanup()

	identityID := uuid.New()
	identity := &IdentityEntity{
		ID:           identityID,
		TenantID:     testTenantIDStr,
		Email:        "cascadepr@example.com",
		Status:       "ACTIVE",
		PasswordHash: "hashed",
	}
	require.NoError(t, db.Create(identity).Error)

	token := &PasswordResetTokenEntity{
		TenantID:   testTenantIDStr,
		IdentityID: identityID,
		TokenHash:  "cascadeprhash000000000000000000000000000000000000000000000000000",
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}
	require.NoError(t, db.Create(token).Error)

	require.NoError(t, db.Delete(identity).Error)

	var count int64
	db.Model(&PasswordResetTokenEntity{}).Where("identity_id = ?", identityID).Count(&count)
	assert.Equal(t, int64(0), count, "tokens should be cascade deleted with identity")
}
