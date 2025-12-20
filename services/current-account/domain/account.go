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

// CurrentAccount represents a BIAN current account facility domain model.
// This type is immutable: all fields are unexported and all methods return
// new instances rather than mutating the receiver.
type CurrentAccount struct {
	id                    uuid.UUID
	accountID             string
	accountIdentification string // IBAN
	partyID               string
	balance               Money
	availableBalance      Money
	status                AccountStatus
	overdraftLimit        Money
	overdraftEnabled      bool
	overdraftRate         float64
	balanceUpdatedAt      time.Time
	version               int64
	createdAt             time.Time
	updatedAt             time.Time
}

// NewCurrentAccount creates a new current account with the given parameters.
// Returns a value type (not pointer) following immutability principles.
func NewCurrentAccount(accountID, iban, partyID, currency string) (CurrentAccount, error) {
	now := time.Now()
	zeroMoney, err := NewMoney(currency, 0)
	if err != nil {
		return CurrentAccount{}, err
	}

	return CurrentAccount{
		id:                    uuid.New(),
		accountID:             accountID,
		accountIdentification: iban,
		partyID:               partyID,
		balance:               zeroMoney,
		availableBalance:      zeroMoney,
		status:                AccountStatusActive,
		overdraftLimit:        zeroMoney,
		overdraftEnabled:      false,
		overdraftRate:         0,
		balanceUpdatedAt:      now,
		version:               1,
		createdAt:             now,
		updatedAt:             now,
	}, nil
}

// Deposit adds funds to the account and returns a new account with the updated balance.
// The original account is not modified.
func (a CurrentAccount) Deposit(amount Money) (CurrentAccount, error) {
	if !amount.IsPositive() {
		return CurrentAccount{}, ErrInvalidAmount
	}

	if a.status == AccountStatusFrozen {
		return CurrentAccount{}, ErrAccountFrozen
	}

	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrAccountClosed
	}

	if amount.Currency() != a.balance.Currency() {
		return CurrentAccount{}, ErrCurrencyMismatch
	}

	// Use immutable Add method
	newBalance, err := a.balance.Add(amount)
	if err != nil {
		return CurrentAccount{}, err
	}

	now := time.Now()
	newAvailableBalance := calculateAvailableBalance(newBalance, a.overdraftLimit, a.overdraftEnabled)

	return CurrentAccount{
		id:                    a.id,
		accountID:             a.accountID,
		accountIdentification: a.accountIdentification,
		partyID:               a.partyID,
		balance:               newBalance,
		availableBalance:      newAvailableBalance,
		status:                a.status,
		overdraftLimit:        a.overdraftLimit,
		overdraftEnabled:      a.overdraftEnabled,
		overdraftRate:         a.overdraftRate,
		balanceUpdatedAt:      now,
		version:               a.version + 1,
		createdAt:             a.createdAt,
		updatedAt:             now,
	}, nil
}

// Withdraw removes funds from the account and returns a new account with the updated balance.
// The original account is not modified.
func (a CurrentAccount) Withdraw(amount Money) (CurrentAccount, error) {
	if !amount.IsPositive() {
		return CurrentAccount{}, ErrInvalidAmount
	}

	if a.status == AccountStatusFrozen {
		return CurrentAccount{}, ErrAccountFrozen
	}

	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrAccountClosed
	}

	if amount.Currency() != a.balance.Currency() {
		return CurrentAccount{}, ErrCurrencyMismatch
	}

	// Check if sufficient funds (including overdraft)
	cmp, _ := amount.Compare(a.availableBalance) // Same currency already verified above
	if cmp > 0 {
		return CurrentAccount{}, ErrInsufficientFunds
	}

	// Use immutable Subtract method
	newBalance, err := a.balance.Subtract(amount)
	if err != nil {
		return CurrentAccount{}, err
	}

	now := time.Now()
	newAvailableBalance := calculateAvailableBalance(newBalance, a.overdraftLimit, a.overdraftEnabled)

	return CurrentAccount{
		id:                    a.id,
		accountID:             a.accountID,
		accountIdentification: a.accountIdentification,
		partyID:               a.partyID,
		balance:               newBalance,
		availableBalance:      newAvailableBalance,
		status:                a.status,
		overdraftLimit:        a.overdraftLimit,
		overdraftEnabled:      a.overdraftEnabled,
		overdraftRate:         a.overdraftRate,
		balanceUpdatedAt:      now,
		version:               a.version + 1,
		createdAt:             a.createdAt,
		updatedAt:             now,
	}, nil
}

// calculateAvailableBalance is a pure function that computes available balance
// based on current balance and overdraft settings.
func calculateAvailableBalance(balance, overdraftLimit Money, overdraftEnabled bool) Money {
	if overdraftEnabled {
		// Use immutable Add method; should never fail if SetOverdraftLimit validated correctly
		newAvail, err := balance.Add(overdraftLimit)
		if err != nil {
			// This indicates a bug: either currency mismatch or overflow that bypassed validation
			panic("BUG: OverdraftLimit currency mismatch or overflow detected in calculateAvailableBalance: " + err.Error())
		}
		return newAvail
	}
	return balance
}

// withStatus returns a new account with the given status.
// This is a helper method to reduce duplication in status transition methods.
func (a CurrentAccount) withStatus(status AccountStatus) CurrentAccount {
	return CurrentAccount{
		id:                    a.id,
		accountID:             a.accountID,
		accountIdentification: a.accountIdentification,
		partyID:               a.partyID,
		balance:               a.balance,
		availableBalance:      a.availableBalance,
		status:                status,
		overdraftLimit:        a.overdraftLimit,
		overdraftEnabled:      a.overdraftEnabled,
		overdraftRate:         a.overdraftRate,
		balanceUpdatedAt:      a.balanceUpdatedAt,
		version:               a.version + 1,
		createdAt:             a.createdAt,
		updatedAt:             time.Now(),
	}
}

// Freeze suspends the account and returns a new account with frozen status.
// The original account is not modified.
func (a CurrentAccount) Freeze() (CurrentAccount, error) {
	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrInvalidStatusTransition
	}

	return a.withStatus(AccountStatusFrozen), nil
}

// Activate restores the account to active status and returns a new account.
// The original account is not modified.
func (a CurrentAccount) Activate() (CurrentAccount, error) {
	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrInvalidStatusTransition
	}

	return a.withStatus(AccountStatusActive), nil
}

// Close permanently closes the account and returns a new account with closed status.
// The original account is not modified.
func (a CurrentAccount) Close() (CurrentAccount, error) {
	return a.withStatus(AccountStatusClosed), nil
}

// SetOverdraftLimit configures the overdraft facility and returns a new account.
// The original account is not modified.
func (a CurrentAccount) SetOverdraftLimit(limit Money, rate float64, enabled bool) (CurrentAccount, error) {
	if limit.Currency() != a.balance.Currency() {
		return CurrentAccount{}, ErrCurrencyMismatch
	}

	// Validate that Balance + OverdraftLimit won't overflow if enabled
	if enabled {
		_, err := a.balance.Add(limit)
		if err != nil {
			return CurrentAccount{}, err // Return overflow error to caller
		}
	}

	newAvailableBalance := calculateAvailableBalance(a.balance, limit, enabled)

	return CurrentAccount{
		id:                    a.id,
		accountID:             a.accountID,
		accountIdentification: a.accountIdentification,
		partyID:               a.partyID,
		balance:               a.balance,
		availableBalance:      newAvailableBalance,
		status:                a.status,
		overdraftLimit:        limit,
		overdraftEnabled:      enabled,
		overdraftRate:         rate,
		balanceUpdatedAt:      a.balanceUpdatedAt,
		version:               a.version + 1,
		createdAt:             a.createdAt,
		updatedAt:             time.Now(),
	}, nil
}

// Accessor methods for unexported fields.
// These return copies of values, preserving immutability.

// ID returns the internal UUID of the account.
func (a CurrentAccount) ID() uuid.UUID { return a.id }

// AccountID returns the business account identifier (e.g., "ACC-xxx").
func (a CurrentAccount) AccountID() string { return a.accountID }

// AccountIdentification returns the IBAN.
func (a CurrentAccount) AccountIdentification() string { return a.accountIdentification }

// PartyID returns the party (customer) identifier.
func (a CurrentAccount) PartyID() string { return a.partyID }

// Balance returns the current balance.
func (a CurrentAccount) Balance() Money { return a.balance }

// AvailableBalance returns the available balance (including overdraft if enabled).
func (a CurrentAccount) AvailableBalance() Money { return a.availableBalance }

// Status returns the account status.
func (a CurrentAccount) Status() AccountStatus { return a.status }

// OverdraftLimit returns the configured overdraft limit.
func (a CurrentAccount) OverdraftLimit() Money { return a.overdraftLimit }

// OverdraftEnabled returns whether overdraft is enabled.
func (a CurrentAccount) OverdraftEnabled() bool { return a.overdraftEnabled }

// OverdraftRate returns the overdraft interest rate.
func (a CurrentAccount) OverdraftRate() float64 { return a.overdraftRate }

// BalanceUpdatedAt returns when the balance was last updated.
func (a CurrentAccount) BalanceUpdatedAt() time.Time { return a.balanceUpdatedAt }

// Version returns the optimistic locking version.
func (a CurrentAccount) Version() int64 { return a.version }

// CreatedAt returns when the account was created.
func (a CurrentAccount) CreatedAt() time.Time { return a.createdAt }

// UpdatedAt returns when the account was last updated.
func (a CurrentAccount) UpdatedAt() time.Time { return a.updatedAt }

// Builder pattern for reconstructing accounts from persistence layer.
// This is needed because the persistence layer needs to set all fields
// when loading from the database.

// CurrentAccountBuilder provides a fluent API for constructing CurrentAccount instances.
// This is primarily used by the persistence layer to reconstruct accounts from database rows.
type CurrentAccountBuilder struct {
	account CurrentAccount
}

// NewCurrentAccountBuilder creates a new builder instance.
func NewCurrentAccountBuilder() *CurrentAccountBuilder {
	return &CurrentAccountBuilder{}
}

// WithID sets the account UUID.
func (b *CurrentAccountBuilder) WithID(id uuid.UUID) *CurrentAccountBuilder {
	b.account.id = id
	return b
}

// WithAccountID sets the business account identifier.
func (b *CurrentAccountBuilder) WithAccountID(accountID string) *CurrentAccountBuilder {
	b.account.accountID = accountID
	return b
}

// WithAccountIdentification sets the IBAN.
func (b *CurrentAccountBuilder) WithAccountIdentification(iban string) *CurrentAccountBuilder {
	b.account.accountIdentification = iban
	return b
}

// WithPartyID sets the party identifier.
func (b *CurrentAccountBuilder) WithPartyID(partyID string) *CurrentAccountBuilder {
	b.account.partyID = partyID
	return b
}

// WithBalance sets the balance.
func (b *CurrentAccountBuilder) WithBalance(balance Money) *CurrentAccountBuilder {
	b.account.balance = balance
	return b
}

// WithAvailableBalance sets the available balance.
func (b *CurrentAccountBuilder) WithAvailableBalance(availableBalance Money) *CurrentAccountBuilder {
	b.account.availableBalance = availableBalance
	return b
}

// WithStatus sets the account status.
func (b *CurrentAccountBuilder) WithStatus(status AccountStatus) *CurrentAccountBuilder {
	b.account.status = status
	return b
}

// WithOverdraftLimit sets the overdraft limit.
func (b *CurrentAccountBuilder) WithOverdraftLimit(limit Money) *CurrentAccountBuilder {
	b.account.overdraftLimit = limit
	return b
}

// WithOverdraftEnabled sets whether overdraft is enabled.
func (b *CurrentAccountBuilder) WithOverdraftEnabled(enabled bool) *CurrentAccountBuilder {
	b.account.overdraftEnabled = enabled
	return b
}

// WithOverdraftRate sets the overdraft interest rate.
func (b *CurrentAccountBuilder) WithOverdraftRate(rate float64) *CurrentAccountBuilder {
	b.account.overdraftRate = rate
	return b
}

// WithBalanceUpdatedAt sets when the balance was last updated.
func (b *CurrentAccountBuilder) WithBalanceUpdatedAt(t time.Time) *CurrentAccountBuilder {
	b.account.balanceUpdatedAt = t
	return b
}

// WithVersion sets the optimistic locking version.
func (b *CurrentAccountBuilder) WithVersion(version int64) *CurrentAccountBuilder {
	b.account.version = version
	return b
}

// WithCreatedAt sets when the account was created.
func (b *CurrentAccountBuilder) WithCreatedAt(t time.Time) *CurrentAccountBuilder {
	b.account.createdAt = t
	return b
}

// WithUpdatedAt sets when the account was last updated.
func (b *CurrentAccountBuilder) WithUpdatedAt(t time.Time) *CurrentAccountBuilder {
	b.account.updatedAt = t
	return b
}

// Build returns the constructed CurrentAccount.
func (b *CurrentAccountBuilder) Build() CurrentAccount {
	return b.account
}
