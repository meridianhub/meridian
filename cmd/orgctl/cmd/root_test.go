package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Sentinel errors for test cases.
var (
	errAlreadyExists      = errors.New("rpc error: code = AlreadyExists")
	errNotFound           = errors.New("rpc error: code = NotFound")
	errInvalidArgument    = errors.New("rpc error: code = InvalidArgument")
	errFailedPrecondition = errors.New("rpc error: code = FailedPrecondition")
	errUnavailable        = errors.New("rpc error: code = Unavailable")
	errDeadlineExceeded   = errors.New("rpc error: code = DeadlineExceeded")
	errUnknown            = errors.New("some unknown error")
)

func TestHandleGRPCError_Nil(t *testing.T) {
	exitCode := handleGRPCError(nil, "test operation")
	assert.Equal(t, 0, exitCode)
}

func TestHandleGRPCError_AlreadyExists(t *testing.T) {
	err := fmt.Errorf("%w desc = organization already exists", errAlreadyExists)
	exitCode := handleGRPCError(err, "register")
	assert.Equal(t, 0, exitCode, "AlreadyExists should return 0 (idempotent success)")
}

func TestHandleGRPCError_NotFound(t *testing.T) {
	err := fmt.Errorf("%w desc = organization not found", errNotFound)
	exitCode := handleGRPCError(err, "deprovision")
	assert.Equal(t, 0, exitCode, "NotFound should return 0 (idempotent success)")
}

func TestHandleGRPCError_InvalidArgument(t *testing.T) {
	err := fmt.Errorf("%w desc = invalid organization ID", errInvalidArgument)
	exitCode := handleGRPCError(err, "register")
	assert.Equal(t, 1, exitCode, "InvalidArgument should return 1")
}

func TestHandleGRPCError_FailedPrecondition(t *testing.T) {
	err := fmt.Errorf("%w desc = invalid status transition", errFailedPrecondition)
	exitCode := handleGRPCError(err, "update status")
	assert.Equal(t, 1, exitCode, "FailedPrecondition should return 1")
}

func TestHandleGRPCError_Unavailable(t *testing.T) {
	err := fmt.Errorf("%w desc = connection refused", errUnavailable)
	exitCode := handleGRPCError(err, "list")
	assert.Equal(t, 1, exitCode, "Unavailable should return 1")
}

func TestHandleGRPCError_DeadlineExceeded(t *testing.T) {
	err := fmt.Errorf("%w desc = timeout", errDeadlineExceeded)
	exitCode := handleGRPCError(err, "get")
	assert.Equal(t, 1, exitCode, "DeadlineExceeded should return 1")
}

func TestHandleGRPCError_Unknown(t *testing.T) {
	exitCode := handleGRPCError(errUnknown, "operation")
	assert.Equal(t, 1, exitCode, "Unknown errors should return 1")
}

func TestStringsContains(t *testing.T) {
	// Verify that strings.Contains works as expected for our use cases
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{"exact match", "hello", "hello", true},
		{"substring at start", "hello world", "hello", true},
		{"substring at end", "hello world", "world", true},
		{"substring in middle", "hello world", "lo wo", true},
		{"not found", "hello world", "xyz", false},
		{"empty substring", "hello", "", true},
		{"empty string", "", "hello", false},
		{"both empty", "", "", true},
		{"longer substr than string", "hi", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.Contains(tt.s, tt.substr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	// Test default value when env var is not set
	result := getEnvOrDefault("NONEXISTENT_VAR_12345", "default_value")
	assert.Equal(t, "default_value", result)

	// Test actual env var - don't set one for this test to avoid affecting other tests
}
