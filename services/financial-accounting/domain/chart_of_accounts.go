package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidAccountClassification is returned when account classification is invalid.
	ErrInvalidAccountClassification = errors.New("invalid account classification")
	// ErrInvalidAccountCode is returned when account code is empty.
	ErrInvalidAccountCode = errors.New("account code cannot be empty")
	// ErrInvalidAccountName is returned when account name is empty.
	ErrInvalidAccountName = errors.New("account name cannot be empty")
	// ErrInvalidAccountCurrency is returned when account currency is invalid.
	ErrInvalidAccountCurrency = errors.New("invalid account currency")
	// ErrAccountInactive is returned when attempting to use an inactive account.
	ErrAccountInactive = errors.New("account is inactive")
)

// AccountClassification represents the classification of a financial account
// in the chart of accounts.
type AccountClassification string

// Supported account classifications for the financial accounting system.
const (
	AccountClassificationAsset     AccountClassification = "ASSET"
	AccountClassificationLiability AccountClassification = "LIABILITY"
	AccountClassificationClearing  AccountClassification = "CLEARING"
	AccountClassificationNostro    AccountClassification = "NOSTRO"
)

// IsValid checks if the account classification is valid.
func (c AccountClassification) IsValid() bool {
	switch c {
	case AccountClassificationAsset, AccountClassificationLiability,
		AccountClassificationClearing, AccountClassificationNostro:
		return true
	}
	return false
}

// String returns the string representation of the account classification.
func (c AccountClassification) String() string {
	return string(c)
}

// Account represents a financial account in the chart of accounts.
// Used for internal accounts such as clearing accounts, nostro accounts,
// and acquirer settlement accounts.
type Account struct {
	ID             uuid.UUID
	AccountCode    string // e.g., "1000" for cash accounts
	Name           string
	Classification AccountClassification
	Currency       Currency
	IsActive       bool
	CreatedAt      time.Time
}

// NewAccount creates a new account with validation.
func NewAccount(
	accountCode string,
	name string,
	classification AccountClassification,
	currency Currency,
) (*Account, error) {
	if accountCode == "" {
		return nil, ErrInvalidAccountCode
	}
	if name == "" {
		return nil, ErrInvalidAccountName
	}
	if !classification.IsValid() {
		return nil, ErrInvalidAccountClassification
	}
	if !currency.IsValid() {
		return nil, ErrInvalidAccountCurrency
	}

	return &Account{
		ID:             uuid.New(),
		AccountCode:    accountCode,
		Name:           name,
		Classification: classification,
		Currency:       currency,
		IsActive:       true,
		CreatedAt:      time.Now(),
	}, nil
}

// NewClearingAccount creates a new clearing account.
// Clearing accounts are used for temporary holding of funds during transaction processing.
func NewClearingAccount(name string, currency Currency) (*Account, error) {
	return NewAccount(generateAccountCode(), name, AccountClassificationClearing, currency)
}

// NewNostroAccount creates a new nostro account.
// Nostro accounts represent our accounts held at other banks.
func NewNostroAccount(name string, currency Currency) (*Account, error) {
	return NewAccount(generateAccountCode(), name, AccountClassificationNostro, currency)
}

// NewAssetAccount creates a new asset account.
// Asset accounts increase with debits and decrease with credits.
func NewAssetAccount(name string, currency Currency) (*Account, error) {
	return NewAccount(generateAccountCode(), name, AccountClassificationAsset, currency)
}

// NewLiabilityAccount creates a new liability account.
// Liability accounts increase with credits and decrease with debits.
func NewLiabilityAccount(name string, currency Currency) (*Account, error) {
	return NewAccount(generateAccountCode(), name, AccountClassificationLiability, currency)
}

// CanDebit returns true if debits increase this account's balance.
// Assets and clearing accounts increase with debits.
func (a *Account) CanDebit() bool {
	return a.Classification == AccountClassificationAsset ||
		a.Classification == AccountClassificationClearing
}

// CanCredit returns true if credits increase this account's balance.
// Liabilities and nostro accounts increase with credits.
func (a *Account) CanCredit() bool {
	return a.Classification == AccountClassificationLiability ||
		a.Classification == AccountClassificationNostro
}

// Deactivate marks the account as inactive.
func (a *Account) Deactivate() {
	a.IsActive = false
}

// Activate marks the account as active.
func (a *Account) Activate() {
	a.IsActive = true
}

// ValidateForPosting checks if the account can be used for posting transactions.
func (a *Account) ValidateForPosting() error {
	if !a.IsActive {
		return ErrAccountInactive
	}
	return nil
}

// generateAccountCode generates a unique account code based on UUID.
// In a real implementation, this might follow a specific numbering scheme.
func generateAccountCode() string {
	return uuid.New().String()[:8]
}
