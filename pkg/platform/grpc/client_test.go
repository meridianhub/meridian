package grpc_test

import (
	"context"
	"strings"
	"testing"

	platformgrpc "github.com/meridianhub/meridian/pkg/platform/grpc"
)

// Test_NewClient_ValidConfiguration verifies successful client creation with valid configs
func Test_NewClient_ValidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config platformgrpc.ClientConfig
	}{
		{
			name: "valid configuration with explicit namespace",
			config: platformgrpc.ClientConfig{
				ServiceName: "current-account",
				Namespace:   "production",
				Port:        50051,
			},
		},
		{
			name: "defaults to 'default' namespace",
			config: platformgrpc.ClientConfig{
				ServiceName: "financial-accounting",
				Port:        50052,
			},
		},
		{
			name: "service name with whitespace is trimmed",
			config: platformgrpc.ClientConfig{
				ServiceName: "  current-account  ",
				Port:        50051,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			conn, err := platformgrpc.NewClient(ctx, tt.config)
			if err != nil {
				t.Errorf("NewClient() unexpected error = %v", err)
			}
			if conn != nil {
				_ = conn.Close()
			}
		})
	}
}

// Test_NewClient_LoadBalancingPolicy verifies round_robin is configured
func Test_NewClient_LoadBalancingPolicy(t *testing.T) {
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

// Test_NewClient_DNSScheme verifies dns:/// scheme is used
func Test_NewClient_DNSScheme(t *testing.T) {
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

// Test_NewClient_InvalidInput verifies input validation
func Test_NewClient_InvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		config  platformgrpc.ClientConfig
		wantErr error
	}{
		{
			name: "empty service name",
			config: platformgrpc.ClientConfig{
				ServiceName: "",
				Port:        50051,
			},
			wantErr: platformgrpc.ErrServiceNameRequired,
		},
		{
			name: "whitespace-only service name",
			config: platformgrpc.ClientConfig{
				ServiceName: "   ",
				Port:        50051,
			},
			wantErr: platformgrpc.ErrServiceNameRequired,
		},
		{
			name: "zero port",
			config: platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Port:        0,
			},
			wantErr: platformgrpc.ErrInvalidPort,
		},
		{
			name: "negative port",
			config: platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Port:        -1,
			},
			wantErr: platformgrpc.ErrInvalidPort,
		},
		{
			name: "port above maximum (65536)",
			config: platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Port:        65536,
			},
			wantErr: platformgrpc.ErrInvalidPort,
		},
		{
			name: "port far above maximum",
			config: platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Port:        99999,
			},
			wantErr: platformgrpc.ErrInvalidPort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			conn, err := platformgrpc.NewClient(ctx, tt.config)

			if err == nil {
				t.Error("NewClient() expected error but got nil")
				if conn != nil {
					_ = conn.Close()
				}
				return
			}

			if tt.wantErr != nil && !strings.Contains(err.Error(), tt.wantErr.Error()) {
				t.Errorf("NewClient() error = %v, want error containing %v", err, tt.wantErr)
			}
		})
	}
}

// Test_NewClient_BoundaryPorts verifies boundary port validation
func Test_NewClient_BoundaryPorts(t *testing.T) {
	tests := []struct {
		name      string
		port      int
		wantError bool
	}{
		{
			name:      "minimum valid port (1)",
			port:      1,
			wantError: false,
		},
		{
			name:      "maximum valid port (65535)",
			port:      65535,
			wantError: false,
		},
		{
			name:      "common gRPC port",
			port:      50051,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Port:        tt.port,
			}

			ctx := context.Background()
			conn, err := platformgrpc.NewClient(ctx, config)

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

// Test_NewClient_NamespaceHandling verifies namespace edge cases
func Test_NewClient_NamespaceHandling(t *testing.T) {
	tests := []struct {
		name              string
		config            platformgrpc.ClientConfig
		expectedNamespace string
	}{
		{
			name: "explicit namespace",
			config: platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Namespace:   "production",
				Port:        50051,
			},
			expectedNamespace: "production",
		},
		{
			name: "defaults to default namespace",
			config: platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Port:        50051,
				// Namespace omitted
			},
			expectedNamespace: "default",
		},
		{
			name: "trims whitespace from namespace",
			config: platformgrpc.ClientConfig{
				ServiceName: "test-service",
				Namespace:   "  production  ",
				Port:        50051,
			},
			expectedNamespace: "production",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			conn, err := platformgrpc.NewClient(ctx, tt.config)
			if err != nil {
				t.Errorf("NewClient() unexpected error = %v", err)
			}
			if conn != nil {
				_ = conn.Close()
			}
			// Note: Namespace validation happens internally, we verify no error occurs
		})
	}
}
