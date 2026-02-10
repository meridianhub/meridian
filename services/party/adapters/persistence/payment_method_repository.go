// Package persistence provides database persistence for the party domain
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// Payment method repository errors
var (
	ErrPaymentMethodNotFound = errors.New("payment method not found")
)

// PaymentMethodRepository provides persistence operations for party payment methods
type PaymentMethodRepository struct {
	db *gorm.DB
}

// NewPaymentMethodRepository creates a new payment method repository
func NewPaymentMethodRepository(database *gorm.DB) *PaymentMethodRepository {
	return &PaymentMethodRepository{db: database}
}

// DB returns the underlying database connection for transaction support.
func (r *PaymentMethodRepository) DB() *gorm.DB {
	return r.db
}

// WithTx returns a new PaymentMethodRepository that uses the provided transaction.
func (r *PaymentMethodRepository) WithTx(tx *gorm.DB) *PaymentMethodRepository {
	return &PaymentMethodRepository{db: tx}
}

// withTenantTransaction executes the given function with tenant scoping.
func (r *PaymentMethodRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Create inserts a new payment method.
// If the payment method is marked as default, any existing default for the same party
// is atomically unset within the same transaction.
func (r *PaymentMethodRepository) Create(ctx context.Context, pm *domain.PaymentMethod) error {
	entity := paymentMethodToEntity(pm)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// If this is the default, unset any existing default for the party
		if entity.IsDefault {
			if err := unsetPartyDefault(tx, entity.PartyID, uuid.Nil); err != nil {
				return err
			}
		}

		if err := tx.Create(&entity).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrPaymentMethodExists
			}
			return err
		}
		return nil
	})
}

// ErrPaymentMethodExists is returned when a duplicate payment method is detected
var ErrPaymentMethodExists = errors.New("payment method already exists")

// Update saves changes to an existing payment method with optimistic locking.
// If the payment method is being set as default, any existing default for the same party
// is atomically unset within the same transaction.
func (r *PaymentMethodRepository) Update(ctx context.Context, pm *domain.PaymentMethod) error {
	entity := paymentMethodToEntity(pm)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// If becoming the default, unset any existing default for the party (excluding self)
		if entity.IsDefault {
			if err := unsetPartyDefault(tx, entity.PartyID, entity.ID); err != nil {
				return err
			}
		}

		// Optimistic locking: only update if version matches
		expectedDBVersion := entity.Version - 1
		result := tx.Model(&PaymentMethodEntity{}).
			Where("id = ? AND version = ?", entity.ID, expectedDBVersion).
			Updates(map[string]interface{}{
				"is_default": entity.IsDefault,
				"metadata":   entity.Metadata,
				"status":     entity.Status,
				"version":    entity.Version,
				"updated_at": entity.UpdatedAt,
			})

		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		return nil
	})
}

// FindByID retrieves a payment method by its UUID.
func (r *PaymentMethodRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.PaymentMethod, error) {
	var pm *domain.PaymentMethod
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PaymentMethodEntity
		result := tx.Where("id = ?", id).First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPaymentMethodNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		pm = paymentMethodToDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pm, nil
}

// ListActiveByParty returns all active payment methods for a party.
func (r *PaymentMethodRepository) ListActiveByParty(ctx context.Context, partyID uuid.UUID) ([]*domain.PaymentMethod, error) {
	var methods []*domain.PaymentMethod
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []PaymentMethodEntity
		result := tx.Where("party_id = ? AND status = ?", partyID, "ACTIVE").
			Order("created_at ASC").
			Find(&entities)
		if result.Error != nil {
			return result.Error
		}
		methods = make([]*domain.PaymentMethod, len(entities))
		for i := range entities {
			methods[i] = paymentMethodToDomain(&entities[i])
		}
		return nil
	})
	return methods, err
}

// FindDefaultByParty returns the default active payment method for a party, if any.
// Returns (nil, nil) if no default payment method exists.
func (r *PaymentMethodRepository) FindDefaultByParty(ctx context.Context, partyID uuid.UUID) (*domain.PaymentMethod, error) {
	var pm *domain.PaymentMethod
	var found bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PaymentMethodEntity
		result := tx.Where("party_id = ? AND is_default = true AND status = ?", partyID, "ACTIVE").
			First(&entity)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		found = true
		pm = paymentMethodToDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil //nolint:nilnil // nil,nil signals "not found" without error
	}
	return pm, nil
}

// unsetPartyDefault clears the is_default flag on any active default payment method for the party.
// excludeID can be set to skip a specific payment method (e.g., the one being set as default).
// Pass uuid.Nil to not exclude any.
func unsetPartyDefault(tx *gorm.DB, partyID uuid.UUID, excludeID uuid.UUID) error {
	query := tx.Model(&PaymentMethodEntity{}).
		Where("party_id = ? AND is_default = true AND status = ?", partyID, "ACTIVE")

	if excludeID != uuid.Nil {
		query = query.Where("id != ?", excludeID)
	}

	return query.Updates(map[string]interface{}{
		"is_default": false,
		"version":    gorm.Expr("version + 1"),
		"updated_at": time.Now(),
	}).Error
}

// paymentMethodToEntity converts domain model to database entity
func paymentMethodToEntity(pm *domain.PaymentMethod) *PaymentMethodEntity {
	entity := &PaymentMethodEntity{
		ID:                 pm.ID(),
		PartyID:            pm.PartyID(),
		Provider:           string(pm.Provider()),
		ProviderCustomerID: pm.ProviderCustomerID(),
		ProviderMethodID:   pm.ProviderMethodID(),
		MethodType:         string(pm.MethodType()),
		IsDefault:          pm.IsDefault(),
		Status:             string(pm.Status()),
		Version:            pm.Version(),
		CreatedAt:          pm.CreatedAt(),
		UpdatedAt:          pm.UpdatedAt(),
	}

	if pm.Metadata() != nil {
		data, err := json.Marshal(pm.Metadata())
		if err == nil {
			s := string(data)
			entity.Metadata = &s
		}
	}

	return entity
}

// paymentMethodToDomain converts database entity to domain model
func paymentMethodToDomain(entity *PaymentMethodEntity) *domain.PaymentMethod {
	var metadata *domain.PaymentMethodMetadata
	if entity.Metadata != nil && *entity.Metadata != "" && *entity.Metadata != "{}" {
		var m domain.PaymentMethodMetadata
		if err := json.Unmarshal([]byte(*entity.Metadata), &m); err == nil {
			metadata = &m
		}
	}

	return domain.ReconstructPaymentMethod(
		entity.ID,
		entity.PartyID,
		domain.PaymentProvider(entity.Provider),
		entity.ProviderCustomerID,
		entity.ProviderMethodID,
		domain.PaymentMethodType(entity.MethodType),
		entity.IsDefault,
		metadata,
		domain.PaymentMethodStatus(entity.Status),
		entity.CreatedAt,
		entity.UpdatedAt,
		entity.Version,
	)
}
