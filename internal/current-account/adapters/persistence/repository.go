package persistence

import (
	"errors"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/internal/current-account/domain"
	"gorm.io/gorm"
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

// Save creates or updates an account with optimistic locking
func (r *Repository) Save(account *domain.CurrentAccount) error {
	entity := toEntity(account)

	// Check if exists
	var existing CurrentAccountEntity
	result := r.db.Where("account_id = ?", entity.AccountID).First(&existing)

	if result.Error == nil {
		// Update existing with optimistic locking check
		entity.ID = existing.ID
		entity.CreatedAt = existing.CreatedAt

		// Optimistic locking: Check version hasn't changed
		if account.Version != existing.Version {
			return ErrVersionConflict
		}

		// Increment version for update
		newVersion := existing.Version + 1

		// Use WHERE clause with version check for atomic update
		updateResult := r.db.Model(&CurrentAccountEntity{}).
			Where("account_id = ? AND version = ?", entity.AccountID, existing.Version).
			Updates(map[string]interface{}{
				"balance_cents":           entity.BalanceCents,
				"available_balance_cents": entity.AvailableBalanceCents,
				"status":                  entity.Status,
				"overdraft_limit_cents":   entity.OverdraftLimitCents,
				"overdraft_enabled":       entity.OverdraftEnabled,
				"overdraft_rate":          entity.OverdraftRate,
				"balance_updated_at":      entity.BalanceUpdatedAt,
				"updated_at":              entity.UpdatedAt,
				"version":                 newVersion,
			})

		if updateResult.Error != nil {
			return updateResult.Error
		}

		// Check if any rows were updated (version conflict)
		if updateResult.RowsAffected == 0 {
			return ErrVersionConflict
		}

		// Update domain model version
		account.Version = newVersion

		return nil
	}

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Create new
		return r.db.Create(&entity).Error
	}

	return result.Error
}

// FindByID retrieves an account by its account ID
func (r *Repository) FindByID(accountID string) (*domain.CurrentAccount, error) {
	var entity CurrentAccountEntity
	result := r.db.Where("account_id = ? AND deleted_at IS NULL", accountID).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByIBAN retrieves an account by its IBAN
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
		Where("account_id = ?", accountID).
		Update("deleted_at", time.Now()).Error
}

// toEntity converts domain model to database entity
func toEntity(account *domain.CurrentAccount) *CurrentAccountEntity {
	return &CurrentAccountEntity{
		ID:                    account.ID,
		AccountID:             account.AccountID,
		AccountIdentification: account.AccountIdentification,
		CustomerID:            account.CustomerID,
		BalanceCents:          account.Balance.AmountCents(),
		AvailableBalanceCents: account.AvailableBalance.AmountCents(),
		Currency:              account.Balance.Currency(),
		Status:                string(account.Status),
		OverdraftLimitCents:   account.OverdraftLimit.AmountCents(),
		OverdraftEnabled:      account.OverdraftEnabled,
		OverdraftRate:         account.OverdraftRate,
		BalanceUpdatedAt:      account.BalanceUpdatedAt,
		CreatedAt:             account.CreatedAt,
		UpdatedAt:             account.UpdatedAt,
		Version:               account.Version,
	}
}

// toDomain converts database entity to domain model
func toDomain(entity *CurrentAccountEntity) (*domain.CurrentAccount, error) {
	// Use NewMoney constructor - errors indicate data corruption
	balance, err := domain.NewMoney(entity.Currency, entity.BalanceCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create balance from database: %w", err)
	}

	availableBalance, err := domain.NewMoney(entity.Currency, entity.AvailableBalanceCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create available balance from database: %w", err)
	}

	overdraftLimit, err := domain.NewMoney(entity.Currency, entity.OverdraftLimitCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create overdraft limit from database: %w", err)
	}

	return &domain.CurrentAccount{
		ID:                    entity.ID,
		AccountID:             entity.AccountID,
		AccountIdentification: entity.AccountIdentification,
		CustomerID:            entity.CustomerID,
		Balance:               balance,
		AvailableBalance:      availableBalance,
		Status:                domain.AccountStatus(entity.Status),
		OverdraftLimit:        overdraftLimit,
		OverdraftEnabled:      entity.OverdraftEnabled,
		OverdraftRate:         entity.OverdraftRate,
		BalanceUpdatedAt:      entity.BalanceUpdatedAt,
		Version:               entity.Version,
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
	}, nil
}
