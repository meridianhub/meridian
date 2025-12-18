// Package config provides configuration structures for the current-account service.
package config

import (
	"errors"
	"os"
	"strings"
)

// AccountConfig holds configuration for internal accounts used in transaction routing.
//
// DepositClearingAccountID identifies the bank's clearing account that receives the
// debit side of customer deposit transactions. When a customer deposits funds, this
// clearing account is debited while the customer's account is credited, representing
// funds received but not yet settled through the banking system.
type AccountConfig struct {
	// DepositClearingAccountID is the account ID for deposit clearing (debit side).
	// This is typically a bank internal clearing account in FinancialAccounting.
	DepositClearingAccountID string

	// NostroAccountID is the nostro account for external settlements (optional).
	// Used when funds need to be settled with external banking partners.
	NostroAccountID string
}

// Validation errors
var (
	ErrEmptyDepositClearingAccountID = errors.New("deposit clearing account ID is required")
)

// LoadAccountConfig loads account configuration from environment variables.
//
// Required environment variables:
//   - DEPOSIT_CLEARING_ACCOUNT_ID: Account ID for deposit clearing (required)
//
// Optional environment variables:
//   - NOSTRO_ACCOUNT_ID: Nostro account for external settlements
func LoadAccountConfig() (*AccountConfig, error) {
	cfg := &AccountConfig{
		DepositClearingAccountID: strings.TrimSpace(os.Getenv("DEPOSIT_CLEARING_ACCOUNT_ID")),
		NostroAccountID:          strings.TrimSpace(os.Getenv("NOSTRO_ACCOUNT_ID")),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate validates the account configuration.
func (c *AccountConfig) Validate() error {
	if c.DepositClearingAccountID == "" {
		return ErrEmptyDepositClearingAccountID
	}
	return nil
}
