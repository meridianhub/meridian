package clients_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

// TestExtractCorrelationID_FromContextValue verifies extraction from context value
func TestExtractCorrelationID_FromContextValue(t *testing.T) {
	t.Parallel()

	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx := context.WithValue(context.Background(), "x-correlation-id", "test-123")

	result := clients.ExtractCorrelationID(ctx)

	assert.Equal(t, "test-123", result)
}

// TestExtractCorrelationID_FromIncomingMetadata verifies extraction from gRPC incoming metadata
func TestExtractCorrelationID_FromIncomingMetadata(t *testing.T) {
	t.Parallel()

	md := metadata.New(map[string]string{
		"x-correlation-id": "metadata-456",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	result := clients.ExtractCorrelationID(ctx)

	assert.Equal(t, "metadata-456", result)
}

// TestExtractCorrelationID_MultipleKeys tests all supported correlation ID keys
func TestExtractCorrelationID_MultipleKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "correlation-id key",
			key:      "correlation-id",
			value:    "corr-123",
			expected: "corr-123",
		},
		{
			name:     "x-correlation-id key",
			key:      "x-correlation-id",
			value:    "x-corr-456",
			expected: "x-corr-456",
		},
		{
			name:     "x-request-id key",
			key:      "x-request-id",
			value:    "x-req-789",
			expected: "x-req-789",
		},
		{
			name:     "request-id key",
			key:      "request-id",
			value:    "req-012",
			expected: "req-012",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Test from context value
			//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
			ctx := context.WithValue(context.Background(), tt.key, tt.value)
			result := clients.ExtractCorrelationID(ctx)
			assert.Equal(t, tt.expected, result, "should extract from context value")

			// Test from incoming metadata
			md := metadata.New(map[string]string{tt.key: tt.value})
			ctxMD := metadata.NewIncomingContext(context.Background(), md)
			resultMD := clients.ExtractCorrelationID(ctxMD)
			assert.Equal(t, tt.expected, resultMD, "should extract from incoming metadata")
		})
	}
}

// TestExtractCorrelationID_NotFound verifies empty string is returned when no correlation ID exists
func TestExtractCorrelationID_NotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	result := clients.ExtractCorrelationID(ctx)

	assert.Equal(t, "", result)
}

// TestExtractCorrelationID_EmptyValue verifies empty correlation ID values are ignored
func TestExtractCorrelationID_EmptyValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "empty string in context value",
			//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
			ctx: context.WithValue(context.Background(), "x-correlation-id", ""),
		},
		{
			name: "empty string in metadata",
			ctx: metadata.NewIncomingContext(
				context.Background(),
				metadata.New(map[string]string{"x-correlation-id": ""}),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := clients.ExtractCorrelationID(tt.ctx)

			assert.Equal(t, "", result)
		})
	}
}

// TestExtractCorrelationID_ContextValuePriority verifies context values are checked before metadata
func TestExtractCorrelationID_ContextValuePriority(t *testing.T) {
	t.Parallel()

	// Create context with both context value and metadata
	md := metadata.New(map[string]string{
		"x-correlation-id": "metadata-value",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx = context.WithValue(ctx, "x-correlation-id", "context-value")

	result := clients.ExtractCorrelationID(ctx)

	// Should prefer context value over metadata
	assert.Equal(t, "context-value", result)
}

// TestExtractCorrelationID_NonStringValue verifies non-string values are ignored
func TestExtractCorrelationID_NonStringValue(t *testing.T) {
	t.Parallel()

	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx := context.WithValue(context.Background(), "x-correlation-id", 12345)

	result := clients.ExtractCorrelationID(ctx)

	assert.Equal(t, "", result)
}

// TestPropagateCorrelationID_Success verifies correlation ID is added to outgoing metadata
func TestPropagateCorrelationID_Success(t *testing.T) {
	t.Parallel()

	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx := context.WithValue(context.Background(), "x-correlation-id", "test-789")

	result := clients.PropagateCorrelationID(ctx)

	// Verify correlation ID was added to outgoing metadata
	md, ok := metadata.FromOutgoingContext(result)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"test-789"}, md.Get("x-correlation-id"))
}

// TestPropagateCorrelationID_NoCorrelationID verifies context is unchanged when no correlation ID exists
func TestPropagateCorrelationID_NoCorrelationID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	result := clients.PropagateCorrelationID(ctx)

	// Context should be returned unchanged
	assert.Equal(t, ctx, result)
}

// TestPropagateCorrelationID_ExistingMetadata verifies existing metadata is preserved
func TestPropagateCorrelationID_ExistingMetadata(t *testing.T) {
	t.Parallel()

	// Create context with existing outgoing metadata
	existingMD := metadata.New(map[string]string{
		"existing-key": "existing-value",
	})
	ctx := metadata.NewOutgoingContext(context.Background(), existingMD)
	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx = context.WithValue(ctx, "x-correlation-id", "test-999")

	result := clients.PropagateCorrelationID(ctx)

	// Verify both existing metadata and correlation ID are present
	md, ok := metadata.FromOutgoingContext(result)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"existing-value"}, md.Get("existing-key"), "should preserve existing metadata")
	assert.Equal(t, []string{"test-999"}, md.Get("x-correlation-id"), "should add correlation ID")
}

// TestPropagateCorrelationID_OverwritesExistingCorrelationID verifies correlation ID is updated if it already exists
func TestPropagateCorrelationID_OverwritesExistingCorrelationID(t *testing.T) {
	t.Parallel()

	// Create context with existing correlation ID in outgoing metadata
	existingMD := metadata.New(map[string]string{
		"x-correlation-id": "old-id",
	})
	ctx := metadata.NewOutgoingContext(context.Background(), existingMD)
	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx = context.WithValue(ctx, "x-correlation-id", "new-id")

	result := clients.PropagateCorrelationID(ctx)

	// Verify correlation ID was updated
	md, ok := metadata.FromOutgoingContext(result)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"new-id"}, md.Get("x-correlation-id"), "should update correlation ID")
}

// TestWithTimeout_AppliesTimeout verifies timeout is applied when context has no deadline
func TestWithTimeout_AppliesTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	timeout := 5 * time.Second

	resultCtx, cancel := clients.WithTimeout(ctx, timeout)
	defer cancel()

	deadline, ok := resultCtx.Deadline()
	assert.True(t, ok, "should have deadline")
	assert.WithinDuration(t, time.Now().Add(timeout), deadline, 100*time.Millisecond)
}

// TestWithTimeout_PreservesExistingDeadline verifies existing deadline is not changed
func TestWithTimeout_PreservesExistingDeadline(t *testing.T) {
	t.Parallel()

	existingDeadline := time.Now().Add(10 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), existingDeadline)
	defer cancel()

	resultCtx, resultCancel := clients.WithTimeout(ctx, 5*time.Second)
	defer resultCancel()

	deadline, ok := resultCtx.Deadline()
	assert.True(t, ok, "should have deadline")
	assert.Equal(t, existingDeadline, deadline, "should preserve existing deadline")
}

// TestWithTimeout_ZeroTimeout verifies context is unchanged with zero timeout
func TestWithTimeout_ZeroTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resultCtx, cancel := clients.WithTimeout(ctx, 0)
	defer cancel()

	// Context should be returned unchanged
	assert.Equal(t, ctx, resultCtx)

	// Should not have a deadline
	_, ok := resultCtx.Deadline()
	assert.False(t, ok, "should not have deadline")
}

// TestWithTimeout_NegativeTimeout verifies negative timeout is treated as zero
func TestWithTimeout_NegativeTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resultCtx, cancel := clients.WithTimeout(ctx, -5*time.Second)
	defer cancel()

	// Context should be returned unchanged (negative timeout treated as no timeout)
	assert.Equal(t, ctx, resultCtx)

	// Should not have a deadline
	_, ok := resultCtx.Deadline()
	assert.False(t, ok, "should not have deadline")
}

// TestPropagateOrganization_Success verifies organization ID is added to outgoing metadata
func TestPropagateOrganization_Success(t *testing.T) {
	t.Parallel()

	orgID := tenant.MustNewTenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), orgID)

	result := clients.PropagateOrganization(ctx)

	// Verify organization ID was added to outgoing metadata
	md, ok := metadata.FromOutgoingContext(result)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"acme_bank"}, md.Get(tenant.TenantIDKey))
}

// TestPropagateOrganization_NoOrganization verifies context is unchanged when no organization exists
func TestPropagateOrganization_NoOrganization(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	result := clients.PropagateOrganization(ctx)

	// Context should be returned unchanged
	assert.Equal(t, ctx, result)

	// Should not have outgoing metadata
	_, ok := metadata.FromOutgoingContext(result)
	assert.False(t, ok, "should not have outgoing metadata")
}

// TestPropagateOrganization_NilContext verifies nil context does not panic
func TestPropagateOrganization_NilContext(t *testing.T) {
	t.Parallel()

	var nilCtx context.Context

	assert.NotPanics(t, func() {
		result := clients.PropagateOrganization(nilCtx)
		assert.Nil(t, result, "should return nil for nil context")
	})
}

// TestPropagateTenant_EmptyTenantID verifies empty tenant ID returns unchanged context
func TestPropagateTenant_EmptyTenantID(t *testing.T) {
	t.Parallel()

	// Create context with empty organization ID
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(""))

	result := clients.PropagateOrganization(ctx)

	// Context should be returned unchanged (empty tenant ID treated as missing)
	assert.Equal(t, ctx, result)

	// Should not have outgoing metadata with org ID
	_, ok := metadata.FromOutgoingContext(result)
	assert.False(t, ok, "should not have outgoing metadata for empty tenant ID")
}

// TestPropagateOrganization_ExistingMetadata verifies existing metadata is preserved
func TestPropagateOrganization_ExistingMetadata(t *testing.T) {
	t.Parallel()

	// Create context with existing outgoing metadata
	existingMD := metadata.New(map[string]string{
		"existing-key": "existing-value",
	})
	ctx := metadata.NewOutgoingContext(context.Background(), existingMD)
	orgID := tenant.MustNewTenantID("test_org")
	ctx = tenant.WithTenant(ctx, orgID)

	result := clients.PropagateOrganization(ctx)

	// Verify both existing metadata and organization ID are present
	md, ok := metadata.FromOutgoingContext(result)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"existing-value"}, md.Get("existing-key"), "should preserve existing metadata")
	assert.Equal(t, []string{"test_org"}, md.Get(tenant.TenantIDKey), "should add organization ID")
}

// TestPropagateOrganization_OverwritesExistingOrgID verifies organization ID is updated if it already exists
func TestPropagateOrganization_OverwritesExistingOrgID(t *testing.T) {
	t.Parallel()

	// Create context with existing org ID in outgoing metadata
	existingMD := metadata.New(map[string]string{
		tenant.TenantIDKey: "old_org",
	})
	ctx := metadata.NewOutgoingContext(context.Background(), existingMD)
	orgID := tenant.MustNewTenantID("new_org")
	ctx = tenant.WithTenant(ctx, orgID)

	result := clients.PropagateOrganization(ctx)

	// Verify organization ID was updated
	md, ok := metadata.FromOutgoingContext(result)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"new_org"}, md.Get(tenant.TenantIDKey), "should update organization ID")
}

// TestPropagateOrganization_ChainWithCorrelationID verifies both propagation functions work together
func TestPropagateOrganization_ChainWithCorrelationID(t *testing.T) {
	t.Parallel()

	// Create context with both correlation ID and organization
	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx := context.WithValue(context.Background(), "x-correlation-id", "corr-123")
	orgID := tenant.MustNewTenantID("chain_test")
	ctx = tenant.WithTenant(ctx, orgID)

	// Apply both propagation functions
	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// Verify both headers are present
	md, ok := metadata.FromOutgoingContext(ctx)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"corr-123"}, md.Get("x-correlation-id"), "should have correlation ID")
	assert.Equal(t, []string{"chain_test"}, md.Get(tenant.TenantIDKey), "should have organization ID")
}
