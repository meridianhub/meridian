// Package domain contains the core domain models for the event-router service.
package domain

import "context"

// SagaTrigger defines the port for triggering saga workflows.
// Implementations connect to the control-plane's SagaExecutionService via gRPC.
type SagaTrigger interface {
	// TriggerSaga starts a new saga instance by name with the given input data.
	// The idempotencyKey ensures duplicate triggers (e.g., Kafka redelivery) are
	// handled safely — the existing saga_id is returned without re-executing.
	// Returns the saga instance ID on success.
	TriggerSaga(ctx context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error)

	// Close releases any resources held by the client (e.g., gRPC connections).
	Close() error
}
