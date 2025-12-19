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

// AccountClassification represents the fundamental accounting classification.
// This determines the normal balance direction for double-entry bookkeeping.
//
// Note: This is distinct from AccountType (in account_type.go) which represents
// the specific type of account (NOSTRO, VOSTRO, CURRENT, etc.). An account has
// both a Classification (determines accounting behavior) and a Type (describes
// what the account is used for).
type AccountClassification string

// Supported account classifications following standard accounting principles.
// These are the fundamental categories that determine debit/credit behavior.
const (
	// AccountClassificationAsset represents asset accounts.
	// Assets have a normal debit balance - debits increase, credits decrease.
	// Examples: cash, receivables, nostro accounts (our money at other banks).
	AccountClassificationAsset AccountClassification = "ASSET"

	// AccountClassificationLiability represents liability accounts.
	// Liabilities have a normal credit balance - credits increase, debits decrease.
	// Examples: payables, customer deposits, vostro accounts (their money at our bank).
	AccountClassificationLiability AccountClassification = "LIABILITY"
)

// IsValid checks if the account classification is valid.
func (c AccountClassification) IsValid() bool {
	switch c {
	case AccountClassificationAsset, AccountClassificationLiability:
		return true
	default:
		return false
	}
}

// String returns the string representation of the account classification.
func (c AccountClassification) String() string {
	return string(c)
}

// IncreasesWithDebit returns true if debit postings increase this classification's balance.
// Asset accounts increase with debits (normal debit balance).
func (c AccountClassification) IncreasesWithDebit() bool {
	return c == AccountClassificationAsset
}

// IncreasesWithCredit returns true if credit postings increase this classification's balance.
// Liability accounts increase with credits (normal credit balance).
func (c AccountClassification) IncreasesWithCredit() bool {
	return c == AccountClassificationLiability
}

// Account represents a financial account in the chart of accounts.
// This is an immutable value type - use With* methods to create modified copies.
//
// Accounts are used for internal ledger entries such as clearing accounts,
// nostro/vostro accounts, and settlement accounts.
type Account struct {
	ID             uuid.UUID
	AccountCode    string // Human-readable code, e.g., "1000" for cash, "CLR-001" for clearing
	Name           string
	Classification AccountClassification
	Currency       Currency
	IsActive       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewAccount creates a new account with validation.
// All fields are validated; returns an error if any validation fails.
func NewAccount(
	accountCode string,
	name string,
	classification AccountClassification,
	currency Currency,
) (Account, error) {
	if accountCode == "" {
		return Account{}, ErrInvalidAccountCode
	}
	if name == "" {
		return Account{}, ErrInvalidAccountName
	}
	if !classification.IsValid() {
		return Account{}, ErrInvalidAccountClassification
	}
	if !currency.IsValid() {
		return Account{}, ErrInvalidAccountCurrency
	}

	now := time.Now()
	return Account{
		ID:             uuid.New(),
		AccountCode:    accountCode,
		Name:           name,
		Classification: classification,
		Currency:       currency,
		IsActive:       true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// NewClearingAccount creates a new clearing account with ASSET classification.
// Clearing accounts are temporary holding accounts used during transaction processing.
// They have a normal debit balance (increase with debits, decrease with credits).
func NewClearingAccount(accountCode string, name string, currency Currency) (Account, error) {
	return NewAccount(accountCode, name, AccountClassificationAsset, currency)
}

// NewNostroAccount creates a new nostro account with ASSET classification.
// Nostro ("ours" in Latin) accounts represent our money held at other banks.
// They are assets from our perspective - we own the funds held elsewhere.
// They have a normal debit balance (increase with debits, decrease with credits).
func NewNostroAccount(accountCode string, name string, currency Currency) (Account, error) {
	return NewAccount(accountCode, name, AccountClassificationAsset, currency)
}

// NewVostroAccount creates a new vostro account with LIABILITY classification.
// Vostro ("yours" in Latin) accounts represent other banks' money held with us.
// They are liabilities from our perspective - we owe the funds to them.
// They have a normal credit balance (increase with credits, decrease with debits).
func NewVostroAccount(accountCode string, name string, currency Currency) (Account, error) {
	return NewAccount(accountCode, name, AccountClassificationLiability, currency)
}

// NewAssetAccount creates a new general asset account.
// Asset accounts have a normal debit balance (increase with debits).
func NewAssetAccount(accountCode string, name string, currency Currency) (Account, error) {
	return NewAccount(accountCode, name, AccountClassificationAsset, currency)
}

// NewLiabilityAccount creates a new general liability account.
// Liability accounts have a normal credit balance (increase with credits).
func NewLiabilityAccount(accountCode string, name string, currency Currency) (Account, error) {
	return NewAccount(accountCode, name, AccountClassificationLiability, currency)
}

// IncreasesWithDebit returns true if debit postings increase this account's balance.
// This is determined by the account's classification.
func (a Account) IncreasesWithDebit() bool {
	return a.Classification.IncreasesWithDebit()
}

// IncreasesWithCredit returns true if credit postings increase this account's balance.
// This is determined by the account's classification.
func (a Account) IncreasesWithCredit() bool {
	return a.Classification.IncreasesWithCredit()
}

// WithActive returns a copy of the account with the IsActive field updated.
// This is the immutable way to change activation status.
func (a Account) WithActive(active bool) Account {
	a.IsActive = active
	a.UpdatedAt = time.Now()
	return a
}

// ValidateForPosting checks if the account can be used for posting transactions.
// Returns an error if the account is inactive.
func (a Account) ValidateForPosting() error {
	if !a.IsActive {
		return ErrAccountInactive
	}
	return nil
}
