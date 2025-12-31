// Package client provides a gRPC client for the Tenant service.
//
// The Tenant service provides platform tenant lifecycle management including
// tenant creation, status updates, and migration reconciliation. This client
// enables inter-service communication with proper context propagation, tracing,
// and resilience patterns.
//
// Usage with Kubernetes DNS-based load balancing (recommended for production):
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "tenant",
//	    Namespace:   "default",
//	    Port:        50056,
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
//	    Target:  "localhost:50056",
//	    Timeout: 30 * time.Second,
//	})
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// DefaultPort is the default gRPC port for the Tenant service.
	DefaultPort = 50056

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for Tenant.
	ServiceName = "tenant"
)

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = errors.New("either Target or ServiceName must be provided")

// Config holds configuration for the Tenant client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50056" or "tenant:50056").
	// If set, overrides Kubernetes DNS-based discovery.
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "tenant").
	// When specified, enables DNS-based client-side load balancing via pkg/platform/grpc.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50056 if not specified.
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

// Client provides access to the Tenant service.
type Client struct {
	conn      *grpc.ClientConn
	tenant    tenantv1.TenantServiceClient
	tracer    *observability.Tracer
	resilient *clients.ResilientClient
	timeout   time.Duration
}

// New creates a new Tenant gRPC client.
//
// Returns the client, a cleanup function to close the connection, and any error.
// The cleanup function should be deferred immediately after checking the error.
//
// Example:
//
//	client, cleanup, err := client.New(client.Config{
//	    ServiceName: "tenant",
//	    Namespace:   "default",
//	    Port:        50056,
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
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
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
			return nil, nil, fmt.Errorf("failed to create tenant gRPC connection via platform factory: %w", err)
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
		if cfg.Tracer != nil {
			dialOpts = append(dialOpts,
				grpc.WithUnaryInterceptor(cfg.Tracer.UnaryClientInterceptor()),
				grpc.WithStreamInterceptor(cfg.Tracer.StreamClientInterceptor()),
			)
		}

		conn, err = grpc.NewClient(cfg.Target, dialOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create tenant gRPC connection to %s: %w", cfg.Target, err)
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
		tenant:    tenantv1.NewTenantServiceClient(conn),
		tracer:    cfg.Tracer,
		resilient: resilient,
		timeout:   cfg.Timeout,
	}

	cleanup := func() {
		if client.conn != nil {
			_ = client.conn.Close()
		}
	}

	return client, cleanup, nil
}

// InitiateTenant creates a new tenant in the platform registry (BIAN: Initiate).
//
// This operation may complete synchronously or asynchronously depending on system load.
// Always check the provisioning_hint field in the response to determine next steps.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) InitiateTenant(ctx context.Context, req *tenantv1.InitiateTenantRequest) (*tenantv1.InitiateTenantResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	// Note: Tenant service may not have tenant context yet during bootstrap

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "InitiateTenant", func() (*tenantv1.InitiateTenantResponse, error) {
			return c.tenant.InitiateTenant(ctx, req)
		})
	}

	resp, err := c.tenant.InitiateTenant(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate tenant: %w", err)
	}

	return resp, nil
}

// RetrieveTenant gets tenant details by ID (BIAN: Retrieve).
//
// This endpoint is used for:
// 1. Retrieving current tenant details
// 2. Polling provisioning status when InitiateTenant returns provisioning_hint="pending"
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) RetrieveTenant(ctx context.Context, req *tenantv1.RetrieveTenantRequest) (*tenantv1.RetrieveTenantResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveTenant", func() (*tenantv1.RetrieveTenantResponse, error) {
			return c.tenant.RetrieveTenant(ctx, req)
		})
	}

	resp, err := c.tenant.RetrieveTenant(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve tenant: %w", err)
	}

	return resp, nil
}

// UpdateTenantStatus changes the lifecycle status of a tenant (BIAN: Update).
// Status updates are idempotent when using version-based concurrency, so retry is enabled.
func (c *Client) UpdateTenantStatus(ctx context.Context, req *tenantv1.UpdateTenantStatusRequest) (*tenantv1.UpdateTenantStatusResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	// Use resilience patterns if configured (with retry for idempotent update)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "UpdateTenantStatus", func() (*tenantv1.UpdateTenantStatusResponse, error) {
			return c.tenant.UpdateTenantStatus(ctx, req)
		})
	}

	resp, err := c.tenant.UpdateTenantStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to update tenant status: %w", err)
	}

	return resp, nil
}

// ListTenants returns all tenants with optional status filter (BIAN: Control).
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) ListTenants(ctx context.Context, req *tenantv1.ListTenantsRequest) (*tenantv1.ListTenantsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListTenants", func() (*tenantv1.ListTenantsResponse, error) {
			return c.tenant.ListTenants(ctx, req)
		})
	}

	resp, err := c.tenant.ListTenants(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list tenants: %w", err)
	}

	return resp, nil
}

// ReconcileMigrations applies new migrations to existing tenant schemas.
// When services add new migrations after tenants are created, existing tenant
// schemas may be missing these migrations. This operation detects and applies
// new migrations to bring tenant schemas up to date.
// This is a non-idempotent operation, so it uses circuit breaker without retry.
func (c *Client) ReconcileMigrations(ctx context.Context, req *tenantv1.ReconcileMigrationsRequest) (*tenantv1.ReconcileMigrationsResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	// Use resilience patterns if configured (no retry for non-idempotent operations)
	if c.resilient != nil {
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "ReconcileMigrations", func() (*tenantv1.ReconcileMigrationsResponse, error) {
			return c.tenant.ReconcileMigrations(ctx, req)
		})
	}

	resp, err := c.tenant.ReconcileMigrations(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile migrations: %w", err)
	}

	return resp, nil
}

// GetTenantProvisioningStatus retrieves detailed provisioning status for a tenant.
// Returns per-service provisioning progress including migration versions and error details.
// This is an idempotent read operation, so it uses circuit breaker with retry.
func (c *Client) GetTenantProvisioningStatus(ctx context.Context, req *tenantv1.GetTenantProvisioningStatusRequest) (*tenantv1.GetTenantProvisioningStatusResponse, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	// Use resilience patterns if configured (with retry for idempotent read)
	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "GetTenantProvisioningStatus", func() (*tenantv1.GetTenantProvisioningStatusResponse, error) {
			return c.tenant.GetTenantProvisioningStatus(ctx, req)
		})
	}

	resp, err := c.tenant.GetTenantProvisioningStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant provisioning status: %w", err)
	}

	return resp, nil
}

// Close terminates the gRPC connection gracefully.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close tenant client connection: %w", err)
		}
	}
	return nil
}
