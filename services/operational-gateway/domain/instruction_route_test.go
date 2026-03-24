package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewRoute_Success verifies a route is created with all required fields.
func TestNewRoute_Success(t *testing.T) {
	tenantID := "tenant-uuid-001"
	instructionType := "kyc.verify"
	connectionID := "conn-uuid-001"

	route, err := NewRoute(tenantID, instructionType, connectionID)

	require.NoError(t, err)
	require.NotNil(t, route)
	assert.Equal(t, tenantID, route.TenantID)
	assert.Equal(t, instructionType, route.InstructionType)
	assert.Equal(t, connectionID, route.ConnectionID)
	assert.Empty(t, route.FallbackConnectionID)
	assert.Empty(t, route.OutboundMapping)
	assert.Empty(t, route.InboundMapping)
	assert.Empty(t, route.HTTPMethod)
	assert.Empty(t, route.PathTemplate)
}

// TestNewRoute_SetsTimestamps verifies CreatedAt and UpdatedAt are set on construction.
func TestNewRoute_SetsTimestamps(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	route, err := NewRoute("tenant-1", "device.command", "conn-1")
	after := time.Now().UTC().Add(time.Second)

	require.NoError(t, err)
	assert.True(t, route.CreatedAt.After(before), "CreatedAt should be after test start")
	assert.True(t, route.CreatedAt.Before(after), "CreatedAt should be before test end")
	assert.True(t, route.UpdatedAt.After(before), "UpdatedAt should be after test start")
	assert.True(t, route.UpdatedAt.Before(after), "UpdatedAt should be before test end")
}

// TestNewRoute_MissingTenantID verifies ErrTenantIDRequired is returned.
func TestNewRoute_MissingTenantID(t *testing.T) {
	_, err := NewRoute("", "kyc.verify", "conn-1")
	require.ErrorIs(t, err, ErrTenantIDRequired)
}

// TestNewRoute_MissingInstructionType verifies ErrInstructionTypeRequired is returned.
func TestNewRoute_MissingInstructionType(t *testing.T) {
	_, err := NewRoute("tenant-1", "", "conn-1")
	require.ErrorIs(t, err, ErrInstructionTypeRequired)
}

// TestNewRoute_MissingConnectionID verifies ErrConnectionIDRequired is returned.
func TestNewRoute_MissingConnectionID(t *testing.T) {
	_, err := NewRoute("tenant-1", "kyc.verify", "")
	require.ErrorIs(t, err, ErrConnectionIDRequired)
}

// TestRoute_OptionalFieldsCanBeSet verifies optional fields can be set after construction.
func TestRoute_OptionalFieldsCanBeSet(t *testing.T) {
	route, err := NewRoute("tenant-1", "payment.collect", "conn-primary")
	require.NoError(t, err)

	route.FallbackConnectionID = "conn-fallback"
	route.OutboundMapping = "payment-outbound"
	route.InboundMapping = "payment-inbound"
	route.HTTPMethod = "POST"
	route.PathTemplate = "/v1/payments"

	assert.Equal(t, "conn-fallback", route.FallbackConnectionID)
	assert.Equal(t, "payment-outbound", route.OutboundMapping)
	assert.Equal(t, "payment-inbound", route.InboundMapping)
	assert.Equal(t, "POST", route.HTTPMethod)
	assert.Equal(t, "/v1/payments", route.PathTemplate)
}

// TestNewRoute_InstructionTypeVariants verifies dotted instruction types are accepted.
func TestNewRoute_InstructionTypeVariants(t *testing.T) {
	cases := []struct {
		name            string
		instructionType string
	}{
		{"simple", "kyc"},
		{"dotted", "kyc.verify"},
		{"deep", "payment.collect.retry"},
		{"with-dash", "notification.send-sms"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route, err := NewRoute("tenant-1", tc.instructionType, "conn-1")
			require.NoError(t, err)
			assert.Equal(t, tc.instructionType, route.InstructionType)
		})
	}
}

// TestRoute_HasFallback verifies fallback detection via non-empty FallbackConnectionID.
func TestRoute_HasFallback(t *testing.T) {
	route, err := NewRoute("tenant-1", "kyc.verify", "conn-primary")
	require.NoError(t, err)
	assert.Empty(t, route.FallbackConnectionID, "no fallback set initially")

	route.FallbackConnectionID = "conn-fallback"
	assert.NotEmpty(t, route.FallbackConnectionID, "fallback should be set")
}

// TestRoute_Errors_AreDistinct verifies route error sentinels are unique values.
func TestRoute_Errors_AreDistinct(t *testing.T) {
	assert.NotEqual(t, ErrInstructionTypeRequired, ErrConnectionIDRequired)
	assert.NotEqual(t, ErrInstructionTypeRequired, ErrTenantIDRequired)
	assert.NotEqual(t, ErrConnectionIDRequired, ErrTenantIDRequired)
	assert.NotEqual(t, ErrInstructionRouteNotFound, ErrInstructionTypeRequired)
}
