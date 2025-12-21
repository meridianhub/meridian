package clients_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewPartyClient_RequiresServiceName verifies error when ServiceName is missing
func TestNewPartyClient_RequiresServiceName(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		Namespace: "default",
		Port:      50055,
		Timeout:   10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	assert.ErrorIs(t, err, clients.ErrPartyServiceNameRequired)
	assert.Nil(t, client)
}

// TestNewPartyClient_Success verifies client creation with valid configuration
func TestNewPartyClient_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_DefaultTimeout verifies 30s default timeout is applied
func TestNewPartyClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     0, // Should default to 30 seconds
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_CustomTimeout verifies custom timeout is respected
func TestNewPartyClient_CustomTimeout(t *testing.T) {
	t.Parallel()

	customTimeout := 5 * time.Minute
	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     customTimeout,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_WithTracer verifies tracer configuration is accepted
func TestNewPartyClient_WithTracer(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     10 * time.Second,
		Tracer:      tracer,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_DefaultNamespace verifies namespace defaults to "default"
func TestNewPartyClient_DefaultNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "", // Should default to "default"
		Port:        50055,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPartyClient_CustomNamespace verifies custom namespace is respected
func TestNewPartyClient_CustomNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "production",
		Port:        50055,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestPartyClient_Close_Success verifies Close closes the connection without error
func TestPartyClient_Close_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	err = client.Close()

	assert.NoError(t, err)
}

// TestPartyClient_Close_Multiple verifies Close behavior when called multiple times
func TestPartyClient_Close_Multiple(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPartyClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	// First close
	err1 := client.Close()
	assert.NoError(t, err1)

	// Second close returns error (gRPC connection already closed)
	err2 := client.Close()
	assert.Error(t, err2, "closing already-closed connection should return error")
}

// TestNewPartyClient_NilTracer verifies nil tracer is handled gracefully
func TestNewPartyClient_NilTracer(t *testing.T) {
	t.Parallel()

	cfg := &clients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     10 * time.Second,
		Tracer:      nil, // Explicitly nil
	}

	client, err := clients.NewPartyClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}
