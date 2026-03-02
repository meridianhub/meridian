package persistence

import (
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
)

// InvitationEntity represents the database persistence model for invitations.
type InvitationEntity struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	IdentityID uuid.UUID `gorm:"column:identity_id;type:uuid;not null;index:idx_invitation_identity"`
	InvitedBy  uuid.UUID `gorm:"column:invited_by;type:uuid;not null"`
	// TokenHash stores the SHA256 hash of the invitation token.
	// The unique index enforces one-lookup semantics per token.
	TokenHash string    `gorm:"column:token_hash;type:varchar(64);not null;uniqueIndex:idx_invitation_token_hash"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null"`
	Status    string    `gorm:"column:status;type:varchar(20);not null;default:'PENDING'"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name.
func (InvitationEntity) TableName() string {
	return "invitation"
}

// toInvitationEntity converts a domain Invitation to a persistence entity.
func toInvitationEntity(inv *domain.Invitation) *InvitationEntity {
	return &InvitationEntity{
		ID:         inv.ID(),
		IdentityID: inv.IdentityID(),
		InvitedBy:  inv.InvitedBy(),
		TokenHash:  inv.TokenHash(),
		ExpiresAt:  inv.ExpiresAt(),
		Status:     string(inv.Status()),
		CreatedAt:  inv.CreatedAt(),
		UpdatedAt:  inv.UpdatedAt(),
	}
}

// toInvitationDomain converts a persistence entity to a domain Invitation.
func toInvitationDomain(entity *InvitationEntity) *domain.Invitation {
	return domain.ReconstructInvitation(
		entity.ID,
		entity.IdentityID,
		entity.InvitedBy,
		entity.TokenHash,
		entity.ExpiresAt,
		domain.InvitationStatus(entity.Status),
		entity.CreatedAt,
		entity.UpdatedAt,
	)
}
