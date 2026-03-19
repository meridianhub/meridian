// Package testhelpers provides shared test factories and utilities for internal-account tests.
package testhelpers

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/require"
)

// NewInternalAccount creates a test internal account of the given type.
// CLEARING accounts are automatically given ClearingPurposeGeneral.
func NewInternalAccount(t *testing.T, accountType domain.AccountType) domain.InternalAccount {
	t.Helper()

	accountID := "IBA-TEST"
	accountCode := "TEST_" + string(accountType)
	name := "Test " + string(accountType) + " Account"

	clearingPurpose := domain.ClearingPurposeUnspecified
	if accountType == domain.AccountTypeClearing {
		clearingPurpose = domain.ClearingPurposeGeneral
	}

	account, err := domain.NewInternalAccount(
		accountID, accountCode, name,
		accountType, clearingPurpose,
		"GBP", "CURRENCY",
	)
	require.NoError(t, err)
	return account
}

// NewInternalAccountWithID creates a test internal account with a specific ID and code.
func NewInternalAccountWithID(t *testing.T, accountID, accountCode, name string, accountType domain.AccountType) domain.InternalAccount {
	t.Helper()

	clearingPurpose := domain.ClearingPurposeUnspecified
	if accountType == domain.AccountTypeClearing {
		clearingPurpose = domain.ClearingPurposeGeneral
	}

	account, err := domain.NewInternalAccount(
		accountID, accountCode, name,
		accountType, clearingPurpose,
		"GBP", "CURRENCY",
	)
	require.NoError(t, err)
	return account
}

// NewLien creates a test lien with the given status. All fields are exported
// on the Lien struct so this can be used from any package.
func NewLien(t *testing.T, status domain.LienStatus) *domain.Lien {
	t.Helper()
	now := time.Now()
	return &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		InstrumentCode:        "GBP",
		Status:                status,
		PaymentOrderReference: "PO-TEST-001",
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}
