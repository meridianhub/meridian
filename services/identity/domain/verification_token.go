package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
)

// VerificationToken represents an email verification token issued during self-registration.
type VerificationToken struct {
	id         uuid.UUID
	tenantID   string
	identityID uuid.UUID
	tokenHash  string
	expiresAt  time.Time
	consumedAt *time.Time
	createdAt  time.Time
}

// NewVerificationToken creates a new verification token and returns both the domain object
// and the plaintext token that must be delivered to the user. The plaintext token is never
// stored; only its SHA256 hash is persisted.
func NewVerificationToken(tenantID string, identityID uuid.UUID) (*VerificationToken, string, error) {
	plaintext, hash, err := tokens.GenerateToken(tokens.EmailVerificationTokenLength)
	if err != nil {
		return nil, "", err
	}

	now := time.Now()
	vt := &VerificationToken{
		id:         uuid.New(),
		tenantID:   tenantID,
		identityID: identityID,
		tokenHash:  hash,
		expiresAt:  now.Add(tokens.EmailVerificationTokenTTL),
		createdAt:  now,
	}
	return vt, plaintext, nil
}

// ReconstructVerificationToken recreates a VerificationToken from persistence layer data.
func ReconstructVerificationToken(
	id uuid.UUID,
	tenantID string,
	identityID uuid.UUID,
	tokenHash string,
	expiresAt time.Time,
	consumedAt *time.Time,
	createdAt time.Time,
) *VerificationToken {
	return &VerificationToken{
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
func (vt *VerificationToken) Consume() error {
	if vt.consumedAt != nil {
		return ErrVerificationTokenAlreadyConsumed
	}
	if !time.Now().Before(vt.expiresAt) {
		return ErrVerificationTokenExpired
	}
	now := time.Now()
	vt.consumedAt = &now
	return nil
}

// ID returns the token's unique identifier.
func (vt *VerificationToken) ID() uuid.UUID { return vt.id }

// TenantID returns the tenant this token belongs to.
func (vt *VerificationToken) TenantID() string { return vt.tenantID }

// IdentityID returns the identity this token is for.
func (vt *VerificationToken) IdentityID() uuid.UUID { return vt.identityID }

// TokenHash returns the SHA256 hash of the verification token.
func (vt *VerificationToken) TokenHash() string { return vt.tokenHash }

// ExpiresAt returns when the token expires.
func (vt *VerificationToken) ExpiresAt() time.Time { return vt.expiresAt }

// ConsumedAt returns when the token was consumed, or nil if not yet consumed.
func (vt *VerificationToken) ConsumedAt() *time.Time { return vt.consumedAt }

// CreatedAt returns when the token was created.
func (vt *VerificationToken) CreatedAt() time.Time { return vt.createdAt }
