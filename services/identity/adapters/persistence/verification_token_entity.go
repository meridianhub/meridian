package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
)

// EmailVerificationTokenEntity represents the database persistence model for email verification tokens.
// Tokens are created during self-registration and consumed when the user clicks the verification link.
type EmailVerificationTokenEntity struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	TenantID   string     `gorm:"column:tenant_id;type:varchar(50);not null;index:idx_verification_token_tenant"`
	IdentityID uuid.UUID  `gorm:"column:identity_id;type:uuid;not null"`
	TokenHash  string     `gorm:"column:token_hash;type:varchar(64);not null;uniqueIndex:idx_verification_token_hash"`
	ExpiresAt  time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt *time.Time `gorm:"column:consumed_at"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;default:now()"`
}

// TableName overrides the default table name.
func (EmailVerificationTokenEntity) TableName() string {
	return "email_verification_token"
}

// toVerificationTokenEntity converts a domain VerificationToken to a persistence entity.
func toVerificationTokenEntity(vt *domain.VerificationToken) *EmailVerificationTokenEntity {
	return &EmailVerificationTokenEntity{
		ID:         vt.ID(),
		TenantID:   vt.TenantID(),
		IdentityID: vt.IdentityID(),
		TokenHash:  vt.TokenHash(),
		ExpiresAt:  vt.ExpiresAt(),
		ConsumedAt: vt.ConsumedAt(),
		CreatedAt:  vt.CreatedAt(),
	}
}

// toVerificationTokenDomain converts a persistence entity to a domain VerificationToken.
func toVerificationTokenDomain(entity *EmailVerificationTokenEntity) *domain.VerificationToken {
	return domain.ReconstructVerificationToken(
		entity.ID,
		entity.TenantID,
		entity.IdentityID,
		entity.TokenHash,
		entity.ExpiresAt,
		entity.ConsumedAt,
		entity.CreatedAt,
	)
}
