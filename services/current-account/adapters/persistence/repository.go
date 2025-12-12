//nolint:staticcheck // Uses AmountCents() for database persistence (backward compatible)
package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository errors
var (
	ErrAccountNotFound = errors.New("account not found")
	ErrAccountExists   = errors.New("account already exists")
	ErrVersionConflict = errors.New("version conflict: account was modified by another transaction")
)

// Repository provides persistence operations for current accounts
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new account repository
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
// IMPORTANT: In multi-org mode, the repository methods (FindByIDForUpdate,
// FindByUUIDForUpdate, etc.) will automatically set the organization scope
// on the transaction. However, for optimal performance and correct behavior,
// consider setting the org scope once at the start of your transaction using
// db.WithGormTenantScope() rather than relying on per-operation scoping.
//
// Example:
//
//	err := repo.DB().Transaction(func(tx *gorm.DB) error {
//	    // Set org scope once for the entire transaction
//	    tx, err := db.WithGormTenantScope(ctx, tx)
//	    if err != nil {
//	        return err
//	    }
//	    txRepo := repo.WithTx(tx)
//	    // All operations now use the scoped transaction
//	    account, err := txRepo.FindByIDForUpdate(ctx, accountID)
//	    // ...
//	})
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// hasOrganizationContext checks if organization context is present (multi-org mode).
func (r *Repository) hasOrganizationContext(ctx context.Context) bool {
	_, ok := tenant.FromContext(ctx)
	return ok
}

// withTenantScope returns a GORM DB instance scoped to the organization from context.
// If organization context is present (multi-org mode), it sets the PostgreSQL search_path.
// If organization context is missing (single-tenant mode), it returns the DB unchanged.
//
// This must be called within a transaction for the search_path setting to work correctly.
func (r *Repository) withTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	if r.hasOrganizationContext(ctx) {
		return db.WithGormTenantScope(ctx, tx)
	}
	// Single-tenant mode: no organization scope needed
	return tx, nil
}

// withOptionalOrgScope executes the given function with optional organization scoping.
// In single-tenant mode (no org context), it runs the function directly without a transaction.
// In multi-org mode, it wraps the function in a transaction and sets the search_path.
// This helper reduces code duplication across repository methods.
func (r *Repository) withOptionalOrgScope(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if !r.hasOrganizationContext(ctx) {
		// Single-tenant mode: run directly without transaction overhead
		return fn(r.db.WithContext(ctx))
	}
	// Multi-org mode: use the shared helper that handles transaction + org scope
	return db.WithGormOrganizationTransaction(ctx, r.db, fn)
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
		// The caller (e.g., lien_service.go) is responsible for the outer transaction,
		// but we still need to set the org scope for this operation.
		// Note: withTenantScope returns already-wrapped errors, so don't re-wrap.
		tx, err := r.withTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}

	// Not in a transaction - use the shared helper that handles transaction + org scope
	if r.hasOrganizationContext(ctx) {
		return db.WithGormOrganizationTransaction(ctx, r.db, fn)
	}

	// Single-tenant mode: still need a transaction for FOR UPDATE, but no org scope
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(tx)
	})
}

// Save creates or updates an account with optimistic locking.
// The context is used to extract audit information (user ID) for the created_by/updated_by fields.
// In multi-org mode, the context must contain the organization ID for schema routing.
//
// For updates, the version in the domain model must match the version in the database.
// If another transaction has modified the record (incremented the version), this save
// will fail with ErrVersionConflict. The caller should reload the entity and retry.
//
// Alternative: Use FindByIDForUpdate() with SELECT FOR UPDATE for pessimistic locking
// within a transaction when you need guaranteed exclusive access.
func (r *Repository) Save(ctx context.Context, account *domain.CurrentAccount) error {
	entity, err := toEntity(ctx, account)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Set organization scope if in multi-org mode
		// Note: withTenantScope returns already-wrapped errors from db.WithGormTenantScope
		tx, err := r.withTenantScope(ctx, tx)
		if err != nil {
			return err
		}

		// Check if exists by account_identification (IBAN)
		var existing CurrentAccountEntity
		result := tx.Where("account_identification = ?", entity.AccountIdentification).First(&existing)

		if result.Error == nil {
			// Update existing with optimistic locking
			entity.ID = existing.ID
			entity.CreatedAt = existing.CreatedAt
			entity.CreatedBy = existing.CreatedBy

			// Optimistic locking: only update if version matches, then increment
			updateResult := tx.Model(&CurrentAccountEntity{}).
				Where("account_identification = ? AND version = ?", entity.AccountIdentification, entity.Version).
				Updates(map[string]interface{}{
					"balance":            entity.Balance,
					"available_balance":  entity.AvailableBalance,
					"status":             entity.Status,
					"overdraft_limit":    entity.OverdraftLimit,
					"overdraft_rate":     entity.OverdraftRate,
					"balance_updated_at": entity.BalanceUpdatedAt,
					"version":            entity.Version + 1,
					"updated_at":         entity.UpdatedAt,
					"updated_by":         entity.UpdatedBy,
				})

			if updateResult.Error != nil {
				return updateResult.Error
			}

			// If no rows were affected, the version didn't match (concurrent modification)
			if updateResult.RowsAffected == 0 {
				return ErrVersionConflict
			}

			// Update the domain model's version to reflect the new database state
			account.Version = account.Version + 1

			return nil
		}

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create new - version starts at 1 (set by toEntity)
			if err := tx.Create(&entity).Error; err != nil {
				// Handle race condition: another transaction created the same account
				if isDuplicateKeyError(err) {
					return ErrAccountExists
				}
				return err
			}
			return nil
		}

		return result.Error
	})
}

// FindByID retrieves an account by its account identification (IBAN).
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByID(ctx context.Context, accountID string) (*domain.CurrentAccount, error) {
	var account *domain.CurrentAccount
	err := r.withOptionalOrgScope(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Where("account_identification = ? AND deleted_at IS NULL", accountID).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// FindByIDForUpdate retrieves an account by its account identification with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
// In multi-org mode, the context must contain the organization ID for schema routing.
//
// IMPORTANT: This method expects to be called within an existing transaction that already
// has the organization scope set. When using WithTx(), the caller is responsible for setting
// the org scope on the outer transaction. This method will set the org scope if not already
// in a transaction, but when called via WithTx(), it uses the existing transaction directly.
func (r *Repository) FindByIDForUpdate(ctx context.Context, accountID string) (*domain.CurrentAccount, error) {
	var account *domain.CurrentAccount

	// Perform the FOR UPDATE query, wrapping in org-scoped transaction if needed
	err := r.withForUpdateScope(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_identification = ? AND deleted_at IS NULL", accountID).
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// FindByIBAN retrieves an account by its IBAN (stored in account_identification column).
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByIBAN(ctx context.Context, iban string) (*domain.CurrentAccount, error) {
	var account *domain.CurrentAccount
	err := r.withOptionalOrgScope(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Where("account_identification = ? AND deleted_at IS NULL", iban).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// FindByUUID retrieves an account by its internal UUID.
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByUUID(ctx context.Context, id uuid.UUID) (*domain.CurrentAccount, error) {
	var account *domain.CurrentAccount
	err := r.withOptionalOrgScope(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Where("id = ? AND deleted_at IS NULL", id).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// FindByUUIDForUpdate retrieves an account by its internal UUID with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
// In multi-org mode, the context must contain the organization ID for schema routing.
//
// IMPORTANT: This method expects to be called within an existing transaction that already
// has the organization scope set. When using WithTx(), the caller is responsible for setting
// the org scope on the outer transaction. This method will set the org scope if not already
// in a transaction, but when called via WithTx(), it uses the existing transaction directly.
func (r *Repository) FindByUUIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.CurrentAccount, error) {
	var account *domain.CurrentAccount

	// Perform the FOR UPDATE query, wrapping in org-scoped transaction if needed
	err := r.withForUpdateScope(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// FindByPartyID retrieves all accounts for a party.
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByPartyID(ctx context.Context, partyID string) ([]*domain.CurrentAccount, error) {
	var accounts []*domain.CurrentAccount
	err := r.withOptionalOrgScope(ctx, func(tx *gorm.DB) error {
		var entities []CurrentAccountEntity
		result := tx.Where("party_id = ? AND deleted_at IS NULL", partyID).Find(&entities)

		if result.Error != nil {
			return result.Error
		}

		accounts = make([]*domain.CurrentAccount, 0, len(entities))
		for _, entity := range entities {
			account, err := toDomain(&entity)
			if err != nil {
				return err
			}
			accounts = append(accounts, account)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// Delete soft deletes an account.
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) Delete(ctx context.Context, accountID string) error {
	return r.withOptionalOrgScope(ctx, func(tx *gorm.DB) error {
		return tx.Model(&CurrentAccountEntity{}).
			Where("account_identification = ?", accountID).
			Update("deleted_at", time.Now()).Error
	})
}

// Ping checks database connectivity without triggering record-not-found logging.
// This is used by health checks to verify the database is reachable.
func (r *Repository) Ping() error {
	var result int
	return r.db.Raw("SELECT 1").Scan(&result).Error
}

// toEntity converts domain model to database entity
// Note: The entity schema matches migrations/current_account/*.sql
// OverdraftEnabled is derived from OverdraftLimit > 0
func toEntity(ctx context.Context, account *domain.CurrentAccount) (*CurrentAccountEntity, error) {
	// Parse PartyID as UUID - domain model uses string for flexibility
	partyUUID, err := uuid.Parse(account.PartyID)
	if err != nil {
		return nil, fmt.Errorf("invalid party ID %q: %w", account.PartyID, err)
	}

	// Extract audit user from context (falls back to "system" if not available)
	auditUser := audit.GetUserFromContext(ctx)

	return &CurrentAccountEntity{
		ID:                    account.ID,
		AccountID:             account.AccountID,             // Business account identifier
		AccountIdentification: account.AccountIdentification, // IBAN stored in account_identification
		AccountType:           "current",                     // Default for current accounts
		Currency:              string(account.Balance.Currency()),
		Status:                string(account.Status),
		PartyID:               partyUUID,
		Balance:               account.Balance.AmountCents(),
		AvailableBalance:      account.AvailableBalance.AmountCents(),
		OverdraftLimit:        account.OverdraftLimit.AmountCents(),
		OverdraftRate:         account.OverdraftRate,
		BalanceUpdatedAt:      &account.BalanceUpdatedAt,
		Version:               account.Version,
		CreatedAt:             account.CreatedAt,
		UpdatedAt:             account.UpdatedAt,
		CreatedBy:             auditUser,
		UpdatedBy:             auditUser,
	}, nil
}

// toDomain converts database entity to domain model
// Note: OverdraftEnabled is derived from OverdraftLimit > 0
func toDomain(entity *CurrentAccountEntity) (*domain.CurrentAccount, error) {
	// Use NewMoney constructor - errors indicate data corruption
	balance, err := domain.NewMoney(entity.Currency, entity.Balance)
	if err != nil {
		return nil, fmt.Errorf("failed to create balance from database: %w", err)
	}

	availableBalance, err := domain.NewMoney(entity.Currency, entity.AvailableBalance)
	if err != nil {
		return nil, fmt.Errorf("failed to create available balance from database: %w", err)
	}

	overdraftLimit, err := domain.NewMoney(entity.Currency, entity.OverdraftLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create overdraft limit from database: %w", err)
	}

	// Derive overdraft enabled from limit > 0
	overdraftEnabled := entity.OverdraftLimit > 0

	// Handle balance_updated_at - fallback to updated_at if null (for legacy rows)
	balanceUpdatedAt := entity.UpdatedAt
	if entity.BalanceUpdatedAt != nil {
		balanceUpdatedAt = *entity.BalanceUpdatedAt
	}

	return &domain.CurrentAccount{
		ID:                    entity.ID,
		AccountID:             entity.AccountID,             // Business account identifier
		AccountIdentification: entity.AccountIdentification, // IBAN stored in account_identification
		PartyID:               entity.PartyID.String(),
		Balance:               balance,
		AvailableBalance:      availableBalance,
		Status:                domain.AccountStatus(entity.Status),
		OverdraftLimit:        overdraftLimit,
		OverdraftEnabled:      overdraftEnabled,
		OverdraftRate:         entity.OverdraftRate,
		BalanceUpdatedAt:      balanceUpdatedAt,
		Version:               entity.Version,
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
	}, nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
// This handles the race condition where two concurrent creates attempt to insert
// the same account_identification (IBAN).
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
