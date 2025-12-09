package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository errors
var (
	ErrPartyNotFound   = errors.New("party not found")
	ErrPartyExists     = errors.New("party already exists")
	ErrVersionConflict = errors.New("version conflict: party was modified by another transaction")
)

// Repository provides persistence operations for parties
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new party repository
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// DB returns the underlying database connection for transaction support.
// Use this to wrap multiple repository operations in a single transaction.
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// WithTx returns a new Repository that uses the provided transaction.
// This enables multiple repository operations within a single transaction.
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// Save creates or updates a party with optimistic locking.
// The context is used to extract audit information (user ID) for the created_by/updated_by fields.
//
// For updates, the version in the domain model must match the version in the database.
// If another transaction has modified the record (incremented the version), this save
// will fail with ErrVersionConflict. The caller should reload the entity and retry.
func (r *Repository) Save(ctx context.Context, party *domain.Party) error {
	entity := toEntity(ctx, party)

	// Check if exists by ID
	var existing PartyEntity
	result := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", entity.ID).First(&existing)

	if result.Error == nil {
		// Update existing with optimistic locking
		entity.CreatedAt = existing.CreatedAt
		entity.CreatedBy = existing.CreatedBy

		// The domain model auto-increments version on mutation, so entity.Version
		// is already the target version. We check that DB still has the previous version.
		// Example: loaded party with version=1, mutated (now version=2), we check DB has version=1.
		expectedDBVersion := entity.Version - 1

		// Optimistic locking: only update if version matches expected
		updateResult := r.db.WithContext(ctx).Model(&PartyEntity{}).
			Where("id = ? AND version = ? AND deleted_at IS NULL", entity.ID, expectedDBVersion).
			Updates(map[string]interface{}{
				"party_type":              entity.PartyType,
				"legal_name":              entity.LegalName,
				"display_name":            entity.DisplayName,
				"status":                  entity.Status,
				"external_reference":      entity.ExternalReference,
				"external_reference_type": entity.ExternalReferenceType,
				"version":                 entity.Version,
				"updated_at":              entity.UpdatedAt,
				"updated_by":              entity.UpdatedBy,
			})

		if updateResult.Error != nil {
			return updateResult.Error
		}

		// If no rows were affected, the version didn't match (concurrent modification)
		if updateResult.RowsAffected == 0 {
			return ErrVersionConflict
		}

		return nil
	}

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Create new - version starts at 1 (set by toEntity)
		if err := r.db.WithContext(ctx).Create(&entity).Error; err != nil {
			// Handle race condition: another transaction created the same external reference
			if isDuplicateKeyError(err) {
				return ErrPartyExists
			}
			return err
		}
		return nil
	}

	return result.Error
}

// FindByID retrieves a party by its UUID.
// The context is used for cancellation, timeout, and tracing support.
func (r *Repository) FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error) {
	var entity PartyEntity
	result := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", partyID).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPartyNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity), nil
}

// FindByIDForUpdate retrieves a party by its UUID with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
// The context is used for cancellation, timeout, and tracing support.
func (r *Repository) FindByIDForUpdate(ctx context.Context, partyID uuid.UUID) (*domain.Party, error) {
	var entity PartyEntity
	result := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND deleted_at IS NULL", partyID).
		First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPartyNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity), nil
}

// FindByExternalReference retrieves a party by its external reference and type.
// The context is used for cancellation, timeout, and tracing support.
func (r *Repository) FindByExternalReference(ctx context.Context, ref, refType string) (*domain.Party, error) {
	var entity PartyEntity
	result := r.db.WithContext(ctx).Where("external_reference = ? AND external_reference_type = ? AND deleted_at IS NULL", ref, refType).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPartyNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity), nil
}

// ExistsByID checks if a party exists by its UUID.
// This is a lightweight check useful for validation without loading the full entity.
func (r *Repository) ExistsByID(partyID uuid.UUID) (bool, error) {
	var count int64
	result := r.db.Model(&PartyEntity{}).
		Where("id = ? AND deleted_at IS NULL", partyID).
		Count(&count)

	if result.Error != nil {
		return false, result.Error
	}

	return count > 0, nil
}

// Delete soft deletes a party
func (r *Repository) Delete(partyID uuid.UUID) error {
	return r.db.Model(&PartyEntity{}).
		Where("id = ?", partyID).
		Update("deleted_at", time.Now()).Error
}

// Ping checks database connectivity without triggering record-not-found logging.
// This is used by health checks to verify the database is reachable.
func (r *Repository) Ping() error {
	var result int
	return r.db.Raw("SELECT 1").Scan(&result).Error
}

// toEntity converts domain model to database entity
func toEntity(ctx context.Context, party *domain.Party) *PartyEntity {
	auditUser := audit.GetUserFromContext(ctx)

	entity := &PartyEntity{
		ID:        party.ID(),
		PartyType: string(party.PartyType()),
		LegalName: party.LegalName(),
		Status:    string(party.Status()),
		Version:   party.Version(),
		CreatedAt: party.CreatedAt(),
		UpdatedAt: party.UpdatedAt(),
		CreatedBy: auditUser,
		UpdatedBy: auditUser,
	}

	// Handle optional display name
	if displayName := party.DisplayName(); displayName != "" {
		entity.DisplayName = &displayName
	}

	// Handle optional external reference
	if extRef := party.ExternalReference(); extRef != "" {
		entity.ExternalReference = &extRef
		extRefType := string(party.ExternalReferenceType())
		entity.ExternalReferenceType = &extRefType
	}

	return entity
}

// toDomain converts database entity to domain model
func toDomain(entity *PartyEntity) *domain.Party {
	// Handle optional fields
	displayName := ""
	if entity.DisplayName != nil {
		displayName = *entity.DisplayName
	}

	externalRef := ""
	if entity.ExternalReference != nil {
		externalRef = *entity.ExternalReference
	}

	var externalRefType domain.ExternalReferenceType
	if entity.ExternalReferenceType != nil {
		externalRefType = domain.ExternalReferenceType(*entity.ExternalReferenceType)
	}

	return domain.ReconstructParty(
		entity.ID,
		domain.PartyType(entity.PartyType),
		entity.LegalName,
		displayName,
		domain.PartyStatus(entity.Status),
		externalRef,
		externalRefType,
		entity.CreatedAt,
		entity.UpdatedAt,
		entity.Version,
	)
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
// This handles the race condition where two concurrent creates attempt to insert
// the same external reference.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// PostgreSQL unique violation error code is 23505
	// GORM wraps this, so we check the error message
	errStr := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
