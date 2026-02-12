package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"gorm.io/gorm"
)

// sagaExecutionEntity is the GORM entity for the saga_executions table.
type sagaExecutionEntity struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	PaymentOrderID uuid.UUID  `gorm:"type:uuid;not null"`
	SagaName       string     `gorm:"column:saga_name;type:varchar(128);not null"`
	SagaVersion    int        `gorm:"column:saga_version;not null;default:0"`
	Status         string     `gorm:"column:status;type:varchar(32);not null;default:'RUNNING'"`
	CorrelationID  string     `gorm:"column:correlation_id;type:varchar(128);not null;default:''"`
	Input          []byte     `gorm:"column:input;type:jsonb;not null;default:'{}'"`
	Output         []byte     `gorm:"column:output;type:jsonb;not null;default:'{}'"`
	ErrorMessage   string     `gorm:"column:error_message;type:text;not null;default:''"`
	StepCount      int        `gorm:"column:step_count;not null;default:0"`
	DurationMs     int64      `gorm:"column:duration_ms;not null;default:0"`
	StartedAt      time.Time  `gorm:"column:started_at;not null;default:now()"`
	CompletedAt    *time.Time `gorm:"column:completed_at"`
}

func (sagaExecutionEntity) TableName() string {
	return "saga_executions"
}

// SagaExecutionRepository implements domain.SagaExecutionLogger using GORM.
type SagaExecutionRepository struct {
	db *gorm.DB
}

// NewSagaExecutionRepository creates a new SagaExecutionRepository.
func NewSagaExecutionRepository(db *gorm.DB) *SagaExecutionRepository {
	return &SagaExecutionRepository{db: db}
}

// PersistExecution creates or updates a saga execution record.
func (r *SagaExecutionRepository) PersistExecution(ctx context.Context, execution *domain.SagaExecution) error {
	if execution == nil {
		return fmt.Errorf("nil saga execution")
	}

	// Normalize nil maps to empty objects so json.Marshal produces "{}" not "null",
	// which would violate the NOT NULL JSONB column constraints.
	input := execution.Input
	if input == nil {
		input = map[string]any{}
	}
	output := execution.Output
	if output == nil {
		output = map[string]any{}
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal saga execution input: %w", err)
	}

	outputJSON, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal saga execution output: %w", err)
	}

	entity := sagaExecutionEntity{
		ID:             execution.ID,
		PaymentOrderID: execution.PaymentOrderID,
		SagaName:       execution.SagaName,
		SagaVersion:    execution.SagaVersion,
		Status:         string(execution.Status),
		CorrelationID:  execution.CorrelationID,
		Input:          inputJSON,
		Output:         outputJSON,
		ErrorMessage:   execution.ErrorMessage,
		StepCount:      execution.StepCount,
		DurationMs:     execution.DurationMs,
		StartedAt:      execution.StartedAt,
		CompletedAt:    execution.CompletedAt,
	}

	result := r.db.WithContext(ctx).Save(&entity)
	if result.Error != nil {
		return fmt.Errorf("failed to persist saga execution: %w", result.Error)
	}

	return nil
}
