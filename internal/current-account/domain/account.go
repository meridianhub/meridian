package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInsufficientFunds       = errors.New("insufficient funds")
	ErrAccountFrozen           = errors.New("account is frozen")
	ErrAccountClosed           = errors.New("account is closed")
	ErrInvalidAmount           = errors.New("invalid amount")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
)

// AccountStatus represents the lifecycle state of an account
type AccountStatus string

const (
	AccountStatusActive AccountStatus = "ACTIVE"
	AccountStatusFrozen AccountStatus = "FROZEN"
	AccountStatusClosed AccountStatus = "CLOSED"
)

// Money represents a monetary amount with currency
type Money struct {
	AmountCents int64
	Currency    string
}

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
func NewCurrentAccount(accountID, iban, customerID, currency string) *CurrentAccount {
	now := time.Now()
	return &CurrentAccount{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AccountIdentification: iban,
		CustomerID:            customerID,
		Balance: Money{
			AmountCents: 0,
			Currency:    currency,
		},
		AvailableBalance: Money{
			AmountCents: 0,
			Currency:    currency,
		},
		Status: AccountStatusActive,
		OverdraftLimit: Money{
			AmountCents: 0,
			Currency:    currency,
		},
		OverdraftEnabled: false,
		OverdraftRate:    0,
		BalanceUpdatedAt: now,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// Deposit adds funds to the account
func (a *CurrentAccount) Deposit(amount Money) error {
	if amount.AmountCents <= 0 {
		return ErrInvalidAmount
	}

	if a.Status == AccountStatusFrozen {
		return ErrAccountFrozen
	}

	if a.Status == AccountStatusClosed {
		return ErrAccountClosed
	}

	if amount.Currency != a.Balance.Currency {
		return errors.New("currency mismatch")
	}

	a.Balance.AmountCents += amount.AmountCents
	a.calculateAvailableBalance()
	a.BalanceUpdatedAt = time.Now()
	a.UpdatedAt = time.Now()

	return nil
}

// Withdraw removes funds from the account
func (a *CurrentAccount) Withdraw(amount Money) error {
	if amount.AmountCents <= 0 {
		return ErrInvalidAmount
	}

	if a.Status == AccountStatusFrozen {
		return ErrAccountFrozen
	}

	if a.Status == AccountStatusClosed {
		return ErrAccountClosed
	}

	if amount.Currency != a.Balance.Currency {
		return errors.New("currency mismatch")
	}

	// Check if sufficient funds (including overdraft)
	if amount.AmountCents > a.AvailableBalance.AmountCents {
		return ErrInsufficientFunds
	}

	a.Balance.AmountCents -= amount.AmountCents
	a.calculateAvailableBalance()
	a.BalanceUpdatedAt = time.Now()
	a.UpdatedAt = time.Now()

	return nil
}

// calculateAvailableBalance updates available balance based on overdraft settings
func (a *CurrentAccount) calculateAvailableBalance() {
	if a.OverdraftEnabled {
		a.AvailableBalance.AmountCents = a.Balance.AmountCents + a.OverdraftLimit.AmountCents
	} else {
		a.AvailableBalance.AmountCents = a.Balance.AmountCents
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
	if limit.Currency != a.Balance.Currency {
		return errors.New("currency mismatch")
	}

	a.OverdraftLimit = limit
	a.OverdraftRate = rate
	a.OverdraftEnabled = enabled
	a.calculateAvailableBalance()
	a.UpdatedAt = time.Now()

	return nil
}
