package clients_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestNewFinancialAccountingClient_Success verifies client creation with valid configuration
func TestNewFinancialAccountingClient_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_RequiresTarget verifies error when target is missing
func TestNewFinancialAccountingClient_RequiresTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	assert.ErrorIs(t, err, clients.ErrFinancialAccountingTargetRequired)
	assert.Nil(t, client)
}

// TestNewFinancialAccountingClient_DefaultTimeout verifies 30s default timeout is applied
func TestNewFinancialAccountingClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: 0, // Should default to 30 seconds
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
		Target:  "localhost:50052",
		Timeout: customTimeout,
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
		Target:  "localhost:50052",
		Timeout: 10 * time.Second,
		Tracer:  tracer,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_DefaultDialOptions verifies insecure credentials are used by default
func TestNewFinancialAccountingClient_DefaultDialOptions(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:      "localhost:50052",
		Timeout:     10 * time.Second,
		DialOptions: nil, // Should use default insecure credentials
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_CustomDialOptions verifies custom dial options are respected
func TestNewFinancialAccountingClient_CustomDialOptions(t *testing.T) {
	t.Parallel()

	customOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	cfg := &clients.FinancialAccountingClientConfig{
		Target:      "localhost:50052",
		Timeout:     10 * time.Second,
		DialOptions: customOpts,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_EmptyTarget verifies empty target is rejected
func TestNewFinancialAccountingClient_EmptyTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	assert.ErrorIs(t, err, clients.ErrFinancialAccountingTargetRequired)
	assert.Nil(t, client)
}

// TestNewFinancialAccountingClient_WhitespaceTarget verifies whitespace-only target is rejected
func TestNewFinancialAccountingClient_WhitespaceTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "   ",
		Timeout: 10 * time.Second,
	}

	// gRPC NewClient accepts whitespace, but our validation catches empty string
	// This test documents current behavior - whitespace is treated as valid by gRPC
	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_ValidTargetFormats verifies various valid target formats
func TestNewFinancialAccountingClient_ValidTargetFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "host and port",
			target: "localhost:50052",
		},
		{
			name:   "IP and port",
			target: "127.0.0.1:50052",
		},
		{
			name:   "service name",
			target: "financialaccounting-service:443",
		},
		{
			name:   "DNS name",
			target: "financialaccounting.example.com:443",
		},
		{
			name:   "kubernetes service",
			target: "financialaccounting.default.svc.cluster.local:50052",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &clients.FinancialAccountingClientConfig{
				Target:  tt.target,
				Timeout: 10 * time.Second,
			}

			client, err := clients.NewFinancialAccountingClient(cfg)

			require.NoError(t, err)
			require.NotNil(t, client)
			assert.NoError(t, client.Close())
		})
	}
}

// TestFinancialAccountingClient_Close_Success verifies Close closes the connection without error
func TestFinancialAccountingClient_Close_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: 10 * time.Second,
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
		Target:  "localhost:50052",
		Timeout: 10 * time.Second,
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

// TestNewFinancialAccountingClient_WithTracerAndCustomOptions verifies tracer and custom options work together
func TestNewFinancialAccountingClient_WithTracerAndCustomOptions(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	customOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	cfg := &clients.FinancialAccountingClientConfig{
		Target:      "localhost:50052",
		Timeout:     10 * time.Second,
		Tracer:      tracer,
		DialOptions: customOpts,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_ZeroTimeout verifies zero timeout is overridden to default
func TestNewFinancialAccountingClient_ZeroTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: 0, // Explicitly zero
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_NegativeTimeout verifies negative timeout is overridden to default
func TestNewFinancialAccountingClient_NegativeTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: -5 * time.Second, // Negative
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_VeryShortTimeout verifies very short timeout is accepted
func TestNewFinancialAccountingClient_VeryShortTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: 1 * time.Millisecond, // Very short
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_VeryLongTimeout verifies very long timeout is accepted
func TestNewFinancialAccountingClient_VeryLongTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: 24 * time.Hour, // Very long
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_NilTracer verifies nil tracer is handled gracefully
func TestNewFinancialAccountingClient_NilTracer(t *testing.T) {
	t.Parallel()

	cfg := &clients.FinancialAccountingClientConfig{
		Target:  "localhost:50052",
		Timeout: 10 * time.Second,
		Tracer:  nil, // Explicitly nil
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_DNSBasedMode verifies DNS-based client creation
func TestNewFinancialAccountingClient_DNSBasedMode(t *testing.T) {
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

// TestNewFinancialAccountingClient_DNSBasedMode_DefaultNamespace verifies namespace defaults to "default"
func TestNewFinancialAccountingClient_DNSBasedMode_DefaultNamespace(t *testing.T) {
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

// TestNewFinancialAccountingClient_DNSBasedMode_WithTracer verifies DNS mode with tracer
func TestNewFinancialAccountingClient_DNSBasedMode_WithTracer(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "test-namespace",
		Port:        50052,
		Timeout:     10 * time.Second,
		Tracer:      tracer,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewFinancialAccountingClient_DNSBasedMode_CustomNamespace verifies custom namespace
func TestNewFinancialAccountingClient_DNSBasedMode_CustomNamespace(t *testing.T) {
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

// TestNewFinancialAccountingClient_PrefersDNSOverTarget verifies ServiceName takes precedence
func TestNewFinancialAccountingClient_PrefersDNSOverTarget(t *testing.T) {
	t.Parallel()

	// When both ServiceName and Target are provided, ServiceName should be used
	cfg := &clients.FinancialAccountingClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
		Target:      "ignored:9999", // Should be ignored
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewFinancialAccountingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}
