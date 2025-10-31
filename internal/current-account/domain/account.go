// Package domain contains the core business logic for current accounts
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Domain errors
var (
	ErrInsufficientFunds       = errors.New("insufficient funds")
	ErrAccountFrozen           = errors.New("account is frozen")
	ErrAccountClosed           = errors.New("account is closed")
	ErrInvalidAmount           = errors.New("invalid amount")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
)

// AccountStatus represents the lifecycle state of an account
type AccountStatus string

// Account status constants
const (
	AccountStatusActive AccountStatus = "ACTIVE"
	AccountStatusFrozen AccountStatus = "FROZEN"
	AccountStatusClosed AccountStatus = "CLOSED"
)

// CurrentAccount represents a BIAN current account facility domain model
type CurrentAccount struct {
	ID                    uuid.UUID
	AccountID             string
	AccountIdentification string // IBAN
	CustomerID            string
	Balance               Money
	AvailableBalance      Money
	Status                AccountStatus
	OverdraftLimit        Money
	OverdraftEnabled      bool
	OverdraftRate         float64
	BalanceUpdatedAt      time.Time
	Version               int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// NewCurrentAccount creates a new current account
func NewCurrentAccount(accountID, iban, customerID, currency string) (*CurrentAccount, error) {
	now := time.Now()
	zeroMoney, err := NewMoney(currency, 0)
	if err != nil {
		return nil, err
	}

	return &CurrentAccount{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AccountIdentification: iban,
		CustomerID:            customerID,
		Balance:               zeroMoney,
		AvailableBalance:      zeroMoney,
		Status:                AccountStatusActive,
		OverdraftLimit:        zeroMoney,
		OverdraftEnabled:      false,
		OverdraftRate:         0,
		BalanceUpdatedAt:      now,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

// Deposit adds funds to the account
func (a *CurrentAccount) Deposit(amount Money) error {
	if !amount.IsPositive() {
		return ErrInvalidAmount
	}

	if a.Status == AccountStatusFrozen {
		return ErrAccountFrozen
	}

	if a.Status == AccountStatusClosed {
		return ErrAccountClosed
	}

	if amount.Currency() != a.Balance.Currency() {
		return ErrCurrencyMismatch
	}

	// Use immutable Add method
	newBalance, err := a.Balance.Add(amount)
	if err != nil {
		return err
	}

	a.Balance = newBalance
	a.calculateAvailableBalance()

	now := time.Now()
	a.BalanceUpdatedAt = now
	a.UpdatedAt = now

	return nil
}

// Withdraw removes funds from the account
func (a *CurrentAccount) Withdraw(amount Money) error {
	if !amount.IsPositive() {
		return ErrInvalidAmount
	}

	if a.Status == AccountStatusFrozen {
		return ErrAccountFrozen
	}

	if a.Status == AccountStatusClosed {
		return ErrAccountClosed
	}

	if amount.Currency() != a.Balance.Currency() {
		return ErrCurrencyMismatch
	}

	// Check if sufficient funds (including overdraft)
	if amount.AmountCents() > a.AvailableBalance.AmountCents() {
		return ErrInsufficientFunds
	}

	// Use immutable Subtract method
	newBalance, err := a.Balance.Subtract(amount)
	if err != nil {
		return err
	}

	a.Balance = newBalance
	a.calculateAvailableBalance()

	now := time.Now()
	a.BalanceUpdatedAt = now
	a.UpdatedAt = now

	return nil
}

// calculateAvailableBalance updates available balance based on overdraft settings
func (a *CurrentAccount) calculateAvailableBalance() {
	if a.OverdraftEnabled {
		// Use immutable Add method
		newAvail, _ := a.Balance.Add(a.OverdraftLimit) // Safe: same currency
		a.AvailableBalance = newAvail
	} else {
		a.AvailableBalance = a.Balance
	}
}

// Freeze suspends the account
func (a *CurrentAccount) Freeze() error {
	if a.Status == AccountStatusClosed {
		return ErrInvalidStatusTransition
	}

	a.Status = AccountStatusFrozen
	a.UpdatedAt = time.Now()
	return nil
}

// Activate restores the account to active status
func (a *CurrentAccount) Activate() error {
	if a.Status == AccountStatusClosed {
		return ErrInvalidStatusTransition
	}

	a.Status = AccountStatusActive
	a.UpdatedAt = time.Now()
	return nil
}

// Close permanently closes the account
func (a *CurrentAccount) Close() error {
	a.Status = AccountStatusClosed
	a.UpdatedAt = time.Now()
	return nil
}

// SetOverdraftLimit configures the overdraft facility
func (a *CurrentAccount) SetOverdraftLimit(limit Money, rate float64, enabled bool) error {
	if limit.Currency() != a.Balance.Currency() {
		return ErrCurrencyMismatch
	}

	a.OverdraftLimit = limit
	a.OverdraftRate = rate
	a.OverdraftEnabled = enabled
	a.calculateAvailableBalance()
	a.UpdatedAt = time.Now()

	return nil
}
