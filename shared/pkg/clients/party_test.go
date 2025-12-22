package clients_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// TestNewBasePartyClient_Success verifies that a valid config creates a client.
func TestNewBasePartyClient_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     30 * time.Second,
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		err := client.Close()
		require.NoError(t, err)
	}()

	// Verify client is accessible
	assert.NotNil(t, client.Client())
	assert.Equal(t, 30*time.Second, client.Timeout())
}

// TestNewBasePartyClient_MissingServiceName verifies that ErrPartyServiceNameRequired is returned
// when ServiceName is not provided.
func TestNewBasePartyClient_MissingServiceName(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "", // Missing service name
		Namespace:   "default",
		Port:        50055,
		Timeout:     30 * time.Second,
	}

	client, err := clients.NewBasePartyClient(cfg)

	assert.Nil(t, client)
	assert.ErrorIs(t, err, clients.ErrPartyServiceNameRequired)
}

// TestNewBasePartyClient_DefaultTimeout verifies that the default timeout of 30s is applied
// when Timeout is not specified (zero value).
func TestNewBasePartyClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     0, // Zero timeout should default to 30s
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		err := client.Close()
		require.NoError(t, err)
	}()

	// Verify default timeout was applied
	assert.Equal(t, clients.DefaultPartyClientTimeout, client.Timeout())
	assert.Equal(t, 30*time.Second, client.Timeout())
}

// TestBasePartyClient_PrepareContext verifies that PrepareContext applies timeout,
// propagates correlation ID, and propagates organization ID to outgoing metadata.
func TestBasePartyClient_PrepareContext(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     5 * time.Second,
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		err := client.Close()
		require.NoError(t, err)
	}()

	// Set up context with correlation ID and organization
	//nolint:revive,staticcheck // Using string key as expected by ExtractCorrelationID implementation
	ctx := context.WithValue(context.Background(), "x-correlation-id", "test-corr-123")
	orgID := tenant.MustNewTenantID("acme_bank")
	ctx = tenant.WithTenant(ctx, orgID)

	// Call PrepareContext
	preparedCtx, cancel := client.PrepareContext(ctx)
	defer cancel()

	// Verify timeout was applied
	deadline, hasDeadline := preparedCtx.Deadline()
	assert.True(t, hasDeadline, "should have deadline")
	assert.WithinDuration(t, time.Now().Add(5*time.Second), deadline, 100*time.Millisecond)

	// Verify correlation ID was propagated to outgoing metadata
	md, ok := metadata.FromOutgoingContext(preparedCtx)
	assert.True(t, ok, "should have outgoing metadata")
	assert.Equal(t, []string{"test-corr-123"}, md.Get("x-correlation-id"))

	// Verify organization ID was propagated to outgoing metadata
	assert.Equal(t, []string{"acme_bank"}, md.Get(tenant.TenantIDKey))
}

// TestBasePartyClient_PrepareContext_ExistingDeadline verifies that PrepareContext
// preserves an existing deadline instead of overwriting it.
func TestBasePartyClient_PrepareContext_ExistingDeadline(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     5 * time.Second,
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		err := client.Close()
		require.NoError(t, err)
	}()

	// Set up context with existing deadline
	existingDeadline := time.Now().Add(10 * time.Second)
	ctx, existingCancel := context.WithDeadline(context.Background(), existingDeadline)
	defer existingCancel()

	// Call PrepareContext
	preparedCtx, cancel := client.PrepareContext(ctx)
	defer cancel()

	// Verify existing deadline was preserved (not overwritten with 5s)
	deadline, hasDeadline := preparedCtx.Deadline()
	assert.True(t, hasDeadline, "should have deadline")
	assert.Equal(t, existingDeadline, deadline, "should preserve existing deadline")
}

// TestBasePartyClient_PrepareContext_NoCorrelationID verifies that PrepareContext
// handles missing correlation ID gracefully.
func TestBasePartyClient_PrepareContext_NoCorrelationID(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     5 * time.Second,
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		err := client.Close()
		require.NoError(t, err)
	}()

	// Context without correlation ID
	ctx := context.Background()

	// Call PrepareContext - should not panic
	preparedCtx, cancel := client.PrepareContext(ctx)
	defer cancel()

	// Verify timeout was still applied
	_, hasDeadline := preparedCtx.Deadline()
	assert.True(t, hasDeadline, "should have deadline")

	// No correlation ID should be in metadata
	md, ok := metadata.FromOutgoingContext(preparedCtx)
	// Metadata might not exist or correlation-id might not be set
	if ok {
		assert.Empty(t, md.Get("x-correlation-id"), "should not have correlation ID")
	}
}

// TestBasePartyClient_Close verifies that Close properly closes the connection.
func TestBasePartyClient_Close(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     30 * time.Second,
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Close should succeed
	err = client.Close()
	assert.NoError(t, err)
}

// TestNewBasePartyClient_CustomTimeout verifies that a custom timeout is respected.
func TestNewBasePartyClient_CustomTimeout(t *testing.T) {
	t.Parallel()

	customTimeout := 60 * time.Second
	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     customTimeout,
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		err := client.Close()
		require.NoError(t, err)
	}()

	assert.Equal(t, customTimeout, client.Timeout())
}

// TestNewBasePartyClient_DefaultNamespace verifies that namespace defaults work correctly.
func TestNewBasePartyClient_DefaultNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "", // Empty namespace should default to "default"
		Port:        50055,
		Timeout:     30 * time.Second,
	}

	client, err := clients.NewBasePartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		err := client.Close()
		require.NoError(t, err)
	}()

	// Client should be created successfully
	assert.NotNil(t, client.Client())
}
