// Package grpc provides gRPC client adapters for external service communication.
package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	auditdomain "github.com/meridianhub/meridian/internal/audit-consumer/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"google.golang.org/grpc"
)

// Static errors for validation
var (
	// ErrServiceNameRequired is returned when ServiceName is not provided in client configuration
	ErrServiceNameRequired = fmt.Errorf("ServiceName is required for position keeping client")

	// ErrRecordMeasurementNotImplemented is returned when RecordMeasurement is called in non-simulation mode
	ErrRecordMeasurementNotImplemented = fmt.Errorf("RecordMeasurement endpoint not yet implemented in Position Keeping service")
)

// PositionKeepingGRPCClient implements domain.PositionKeepingClient using gRPC.
type PositionKeepingGRPCClient struct {
	conn           *grpc.ClientConn
	client         positionkeepingv1.PositionKeepingServiceClient
	timeout        time.Duration
	logger         *slog.Logger
	simulationMode bool // true when RecordMeasurement endpoint doesn't exist yet
}

// ClientConfig holds configuration for the Position Keeping gRPC client.
type ClientConfig struct {
	// ServiceName is the Kubernetes service name (e.g., "position-keeping").
	// Required for DNS-based load balancing.
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default" if empty).
	Namespace string

	// Port is the service port number (Position Keeping uses 50053).
	Port int

	// Timeout is the default timeout for RPC calls (defaults to 10 seconds).
	Timeout time.Duration

	// Logger is used for structured logging.
	Logger *slog.Logger

	// SimulationMode enables logging-only mode when true (for testing before API exists).
	SimulationMode bool

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// NewPositionKeepingClient creates a new Position Keeping gRPC client.
//
// The client uses DNS-based service discovery and round-robin load balancing.
// When SimulationMode is enabled, RecordMeasurement calls will only log and not
// actually call the Position Keeping service.
//
// Example:
//
//	config := &grpc.ClientConfig{
//	    ServiceName:    "position-keeping",
//	    Namespace:      "default",
//	    Port:           50053,
//	    Timeout:        10 * time.Second,
//	    Logger:         logger,
//	    SimulationMode: false,
//	}
//	client, err := grpc.NewPositionKeepingClient(config)
func NewPositionKeepingClient(cfg *ClientConfig) (*PositionKeepingGRPCClient, error) {
	if cfg.ServiceName == "" {
		return nil, ErrServiceNameRequired
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	// Create gRPC connection using shared platform client
	conn, err := platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		DialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	client := &PositionKeepingGRPCClient{
		conn:           conn,
		client:         positionkeepingv1.NewPositionKeepingServiceClient(conn),
		timeout:        cfg.Timeout,
		logger:         cfg.Logger,
		simulationMode: cfg.SimulationMode,
	}

	if client.simulationMode {
		cfg.Logger.Warn("Position Keeping client running in SIMULATION mode - measurements will be logged but not sent")
	}

	return client, nil
}

// RecordMeasurement sends a utilization measurement to Position Keeping.
//
// TODO: This is currently in SIMULATION mode because the Position Keeping service
// does not yet have a RecordMeasurement endpoint. When the endpoint is added to
// api/proto/meridian/position_keeping/v1/position_keeping.proto, update this method to:
//
//  1. Create RecordMeasurementRequest proto message
//  2. Call client.RecordMeasurement(ctx, req)
//  3. Handle response and errors
//  4. Remove simulation mode flag
//
// Expected proto structure (to be added):
//
//	message RecordMeasurementRequest {
//	  string account_id = 1;
//	  string asset_code = 2;
//	  string quantity = 3;
//	  google.protobuf.Timestamp period_start = 4;
//	  google.protobuf.Timestamp period_end = 5;
//	  string source = 6;
//	  double quality_score = 7;
//	  map<string, string> attributes = 8;
//	}
func (c *PositionKeepingGRPCClient) RecordMeasurement(ctx context.Context, measurement *auditdomain.Measurement) error {
	// Apply timeout
	ctx, cancel := sharedclients.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Propagate context metadata (will be used when real gRPC call is implemented)
	ctx = sharedclients.PropagateCorrelationID(ctx)
	ctx = sharedclients.PropagateOrganization(ctx)
	_ = ctx // Suppress linter warning until real gRPC call is implemented (see TODO below)

	if c.simulationMode {
		// Simulation mode: log what would be sent
		c.logger.Info("SIMULATION: would record measurement to Position Keeping",
			"measurement_id", measurement.ID,
			"account_id", measurement.AccountID,
			"asset_code", measurement.AssetCode,
			"quantity", measurement.Quantity,
			"period", measurement.Period,
			"source", measurement.Source,
			"quality_score", measurement.QualityScore,
			"attributes", measurement.Attributes,
		)
		return nil
	}

	// TODO: Real implementation when RecordMeasurement endpoint exists
	// This would convert measurement to proto and call:
	// req := &positionkeepingv1.RecordMeasurementRequest{
	//     AccountId:    measurement.TenantID,
	//     AssetCode:    measurement.UnitOfMeasure,
	//     Quantity:     fmt.Sprintf("%d", measurement.Quantity),
	//     PeriodStart:  timestamppb.New(measurement.Timestamp),
	//     PeriodEnd:    timestamppb.New(measurement.Timestamp),
	//     Source:       fmt.Sprintf("%s.%s", measurement.ServiceName, measurement.OperationType),
	//     QualityScore: 1.0,
	//     Attributes: map[string]string{
	//         "correlation_id": measurement.CorrelationID,
	//         "service":        measurement.ServiceName,
	//         "operation":      measurement.OperationType,
	//     },
	// }
	// resp, err := c.client.RecordMeasurement(ctx, req)
	// if err != nil {
	//     return fmt.Errorf("failed to record measurement: %w", err)
	// }

	return ErrRecordMeasurementNotImplemented
}

// Close releases the gRPC client connection.
func (c *PositionKeepingGRPCClient) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("failed to close position keeping client connection: %w", err)
		}
	}
	return nil
}
