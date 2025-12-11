// Package client provides a gRPC client for the Organization service.
package client

import (
	"context"
	"fmt"
	"time"

	organizationv1 "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	sharedgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// OrganizationClient wraps the gRPC client for the Organization service.
type OrganizationClient struct {
	conn    *grpc.ClientConn
	client  organizationv1.OrganizationServiceClient
	timeout time.Duration
}

// Config holds configuration for the OrganizationClient.
type Config struct {
	// ServiceURL is the direct URL to the organization service (e.g., "localhost:50056").
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
		ServiceName: "organization",
		Namespace:   "default",
		Port:        50056,
		Timeout:     30 * time.Second,
	}
}

// NewOrganizationClient creates a new OrganizationClient.
func NewOrganizationClient(ctx context.Context, cfg Config) (*OrganizationClient, error) {
	// Apply defaults
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
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
			return nil, fmt.Errorf("failed to create organization gRPC connection: %w", err)
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
			return nil, fmt.Errorf("failed to create organization gRPC connection: %w", err)
		}
	}

	return &OrganizationClient{
		conn:    conn,
		client:  organizationv1.NewOrganizationServiceClient(conn),
		timeout: cfg.Timeout,
	}, nil
}

// InitiateOrganization creates a new organization (BIAN: Initiate).
func (c *OrganizationClient) InitiateOrganization(ctx context.Context, req *organizationv1.InitiateOrganizationRequest) (*organizationv1.InitiateOrganizationResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.InitiateOrganization(ctx, req)
}

// RetrieveOrganization gets organization details by ID (BIAN: Retrieve).
func (c *OrganizationClient) RetrieveOrganization(ctx context.Context, req *organizationv1.RetrieveOrganizationRequest) (*organizationv1.RetrieveOrganizationResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.RetrieveOrganization(ctx, req)
}

// UpdateOrganizationStatus changes the lifecycle status (BIAN: Update).
func (c *OrganizationClient) UpdateOrganizationStatus(ctx context.Context, req *organizationv1.UpdateOrganizationStatusRequest) (*organizationv1.UpdateOrganizationStatusResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.UpdateOrganizationStatus(ctx, req)
}

// ListOrganizations lists all organizations with optional filter (BIAN: Control).
func (c *OrganizationClient) ListOrganizations(ctx context.Context, req *organizationv1.ListOrganizationsRequest) (*organizationv1.ListOrganizationsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.ListOrganizations(ctx, req)
}

// Close closes the underlying gRPC connection.
func (c *OrganizationClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
