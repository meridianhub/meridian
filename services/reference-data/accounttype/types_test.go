package accounttype_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// BehaviorClass.String
// ---------------------------------------------------------------------------

func TestBehaviorClass_String(t *testing.T) {
	tests := []struct {
		name   string
		bc     accounttype.BehaviorClass
		expect string
	}{
		{"CUSTOMER", accounttype.BehaviorClassCustomer, "CUSTOMER"},
		{"CLEARING", accounttype.BehaviorClassClearing, "CLEARING"},
		{"NOSTRO", accounttype.BehaviorClassNostro, "NOSTRO"},
		{"VOSTRO", accounttype.BehaviorClassVostro, "VOSTRO"},
		{"HOLDING", accounttype.BehaviorClassHolding, "HOLDING"},
		{"SUSPENSE", accounttype.BehaviorClassSuspense, "SUSPENSE"},
		{"REVENUE", accounttype.BehaviorClassRevenue, "REVENUE"},
		{"EXPENSE", accounttype.BehaviorClassExpense, "EXPENSE"},
		{"INVENTORY", accounttype.BehaviorClassInventory, "INVENTORY"},
		{"unknown", accounttype.BehaviorClass("UNKNOWN"), "UNKNOWN"},
		{"empty", accounttype.BehaviorClass(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.bc.String())
		})
	}
}

// ---------------------------------------------------------------------------
// NormalBalance.String
// ---------------------------------------------------------------------------

func TestNormalBalance_String(t *testing.T) {
	tests := []struct {
		name   string
		nb     accounttype.NormalBalance
		expect string
	}{
		{"DEBIT", accounttype.NormalBalanceDebit, "DEBIT"},
		{"CREDIT", accounttype.NormalBalanceCredit, "CREDIT"},
		{"unknown", accounttype.NormalBalance("UNKNOWN"), "UNKNOWN"},
		{"empty", accounttype.NormalBalance(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.nb.String())
		})
	}
}

// ---------------------------------------------------------------------------
// AccountTypeStatus.String
// ---------------------------------------------------------------------------

func TestAccountTypeStatus_String(t *testing.T) {
	tests := []struct {
		name   string
		s      accounttype.Status
		expect string
	}{
		{"DRAFT", accounttype.StatusDraft, "DRAFT"},
		{"ACTIVE", accounttype.StatusActive, "ACTIVE"},
		{"DEPRECATED", accounttype.StatusDeprecated, "DEPRECATED"},
		{"unknown", accounttype.Status("UNKNOWN"), "UNKNOWN"},
		{"empty", accounttype.Status(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.s.String())
		})
	}
}
