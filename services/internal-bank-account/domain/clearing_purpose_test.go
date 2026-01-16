package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClearingPurpose_IsValid(t *testing.T) {
	tests := []struct {
		name            string
		clearingPurpose ClearingPurpose
		want            bool
	}{
		{
			name:            "UNSPECIFIED is valid",
			clearingPurpose: ClearingPurposeUnspecified,
			want:            true,
		},
		{
			name:            "DEPOSIT is valid",
			clearingPurpose: ClearingPurposeDeposit,
			want:            true,
		},
		{
			name:            "WITHDRAWAL is valid",
			clearingPurpose: ClearingPurposeWithdrawal,
			want:            true,
		},
		{
			name:            "SETTLEMENT is valid",
			clearingPurpose: ClearingPurposeSettlement,
			want:            true,
		},
		{
			name:            "GENERAL is valid",
			clearingPurpose: ClearingPurposeGeneral,
			want:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.clearingPurpose.IsValid())
		})
	}
}

func TestClearingPurpose_Invalid(t *testing.T) {
	tests := []struct {
		name            string
		clearingPurpose ClearingPurpose
	}{
		{
			name:            "empty string is invalid",
			clearingPurpose: ClearingPurpose(""),
		},
		{
			name:            "unknown type is invalid",
			clearingPurpose: ClearingPurpose("UNKNOWN"),
		},
		{
			name:            "lowercase is invalid",
			clearingPurpose: ClearingPurpose("deposit"),
		},
		{
			name:            "mixed case is invalid",
			clearingPurpose: ClearingPurpose("Deposit"),
		},
		{
			name:            "typo is invalid",
			clearingPurpose: ClearingPurpose("DEPOSITT"),
		},
		{
			name:            "extra whitespace is invalid",
			clearingPurpose: ClearingPurpose(" DEPOSIT"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.clearingPurpose.IsValid())
		})
	}
}

func TestClearingPurpose_String(t *testing.T) {
	tests := []struct {
		name            string
		clearingPurpose ClearingPurpose
		want            string
	}{
		{
			name:            "UNSPECIFIED string",
			clearingPurpose: ClearingPurposeUnspecified,
			want:            "CLEARING_PURPOSE_UNSPECIFIED",
		},
		{
			name:            "DEPOSIT string",
			clearingPurpose: ClearingPurposeDeposit,
			want:            "CLEARING_PURPOSE_DEPOSIT",
		},
		{
			name:            "WITHDRAWAL string",
			clearingPurpose: ClearingPurposeWithdrawal,
			want:            "CLEARING_PURPOSE_WITHDRAWAL",
		},
		{
			name:            "SETTLEMENT string",
			clearingPurpose: ClearingPurposeSettlement,
			want:            "CLEARING_PURPOSE_SETTLEMENT",
		},
		{
			name:            "GENERAL string",
			clearingPurpose: ClearingPurposeGeneral,
			want:            "CLEARING_PURPOSE_GENERAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.clearingPurpose.String())
		})
	}
}

func TestClearingPurpose_Constants(t *testing.T) {
	// Verify the constant values are as expected
	assert.Equal(t, ClearingPurpose("CLEARING_PURPOSE_UNSPECIFIED"), ClearingPurposeUnspecified)
	assert.Equal(t, ClearingPurpose("CLEARING_PURPOSE_DEPOSIT"), ClearingPurposeDeposit)
	assert.Equal(t, ClearingPurpose("CLEARING_PURPOSE_WITHDRAWAL"), ClearingPurposeWithdrawal)
	assert.Equal(t, ClearingPurpose("CLEARING_PURPOSE_SETTLEMENT"), ClearingPurposeSettlement)
	assert.Equal(t, ClearingPurpose("CLEARING_PURPOSE_GENERAL"), ClearingPurposeGeneral)
}

func TestClearingPurpose_AllValidPurposesCount(t *testing.T) {
	// Test that we have exactly 5 valid clearing purposes
	validPurposes := []ClearingPurpose{
		ClearingPurposeUnspecified,
		ClearingPurposeDeposit,
		ClearingPurposeWithdrawal,
		ClearingPurposeSettlement,
		ClearingPurposeGeneral,
	}

	for _, cp := range validPurposes {
		assert.True(t, cp.IsValid(), "expected %s to be valid", cp)
	}

	assert.Len(t, validPurposes, 5, "expected exactly 5 valid clearing purposes")
}

func TestClearingPurpose_StringOnInvalid(t *testing.T) {
	// Test String() on invalid types returns the underlying string
	invalid := ClearingPurpose("INVALID")
	assert.Equal(t, "INVALID", invalid.String())

	empty := ClearingPurpose("")
	assert.Equal(t, "", empty.String())
}
