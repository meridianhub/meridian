package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/platform/audit"
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
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// Save creates or updates an account.
// The context is used to extract audit information (user ID) for the created_by/updated_by fields.
//
// NOTE: Optimistic locking via version column is NOT currently implemented because
// the migration schema doesn't include a version column. The previous code referenced
// a non-existent 'version' column which would have failed at runtime.
// TODO: Add version column migration and restore optimistic locking (see ADR-008)
// For now, use FindByIDForUpdate() with SELECT FOR UPDATE for concurrent modifications.
func (r *Repository) Save(ctx context.Context, account *domain.CurrentAccount) error {
	entity, err := toEntity(ctx, account)
	if err != nil {
		return err
	}

	// Check if exists by account_identification (IBAN)
	var existing CurrentAccountEntity
	result := r.db.WithContext(ctx).Where("account_identification = ?", entity.AccountIdentification).First(&existing)

	if result.Error == nil {
		// Update existing
		entity.ID = existing.ID
		entity.CreatedAt = existing.CreatedAt
		entity.CreatedBy = existing.CreatedBy

		// Use WHERE clause for atomic update
		updateResult := r.db.WithContext(ctx).Model(&CurrentAccountEntity{}).
			Where("account_identification = ?", entity.AccountIdentification).
			Updates(map[string]interface{}{
				"balance":            entity.Balance,
				"available_balance":  entity.AvailableBalance,
				"status":             entity.Status,
				"overdraft_limit":    entity.OverdraftLimit,
				"overdraft_rate":     entity.OverdraftRate,
				"balance_updated_at": entity.BalanceUpdatedAt,
				"updated_at":         entity.UpdatedAt,
				"updated_by":         entity.UpdatedBy,
			})

		if updateResult.Error != nil {
			return updateResult.Error
		}

		return nil
	}

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Create new
		return r.db.WithContext(ctx).Create(&entity).Error
	}

	return result.Error
}

// FindByID retrieves an account by its account identification (IBAN)
func (r *Repository) FindByID(accountID string) (*domain.CurrentAccount, error) {
	var entity CurrentAccountEntity
	result := r.db.Where("account_identification = ? AND deleted_at IS NULL", accountID).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByIDForUpdate retrieves an account by its account identification with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
func (r *Repository) FindByIDForUpdate(accountID string) (*domain.CurrentAccount, error) {
	var entity CurrentAccountEntity
	result := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("account_identification = ? AND deleted_at IS NULL", accountID).
		First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByIBAN retrieves an account by its IBAN (stored in account_identification column)
func (r *Repository) FindByIBAN(iban string) (*domain.CurrentAccount, error) {
	var entity CurrentAccountEntity
	result := r.db.Where("account_identification = ? AND deleted_at IS NULL", iban).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByUUID retrieves an account by its internal UUID
func (r *Repository) FindByUUID(id uuid.UUID) (*domain.CurrentAccount, error) {
	var entity CurrentAccountEntity
	result := r.db.Where("id = ? AND deleted_at IS NULL", id).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByUUIDForUpdate retrieves an account by its internal UUID with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
func (r *Repository) FindByUUIDForUpdate(id uuid.UUID) (*domain.CurrentAccount, error) {
	var entity CurrentAccountEntity
	result := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND deleted_at IS NULL", id).
		First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByCustomerID retrieves all accounts for a customer
func (r *Repository) FindByCustomerID(customerID string) ([]*domain.CurrentAccount, error) {
	var entities []CurrentAccountEntity
	result := r.db.Where("customer_id = ? AND deleted_at IS NULL", customerID).Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	accounts := make([]*domain.CurrentAccount, 0, len(entities))
	for _, entity := range entities {
		account, err := toDomain(&entity)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}

	return accounts, nil
}

// Delete soft deletes an account
func (r *Repository) Delete(accountID string) error {
	return r.db.Model(&CurrentAccountEntity{}).
		Where("account_identification = ?", accountID).
		Update("deleted_at", time.Now()).Error
}

// Ping checks database connectivity without triggering record-not-found logging.
// This is used by health checks to verify the database is reachable.
func (r *Repository) Ping() error {
	var result int
	return r.db.Raw("SELECT 1").Scan(&result).Error
}

// toEntity converts domain model to database entity
// Note: The entity schema matches migrations/current_account/*.sql
// Some domain fields don't have corresponding database columns yet:
// - OverdraftEnabled is derived from OverdraftLimit > 0
// - Version needs migration (tracked in #206)
func toEntity(ctx context.Context, account *domain.CurrentAccount) (*CurrentAccountEntity, error) {
	// Parse CustomerID as UUID - domain model uses string for flexibility
	customerUUID, err := uuid.Parse(account.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invalid customer ID %q: %w", account.CustomerID, err)
	}

	// Extract audit user from context (falls back to "system" if not available)
	auditUser := audit.GetUserFromContext(ctx)

	return &CurrentAccountEntity{
		ID:                    account.ID,
		AccountID:             account.AccountID,             // Business account identifier
		AccountIdentification: account.AccountIdentification, // IBAN stored in account_identification
		AccountType:           "current",                     // Default for current accounts
		Currency:              account.Balance.Currency(),
		Status:                string(account.Status),
		CustomerID:            customerUUID,
		Balance:               account.Balance.AmountCents(),
		AvailableBalance:      account.AvailableBalance.AmountCents(),
		OverdraftLimit:        account.OverdraftLimit.AmountCents(),
		OverdraftRate:         account.OverdraftRate,
		BalanceUpdatedAt:      &account.BalanceUpdatedAt,
		CreatedAt:             account.CreatedAt,
		UpdatedAt:             account.UpdatedAt,
		CreatedBy:             auditUser,
		UpdatedBy:             auditUser,
	}, nil
}

// toDomain converts database entity to domain model
// Note: OverdraftEnabled is derived from OverdraftLimit > 0
// Version field needs migration (tracked in #206)
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
		CustomerID:            entity.CustomerID.String(),
		Balance:               balance,
		AvailableBalance:      availableBalance,
		Status:                domain.AccountStatus(entity.Status),
		OverdraftLimit:        overdraftLimit,
		OverdraftEnabled:      overdraftEnabled,
		OverdraftRate:         entity.OverdraftRate,
		BalanceUpdatedAt:      balanceUpdatedAt,
		Version:               1, // Default - column doesn't exist in DB yet (tracked in #206)
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
	}, nil
}
