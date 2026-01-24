// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

// ErrSagaInstanceNotFound is returned when a saga instance cannot be found.
var ErrSagaInstanceNotFound = errors.New("saga instance not found")

// GormTxContextWithOutbox implements TxContextWithOutbox using GORM.
// It provides transactional step result persistence with atomic outbox event writing.
type GormTxContextWithOutbox struct {
	tx          *gorm.DB
	serviceName string
}

// NewGormTxContextWithOutbox creates a new GORM-based transaction context with outbox support.
func NewGormTxContextWithOutbox(tx *gorm.DB, serviceName string) *GormTxContextWithOutbox {
	return &GormTxContextWithOutbox{
		tx:          tx,
		serviceName: serviceName,
	}
}

// SaveStepResult persists a step result within the transaction.
func (t *GormTxContextWithOutbox) SaveStepResult(ctx context.Context, result *SagaStepResult) error {
	if err := t.tx.WithContext(ctx).Create(result).Error; err != nil {
		return fmt.Errorf("failed to save step result: %w", err)
	}
	return nil
}

// UpdateStepIndex updates the saga instance's current step index within the transaction.
func (t *GormTxContextWithOutbox) UpdateStepIndex(ctx context.Context, instanceID uuid.UUID, stepIndex int) error {
	result := t.tx.WithContext(ctx).
		Model(&SagaInstance{}).
		Where("id = ?", instanceID).
		Updates(map[string]interface{}{
			"current_step_index": stepIndex,
			"updated_at":         time.Now(),
		})

	if result.Error != nil {
		return fmt.Errorf("failed to update step index: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrSagaInstanceNotFound
	}
	return nil
}

// WriteOutboxEntry writes a saga event to the event_outbox table within this transaction.
// This uses the same outbox table as the platform events package, allowing the existing
// events.Worker to process and publish saga events to Kafka.
func (t *GormTxContextWithOutbox) WriteOutboxEntry(ctx context.Context, entry *OutboxEntry) error {
	// Convert saga OutboxEntry to platform EventOutbox
	// This allows reuse of the existing outbox worker infrastructure
	platformEntry := &events.EventOutbox{
		ID:            entry.ID,
		EventType:     entry.EventType,
		AggregateID:   entry.AggregateID,
		AggregateType: entry.AggregateType,
		EventPayload:  entry.EventPayload,
		CorrelationID: entry.CorrelationID,
		CausationID:   entry.CausationID,
		Status:        events.StatusPending,
		Topic:         entry.Topic,
		PartitionKey:  entry.AggregateID, // Use aggregate ID as partition key
		CreatedAt:     time.Now(),
		RetryCount:    0,
		ServiceName:   t.serviceName,
	}

	if err := t.tx.WithContext(ctx).Create(platformEntry).Error; err != nil {
		return fmt.Errorf("failed to write outbox entry: %w", err)
	}
	return nil
}

// Commit commits the transaction.
func (t *GormTxContextWithOutbox) Commit() error {
	return t.tx.Commit().Error
}

// Rollback aborts the transaction.
func (t *GormTxContextWithOutbox) Rollback() error {
	return t.tx.Rollback().Error
}

// Compile-time interface verification
var _ TxContextWithOutbox = (*GormTxContextWithOutbox)(nil)

// GormTransactionalRepositoryWithOutbox implements TransactionalRepositoryWithOutbox using GORM.
type GormTransactionalRepositoryWithOutbox struct {
	db          *gorm.DB
	serviceName string
}

// NewGormTransactionalRepositoryWithOutbox creates a new GORM-based transactional repository.
func NewGormTransactionalRepositoryWithOutbox(db *gorm.DB, serviceName string) *GormTransactionalRepositoryWithOutbox {
	return &GormTransactionalRepositoryWithOutbox{
		db:          db,
		serviceName: serviceName,
	}
}

// BeginTxWithOutbox starts a new database transaction with outbox writing capability.
func (r *GormTransactionalRepositoryWithOutbox) BeginTxWithOutbox(ctx context.Context) (TxContextWithOutbox, error) {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	return NewGormTxContextWithOutbox(tx, r.serviceName), nil
}

// Compile-time interface verification
var _ TransactionalRepositoryWithOutbox = (*GormTransactionalRepositoryWithOutbox)(nil)
