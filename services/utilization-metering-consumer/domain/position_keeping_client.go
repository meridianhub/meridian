// Package domain contains the core domain models for utilization metering.
package domain

import (
	"context"

	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
)

// PositionKeepingClient defines the interface for communicating with the Position Keeping service.
// This will be implemented in the adapters/grpc package.
type PositionKeepingClient interface {
	// RecordMeasurement sends a utilization measurement to Position Keeping
	// to be recorded as a position change for tenant-zero billing.
	RecordMeasurement(ctx context.Context, measurement *auditdomain.Measurement) error

	// Close releases any resources held by the client
	Close() error
}
