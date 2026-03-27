package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVerificationToken(t *testing.T) {
	tenantID := "test-tenant"
	identityID := uuid.New()

	vt, plaintext, err := NewVerificationToken(tenantID, identityID)

	require.NoError(t, err)
	assert.NotNil(t, vt)
	assert.NotEmpty(t, plaintext)
	assert.NotEqual(t, uuid.Nil, vt.ID())
	assert.Equal(t, tenantID, vt.TenantID())
	assert.Equal(t, identityID, vt.IdentityID())
	assert.NotEmpty(t, vt.TokenHash())
	assert.Nil(t, vt.ConsumedAt())
	assert.WithinDuration(t, time.Now().Add(tokens.EmailVerificationTokenTTL), vt.ExpiresAt(), 2*time.Second)
	assert.WithinDuration(t, time.Now(), vt.CreatedAt(), 2*time.Second)

	// Plaintext hashes to stored hash
	assert.True(t, tokens.ValidateTokenHash(plaintext, vt.TokenHash()))
}

func TestVerificationToken_Consume(t *testing.T) {
	tenantID := "test-tenant"
	identityID := uuid.New()

	vt, _, err := NewVerificationToken(tenantID, identityID)
	require.NoError(t, err)

	err = vt.Consume()
	require.NoError(t, err)
	assert.NotNil(t, vt.ConsumedAt())
	assert.WithinDuration(t, time.Now(), *vt.ConsumedAt(), 2*time.Second)
}

func TestVerificationToken_Consume_AlreadyConsumed(t *testing.T) {
	tenantID := "test-tenant"
	identityID := uuid.New()

	vt, _, err := NewVerificationToken(tenantID, identityID)
	require.NoError(t, err)

	err = vt.Consume()
	require.NoError(t, err)

	err = vt.Consume()
	assert.ErrorIs(t, err, ErrVerificationTokenAlreadyConsumed)
}

func TestVerificationToken_Consume_Expired(t *testing.T) {
	// Reconstruct an already-expired token
	vt := ReconstructVerificationToken(
		uuid.New(),
		"test-tenant",
		uuid.New(),
		"somehash",
		time.Now().Add(-1*time.Hour), // expired 1 hour ago
		nil,
		time.Now().Add(-25*time.Hour),
	)

	err := vt.Consume()
	assert.ErrorIs(t, err, ErrVerificationTokenExpired)
}

func TestReconstructVerificationToken(t *testing.T) {
	id := uuid.New()
	tenantID := "test-tenant"
	identityID := uuid.New()
	hash := "abc123"
	expiresAt := time.Now().Add(24 * time.Hour)
	now := time.Now()
	consumedAt := &now
	createdAt := time.Now().Add(-1 * time.Hour)

	vt := ReconstructVerificationToken(id, tenantID, identityID, hash, expiresAt, consumedAt, createdAt)

	assert.Equal(t, id, vt.ID())
	assert.Equal(t, tenantID, vt.TenantID())
	assert.Equal(t, identityID, vt.IdentityID())
	assert.Equal(t, hash, vt.TokenHash())
	assert.Equal(t, expiresAt, vt.ExpiresAt())
	assert.Equal(t, consumedAt, vt.ConsumedAt())
	assert.Equal(t, createdAt, vt.CreatedAt())
}
