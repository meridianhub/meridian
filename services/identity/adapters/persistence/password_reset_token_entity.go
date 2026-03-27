package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
)

// PasswordResetTokenEntity represents the database persistence model for password reset tokens.
// Tokens are created during the forgot-password flow and consumed when the user sets a new password.
type PasswordResetTokenEntity struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	TenantID   string     `gorm:"column:tenant_id;type:varchar(50);not null;index:idx_password_reset_token_tenant"`
	IdentityID uuid.UUID  `gorm:"column:identity_id;type:uuid;not null;index:idx_password_reset_token_rate_limit"`
	TokenHash  string     `gorm:"column:token_hash;type:varchar(64);not null;uniqueIndex:idx_password_reset_token_hash"`
	ExpiresAt  time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt *time.Time `gorm:"column:consumed_at"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;default:now()"`
}

// TableName overrides the default table name.
func (PasswordResetTokenEntity) TableName() string {
	return "password_reset_token"
}

// toPasswordResetTokenEntity converts a domain PasswordResetToken to a persistence entity.
func toPasswordResetTokenEntity(prt *domain.PasswordResetToken) *PasswordResetTokenEntity {
	return &PasswordResetTokenEntity{
		ID:         prt.ID(),
		TenantID:   prt.TenantID(),
		IdentityID: prt.IdentityID(),
		TokenHash:  prt.TokenHash(),
		ExpiresAt:  prt.ExpiresAt(),
		ConsumedAt: prt.ConsumedAt(),
		CreatedAt:  prt.CreatedAt(),
	}
}

// toPasswordResetTokenDomain converts a persistence entity to a domain PasswordResetToken.
func toPasswordResetTokenDomain(entity *PasswordResetTokenEntity) *domain.PasswordResetToken {
	return domain.ReconstructPasswordResetToken(
		entity.ID,
		entity.TenantID,
		entity.IdentityID,
		entity.TokenHash,
		entity.ExpiresAt,
		entity.ConsumedAt,
		entity.CreatedAt,
	)
}
