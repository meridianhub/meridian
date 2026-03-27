package persistence

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ReferenceInput represents input for saving a reference.
type ReferenceInput struct {
	RefType          string
	RefValue         string
	IssuingAuthority string
	ExpiryDate       string
}

// SaveReference saves party reference data
func (r *Repository) SaveReference(ctx context.Context, partyID uuid.UUID, refType, refValue, issuingAuthority, expiryDate string) error {
	return r.SaveReferences(ctx, partyID, []ReferenceInput{{
		RefType:          refType,
		RefValue:         refValue,
		IssuingAuthority: issuingAuthority,
		ExpiryDate:       expiryDate,
	}})
}

// SaveReferences saves multiple party references in a single transaction.
func (r *Repository) SaveReferences(ctx context.Context, partyID uuid.UUID, refs []ReferenceInput) error {
	if len(refs) == 0 {
		return nil
	}

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		for _, ref := range refs {
			entity := &PartyReferenceEntity{
				ID:             uuid.New(),
				PartyID:        partyID,
				ReferenceType:  ref.RefType,
				ReferenceValue: ref.RefValue,
				CreatedAt:      time.Now(),
			}

			if ref.IssuingAuthority != "" {
				entity.IssuingAuthority = &ref.IssuingAuthority
			}
			if ref.ExpiryDate != "" {
				parsedDate, err := time.Parse("2006-01-02", ref.ExpiryDate)
				if err == nil {
					entity.ExpiryDate = &parsedDate
				}
			}

			if err := tx.Create(entity).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// FindReferences retrieves all references for a party
func (r *Repository) FindReferences(ctx context.Context, partyID uuid.UUID) ([]PartyReferenceEntity, error) {
	var references []PartyReferenceEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("party_id = ?", partyID).Find(&references).Error
	})
	return references, err
}
