package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInvitation_CreatesValidInvitation(t *testing.T) {
	identityID := uuid.New()
	invitedBy := uuid.New()

	inv, plaintext, err := NewInvitation(identityID, invitedBy)
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, inv.ID())
	assert.Equal(t, identityID, inv.IdentityID())
	assert.Equal(t, invitedBy, inv.InvitedBy())
	assert.Equal(t, InvitationStatusPending, inv.Status())
	assert.NotEmpty(t, plaintext)
	assert.NotEmpty(t, inv.TokenHash())
	assert.NotEqual(t, plaintext, inv.TokenHash(), "plaintext should not equal stored hash")
	assert.True(t, inv.ExpiresAt().After(time.Now()))
	assert.NotZero(t, inv.CreatedAt())
	assert.NotZero(t, inv.UpdatedAt())
}

func TestInvitation_Accept_Success(t *testing.T) {
	identityID := uuid.New()
	invitedBy := uuid.New()

	inv, _, err := NewInvitation(identityID, invitedBy)
	require.NoError(t, err)

	err = inv.Accept()
	require.NoError(t, err)
	assert.Equal(t, InvitationStatusAccepted, inv.Status())
}

func TestInvitation_Accept_AlreadyAccepted(t *testing.T) {
	inv, _, _ := NewInvitation(uuid.New(), uuid.New())
	_ = inv.Accept()

	err := inv.Accept()
	assert.ErrorIs(t, err, ErrInvitationAlreadyAccepted)
}

func TestInvitation_Accept_Expired(t *testing.T) {
	// Reconstruct an expired invitation
	inv := ReconstructInvitation(
		uuid.New(), uuid.New(), uuid.New(),
		"somehash",
		time.Now().Add(-time.Hour), // expired
		InvitationStatusPending,
		time.Now().Add(-2*time.Hour),
		time.Now().Add(-2*time.Hour),
	)

	err := inv.Accept()
	assert.ErrorIs(t, err, ErrInvitationExpired)
}

func TestReconstructInvitation(t *testing.T) {
	id := uuid.New()
	identityID := uuid.New()
	invitedBy := uuid.New()
	hash := "abc123hash"
	expiry := time.Now().Add(24 * time.Hour)
	now := time.Now()

	inv := ReconstructInvitation(
		id, identityID, invitedBy, hash, expiry,
		InvitationStatusAccepted, now, now,
	)

	assert.Equal(t, id, inv.ID())
	assert.Equal(t, identityID, inv.IdentityID())
	assert.Equal(t, invitedBy, inv.InvitedBy())
	assert.Equal(t, hash, inv.TokenHash())
	assert.Equal(t, InvitationStatusAccepted, inv.Status())
}
