package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/db"
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
//
// IMPORTANT: The system is always in multi-tenant mode. For optimal performance
// and correct behavior, set the tenant scope once at the start of your transaction
// using db.WithGormTenantScope() rather than relying on per-operation scoping.
//
// Example:
//
//	err := repo.DB().Transaction(func(tx *gorm.DB) error {
//	    // Set tenant scope once for the entire transaction
//	    tx, err := db.WithGormTenantScope(ctx, tx)
//	    if err != nil {
//	        return err
//	    }
//	    txRepo := repo.WithTx(tx)
//	    // All operations now use the scoped transaction
//	    party, err := txRepo.FindByIDForUpdate(ctx, partyID)
//	    // ...
//	})
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// withTenantScope returns a GORM DB instance scoped to the organization from context.
// The system is always in multi-tenant mode and requires tenant context.
// This must be called within a transaction for the search_path setting to work correctly.
func (r *Repository) withTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	return db.WithGormTenantScope(ctx, tx)
}

// withTenantTransaction executes the given function with tenant scoping.
// The system is always in multi-tenant mode, so this wraps the function in a transaction
// and sets the search_path. This helper reduces code duplication across repository methods.
func (r *Repository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// isInTransaction checks if the repository's db connection is already within a transaction.
// This is used to avoid creating nested transactions when the caller has already established one.
func (r *Repository) isInTransaction() bool {
	// Guard against uninitialized Statement (can happen if no query has been executed yet)
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	// GORM sets ConnPool to a transaction object when in transaction mode.
	// In a transaction, Statement.ConnPool will be of type *sql.Tx (or GORM's tx wrapper).
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// withForUpdateScope executes the given function with FOR UPDATE locking support.
// If already in a transaction (via WithTx), it uses the existing transaction directly
// with org scope set. If not in a transaction, it creates a new one with org scope.
//
// This prevents the security issue where nested transactions would have search_path
// set only on the inner transaction, while the outer transaction operates without it.
func (r *Repository) withForUpdateScope(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r.isInTransaction() {
		// Already in a transaction (via WithTx) - use it directly with org scope
		// The caller is responsible for the outer transaction,
		// but we still need to set the org scope for this operation.
		tx, err := r.withTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}

	// Not in a transaction - use the shared helper that handles transaction + org scope
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Save creates or updates a party with optimistic locking.
// The context is used to extract audit information (user ID) for the created_by/updated_by fields.
// The context must contain the tenant ID for schema routing.
//
// For updates, the version in the domain model must match the version in the database.
// If another transaction has modified the record (incremented the version), this save
// will fail with ErrVersionConflict. The caller should reload the entity and retry.
func (r *Repository) Save(ctx context.Context, party *domain.Party) error {
	entity := toEntity(ctx, party)

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Set organization scope if in multi-org mode
		tx, err := r.withTenantScope(ctx, tx)
		if err != nil {
			return err
		}

		// Check if exists by ID
		var existing PartyEntity
		result := tx.Where("id = ? AND deleted_at IS NULL", entity.ID).First(&existing)

		if result.Error == nil {
			// Update existing with optimistic locking
			entity.CreatedAt = existing.CreatedAt
			entity.CreatedBy = existing.CreatedBy

			// The domain model auto-increments version on mutation, so entity.Version
			// is already the target version. We check that DB still has the previous version.
			// Example: loaded party with version=1, mutated (now version=2), we check DB has version=1.
			expectedDBVersion := entity.Version - 1

			// Optimistic locking: only update if version matches expected
			updateResult := tx.Model(&PartyEntity{}).
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
			if err := tx.Create(&entity).Error; err != nil {
				// Handle race condition: another transaction created the same external reference
				if isDuplicateKeyError(err) {
					return ErrPartyExists
				}
				return err
			}
			return nil
		}

		return result.Error
	})
}

// FindByID retrieves a party by its UUID.
// The context must contain the tenant ID for schema routing.
func (r *Repository) FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error) {
	var party *domain.Party
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PartyEntity
		result := tx.Where("id = ? AND deleted_at IS NULL", partyID).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPartyNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		party = toDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return party, nil
}

// FindByIDForUpdate retrieves a party by its UUID with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
// The context must contain the tenant ID for schema routing.
//
// IMPORTANT: This method expects to be called within an existing transaction that already
// has the tenant scope set. When using WithTx(), the caller is responsible for setting
// the tenant scope on the outer transaction. This method will set the tenant scope if not already
// in a transaction, but when called via WithTx(), it uses the existing transaction directly.
func (r *Repository) FindByIDForUpdate(ctx context.Context, partyID uuid.UUID) (*domain.Party, error) {
	var party *domain.Party

	// Perform the FOR UPDATE query, wrapping in org-scoped transaction if needed
	err := r.withForUpdateScope(ctx, func(tx *gorm.DB) error {
		var entity PartyEntity
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", partyID).
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPartyNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		party = toDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return party, nil
}

// FindByExternalReference retrieves a party by its external reference and type.
// The context must contain the tenant ID for schema routing.
func (r *Repository) FindByExternalReference(ctx context.Context, ref, refType string) (*domain.Party, error) {
	var party *domain.Party
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PartyEntity
		result := tx.Where("external_reference = ? AND external_reference_type = ? AND deleted_at IS NULL", ref, refType).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPartyNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		party = toDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return party, nil
}

// ExistsByID checks if a party exists by its UUID.
// This is a lightweight check useful for validation without loading the full entity.
// The context must contain the tenant ID for schema routing.
func (r *Repository) ExistsByID(ctx context.Context, partyID uuid.UUID) (bool, error) {
	var exists bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var count int64
		result := tx.Model(&PartyEntity{}).
			Where("id = ? AND deleted_at IS NULL", partyID).
			Count(&count)

		if result.Error != nil {
			return result.Error
		}

		exists = count > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return exists, nil
}

// Delete soft deletes a party.
// The context must contain the tenant ID for schema routing.
func (r *Repository) Delete(ctx context.Context, partyID uuid.UUID) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&PartyEntity{}).
			Where("id = ?", partyID).
			Update("deleted_at", time.Now()).Error
	})
}

// Ping checks database connectivity without triggering record-not-found logging.
// This is used by health checks to verify the database is reachable.
// The context is used for cancellation and timeout support.
func (r *Repository) Ping(ctx context.Context) error {
	var result int
	return r.db.WithContext(ctx).Raw("SELECT 1").Scan(&result).Error
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
