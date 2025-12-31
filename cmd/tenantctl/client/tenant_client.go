// Package client provides a gRPC client for the Tenant service.
package client

import (
	"context"
	"fmt"
	"time"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	sharedgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TenantClient wraps the gRPC client for the Tenant service.
type TenantClient struct {
	conn    *grpc.ClientConn
	client  tenantv1.TenantServiceClient
	timeout time.Duration
}

// Config holds configuration for the TenantClient.
type Config struct {
	// ServiceURL is the direct URL to the tenant service (e.g., "localhost:50056").
	// If set, overrides Kubernetes DNS-based discovery.
	ServiceURL string

	// ServiceName is the Kubernetes service name for DNS-based discovery.
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default").
	Namespace string

	// Port is the service port number (defaults to 50056).
	Port int

	// Timeout is the default timeout for gRPC calls (defaults to 30s).
	Timeout time.Duration

	// DialOptions are additional gRPC dial options.
	DialOptions []grpc.DialOption
}

// DefaultConfig returns a Config with sensible defaults for local development.
func DefaultConfig() Config {
	return Config{
		ServiceURL:  "localhost:50056",
		ServiceName: "tenant",
		Namespace:   "default",
		Port:        50056,
		Timeout:     defaults.DefaultRPCTimeout,
	}
}

// NewTenantClient creates a new TenantClient.
func NewTenantClient(ctx context.Context, cfg Config) (*TenantClient, error) {
	// Apply defaults
	if cfg.Timeout == 0 {
		cfg.Timeout = defaults.DefaultRPCTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = 50056
	}

	var conn *grpc.ClientConn
	var err error

	if cfg.ServiceURL != "" {
		// Direct connection for local development or custom endpoints
		opts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
		opts = append(opts, cfg.DialOptions...)
		conn, err = grpc.NewClient(cfg.ServiceURL, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create tenant gRPC connection: %w", err)
		}
	} else {
		// Kubernetes DNS-based discovery
		conn, err = sharedgrpc.NewClient(ctx, sharedgrpc.ClientConfig{
			ServiceName: cfg.ServiceName,
			Namespace:   cfg.Namespace,
			Port:        cfg.Port,
			DialOptions: cfg.DialOptions,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create tenant gRPC connection: %w", err)
		}
	}

	return &TenantClient{
		conn:    conn,
		client:  tenantv1.NewTenantServiceClient(conn),
		timeout: cfg.Timeout,
	}, nil
}

// InitiateTenant creates a new tenant (BIAN: Initiate).
func (c *TenantClient) InitiateTenant(ctx context.Context, req *tenantv1.InitiateTenantRequest) (*tenantv1.InitiateTenantResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.InitiateTenant(ctx, req)
}

// RetrieveTenant gets tenant details by ID (BIAN: Retrieve).
func (c *TenantClient) RetrieveTenant(ctx context.Context, req *tenantv1.RetrieveTenantRequest) (*tenantv1.RetrieveTenantResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.RetrieveTenant(ctx, req)
}

// UpdateTenantStatus changes the lifecycle status (BIAN: Update).
func (c *TenantClient) UpdateTenantStatus(ctx context.Context, req *tenantv1.UpdateTenantStatusRequest) (*tenantv1.UpdateTenantStatusResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.UpdateTenantStatus(ctx, req)
}

// ListTenants lists all tenants with optional filter (BIAN: Control).
func (c *TenantClient) ListTenants(ctx context.Context, req *tenantv1.ListTenantsRequest) (*tenantv1.ListTenantsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.ListTenants(ctx, req)
}

// GetTenantProvisioningStatus retrieves detailed provisioning status for a tenant.
func (c *TenantClient) GetTenantProvisioningStatus(ctx context.Context, req *tenantv1.GetTenantProvisioningStatusRequest) (*tenantv1.GetTenantProvisioningStatusResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.GetTenantProvisioningStatus(ctx, req)
}

// Close closes the underlying gRPC connection.
func (c *TenantClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
