package persistence

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-bank-account/domain"
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

// Repository provides persistence operations for internal bank accounts.
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
func (r *Repository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
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
func (r *Repository) Save(ctx context.Context, account domain.InternalBankAccount) error {
	entity := toEntity(ctx, account)

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx, err := r.withTenantScope(ctx, tx)
		if err != nil {
			return err
		}

		// Check if exists by account_id (business identifier)
		var existing InternalBankAccountEntity
		result := tx.Where("account_id = ?", entity.AccountID).First(&existing)

		if result.Error == nil {
			// Update existing with optimistic locking
			entity.ID = existing.ID
			entity.CreatedAt = existing.CreatedAt
			entity.CreatedBy = existing.CreatedBy

			// Optimistic locking contract: The domain model increments version on all
			// mutations (Suspend, Activate, Close, UpdateCorrespondent) before passing
			// to Save. We check the original version (current - 1) to detect concurrent
			// modifications. If another transaction has modified the record, the version
			// won't match and we return ErrVersionConflict.
			originalVersion := entity.Version - 1
			updateResult := tx.Model(&InternalBankAccountEntity{}).
				Where("account_id = ? AND version = ?", entity.AccountID, originalVersion).
				Updates(map[string]interface{}{
					"account_code":               entity.AccountCode,
					"name":                       entity.Name,
					"status":                     entity.Status,
					"correspondent_bank_id":      entity.CorrespondentBankID,
					"correspondent_bank_name":    entity.CorrespondentBankName,
					"correspondent_external_ref": entity.CorrespondentExternalRef,
					"attributes":                 entity.Attributes,
					"version":                    entity.Version,
					"updated_at":                 entity.UpdatedAt,
					"updated_by":                 entity.UpdatedBy,
				})

			if updateResult.Error != nil {
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

// FindByID retrieves an account by its UUID.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (domain.InternalBankAccount, error) {
	var account domain.InternalBankAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity InternalBankAccountEntity
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
		return domain.InternalBankAccount{}, err
	}
	return account, nil
}

// FindByIDForUpdate retrieves an account by its UUID with a pessimistic lock.
func (r *Repository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (domain.InternalBankAccount, error) {
	var account domain.InternalBankAccount

	err := r.withForUpdateScope(ctx, func(tx *gorm.DB) error {
		var entity InternalBankAccountEntity
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
		return domain.InternalBankAccount{}, err
	}
	return account, nil
}

// FindByCode retrieves an account by its unique code.
func (r *Repository) FindByCode(ctx context.Context, accountCode string) (domain.InternalBankAccount, error) {
	var account domain.InternalBankAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity InternalBankAccountEntity
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
		return domain.InternalBankAccount{}, err
	}
	return account, nil
}

// List returns accounts matching the filter criteria.
func (r *Repository) List(ctx context.Context, filter domain.ListFilter) ([]domain.InternalBankAccount, error) {
	var accounts []domain.InternalBankAccount
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

		// Apply pagination
		if filter.Limit > 0 {
			query = query.Limit(filter.Limit)
		} else {
			query = query.Limit(100) // Default limit
		}
		if filter.Offset > 0 {
			query = query.Offset(filter.Offset)
		}

		var entities []InternalBankAccountEntity
		if err := query.Find(&entities).Error; err != nil {
			return err
		}

		accounts = make([]domain.InternalBankAccount, 0, len(entities))
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

// ExistsByCode checks if an account with the given code exists.
func (r *Repository) ExistsByCode(ctx context.Context, accountCode string) (bool, error) {
	var exists bool
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var count int64
		result := tx.Model(&InternalBankAccountEntity{}).
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
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&InternalBankAccountEntity{}).
			Where("id = ? AND deleted_at IS NULL", id).
			Update("deleted_at", gorm.Expr("now()"))

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
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
