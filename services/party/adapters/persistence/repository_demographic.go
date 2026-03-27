package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SaveDemographic saves or updates demographic data for a party
func (r *Repository) SaveDemographic(ctx context.Context, partyID uuid.UUID, socioEconomicData, employmentHistory string) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Check if exists
		var existing PartyDemographicEntity
		result := tx.Where("party_id = ?", partyID).First(&existing)

		// Prepare strings for JSONB columns
		// If already valid JSON, store as-is; otherwise wrap as JSON string
		socioEconStr := toJSONB(socioEconomicData)
		empHistoryStr := toJSONB(employmentHistory)
		socioEcon := &socioEconStr
		empHistory := &empHistoryStr

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create new
			entity := &PartyDemographicEntity{
				ID:                uuid.New(),
				PartyID:           partyID,
				SocioEconomicData: socioEcon,
				EmploymentHistory: empHistory,
				UpdatedAt:         time.Now(),
			}
			return tx.Create(entity).Error
		}

		// Update existing
		return tx.Model(&PartyDemographicEntity{}).
			Where("party_id = ?", partyID).
			Updates(map[string]interface{}{
				"socio_economic_data": socioEcon,
				"employment_history":  empHistory,
				"updated_at":          time.Now(),
			}).Error
	})
}

// FindDemographic retrieves demographic data for a party.
// Returns (nil, nil) if no demographic data exists for the party.
func (r *Repository) FindDemographic(ctx context.Context, partyID uuid.UUID) (*PartyDemographicEntity, error) {
	var demographic PartyDemographicEntity
	var found bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("party_id = ?", partyID).First(&demographic)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil // Not an error, just no demographic data
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
	return &demographic, nil
}
