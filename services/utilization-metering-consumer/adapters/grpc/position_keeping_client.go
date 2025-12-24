// Package grpc provides gRPC client adapters for external service communication.
package grpc

import (
	"context"

	"github.com/meridianhub/meridian/services/utilization-metering-consumer/domain"
)

// PositionKeepingGRPCClient implements domain.PositionKeepingClient using gRPC.
type PositionKeepingGRPCClient struct {
	// TODO: Add gRPC client connection fields
}

// NewPositionKeepingClient creates a new Position Keeping gRPC client.
// TODO: Implement in subsequent subtask (18.3)
func NewPositionKeepingClient(_ string) (*PositionKeepingGRPCClient, error) {
	// Stub implementation - will be filled in subtask 18.3
	return &PositionKeepingGRPCClient{}, nil
}

// RecordMeasurement sends a utilization measurement to Position Keeping.
// TODO: Implement in subsequent subtask (18.3)
func (c *PositionKeepingGRPCClient) RecordMeasurement(_ context.Context, _ *domain.UtilizationMeasurement) error {
	// Stub implementation - will be filled in subtask 18.3
	return nil
}

// Close releases the gRPC client connection.
// TODO: Implement in subsequent subtask (18.3)
func (c *PositionKeepingGRPCClient) Close() error {
	// Stub implementation - will be filled in subtask 18.3
	return nil
}
