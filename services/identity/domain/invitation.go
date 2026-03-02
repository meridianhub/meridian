package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
)

// InvitationStatus represents the lifecycle state of an invitation.
type InvitationStatus string

// Invitation status constants.
const (
	InvitationStatusPending  InvitationStatus = "PENDING"
	InvitationStatusAccepted InvitationStatus = "ACCEPTED"
)

// Invitation represents a pending invite for an identity to join the platform.
type Invitation struct {
	id         uuid.UUID
	identityID uuid.UUID
	invitedBy  uuid.UUID
	tokenHash  string
	expiresAt  time.Time
	status     InvitationStatus
	createdAt  time.Time
	updatedAt  time.Time
}

// NewInvitation creates a new invitation and returns both the domain object and
// the plaintext token that must be delivered to the invitee. The plaintext token
// is never stored; only its SHA256 hash is persisted.
func NewInvitation(identityID, invitedBy uuid.UUID) (*Invitation, string, error) {
	plaintext, hash, err := tokens.GenerateToken(tokens.InvitationTokenLength)
	if err != nil {
		return nil, "", err
	}

	now := time.Now()
	inv := &Invitation{
		id:         uuid.New(),
		identityID: identityID,
		invitedBy:  invitedBy,
		tokenHash:  hash,
		expiresAt:  now.Add(tokens.InvitationTokenTTL),
		status:     InvitationStatusPending,
		createdAt:  now,
		updatedAt:  now,
	}
	return inv, plaintext, nil
}

// ReconstructInvitation recreates an Invitation from persistence layer data.
func ReconstructInvitation(
	id uuid.UUID,
	identityID uuid.UUID,
	invitedBy uuid.UUID,
	tokenHash string,
	expiresAt time.Time,
	status InvitationStatus,
	createdAt time.Time,
	updatedAt time.Time,
) *Invitation {
	return &Invitation{
		id:         id,
		identityID: identityID,
		invitedBy:  invitedBy,
		tokenHash:  tokenHash,
		expiresAt:  expiresAt,
		status:     status,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}
}

// ID returns the invitation's unique identifier.
func (inv *Invitation) ID() uuid.UUID {
	return inv.id
}

// IdentityID returns the identity this invitation is for.
func (inv *Invitation) IdentityID() uuid.UUID {
	return inv.identityID
}

// InvitedBy returns the identity that created the invitation.
func (inv *Invitation) InvitedBy() uuid.UUID {
	return inv.invitedBy
}

// TokenHash returns the SHA256 hash of the invitation token.
func (inv *Invitation) TokenHash() string {
	return inv.tokenHash
}

// ExpiresAt returns when the invitation expires.
func (inv *Invitation) ExpiresAt() time.Time {
	return inv.expiresAt
}

// Status returns the invitation's current status.
func (inv *Invitation) Status() InvitationStatus {
	return inv.status
}

// CreatedAt returns when the invitation was created.
func (inv *Invitation) CreatedAt() time.Time {
	return inv.createdAt
}

// UpdatedAt returns when the invitation was last updated.
func (inv *Invitation) UpdatedAt() time.Time {
	return inv.updatedAt
}

// Accept marks the invitation as accepted.
// Returns ErrInvitationExpired if the invitation has passed its expiry time.
// Returns ErrInvitationAlreadyAccepted if it has already been accepted.
func (inv *Invitation) Accept() error {
	if inv.status == InvitationStatusAccepted {
		return ErrInvitationAlreadyAccepted
	}
	if !time.Now().Before(inv.expiresAt) {
		return ErrInvitationExpired
	}
	inv.status = InvitationStatusAccepted
	inv.updatedAt = time.Now()
	return nil
}
