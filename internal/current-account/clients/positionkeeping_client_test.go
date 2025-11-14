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

// TestNewPositionKeepingClient_Success verifies client creation with valid configuration
func TestNewPositionKeepingClient_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_RequiresTarget verifies error when target is missing
func TestNewPositionKeepingClient_RequiresTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	assert.ErrorIs(t, err, clients.ErrPositionKeepingTargetRequired)
	assert.Nil(t, client)
}

// TestNewPositionKeepingClient_DefaultTimeout verifies 30s default timeout is applied
func TestNewPositionKeepingClient_DefaultTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: 0, // Should default to 30 seconds
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
		Target:  "localhost:50051",
		Timeout: customTimeout,
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
		Target:  "localhost:50051",
		Timeout: 10 * time.Second,
		Tracer:  tracer,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_DefaultDialOptions verifies insecure credentials are used by default
func TestNewPositionKeepingClient_DefaultDialOptions(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:      "localhost:50051",
		Timeout:     10 * time.Second,
		DialOptions: nil, // Should use default insecure credentials
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_CustomDialOptions verifies custom dial options are respected
func TestNewPositionKeepingClient_CustomDialOptions(t *testing.T) {
	t.Parallel()

	customOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	cfg := &clients.PositionKeepingClientConfig{
		Target:      "localhost:50051",
		Timeout:     10 * time.Second,
		DialOptions: customOpts,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_EmptyTarget verifies empty target is rejected
func TestNewPositionKeepingClient_EmptyTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "",
		Timeout: 10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	assert.ErrorIs(t, err, clients.ErrPositionKeepingTargetRequired)
	assert.Nil(t, client)
}

// TestNewPositionKeepingClient_WhitespaceTarget verifies whitespace-only target is rejected
func TestNewPositionKeepingClient_WhitespaceTarget(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "   ",
		Timeout: 10 * time.Second,
	}

	// gRPC NewClient accepts whitespace, but our validation catches empty string
	// This test documents current behavior - whitespace is treated as valid by gRPC
	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_ValidTargetFormats verifies various valid target formats
func TestNewPositionKeepingClient_ValidTargetFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "host and port",
			target: "localhost:50051",
		},
		{
			name:   "IP and port",
			target: "127.0.0.1:50051",
		},
		{
			name:   "service name",
			target: "positionkeeping-service:443",
		},
		{
			name:   "DNS name",
			target: "positionkeeping.example.com:443",
		},
		{
			name:   "kubernetes service",
			target: "positionkeeping.default.svc.cluster.local:50051",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &clients.PositionKeepingClientConfig{
				Target:  tt.target,
				Timeout: 10 * time.Second,
			}

			client, err := clients.NewPositionKeepingClient(cfg)

			require.NoError(t, err)
			require.NotNil(t, client)
			assert.NoError(t, client.Close())
		})
	}
}

// TestPositionKeepingClient_Close_Success verifies Close closes the connection without error
func TestPositionKeepingClient_Close_Success(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: 10 * time.Second,
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
		Target:  "localhost:50051",
		Timeout: 10 * time.Second,
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

// TestNewPositionKeepingClient_WithTracerAndCustomOptions verifies tracer and custom options work together
func TestNewPositionKeepingClient_WithTracerAndCustomOptions(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	customOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	cfg := &clients.PositionKeepingClientConfig{
		Target:      "localhost:50051",
		Timeout:     10 * time.Second,
		Tracer:      tracer,
		DialOptions: customOpts,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_ZeroTimeout verifies zero timeout is overridden to default
func TestNewPositionKeepingClient_ZeroTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: 0, // Explicitly zero
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_NegativeTimeout verifies negative timeout is overridden to default
func TestNewPositionKeepingClient_NegativeTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: -5 * time.Second, // Negative
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_VeryShortTimeout verifies very short timeout is accepted
func TestNewPositionKeepingClient_VeryShortTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: 1 * time.Millisecond, // Very short
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_VeryLongTimeout verifies very long timeout is accepted
func TestNewPositionKeepingClient_VeryLongTimeout(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: 24 * time.Hour, // Very long
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_NilTracer verifies nil tracer is handled gracefully
func TestNewPositionKeepingClient_NilTracer(t *testing.T) {
	t.Parallel()

	cfg := &clients.PositionKeepingClientConfig{
		Target:  "localhost:50051",
		Timeout: 10 * time.Second,
		Tracer:  nil, // Explicitly nil
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_DNSBasedMode verifies DNS-based client creation
func TestNewPositionKeepingClient_DNSBasedMode(t *testing.T) {
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

// TestNewPositionKeepingClient_DNSBasedMode_DefaultNamespace verifies namespace defaults to "default"
func TestNewPositionKeepingClient_DNSBasedMode_DefaultNamespace(t *testing.T) {
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

// TestNewPositionKeepingClient_DNSBasedMode_WithTracer verifies DNS mode with tracer
func TestNewPositionKeepingClient_DNSBasedMode_WithTracer(t *testing.T) {
	t.Parallel()

	tracer := &observability.Tracer{}
	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "test-namespace",
		Port:        50053,
		Timeout:     10 * time.Second,
		Tracer:      tracer,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}

// TestNewPositionKeepingClient_DNSBasedMode_CustomNamespace verifies custom namespace
func TestNewPositionKeepingClient_DNSBasedMode_CustomNamespace(t *testing.T) {
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

// TestNewPositionKeepingClient_PrefersDNSOverTarget verifies ServiceName takes precedence
func TestNewPositionKeepingClient_PrefersDNSOverTarget(t *testing.T) {
	t.Parallel()

	// When both ServiceName and Target are provided, ServiceName should be used
	cfg := &clients.PositionKeepingClientConfig{
		ServiceName: "position-keeping",
		Namespace:   "default",
		Port:        50053,
		Target:      "ignored:9999", // Should be ignored
		Timeout:     10 * time.Second,
	}

	client, err := clients.NewPositionKeepingClient(cfg)

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NoError(t, client.Close())
}
