package clients_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewPositionKeepingClient_RequiresServiceName verifies error when ServiceName is missing
func TestNewPositionKeepingClient_RequiresServiceName(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Namespace: "default",
		Port:      50053,
		Timeout:   10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	assert.ErrorIs(t, err, clients.ErrPositionKeepingServiceNameRequired)
	assert.Nil(t, client)
}

// TestNewPositionKeepingClient_Success verifies client creation with valid configuration
func TestNewPositionKeepingClient_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_DefaultTimeout verifies 30s default timeout is applied
func TestNewPositionKeepingClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Timeout:     0, // Should default to 30 seconds
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_CustomTimeout verifies custom timeout is respected
func TestNewPositionKeepingClient_CustomTimeout(t *testing.T) {
	t.Parallel()

	customTimeout := 5 * time.Minute
	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Timeout:     customTimeout,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_WithTracer verifies tracer configuration is accepted
func TestNewPositionKeepingClient_WithTracer(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Timeout:     10 * time.Second,
		Tracer:      tracer,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_DefaultNamespace verifies namespace defaults to "default"
func TestNewPositionKeepingClient_DefaultNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "", // Should default to "default"
		Port:        50053,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_CustomNamespace verifies custom namespace is respected
func TestNewPositionKeepingClient_CustomNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "production",
		Port:        50053,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestPositionKeepingClient_Close_Success verifies Close closes the connection without error
func TestPositionKeepingClient_Close_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	err = client.Close()

	assert.NoError(t, err)
}

// TestPositionKeepingClient_Close_Multiple verifies Close behavior when called multiple times
func TestPositionKeepingClient_Close_Multiple(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	// First close
	err1 := client.Close()
	assert.NoError(t, err1)

	// Second close returns error (gRPC connection already closed)
	err2 := client.Close()
	assert.Error(t, err2, "closing already-closed connection should return error")
}

// TestNewPositionKeepingClient_NilTracer verifies nil tracer is handled gracefully
func TestNewPositionKeepingClient_NilTracer(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Timeout:     10 * time.Second,
		Tracer:      nil, // Explicitly nil
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}
