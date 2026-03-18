package client

import (
	"testing"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========== stringToPriority ==========

func TestStringToPriority_ValidValues(t *testing.T) {
	tests := []struct {
		input    string
		expected opgatewayv1.Priority
	}{
		{"LOW", opgatewayv1.Priority_PRIORITY_LOW},
		{"NORMAL", opgatewayv1.Priority_PRIORITY_NORMAL},
		{"HIGH", opgatewayv1.Priority_PRIORITY_HIGH},
		{"CRITICAL", opgatewayv1.Priority_PRIORITY_CRITICAL},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := stringToPriority(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStringToPriority_Invalid(t *testing.T) {
	_, err := stringToPriority("BOGUS")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid")
}

func TestStringToPriority_Empty(t *testing.T) {
	_, err := stringToPriority("")
	require.Error(t, err)
}

func TestStringToPriority_LowerCase(t *testing.T) {
	_, err := stringToPriority("low")
	require.Error(t, err)
}

// ========== instructionStatusToString ==========

func TestInstructionStatusToString_AllStatuses(t *testing.T) {
	tests := []struct {
		status   opgatewayv1.InstructionStatus
		expected string
	}{
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED, "UNKNOWN"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING, "PENDING"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING, "DISPATCHING"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED, "DELIVERED"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED, "ACKNOWLEDGED"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING, "RETRYING"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED, "FAILED"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED, "EXPIRED"},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED, "CANCELLED"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, instructionStatusToString(tt.status))
		})
	}
}

func TestInstructionStatusToString_Unrecognized(t *testing.T) {
	assert.Equal(t, "UNKNOWN", instructionStatusToString(opgatewayv1.InstructionStatus(999)))
}

// ========== optionalStringParam ==========

func TestOptionalStringParam_Present(t *testing.T) {
	params := map[string]any{"key": "value"}
	result, exists, err := optionalStringParam(params, "key")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "value", result)
}

func TestOptionalStringParam_Missing(t *testing.T) {
	params := map[string]any{}
	result, exists, err := optionalStringParam(params, "key")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", result)
}

func TestOptionalStringParam_WrongType(t *testing.T) {
	params := map[string]any{"key": 42}
	_, exists, err := optionalStringParam(params, "key")
	assert.True(t, exists)
	assert.Error(t, err)
}

func TestOptionalStringParam_NilValue(t *testing.T) {
	params := map[string]any{"key": nil}
	result, exists, err := optionalStringParam(params, "key")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", result)
}
