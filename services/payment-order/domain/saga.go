// Package domain contains the PaymentOrder aggregate root and related domain logic
// for orchestrating payment saga workflows.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SagaExecutionStatus represents the lifecycle state of a saga execution record.
type SagaExecutionStatus string

const (
	// SagaExecutionStatusRunning indicates the saga is currently executing.
	SagaExecutionStatusRunning SagaExecutionStatus = "RUNNING"
	// SagaExecutionStatusCompleted indicates the saga completed successfully.
	SagaExecutionStatusCompleted SagaExecutionStatus = "COMPLETED"
	// SagaExecutionStatusFailed indicates the saga failed.
	SagaExecutionStatusFailed SagaExecutionStatus = "FAILED"
	// SagaExecutionStatusCompensated indicates the saga failed and compensation ran.
	SagaExecutionStatusCompensated SagaExecutionStatus = "COMPENSATED"
)

// SagaExecution represents a record of a saga execution in the payment-order service.
type SagaExecution struct {
	ID             uuid.UUID
	PaymentOrderID uuid.UUID
	SagaName       string
	SagaVersion    int
	Status         SagaExecutionStatus
	CorrelationID  string
	Input          map[string]any
	Output         map[string]any
	ErrorMessage   string
	StepCount      int
	DurationMs     int64
	StartedAt      time.Time
	CompletedAt    *time.Time
}

// SagaExecutionLogger persists saga execution records for audit and debugging.
type SagaExecutionLogger interface {
	// PersistExecution creates or updates a saga execution record.
	PersistExecution(ctx context.Context, execution *SagaExecution) error
}

// SagaDefinitionRepository loads saga definitions for execution.
type SagaDefinitionRepository interface {
	// LoadSagaDefinition fetches a saga script by name and version.
	// If version is 0, the ACTIVE version is returned.
	LoadSagaDefinition(ctx context.Context, sagaName string, version int) (sagaScript string, sagaVersion int, err error)
}
