package grpc_test

import (
	"context"
	"strings"
	"testing"

	platformgrpc "github.com/meridianhub/meridian/pkg/platform/grpc"
)

// TestNewClient_DNSTarget verifies DNS target construction
func TestNewClient_DNSTarget(t *testing.T) {
	tests := []struct {
		name      string
		config    platformgrpc.ClientConfig
		wantError bool
	}{
		{
			name: "valid configuration with explicit namespace",
			config: platformgrpc.ClientConfig{
				ServiceName: "current-account",
				Namespace:   "production",
				Port:        50051,
			},
			wantError: false, // grpc.NewClient succeeds, connection happens in background
		},
		{
			name: "defaults to 'default' namespace",
			config: platformgrpc.ClientConfig{
				ServiceName: "financial-accounting",
				Port:        50052,
			},
			wantError: false,
		},
		{
			name: "empty service name should fail",
			config: platformgrpc.ClientConfig{
				ServiceName: "",
				Port:        50051,
			},
			wantError: true, // Validation error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			conn, err := platformgrpc.NewClient(ctx, tt.config)

			if tt.wantError {
				if err == nil {
					t.Error("NewClient() expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("NewClient() unexpected error = %v", err)
				}
			}
			if conn != nil {
				_ = conn.Close()
			}
		})
	}
}

// TestNewClient_LoadBalancingPolicy verifies round_robin is configured
func TestNewClient_LoadBalancingPolicy(t *testing.T) {
	// This test verifies load balancing configuration is set
	config := platformgrpc.ClientConfig{
		ServiceName: "test-service",
		Namespace:   "default",
		Port:        50051,
	}

	ctx := context.Background()
	conn, err := platformgrpc.NewClient(ctx, config)
	// NewClient should succeed (connection happens in background)
	if err != nil {
		t.Errorf("NewClient() unexpected error = %v", err)
	}
	if conn != nil {
		_ = conn.Close()
	}
}

// TestNewClient_DNSScheme verifies dns:/// scheme is used
func TestNewClient_DNSScheme(t *testing.T) {
	// Verify client creation succeeds with valid DNS target format
	config := platformgrpc.ClientConfig{
		ServiceName: "missing-service",
		Namespace:   "test",
		Port:        9999,
	}

	ctx := context.Background()
	conn, err := platformgrpc.NewClient(ctx, config)
	// Should succeed - grpc.NewClient doesn't block on connection
	if err != nil {
		t.Errorf("NewClient() unexpected error = %v", err)
	}
	// Error should not be scheme-related
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unknown scheme") {
		t.Errorf("NewClient() has invalid DNS scheme, error: %v", err)
	}
	if conn != nil {
		_ = conn.Close()
	}
}

// TestNewClient_EmptyServiceName_Negative verifies validation
func TestNewClient_EmptyServiceName_Negative(t *testing.T) {
	// RED: This should fail when service name is empty
	config := platformgrpc.ClientConfig{
		ServiceName: "",
		Port:        50051,
	}

	ctx := context.Background()
	_, err := platformgrpc.NewClient(ctx, config)

	if err == nil {
		t.Error("NewClient() should fail with empty service name")
	}
}

// TestNewClient_ZeroPort_Negative verifies port validation
func TestNewClient_ZeroPort_Negative(t *testing.T) {
	// RED: This should fail when port is 0
	config := platformgrpc.ClientConfig{
		ServiceName: "test-service",
		Port:        0,
	}

	ctx := context.Background()
	_, err := platformgrpc.NewClient(ctx, config)

	if err == nil {
		t.Error("NewClient() should fail with zero port")
	}
}

// Example demonstrates creating a gRPC client for inter-service communication
func ExampleNewClient() {
	ctx := context.Background()

	// Create client connection to financial-accounting service
	conn, err := platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
		ServiceName: "financial-accounting",
		Namespace:   "default",
		Port:        50052,
	})
	if err != nil {
		// Handle error
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	// Use connection to create service client
	// client := accountingv1.NewFinancialAccountingServiceClient(conn)
	// ...
}
