package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrManifestClientConfigRequired is returned when a nil config is passed to NewManifestClient.
var ErrManifestClientConfigRequired = fmt.Errorf("ManifestClientConfig is required")

// ErrManifestClientServiceNameRequired is returned when ServiceName is not provided.
var ErrManifestClientServiceNameRequired = fmt.Errorf("ServiceName is required for manifest client")

// ManifestClient fetches the current manifest saga definitions from the control-plane.
// Used by the event-router to initialize and reload the SagaRegistry on startup.
type ManifestClient struct {
	conn    *grpc.ClientConn
	client  controlplanev1.ManifestHistoryServiceClient
	timeout time.Duration
	logger  *slog.Logger
}

// ManifestClientConfig holds configuration for creating a ManifestClient.
type ManifestClientConfig struct {
	// ServiceName is the Kubernetes service name (e.g., "control-plane").
	ServiceName string

	// Namespace is the Kubernetes namespace. Empty string defaults to "default"
	// (handled by platformgrpc.NewClient).
	Namespace string

	// Port is the service gRPC port number.
	Port int

	// Timeout is the default timeout for RPC calls (defaults to 10 seconds).
	Timeout time.Duration

	// Logger is used for structured logging.
	Logger *slog.Logger

	// DialOptions allows custom gRPC dial options (for testing).
	DialOptions []grpc.DialOption
}

// NewManifestClient creates a new gRPC client for the control-plane ManifestHistoryService.
func NewManifestClient(cfg *ManifestClientConfig) (*ManifestClient, error) {
	if cfg == nil {
		return nil, ErrManifestClientConfigRequired
	}
	if cfg.ServiceName == "" {
		return nil, ErrManifestClientServiceNameRequired
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	conn, err := platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		DialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to control-plane: %w", err)
	}

	return &ManifestClient{
		conn:    conn,
		client:  controlplanev1.NewManifestHistoryServiceClient(conn),
		timeout: cfg.Timeout,
		logger:  cfg.Logger,
	}, nil
}

// GetCurrentSagaDefinitions fetches the saga definitions from the most recently
// applied manifest. Returns an empty slice when no manifest has been applied yet.
func (c *ManifestClient) GetCurrentSagaDefinitions(ctx context.Context) ([]*controlplanev1.SagaDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)

	resp, err := c.client.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.NotFound {
			// No manifest applied yet — return empty slice, not an error.
			c.logger.Info("no manifest found for tenant, saga registry will start empty")
			return []*controlplanev1.SagaDefinition{}, nil
		}
		return nil, fmt.Errorf("fetch current manifest: %w", err)
	}

	mf := resp.GetVersion().GetManifest()
	if mf == nil {
		c.logger.Info("current manifest has no content, saga registry will start empty")
		return []*controlplanev1.SagaDefinition{}, nil
	}

	c.logger.Info("loaded saga definitions from current manifest",
		"count", len(mf.GetSagas()),
		"manifest_version", mf.GetVersion(),
	)

	return mf.GetSagas(), nil
}

// Close releases the gRPC client connection.
func (c *ManifestClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close manifest client connection: %w", err)
		}
	}
	return nil
}
