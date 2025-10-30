package persistence

import (
	"errors"
	"time"

	"github.com/meridianhub/meridian/internal/current-account/domain"
	"gorm.io/gorm"
)

var (
	ErrAccountNotFound = errors.New("account not found")
	ErrAccountExists   = errors.New("account already exists")
)

// Repository provides persistence operations for current accounts
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new account repository
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Save creates or updates an account
func (r *Repository) Save(account *domain.CurrentAccount) error {
	entity := toEntity(account)

	// Check if exists
	var existing CurrentAccountEntity
	result := r.db.Where("account_id = ?", entity.AccountID).First(&existing)

	if result.Error == nil {
		// Update existing
		entity.ID = existing.ID
		entity.CreatedAt = existing.CreatedAt
		entity.Version = existing.Version + 1
		return r.db.Save(&entity).Error
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

	return toDomain(&entity), nil
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

	return toDomain(&entity), nil
}

// FindByCustomerID retrieves all accounts for a customer
func (r *Repository) FindByCustomerID(customerID string) ([]*domain.CurrentAccount, error) {
	var entities []CurrentAccountEntity
	result := r.db.Where("customer_id = ? AND deleted_at IS NULL", customerID).Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	accounts := make([]*domain.CurrentAccount, len(entities))
	for i, entity := range entities {
		accounts[i] = toDomain(&entity)
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
		BalanceCents:          account.Balance.AmountCents,
		AvailableBalanceCents: account.AvailableBalance.AmountCents,
		Currency:              account.Balance.Currency,
		Status:                string(account.Status),
		OverdraftLimitCents:   account.OverdraftLimit.AmountCents,
		OverdraftEnabled:      account.OverdraftEnabled,
		OverdraftRate:         account.OverdraftRate,
		BalanceUpdatedAt:      account.BalanceUpdatedAt,
		CreatedAt:             account.CreatedAt,
		UpdatedAt:             account.UpdatedAt,
		Version:               account.Version,
	}
}

// toDomain converts database entity to domain model
func toDomain(entity *CurrentAccountEntity) *domain.CurrentAccount {
	return &domain.CurrentAccount{
		ID:                    entity.ID,
		AccountID:             entity.AccountID,
		AccountIdentification: entity.AccountIdentification,
		CustomerID:            entity.CustomerID,
		Balance: domain.Money{
			AmountCents: entity.BalanceCents,
			Currency:    entity.Currency,
		},
		AvailableBalance: domain.Money{
			AmountCents: entity.AvailableBalanceCents,
			Currency:    entity.Currency,
		},
		Status: domain.AccountStatus(entity.Status),
		OverdraftLimit: domain.Money{
			AmountCents: entity.OverdraftLimitCents,
			Currency:    entity.Currency,
		},
		OverdraftEnabled: entity.OverdraftEnabled,
		OverdraftRate:    entity.OverdraftRate,
		BalanceUpdatedAt: entity.BalanceUpdatedAt,
		Version:          entity.Version,
		CreatedAt:        entity.CreatedAt,
		UpdatedAt:        entity.UpdatedAt,
	}
}
