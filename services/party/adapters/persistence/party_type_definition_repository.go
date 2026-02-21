package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// PartyTypeDefinitionRepository errors
var (
	ErrPartyTypeDefinitionNotFound = errors.New("party type definition not found")
	ErrPartyTypeDefinitionExists   = errors.New("party type definition already exists for this tenant and party type")
	ErrPartyTypeVersionConflict    = errors.New("version conflict: party type definition was modified by another transaction")
)

// PartyTypeDefinitionRepository provides persistence operations for party type definitions.
type PartyTypeDefinitionRepository struct {
	db *gorm.DB
}

// NewPartyTypeDefinitionRepository creates a new party type definition repository.
func NewPartyTypeDefinitionRepository(db *gorm.DB) *PartyTypeDefinitionRepository {
	return &PartyTypeDefinitionRepository{db: db}
}

// withTenantTransaction executes the given function with tenant scoping.
func (r *PartyTypeDefinitionRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Create inserts a new party type definition.
// Returns ErrPartyTypeDefinitionExists if a definition already exists for the same tenant and party type.
func (r *PartyTypeDefinitionRepository) Create(ctx context.Context, entity *PartyTypeDefinitionEntity) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(entity).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrPartyTypeDefinitionExists
			}
			return err
		}
		return nil
	})
}

// GetByID retrieves a party type definition by its UUID.
// Returns ErrPartyTypeDefinitionNotFound if not found.
func (r *PartyTypeDefinitionRepository) GetByID(ctx context.Context, id uuid.UUID) (*PartyTypeDefinitionEntity, error) {
	var entity PartyTypeDefinitionEntity
	var found bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPartyTypeDefinitionNotFound
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
		return nil, ErrPartyTypeDefinitionNotFound
	}
	return &entity, nil
}

// GetByTenantAndType retrieves a party type definition by tenant ID and party type.
// Returns ErrPartyTypeDefinitionNotFound if not found.
func (r *PartyTypeDefinitionRepository) GetByTenantAndType(ctx context.Context, tenantID, partyType string) (*PartyTypeDefinitionEntity, error) {
	var entity PartyTypeDefinitionEntity
	var found bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("tenant_id = ? AND party_type = ?", tenantID, partyType).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPartyTypeDefinitionNotFound
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
		return nil, ErrPartyTypeDefinitionNotFound
	}
	return &entity, nil
}

// ListByTenant retrieves all party type definitions for a tenant.
// Optionally filters by party type if partyType is non-empty.
func (r *PartyTypeDefinitionRepository) ListByTenant(ctx context.Context, tenantID string, partyType string) ([]*PartyTypeDefinitionEntity, error) {
	var entities []*PartyTypeDefinitionEntity
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		q := tx.Where("tenant_id = ?", tenantID)
		if partyType != "" {
			q = q.Where("party_type = ?", partyType)
		}
		return q.Find(&entities).Error
	})
	if err != nil {
		return nil, err
	}
	return entities, nil
}

// Update applies optimistic-locked updates to a party type definition.
// Returns ErrPartyTypeVersionConflict if another transaction has modified the record.
// Returns ErrPartyTypeDefinitionNotFound if the record does not exist.
func (r *PartyTypeDefinitionRepository) Update(ctx context.Context, entity *PartyTypeDefinitionEntity) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// entity.Version is the target version; DB should still have Version-1.
		expectedDBVersion := entity.Version - 1

		result := tx.Model(&PartyTypeDefinitionEntity{}).
			Where("id = ? AND version = ?", entity.ID, expectedDBVersion).
			Updates(map[string]interface{}{
				"attribute_schema":  entity.AttributeSchema,
				"validation_cel":    entity.ValidationCEL,
				"eligibility_cel":   entity.EligibilityCEL,
				"error_message_cel": entity.ErrorMessageCEL,
				"version":           entity.Version,
				"updated_at":        time.Now(),
			})

		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// Check whether the record exists at all
			var count int64
			if err := tx.Model(&PartyTypeDefinitionEntity{}).Where("id = ?", entity.ID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrPartyTypeDefinitionNotFound
			}
			return ErrPartyTypeVersionConflict
		}
		return nil
	})
}
