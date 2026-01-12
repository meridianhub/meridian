// Package client provides a gRPC client for the InternalBankAccount service.
//
// The InternalBankAccount service provides BIAN-compliant internal bank account
// operations for managing counterparty and operational accounts. This client enables
// inter-service communication with proper context propagation, tracing, and
// resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "internal-bank-account",
//	    Namespace:   "default",
//	    Port:        50057,
//	    Tracer:      tracer,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
//
// Usage with direct connection (for local development):
//
//	client, cleanup, err := client.New(client.Config{
//	    Target:  "localhost:50057",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// DefaultPort is the default gRPC port for the InternalBankAccount service.
	DefaultPort = 50057

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for InternalBankAccount.
	ServiceName = "internal-bank-account"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = errors.New("either Target or ServiceName must be provided")

// Config holds configuration for the InternalBankAccount client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50057" or "internal-bank-account:50057").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "internal-bank-account").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50057 if not specified.
	Port int

	// Timeout is the default timeout for RPC calls.
	// Defaults to 30 seconds if not specified.
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing.
	// If provided, the client will automatically propagate trace context.
	Tracer *observability.Tracer

	// Resilience is an optional configuration for circuit breaker and retry.
	// If provided, calls will be wrapped with resilience patterns.
	Resilience *clients.ResilientClientConfig

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// Client provides access to the InternalBankAccount service.
type Client struct {
	conn      *grpc.ClientConn
	tracer    *observability.Tracer
	resilient *clients.ResilientClient
	timeout   time.Duration
	// TODO: Add generated proto client when proto is defined in task 6
	// internalBankAccount internalbankaccountv1.InternalBankAccountServiceClient
}

// New creates a new InternalBankAccount gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
//
// Example:
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "internal-bank-account",
//	    Namespace:   "default",
//	    Port:        50057,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
func New(cfg Config) (*Client, func(), error) {
	// Apply defaults
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}

	var conn *grpc.ClientConn
	var err error

	// Use platform gRPC factory when ServiceName is provided (preferred)
	if cfg.ServiceName != "" {
		dialOpts := cfg.DialOptions

		// Add tracing interceptors if tracer is provided
		// Use WithChainUnaryInterceptor/WithChainStreamInterceptor to properly chain
		// multiple interceptors instead of overwriting them
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithChainUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithChainStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		// Use platform factory for DNS-based load balancing
		conn, err = platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
			ServiceName: cfg.ServiceName,
			Namespace:   cfg.Namespace,
			Port:        cfg.Port,
			DialOptions: dialOpts,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create internal-bank-account gRPC connection via platform factory: %w", err)
		}
	} else if cfg.Target != "" {
		// Fallback to legacy direct connection for backward compatibility
		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}

		// Add tracing interceptors if tracer is provided
		// Use WithChainUnaryInterceptor/WithChainStreamInterceptor to properly chain
		// multiple interceptors instead of overwriting them
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithChainUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithChainStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		conn, err = grpc.NewClient(cfg.Target, dialOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create internal-bank-account gRPC connection to %s: %w", cfg.Target, err)
		}
	} else {
		return nil, nil, ErrTargetRequired
	}

	// Create resilient client if configuration is provided
	var resilient *clients.ResilientClient
	if cfg.Resilience != nil {
		resilient = clients.NewResilientClient(*cfg.Resilience)
	}

	client := &Client{
		conn:      conn,
		tracer:    cfg.Tracer,
		resilient: resilient,
		timeout:   cfg.Timeout,
		// TODO: Initialize proto client when proto is defined in task 6
		// internalBankAccount: internalbankaccountv1.NewInternalBankAccountServiceClient(conn),
	}

	cleanup := func() {
		if client.conn != nil {
			_ = client.conn.Close()
		}
	}

	return client, cleanup, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close internal-bank-account client connection: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection for creating additional clients
// (e.g., health check clients that bypass the business client's circuit breaker).
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

// TODO: Add service methods when proto is defined in task 6
// Example methods that will be implemented:
//
// - InitiateInternalBankAccount: Create a new internal bank account
// - RetrieveInternalBankAccount: Get account details
// - UpdateInternalBankAccount: Modify account properties
// - TerminateInternalBankAccount: Close an account
