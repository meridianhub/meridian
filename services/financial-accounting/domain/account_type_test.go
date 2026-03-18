package domain

import (
	"testing"
)

func TestAccountType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		atype    AccountType
		wantValid bool
	}{
		{
			name:      "DEBIT is valid",
			atype:     AccountTypeDebit,
			wantValid: true,
		},
		{
			name:      "CREDIT is valid",
			atype:     AccountTypeCredit,
			wantValid: true,
		},
		{
			name:      "VOSTRO is valid",
			atype:     AccountTypeVostro,
			wantValid: true,
		},
		{
			name:      "NOSTRO is valid",
			atype:     AccountTypeNostro,
			wantValid: true,
		},
		{
			name:      "CURRENT is valid",
			atype:     AccountTypeCurrent,
			wantValid: true,
		},
		{
			name:      "SAVINGS is valid",
			atype:     AccountTypeSavings,
			wantValid: true,
		},
		{
			name:      "empty string is invalid",
			atype:     AccountType(""),
			wantValid: false,
		},
		{
			name:      "unknown type is invalid",
			atype:     AccountType("UNKNOWN"),
			wantValid: false,
		},
		{
			name:      "lowercase debit is invalid",
			atype:     AccountType("debit"),
			wantValid: false,
		},
		{
			name:      "ASSET is invalid for this type",
			atype:     AccountType("ASSET"),
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.atype.IsValid(); got != tt.wantValid {
				t.Errorf("AccountType.IsValid() = %v, want %v", got, tt.wantValid)
			}
		})
	}
}

func TestAccountType_String(t *testing.T) {
	tests := []struct {
		name  string
		atype AccountType
		want  string
	}{
		{
			name:  "DEBIT string",
			atype: AccountTypeDebit,
			want:  "DEBIT",
		},
		{
			name:  "CREDIT string",
			atype: AccountTypeCredit,
			want:  "CREDIT",
		},
		{
			name:  "VOSTRO string",
			atype: AccountTypeVostro,
			want:  "VOSTRO",
		},
		{
			name:  "NOSTRO string",
			atype: AccountTypeNostro,
			want:  "NOSTRO",
		},
		{
			name:  "CURRENT string",
			atype: AccountTypeCurrent,
			want:  "CURRENT",
		},
		{
			name:  "SAVINGS string",
			atype: AccountTypeSavings,
			want:  "SAVINGS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.atype.String(); got != tt.want {
				t.Errorf("AccountType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
