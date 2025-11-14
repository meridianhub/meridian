// Package grpc provides utilities for creating gRPC client connections
// with DNS-based load balancing for Kubernetes headless services.
package grpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

var (
	// ErrServiceNameRequired is returned when service name is empty
	ErrServiceNameRequired = errors.New("service name is required")
	// ErrInvalidPort is returned when port is zero or negative
	ErrInvalidPort = errors.New("port must be positive")
)

// ClientConfig holds configuration for creating gRPC clients with load balancing
type ClientConfig struct {
	// ServiceName is the Kubernetes service name (e.g., "current-account")
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default")
	Namespace string

	// Port is the service port number
	Port int

	// Additional dial options to append
	DialOptions []grpc.DialOption
}

// NewClient creates a gRPC client connection with DNS-based client-side load balancing.
//
// This function configures the client to:
//   - Use DNS resolver with the dns:/// scheme for Kubernetes headless services
//   - Apply round_robin load balancing across all pod IPs returned by DNS
//   - Automatically rebalance when pods scale up or down
//
// Requirements:
//   - Kubernetes service must be headless (clusterIP: None)
//   - Service must have stable DNS name in cluster
//
// Example usage:
//
//	conn, err := grpc.NewClient(ctx, grpc.ClientConfig{
//	    ServiceName: "financial-accounting",
//	    Namespace:   "default",
//	    Port:        50052,
//	})
//	if err != nil {
//	    return err
//	}
//	defer conn.Close()
//	client := accountingv1.NewFinancialAccountingServiceClient(conn)
func NewClient(_ context.Context, cfg ClientConfig) (*grpc.ClientConn, error) {
	// Validate required fields
	if cfg.ServiceName == "" {
		return nil, ErrServiceNameRequired
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidPort, cfg.Port)
	}

	// Default namespace if not specified
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}

	// Construct DNS target for Kubernetes headless service
	// Format: dns:///<service-name>.<namespace>.svc.cluster.local:<port>
	target := fmt.Sprintf("dns:///%s.%s.svc.cluster.local:%d",
		cfg.ServiceName,
		cfg.Namespace,
		cfg.Port,
	)

	// Default dial options for production use
	opts := []grpc.DialOption{
		// Use insecure credentials (TLS handled at service mesh level or via mTLS)
		grpc.WithTransportCredentials(insecure.NewCredentials()),

		// Configure default service config with round_robin load balancing
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),

		// Keep-alive configuration for long-lived connections
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second, // Send keepalive ping every 10s
			Timeout:             3 * time.Second,  // Wait 3s for ping ack
			PermitWithoutStream: true,             // Allow pings when no active streams
		}),
	}

	// Append user-provided options (allows overriding defaults)
	opts = append(opts, cfg.DialOptions...)

	// Create connection (grpc.NewClient returns immediately, connection happens in background)
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for %s: %w", target, err)
	}

	return conn, nil
}
