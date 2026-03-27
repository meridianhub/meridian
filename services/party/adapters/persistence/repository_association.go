package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AssociationInput holds optional fields for creating a party association.
type AssociationInput struct {
	Metadata      *string
	Status        string
	EffectiveFrom *time.Time
	EffectiveTo   *time.Time
}

// SaveAssociation saves a party association.
// Returns ErrAssociationExists if an association already exists between the parties.
func (r *Repository) SaveAssociation(ctx context.Context, partyID, relatedPartyID uuid.UUID, relationshipType string) (uuid.UUID, error) {
	return r.SaveAssociationWithInput(ctx, partyID, relatedPartyID, relationshipType, nil)
}

// SaveAssociationWithInput saves a party association with optional metadata and lifecycle fields.
// Returns ErrAssociationExists if an association already exists between the parties.
func (r *Repository) SaveAssociationWithInput(ctx context.Context, partyID, relatedPartyID uuid.UUID, relationshipType string, input *AssociationInput) (uuid.UUID, error) {
	now := time.Now()
	associationID := uuid.New()
	entity := &PartyAssociationEntity{
		ID:               associationID,
		PartyID:          partyID,
		RelatedPartyID:   relatedPartyID,
		RelationshipType: relationshipType,
		Status:           "ACTIVE",
		EffectiveFrom:    now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if input != nil {
		if input.Metadata != nil {
			entity.Metadata = input.Metadata
		}
		if input.Status != "" {
			entity.Status = input.Status
		}
		if input.EffectiveFrom != nil {
			entity.EffectiveFrom = *input.EffectiveFrom
		}
		entity.EffectiveTo = input.EffectiveTo
	}

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return uuid.Nil, ErrAssociationExists
		}
		return uuid.Nil, err
	}
	return associationID, nil
}

// FindAssociations retrieves all associations for a party
func (r *Repository) FindAssociations(ctx context.Context, partyID uuid.UUID) ([]PartyAssociationEntity, error) {
	var associations []PartyAssociationEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("party_id = ?", partyID).Find(&associations).Error
	})
	return associations, err
}

// UpdateAssociation updates an association's relationship type and returns the updated entity.
// Returns an error if the association doesn't exist.
func (r *Repository) UpdateAssociation(ctx context.Context, associationID uuid.UUID, relationshipType string) (*PartyAssociationEntity, error) {
	var entity PartyAssociationEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&PartyAssociationEntity{}).
			Where("id = ?", associationID).
			Updates(map[string]interface{}{
				"relationship_type": relationshipType,
				"updated_at":        time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		// Load the updated entity to return party_id and related_party_id
		return tx.Where("id = ?", associationID).First(&entity).Error
	})
	if err != nil {
		return nil, err
	}
	return &entity, nil
}

// CheckCircularAssociation checks if adding this association would create a circular reference
func (r *Repository) CheckCircularAssociation(ctx context.Context, partyID, relatedPartyID uuid.UUID) (bool, error) {
	// Simple check: verify they're not the same and no direct reverse relationship exists
	if partyID == relatedPartyID {
		return true, nil
	}

	var count int64
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Check if reverse relationship exists
		return tx.Model(&PartyAssociationEntity{}).
			Where("party_id = ? AND related_party_id = ?", relatedPartyID, partyID).
			Count(&count).Error
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListParticipants retrieves all ACTIVE associations where the given orgPartyID is the related_party_id
// (i.e., the org is the syndicate host and participants point to it) with the given relationship type.
func (r *Repository) ListParticipants(ctx context.Context, orgPartyID uuid.UUID, relationshipType string) ([]PartyAssociationEntity, error) {
	var associations []PartyAssociationEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("related_party_id = ? AND relationship_type = ? AND status = ?",
			orgPartyID, relationshipType, "ACTIVE").
			Find(&associations).Error
	})
	return associations, err
}

// GetStructuringData retrieves the metadata JSONB for a specific association.
// Returns an empty map (not an error) if no association is found.
func (r *Repository) GetStructuringData(ctx context.Context, partyID, orgPartyID uuid.UUID, relationshipType string) (map[string]interface{}, error) {
	var entity PartyAssociationEntity
	var found bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("party_id = ? AND related_party_id = ? AND relationship_type = ?",
			partyID, orgPartyID, relationshipType).
			First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil
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
	if !found || entity.Metadata == nil {
		return map[string]interface{}{}, nil
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(*entity.Metadata), &metadata); err != nil {
		return nil, fmt.Errorf("unmarshal association metadata: %w", err)
	}
	return metadata, nil
}
