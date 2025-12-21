package clients_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewFinancialAccountingClient_RequiresServiceName verifies error when ServiceName is missing
func TestNewFinancialAccountingClient_RequiresServiceName(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Namespace: "default",
		Port:      50052,
		Timeout:   10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	assert.ErrorIs(t, err, clients.ErrFinancialAccountingServiceNameRequired)
	assert.Nil(t, client)
}

// TestNewFinancialAccountingClient_Success verifies client creation with valid configuration
func TestNewFinancialAccountingClient_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_DefaultTimeout verifies 30s default timeout is applied
func TestNewFinancialAccountingClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Timeout:     0, // Should default to 30 seconds
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_CustomTimeout verifies custom timeout is respected
func TestNewFinancialAccountingClient_CustomTimeout(t *testing.T) {
	t.Parallel()

	customTimeout := 5 * time.Minute
	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Timeout:     customTimeout,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_WithTracer verifies tracer configuration is accepted
func TestNewFinancialAccountingClient_WithTracer(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Timeout:     10 * time.Second,
		Tracer:      tracer,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_DefaultNamespace verifies namespace defaults to "default"
func TestNewFinancialAccountingClient_DefaultNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "", // Should default to "default"
		Port:        50052,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_CustomNamespace verifies custom namespace is respected
func TestNewFinancialAccountingClient_CustomNamespace(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "production",
		Port:        50052,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestFinancialAccountingClient_Close_Success verifies Close closes the connection without error
func TestFinancialAccountingClient_Close_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	err = client.Close()

	assert.NoError(t, err)
}

// TestFinancialAccountingClient_Close_Multiple verifies Close behavior when called multiple times
func TestFinancialAccountingClient_Close_Multiple(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, client)

	// First close
	err1 := client.Close()
	assert.NoError(t, err1)

	// Second close returns error (gRPC connection already closed)
	err2 := client.Close()
	assert.Error(t, err2, "closing already-closed connection should return error")
}

// TestNewFinancialAccountingClient_NilTracer verifies nil tracer is handled gracefully
func TestNewFinancialAccountingClient_NilTracer(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Timeout:     10 * time.Second,
		Tracer:      nil, // Explicitly nil
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}
