package domain_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/stretchr/testify/assert"
)

func TestAssertionScope_IsValid(t *testing.T) {
	tests := []struct {
		scope domain.AssertionScope
		valid bool
	}{
		{domain.AssertionScopePositionLedger, true},
		{domain.AssertionScopeCrossAccount, true},
		{domain.AssertionScopeNostroVostro, true},
		{domain.AssertionScope("INVALID"), false},
		{domain.AssertionScope(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.scope), func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.scope.IsValid())
		})
	}
}

func TestAssertionScope_String(t *testing.T) {
	assert.Equal(t, "POSITION_LEDGER", domain.AssertionScopePositionLedger.String())
	assert.Equal(t, "CROSS_ACCOUNT", domain.AssertionScopeCrossAccount.String())
	assert.Equal(t, "NOSTRO_VOSTRO", domain.AssertionScopeNostroVostro.String())
}

func TestParseAssertionScope(t *testing.T) {
	assert.Equal(t, domain.AssertionScopePositionLedger, domain.ParseAssertionScope("POSITION_LEDGER"))
	assert.Equal(t, domain.AssertionScopeCrossAccount, domain.ParseAssertionScope("CROSS_ACCOUNT"))
	assert.Equal(t, domain.AssertionScopeNostroVostro, domain.ParseAssertionScope("NOSTRO_VOSTRO"))
	assert.Equal(t, domain.AssertionScopePositionLedger, domain.ParseAssertionScope("INVALID"),
		"unrecognized values should default to POSITION_LEDGER")
}
