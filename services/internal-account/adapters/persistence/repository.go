package persistence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository errors
var (
	ErrAccountNotFound = errors.New("account not found")
	ErrDuplicateCode   = errors.New("account code already exists")
	ErrVersionConflict = errors.New("version conflict: account was modified by another transaction")
)

// Compile-time interface compliance check
var _ domain.Repository = (*Repository)(nil)

// Repository provides persistence operations for internal accounts.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new account repository.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// DB returns the underlying database connection for transaction support.
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// WithTx returns a new Repository that uses the provided transaction.
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// withTenantScope returns a GORM DB instance scoped to the tenant from context.
func (r *Repository) withTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	return db.WithGormTenantScope(ctx, tx)
}

// withTenantTransaction executes the given function with tenant scoping in a transaction.
// If already in a transaction (via WithTx), it uses the existing transaction directly
// with tenant scope set, avoiding nested transactions.
func (r *Repository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r.isInTransaction() {
		tx, err := r.withTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// isInTransaction checks if the repository's db connection is already within a transaction.
func (r *Repository) isInTransaction() bool {
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// withForUpdateScope executes the given function with FOR UPDATE locking support.
// Implementation is identical to withTenantTransaction but kept separate for semantic
// clarity: this method is specifically for operations that will use SELECT FOR UPDATE
// (pessimistic locking), making the intent explicit at call sites.
func (r *Repository) withForUpdateScope(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r.isInTransaction() {
		tx, err := r.withTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Save creates or updates an account with optimistic locking.
// Uses withTenantTransaction to respect existing transactions from WithTx.
func (r *Repository) Save(ctx context.Context, account domain.InternalAccount) error {
	entity := toEntity(ctx, account)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Check if exists by account_id (business identifier)
		// Explicit deleted_at check for code clarity
		var existing InternalAccountEntity
		result := tx.Where("account_id = ? AND deleted_at IS NULL", entity.AccountID).First(&existing)

		if result.Error == nil {
			// Update existing with optimistic locking
			entity.ID = existing.ID
			entity.CreatedAt = existing.CreatedAt
			entity.CreatedBy = existing.CreatedBy

			// Version guard: domain contract says version is incremented before Save
			// on updates. Version 0 indicates an invalid state for updates.
			if entity.Version == 0 {
				return ErrVersionConflict
			}

			// Optimistic locking contract: The domain model increments version on all
			// mutations (Suspend, Activate, Close, UpdateCounterparty) before passing
			// to Save. We check the original version (current - 1) to detect concurrent
			// modifications. If another transaction has modified the record, the version
			// won't match and we return ErrVersionConflict.
			originalVersion := entity.Version - 1
			updateResult := tx.Model(&InternalAccountEntity{}).
				Where("account_id = ? AND version = ? AND deleted_at IS NULL", entity.AccountID, originalVersion).
				Updates(map[string]interface{}{
					"account_code":              entity.AccountCode,
					"name":                      entity.Name,
					"status":                    entity.Status,
					"clearing_purpose":          entity.ClearingPurpose,
					"product_type_code":         entity.ProductTypeCode,
					"product_type_version":      entity.ProductTypeVersion,
					"counterparty_id":           entity.CounterpartyID,
					"counterparty_name":         entity.CounterpartyName,
					"counterparty_external_ref": entity.CounterpartyExternalRef,
					"attributes":                entity.Attributes,
					"version":                   entity.Version,
					"updated_at":                entity.UpdatedAt,
					"updated_by":                entity.UpdatedBy,
				})

			if updateResult.Error != nil {
				if isDuplicateKeyError(updateResult.Error) {
					return ErrDuplicateCode
				}
				return updateResult.Error
			}

			if updateResult.RowsAffected == 0 {
				return ErrVersionConflict
			}

			return nil
		}

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create new
			if err := tx.Create(&entity).Error; err != nil {
				if isDuplicateKeyError(err) {
					return ErrDuplicateCode
				}
				return err
			}
			return nil
		}

		return result.Error
	})
}

// SaveInTx persists a new or updated account within the provided transaction.
// The caller is responsible for managing the transaction boundary.
func (r *Repository) SaveInTx(ctx context.Context, account domain.InternalAccount, tx *gorm.DB) error {
	return r.WithTx(tx).Save(ctx, account)
}

// FindByID retrieves an account by its UUID.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (domain.InternalAccount, error) {
	var account domain.InternalAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity InternalAccountEntity
		result := tx.Where("id = ? AND deleted_at IS NULL", id).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		account = toDomain(&entity)
		return nil
	})
	if err != nil {
		return domain.InternalAccount{}, err
	}
	return account, nil
}

// FindByIDForUpdate retrieves an account by its UUID with a pessimistic lock.
func (r *Repository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (domain.InternalAccount, error) {
	var account domain.InternalAccount

	err := r.withForUpdateScope(ctx, func(tx *gorm.DB) error {
		var entity InternalAccountEntity
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		account = toDomain(&entity)
		return nil
	})
	if err != nil {
		return domain.InternalAccount{}, err
	}
	return account, nil
}

// FindByCode retrieves an account by its unique code.
func (r *Repository) FindByCode(ctx context.Context, accountCode string) (domain.InternalAccount, error) {
	var account domain.InternalAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity InternalAccountEntity
		result := tx.Where("account_code = ? AND deleted_at IS NULL", accountCode).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		account = toDomain(&entity)
		return nil
	})
	if err != nil {
		return domain.InternalAccount{}, err
	}
	return account, nil
}

// List returns accounts matching the filter criteria.
// Query performance supported by: idx_account_type, idx_instrument_code, idx_status, idx_deleted_at.
// For high-volume filtered queries, consider composite index on (account_type, instrument_code).
// Uses offset-based pagination; for large datasets cursor-based pagination would be more performant.
func (r *Repository) List(ctx context.Context, filter domain.ListFilter) ([]domain.InternalAccount, error) {
	var accounts []domain.InternalAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Deterministic ordering is required for stable pagination
		query := tx.Where("deleted_at IS NULL").Order("created_at ASC, id ASC")

		if filter.AccountType != nil {
			query = query.Where("account_type = ?", string(*filter.AccountType))
		}
		if filter.InstrumentCode != nil {
			query = query.Where("instrument_code = ?", *filter.InstrumentCode)
		}
		if filter.Status != nil {
			query = query.Where("status = ?", string(*filter.Status))
		}
		if filter.ClearingPurpose != nil {
			query = query.Where("clearing_purpose = ?", string(*filter.ClearingPurpose))
		}
		if filter.OrgPartyID != nil {
			query = query.Where("org_party_id = ?", *filter.OrgPartyID)
		}

		// Apply pagination
		if filter.Limit > 0 {
			query = query.Limit(filter.Limit)
		} else {
			query = query.Limit(100) // Default limit
		}
		if filter.Offset > 0 {
			query = query.Offset(filter.Offset)
		}

		var entities []InternalAccountEntity
		if err := query.Find(&entities).Error; err != nil {
			return err
		}

		accounts = make([]domain.InternalAccount, 0, len(entities))
		for i := range entities {
			accounts = append(accounts, toDomain(&entities[i]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// FindByOrganization retrieves all accounts scoped to the given organization party ID.
// Returns an empty slice if no accounts match.
func (r *Repository) FindByOrganization(ctx context.Context, orgPartyID uuid.UUID) ([]domain.InternalAccount, error) {
	return r.List(ctx, domain.ListFilter{OrgPartyID: &orgPartyID})
}

// ExistsByCode checks if an account with the given code exists.
func (r *Repository) ExistsByCode(ctx context.Context, accountCode string) (bool, error) {
	var exists bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var count int64
		result := tx.Model(&InternalAccountEntity{}).
			Where("account_code = ? AND deleted_at IS NULL", accountCode).
			Count(&count)

		if result.Error != nil {
			return result.Error
		}
		exists = count > 0
		return nil
	})
	return exists, err
}

// RecordStatusChange persists a status change to the audit trail.
// Note: Does not validate account existence at app level; relies on FK constraint
// (fk_status_history_account) to enforce referential integrity. This allows the
// database to be the single source of truth for constraint enforcement.
func (r *Repository) RecordStatusChange(ctx context.Context, accountID, fromStatus, toStatus, reason string) error {
	auditUser := audit.GetUserFromContext(ctx)

	entity := StatusHistoryEntity{
		AccountID:  accountID,
		FromStatus: fromStatus,
		ToStatus:   toStatus,
		Reason:     reason,
		ChangedBy:  auditUser,
	}

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(&entity).Error
	})
}

// Delete soft deletes an account by its UUID.
// Records both deleted_at and updated_by for complete audit trail.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	auditUser := audit.GetUserFromContext(ctx)

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&InternalAccountEntity{}).
			Where("id = ? AND deleted_at IS NULL", id).
			Updates(map[string]interface{}{
				"deleted_at": gorm.Expr("now()"),
				"updated_at": gorm.Expr("now()"),
				"updated_by": auditUser,
			})

		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrAccountNotFound
		}
		return nil
	})
}

// Ping checks database connectivity.
func (r *Repository) Ping() error {
	var result int
	return r.db.Raw("SELECT 1").Scan(&result).Error
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
// Uses structured pgconn.PgError detection, consistent with other services in the codebase.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}

	// Check for pgconn.PgError with unique violation code (23505)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}

	// Fallback for GORM-wrapped duplicate key errors
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
