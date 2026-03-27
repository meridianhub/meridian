package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
)

// PasswordResetToken represents a password reset token issued during the forgot-password flow.
type PasswordResetToken struct {
	id         uuid.UUID
	tenantID   string
	identityID uuid.UUID
	tokenHash  string
	expiresAt  time.Time
	consumedAt *time.Time
	createdAt  time.Time
}

// NewPasswordResetToken creates a new password reset token and returns both the domain object
// and the plaintext token that must be delivered to the user. The plaintext token is never
// stored; only its SHA256 hash is persisted.
func NewPasswordResetToken(tenantID string, identityID uuid.UUID) (*PasswordResetToken, string, error) {
	plaintext, hash, err := tokens.GenerateToken(tokens.PasswordResetTokenLength)
	if err != nil {
		return nil, "", err
	}

	now := time.Now()
	prt := &PasswordResetToken{
		id:         uuid.New(),
		tenantID:   tenantID,
		identityID: identityID,
		tokenHash:  hash,
		expiresAt:  now.Add(tokens.PasswordResetTokenTTL),
		createdAt:  now,
	}
	return prt, plaintext, nil
}

// ReconstructPasswordResetToken recreates a PasswordResetToken from persistence layer data.
func ReconstructPasswordResetToken(
	id uuid.UUID,
	tenantID string,
	identityID uuid.UUID,
	tokenHash string,
	expiresAt time.Time,
	consumedAt *time.Time,
	createdAt time.Time,
) *PasswordResetToken {
	return &PasswordResetToken{
		id:         id,
		tenantID:   tenantID,
		identityID: identityID,
		tokenHash:  tokenHash,
		expiresAt:  expiresAt,
		consumedAt: consumedAt,
		createdAt:  createdAt,
	}
}

// Consume marks the token as consumed. Returns an error if expired or already consumed.
func (prt *PasswordResetToken) Consume() error {
	if prt.consumedAt != nil {
		return ErrPasswordResetTokenAlreadyConsumed
	}
	if !time.Now().Before(prt.expiresAt) {
		return ErrPasswordResetTokenExpired
	}
	now := time.Now()
	prt.consumedAt = &now
	return nil
}

// ID returns the token's unique identifier.
func (prt *PasswordResetToken) ID() uuid.UUID { return prt.id }

// TenantID returns the tenant this token belongs to.
func (prt *PasswordResetToken) TenantID() string { return prt.tenantID }

// IdentityID returns the identity this token is for.
func (prt *PasswordResetToken) IdentityID() uuid.UUID { return prt.identityID }

// TokenHash returns the SHA256 hash of the password reset token.
func (prt *PasswordResetToken) TokenHash() string { return prt.tokenHash }

// ExpiresAt returns when the token expires.
func (prt *PasswordResetToken) ExpiresAt() time.Time { return prt.expiresAt }

// ConsumedAt returns when the token was consumed, or nil if not yet consumed.
func (prt *PasswordResetToken) ConsumedAt() *time.Time { return prt.consumedAt }

// CreatedAt returns when the token was created.
func (prt *PasswordResetToken) CreatedAt() time.Time { return prt.createdAt }
