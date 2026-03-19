package testhelpers

import (
	"testing"

	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/assert"
)

func TestNewCurrentAccount(t *testing.T) {
	account := NewCurrentAccount(t, "ACC-001")
	assert.Equal(t, "ACC-001", account.AccountID())
	assert.Equal(t, "GBP", account.InstrumentCode())
}

func TestNewCurrentAccountWithInstrument(t *testing.T) {
	account := NewCurrentAccountWithInstrument(t, "ACC-002", "EUR")
	assert.Equal(t, "ACC-002", account.AccountID())
	assert.Equal(t, "EUR", account.InstrumentCode())
}

func TestNewLien(t *testing.T) {
	lien := NewLien(t, domain.LienStatusActive)
	assert.Equal(t, domain.LienStatusActive, lien.Status)
	assert.Equal(t, "PO-TEST-001", lien.PaymentOrderReference)
}
