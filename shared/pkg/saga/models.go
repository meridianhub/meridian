// Package saga provides saga orchestration runtime and persistence for durable execution.
// This package implements the Durable Execution Engine as specified in the PRD.
//
// The saga persistence schema is SERVICE-LOCAL: each service (Payment Order, Current Account, etc.)
// has its own saga_instances and saga_step_results tables. The schema definition is common,
// but the data is isolated per service.
package saga

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SagaStatus represents the lifecycle state of a saga instance.
// Status values are defined per PRD Section 3.1.
//
//nolint:revive // SagaStatus naming is intentional for clarity at call sites (saga.SagaStatus)
type SagaStatus string

const (
	// SagaStatusPending indicates the saga is created but not yet started.
	SagaStatusPending SagaStatus = "PENDING"
	// SagaStatusRunning indicates the saga is actively executing steps.
	SagaStatusRunning SagaStatus = "RUNNING"
	// SagaStatusWaitingForEvent indicates the saga is suspended waiting for external callback.
	SagaStatusWaitingForEvent SagaStatus = "WAITING_FOR_EVENT"
	// SagaStatusCompleted indicates the saga finished successfully.
	SagaStatusCompleted SagaStatus = "COMPLETED"
	// SagaStatusCompensating indicates the saga is executing compensation steps.
	SagaStatusCompensating SagaStatus = "COMPENSATING"
	// SagaStatusCompensated indicates compensation completed successfully.
	SagaStatusCompensated SagaStatus = "COMPENSATED"
	// SagaStatusFailed indicates the saga failed and compensation is not possible or completed.
	SagaStatusFailed SagaStatus = "FAILED"
	// SagaStatusFailedManualIntervention indicates the saga requires operator intervention.
	SagaStatusFailedManualIntervention SagaStatus = "FAILED_MANUAL_INTERVENTION"
	// SagaStatusSuspended indicates the saga is temporarily suspended (e.g., waiting for async).
	SagaStatusSuspended SagaStatus = "SUSPENDED"
)

// StepStatus represents the execution status of a saga step.
type StepStatus string

const (
	// StepStatusCompleted indicates the step executed successfully.
	StepStatusCompleted StepStatus = "COMPLETED"
	// StepStatusFailed indicates the step execution failed.
	StepStatusFailed StepStatus = "FAILED"
)

// ErrorCategory classifies step execution failures per FR-28.
type ErrorCategory string

const (
	// ErrorCategoryTransient indicates a retryable failure (network timeout, temporary unavailability).
	ErrorCategoryTransient ErrorCategory = "TRANSIENT"
	// ErrorCategoryFatal indicates a non-retryable failure (business rule violation, validation error).
	ErrorCategoryFatal ErrorCategory = "FATAL"
)

// SagaInstance represents the execution state of a running saga.
// This entity is stored in each service's database per the service-local pattern.
//
// Per PRD Section 3.1:
// - saga_definitions: Reference Data (shared, cached globally)
// - saga_instances: Each service's schema (execution state is service-local)
// - saga_step_results: Each service's schema (step results are service-local)
//
//nolint:revive // SagaInstance naming is intentional for GORM entity clarity
type SagaInstance struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Saga definition reference
	// Note: Cross-service reference to Reference Data's saga_definitions table
	SagaDefinitionID uuid.UUID `gorm:"type:uuid;not null;index"`
	SagaName         string    `gorm:"column:saga_name;type:varchar(64)"`
	SagaVersion      int       `gorm:"column:saga_version"`

	// ScriptHashAtStart is the SHA-256 hash of the script at saga start time.
	// Used during replay to detect script corruption or unexpected changes.
	ScriptHashAtStart string `gorm:"column:script_hash_at_start;type:varchar(64)"`

	// Input and context (for replay)
	// InputSnapshot stores the original input parameters as JSONB for deterministic replay
	InputSnapshot JSONB     `gorm:"column:input_snapshot;type:jsonb"`
	PartyID       uuid.UUID `gorm:"type:uuid;index"`
	// KnowledgeAt enables bi-temporal replay: what we knew at a specific point in time
	KnowledgeAt *time.Time `gorm:"column:knowledge_at"`

	// Tracing (FR-17, FR-24, FR-32)
	// CorrelationID groups ALL related actions across the entire business operation
	CorrelationID uuid.UUID `gorm:"type:uuid;not null;index"`
	// CausationID links cause->effect within saga (parent saga/step that triggered this)
	CausationID *uuid.UUID `gorm:"type:uuid"`
	// ParentSagaID enables causation tree visualization (FR-32)
	ParentSagaID *uuid.UUID `gorm:"type:uuid;index"`
	// ParentStepIndex indicates which step in parent invoked this child saga
	ParentStepIndex *int `gorm:"column:parent_step_index"`

	// Async/Wait (FR-30)
	// SuspendReason stores the reason/data for suspension
	SuspendReason *string `gorm:"column:suspend_reason;type:text"`
	// SuspendData stores additional data for suspended sagas
	SuspendData JSONB `gorm:"column:suspend_data;type:jsonb"`

	// Ownership (race condition prevention via leases)
	// ClaimedByPod identifies which pod currently owns this saga execution
	ClaimedByPod *string `gorm:"column:claimed_by_pod;type:varchar(128)"`
	// ClaimedAt records when the current pod claimed this saga
	ClaimedAt *time.Time `gorm:"column:claimed_at"`
	// LeaseExpiresAt is the deadline after which another pod can claim this saga
	LeaseExpiresAt *time.Time `gorm:"column:lease_expires_at;index"`

	// Progress
	// CurrentStepIndex tracks execution progress for replay
	CurrentStepIndex int `gorm:"column:current_step_index;not null;default:0"`
	// ReplayCount tracks how many times this saga has been replayed (for zombie detection)
	ReplayCount int `gorm:"column:replay_count;not null;default:0"`
	// Status is the current lifecycle state
	Status SagaStatus `gorm:"column:status;type:varchar(32);not null;default:'PENDING';index"`

	// Timestamps
	CreatedAt   time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null;default:now()"`
	StartedAt   *time.Time `gorm:"column:started_at"`
	CompletedAt *time.Time `gorm:"column:completed_at"`

	// Error context
	ErrorMessage    *string `gorm:"column:error_message;type:text"`
	ErrorCategory   *string `gorm:"column:error_category;type:varchar(16)"` // TRANSIENT, FATAL
	FailedStepIndex *int    `gorm:"column:failed_step_index"`

	// Relationships
	StepResults []SagaStepResult `gorm:"foreignKey:SagaInstanceID;constraint:OnDelete:CASCADE"`
}

// TableName returns the table name for the SagaInstance entity.
// Uses singular, unqualified name per database-per-service architecture.
func (SagaInstance) TableName() string {
	return "saga_instances"
}

// SagaStepResult represents the cached result of a completed saga step.
// Step results are critical for replay-based recovery: when a saga is replayed,
// completed steps return their cached results instead of re-executing.
//
//nolint:revive // SagaStepResult naming is intentional for GORM entity clarity
type SagaStepResult struct {
	// Primary key
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Foreign key to saga_instances with cascade delete
	SagaInstanceID uuid.UUID `gorm:"type:uuid;not null;index"`

	// Step identification
	// StepIndex is the position in the saga execution sequence
	StepIndex int `gorm:"column:step_index;not null"`
	// StepName is the human-readable step identifier (optional, for debugging)
	StepName string `gorm:"column:step_name;type:varchar(64)"`

	// Idempotency (critical for replay safety)
	// IdempotencyKey format: saga_{instance_id}_step_{index}
	// This key is passed to downstream services to prevent duplicate processing
	IdempotencyKey string `gorm:"column:idempotency_key;type:varchar(128);not null;uniqueIndex"`

	// Result (for replay - skip re-execution)
	// Result stores the step handler's output as JSONB
	Result JSONB `gorm:"column:result;type:jsonb"`
	// Error stores error details if the step failed
	Error *string `gorm:"column:error;type:text"`
	// Status indicates whether the step completed successfully or failed
	Status StepStatus `gorm:"column:status;type:varchar(16);not null"`

	// Error classification (FR-28)
	// ErrorCategory distinguishes TRANSIENT (retryable) from FATAL (non-retryable) errors
	ErrorCategory *string `gorm:"column:error_category;type:varchar(16)"`

	// Causation linkage
	// CausationID links this step to its parent for audit trail
	CausationID *uuid.UUID `gorm:"type:uuid"`

	// Timestamps
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName returns the table name for the SagaStepResult entity.
func (SagaStepResult) TableName() string {
	return "saga_step_results"
}

// JSONB is a custom type for handling JSONB columns in PostgreSQL.
// It provides proper Value/Scan implementations for GORM.
type JSONB map[string]interface{}

// Value implements driver.Valuer for database writes.
// For nil JSONB, returns empty JSON object "{}".
func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(j)
}

// Scan implements sql.Scanner for database reads.
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("%w: got %T", ErrUnsupportedJSONBType, value)
	}

	return json.Unmarshal(bytes, j)
}

// ErrInvalidSagaStatus is returned when an invalid saga status value is encountered.
var ErrInvalidSagaStatus = errors.New("invalid saga status")

// ErrUnsupportedJSONBType is returned when scanning an unsupported type into JSONB.
var ErrUnsupportedJSONBType = errors.New("cannot scan type into JSONB")

// ValidSagaStatuses returns all valid saga status values.
func ValidSagaStatuses() []SagaStatus {
	return []SagaStatus{
		SagaStatusPending,
		SagaStatusRunning,
		SagaStatusWaitingForEvent,
		SagaStatusCompleted,
		SagaStatusCompensating,
		SagaStatusCompensated,
		SagaStatusFailed,
		SagaStatusFailedManualIntervention,
		SagaStatusSuspended,
	}
}

// IsValidSagaStatus checks if a status value is valid.
func IsValidSagaStatus(status SagaStatus) bool {
	for _, valid := range ValidSagaStatuses() {
		if status == valid {
			return true
		}
	}
	return false
}
