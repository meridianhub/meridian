package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccountType_IsValid(t *testing.T) {
	tests := []struct {
		name        string
		accountType AccountType
		want        bool
	}{
		{
			name:        "CLEARING is valid",
			accountType: AccountTypeClearing,
			want:        true,
		},
		{
			name:        "NOSTRO is valid",
			accountType: AccountTypeNostro,
			want:        true,
		},
		{
			name:        "VOSTRO is valid",
			accountType: AccountTypeVostro,
			want:        true,
		},
		{
			name:        "HOLDING is valid",
			accountType: AccountTypeHolding,
			want:        true,
		},
		{
			name:        "SUSPENSE is valid",
			accountType: AccountTypeSuspense,
			want:        true,
		},
		{
			name:        "REVENUE is valid",
			accountType: AccountTypeRevenue,
			want:        true,
		},
		{
			name:        "EXPENSE is valid",
			accountType: AccountTypeExpense,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.accountType.IsValid())
		})
	}
}

func TestAccountType_Invalid(t *testing.T) {
	tests := []struct {
		name        string
		accountType AccountType
	}{
		{
			name:        "empty string is invalid",
			accountType: AccountType(""),
		},
		{
			name:        "unknown type is invalid",
			accountType: AccountType("UNKNOWN"),
		},
		{
			name:        "lowercase is invalid",
			accountType: AccountType("clearing"),
		},
		{
			name:        "mixed case is invalid",
			accountType: AccountType("Nostro"),
		},
		{
			name:        "typo is invalid",
			accountType: AccountType("CLEERING"),
		},
		{
			name:        "extra whitespace is invalid",
			accountType: AccountType(" CLEARING"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.accountType.IsValid())
		})
	}
}

func TestAccountType_String(t *testing.T) {
	tests := []struct {
		name        string
		accountType AccountType
		want        string
	}{
		{
			name:        "CLEARING string",
			accountType: AccountTypeClearing,
			want:        "CLEARING",
		},
		{
			name:        "NOSTRO string",
			accountType: AccountTypeNostro,
			want:        "NOSTRO",
		},
		{
			name:        "VOSTRO string",
			accountType: AccountTypeVostro,
			want:        "VOSTRO",
		},
		{
			name:        "HOLDING string",
			accountType: AccountTypeHolding,
			want:        "HOLDING",
		},
		{
			name:        "SUSPENSE string",
			accountType: AccountTypeSuspense,
			want:        "SUSPENSE",
		},
		{
			name:        "REVENUE string",
			accountType: AccountTypeRevenue,
			want:        "REVENUE",
		},
		{
			name:        "EXPENSE string",
			accountType: AccountTypeExpense,
			want:        "EXPENSE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.accountType.String())
		})
	}
}

func TestAccountType_RequiresCorrespondent(t *testing.T) {
	tests := []struct {
		name        string
		accountType AccountType
		want        bool
	}{
		{
			name:        "NOSTRO requires correspondent",
			accountType: AccountTypeNostro,
			want:        true,
		},
		{
			name:        "VOSTRO requires correspondent",
			accountType: AccountTypeVostro,
			want:        true,
		},
		{
			name:        "CLEARING does not require correspondent",
			accountType: AccountTypeClearing,
			want:        false,
		},
		{
			name:        "HOLDING does not require correspondent",
			accountType: AccountTypeHolding,
			want:        false,
		},
		{
			name:        "SUSPENSE does not require correspondent",
			accountType: AccountTypeSuspense,
			want:        false,
		},
		{
			name:        "REVENUE does not require correspondent",
			accountType: AccountTypeRevenue,
			want:        false,
		},
		{
			name:        "EXPENSE does not require correspondent",
			accountType: AccountTypeExpense,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.accountType.RequiresCorrespondent())
		})
	}
}

func TestAccountType_Constants(t *testing.T) {
	// Verify the constant values are as expected
	assert.Equal(t, AccountType("CLEARING"), AccountTypeClearing)
	assert.Equal(t, AccountType("NOSTRO"), AccountTypeNostro)
	assert.Equal(t, AccountType("VOSTRO"), AccountTypeVostro)
	assert.Equal(t, AccountType("HOLDING"), AccountTypeHolding)
	assert.Equal(t, AccountType("SUSPENSE"), AccountTypeSuspense)
	assert.Equal(t, AccountType("REVENUE"), AccountTypeRevenue)
	assert.Equal(t, AccountType("EXPENSE"), AccountTypeExpense)
}

func TestAccountType_InvalidRequiresCorrespondent(t *testing.T) {
	// Invalid account types should return false for RequiresCorrespondent
	tests := []struct {
		name        string
		accountType AccountType
	}{
		{"empty string", AccountType("")},
		{"unknown type", AccountType("UNKNOWN")},
		{"invalid type", AccountType("INVALID")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.accountType.RequiresCorrespondent(),
				"invalid account types should not require correspondent")
		})
	}
}

func TestAccountType_AllValidTypesCount(t *testing.T) {
	// Test that we have exactly 7 valid account types
	validTypes := []AccountType{
		AccountTypeClearing,
		AccountTypeNostro,
		AccountTypeVostro,
		AccountTypeHolding,
		AccountTypeSuspense,
		AccountTypeRevenue,
		AccountTypeExpense,
	}

	for _, at := range validTypes {
		assert.True(t, at.IsValid(), "expected %s to be valid", at)
	}

	assert.Len(t, validTypes, 7, "expected exactly 7 valid account types")
}

func TestAccountType_StringOnInvalid(t *testing.T) {
	// Test String() on invalid types returns the underlying string
	invalid := AccountType("INVALID")
	assert.Equal(t, "INVALID", invalid.String())

	empty := AccountType("")
	assert.Equal(t, "", empty.String())
}

func TestAccountType_CorrespondentAccountsAreTwoTypes(t *testing.T) {
	// Verify that exactly two account types require correspondent
	correspondentTypes := []AccountType{}
	allTypes := []AccountType{
		AccountTypeClearing,
		AccountTypeNostro,
		AccountTypeVostro,
		AccountTypeHolding,
		AccountTypeSuspense,
		AccountTypeRevenue,
		AccountTypeExpense,
	}

	for _, at := range allTypes {
		if at.RequiresCorrespondent() {
			correspondentTypes = append(correspondentTypes, at)
		}
	}

	assert.Len(t, correspondentTypes, 2, "exactly two account types should require correspondent")
	assert.Contains(t, correspondentTypes, AccountTypeNostro)
	assert.Contains(t, correspondentTypes, AccountTypeVostro)
}
