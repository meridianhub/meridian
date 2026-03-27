package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SaveBankRelation saves or updates bank relationship data
func (r *Repository) SaveBankRelation(ctx context.Context, partyID uuid.UUID, accountOfficerID, relationshipManagerID, assignedBranch string) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Check if exists
		var existing PartyBankRelationEntity
		result := tx.Where("party_id = ?", partyID).First(&existing)

		var aoID, rmID, branch *string
		if accountOfficerID != "" {
			aoID = &accountOfficerID
		}
		if relationshipManagerID != "" {
			rmID = &relationshipManagerID
		}
		if assignedBranch != "" {
			branch = &assignedBranch
		}

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create new
			entity := &PartyBankRelationEntity{
				ID:                    uuid.New(),
				PartyID:               partyID,
				AccountOfficerID:      aoID,
				RelationshipManagerID: rmID,
				AssignedBranch:        branch,
				UpdatedAt:             time.Now(),
			}
			return tx.Create(entity).Error
		}

		// Update existing
		return tx.Model(&PartyBankRelationEntity{}).
			Where("party_id = ?", partyID).
			Updates(map[string]interface{}{
				"account_officer_id":      aoID,
				"relationship_manager_id": rmID,
				"assigned_branch":         branch,
				"updated_at":              time.Now(),
			}).Error
	})
}

// FindBankRelation retrieves bank relationship data for a party.
// Returns (nil, nil) if no bank relation data exists for the party.
func (r *Repository) FindBankRelation(ctx context.Context, partyID uuid.UUID) (*PartyBankRelationEntity, error) {
	var bankRelation PartyBankRelationEntity
	var found bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("party_id = ?", partyID).First(&bankRelation)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil // Not an error, just no bank relation data
		}
		if result.Error != nil {
			return result.Error
		}
		found = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil //nolint:nilnil // nil,nil signals "not found" without error
	}
	return &bankRelation, nil
}
