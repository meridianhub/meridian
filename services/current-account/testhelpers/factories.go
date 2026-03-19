// Package testhelpers provides shared test factories and utilities for current-account tests.
package testhelpers

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/require"
)

// NewCurrentAccount creates a test current account with sensible defaults.
// The account is created with GBP currency and "CURRENCY" dimension.
func NewCurrentAccount(t *testing.T, accountID string) domain.CurrentAccount {
	t.Helper()
	account, err := domain.NewCurrentAccount(
		accountID,
		"GB"+accountID, // external identifier
		uuid.New().String(),
		"GBP",
	)
	require.NoError(t, err)
	return account
}

// NewCurrentAccountWithInstrument creates a test current account with a specific instrument code.
func NewCurrentAccountWithInstrument(t *testing.T, accountID, instrumentCode string) domain.CurrentAccount {
	t.Helper()
	account, err := domain.NewCurrentAccount(
		accountID,
		"GB"+accountID,
		uuid.New().String(),
		instrumentCode,
	)
	require.NoError(t, err)
	return account
}

// NewLien creates a test lien with the given status.
func NewLien(t *testing.T, status domain.LienStatus) *domain.Lien {
	t.Helper()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)

	now := time.Now()
	return &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		Amount:                amount,
		Status:                status,
		PaymentOrderReference: "PO-TEST-001",
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}
