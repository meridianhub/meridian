package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPasswordResetToken(t *testing.T) {
	tenantID := "test-tenant"
	identityID := uuid.New()

	prt, plaintext, err := NewPasswordResetToken(tenantID, identityID)

	require.NoError(t, err)
	assert.NotNil(t, prt)
	assert.NotEmpty(t, plaintext)
	assert.NotEqual(t, uuid.Nil, prt.ID())
	assert.Equal(t, tenantID, prt.TenantID())
	assert.Equal(t, identityID, prt.IdentityID())
	assert.NotEmpty(t, prt.TokenHash())
	assert.Nil(t, prt.ConsumedAt())
	assert.WithinDuration(t, time.Now().Add(tokens.PasswordResetTokenTTL), prt.ExpiresAt(), 2*time.Second)
	assert.WithinDuration(t, time.Now(), prt.CreatedAt(), 2*time.Second)

	// Plaintext hashes to stored hash
	assert.True(t, tokens.ValidateTokenHash(plaintext, prt.TokenHash()))
}

func TestPasswordResetToken_Consume(t *testing.T) {
	tenantID := "test-tenant"
	identityID := uuid.New()

	prt, _, err := NewPasswordResetToken(tenantID, identityID)
	require.NoError(t, err)

	err = prt.Consume()
	require.NoError(t, err)
	assert.NotNil(t, prt.ConsumedAt())
	assert.WithinDuration(t, time.Now(), *prt.ConsumedAt(), 2*time.Second)
}

func TestPasswordResetToken_Consume_AlreadyConsumed(t *testing.T) {
	tenantID := "test-tenant"
	identityID := uuid.New()

	prt, _, err := NewPasswordResetToken(tenantID, identityID)
	require.NoError(t, err)

	err = prt.Consume()
	require.NoError(t, err)

	err = prt.Consume()
	assert.ErrorIs(t, err, ErrPasswordResetTokenAlreadyConsumed)
}

func TestPasswordResetToken_Consume_Expired(t *testing.T) {
	prt := ReconstructPasswordResetToken(
		uuid.New(),
		"test-tenant",
		uuid.New(),
		"somehash",
		time.Now().Add(-1*time.Minute), // expired
		nil,
		time.Now().Add(-2*time.Hour),
	)

	err := prt.Consume()
	assert.ErrorIs(t, err, ErrPasswordResetTokenExpired)
}

func TestReconstructPasswordResetToken(t *testing.T) {
	id := uuid.New()
	tenantID := "test-tenant"
	identityID := uuid.New()
	hash := "abc123"
	expiresAt := time.Now().Add(1 * time.Hour)
	now := time.Now()
	consumedAt := &now
	createdAt := time.Now().Add(-30 * time.Minute)

	prt := ReconstructPasswordResetToken(id, tenantID, identityID, hash, expiresAt, consumedAt, createdAt)

	assert.Equal(t, id, prt.ID())
	assert.Equal(t, tenantID, prt.TenantID())
	assert.Equal(t, identityID, prt.IdentityID())
	assert.Equal(t, hash, prt.TokenHash())
	assert.Equal(t, expiresAt, prt.ExpiresAt())
	assert.Equal(t, consumedAt, prt.ConsumedAt())
	assert.Equal(t, createdAt, prt.CreatedAt())
}
