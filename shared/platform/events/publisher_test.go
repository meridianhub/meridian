package events

import (
	"context"
	"errors"
	"testing"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Test errors for publisher tests.
var (
	errIntentionalPublisherRollback = errors.New("intentional rollback")
	errBusinessValidationFailed     = errors.New("business validation failed")
)

// testEvent is a simple protobuf message for testing.
// Using timestamppb.Timestamp as it's a standard protobuf message.
type testEvent = timestamppb.Timestamp

func newTestEvent() *testEvent {
	return timestamppb.Now()
}

func setupPublisherTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&EventOutbox{})
	require.NoError(t, err)

	return db
}

func TestNewOutboxPublisher(t *testing.T) {
	t.Run("creates publisher with valid service name", func(t *testing.T) {
		publisher := NewOutboxPublisher("my-service")
		assert.NotNil(t, publisher)
	})

	t.Run("panics on empty service name", func(t *testing.T) {
		assert.Panics(t, func() {
			NewOutboxPublisher("")
		})
	})
}

func TestOutboxPublisher_Publish(t *testing.T) {
	db := setupPublisherTestDB(t)
	publisher := NewOutboxPublisher("test-service")
	ctx := context.Background()

	t.Run("successful publish within transaction", func(t *testing.T) {
		event := newTestEvent()

		config := PublishConfig{
			EventType:     "test.event.created.v1",
			AggregateID:   "aggregate-123",
			AggregateType: "TestAggregate",
			Topic:         "test-topic",
			CorrelationID: "corr-123",
			CausationID:   "cause-456",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, config)
		})

		require.NoError(t, err)

		// Verify entry was persisted
		var entries []EventOutbox
		db.Find(&entries)
		require.Len(t, entries, 1)

		entry := entries[0]
		assert.Equal(t, "test.event.created.v1", entry.EventType)
		assert.Equal(t, "aggregate-123", entry.AggregateID)
		assert.Equal(t, "TestAggregate", entry.AggregateType)
		assert.Equal(t, "test-topic", entry.Topic)
		assert.Equal(t, "test-service", entry.ServiceName)
		assert.Equal(t, "corr-123", entry.CorrelationID)
		assert.Equal(t, "cause-456", entry.CausationID)
		assert.Equal(t, StatusPending, entry.Status)
		assert.Equal(t, "aggregate-123", entry.PartitionKey) // Default

		// Verify payload is serialized correctly
		var deserialized testEvent
		err = proto.Unmarshal(entry.EventPayload, &deserialized)
		require.NoError(t, err)
	})

	t.Run("uses custom partition key when provided", func(t *testing.T) {
		event := newTestEvent()

		config := PublishConfig{
			EventType:     "test.event.v1",
			AggregateID:   "agg-1",
			AggregateType: "Type",
			Topic:         "topic",
			PartitionKey:  "custom-key",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, config)
		})

		require.NoError(t, err)

		var entries []EventOutbox
		db.Where("aggregate_id = ?", "agg-1").Find(&entries)
		require.Len(t, entries, 1)
		assert.Equal(t, "custom-key", entries[0].PartitionKey)
	})

	t.Run("rollback removes entry", func(t *testing.T) {
		event := newTestEvent()

		config := PublishConfig{
			EventType:     "test.rollback.v1",
			AggregateID:   "agg-rollback",
			AggregateType: "Type",
			Topic:         "topic",
		}

		// Clear previous entries
		db.Where("1=1").Delete(&EventOutbox{})

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := publisher.Publish(ctx, tx, event, config); err != nil {
				return err
			}
			return errIntentionalPublisherRollback
		})

		require.Error(t, err)

		var count int64
		db.Model(&EventOutbox{}).Where("aggregate_id = ?", "agg-rollback").Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("returns error for nil transaction", func(t *testing.T) {
		event := newTestEvent()

		config := PublishConfig{
			EventType:     "test.event.v1",
			AggregateID:   "agg-1",
			AggregateType: "Type",
			Topic:         "topic",
		}

		err := publisher.Publish(ctx, nil, event, config)
		assert.ErrorIs(t, err, ErrNilTransaction)
	})

	t.Run("returns error for nil event", func(t *testing.T) {
		config := PublishConfig{
			EventType:     "test.event.v1",
			AggregateID:   "agg-1",
			AggregateType: "Type",
			Topic:         "topic",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, nil, config)
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "event cannot be nil")
	})

	t.Run("returns error for empty event type", func(t *testing.T) {
		event := newTestEvent()

		config := PublishConfig{
			AggregateID:   "agg-1",
			AggregateType: "Type",
			Topic:         "topic",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, config)
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidEventType)
	})

	t.Run("returns error for empty topic", func(t *testing.T) {
		event := newTestEvent()

		config := PublishConfig{
			EventType:     "test.event.v1",
			AggregateID:   "agg-1",
			AggregateType: "Type",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, config)
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "topic cannot be empty")
	})
}

func TestOutboxPublisher_PublishControlEvent(t *testing.T) {
	db := setupPublisherTestDB(t)
	publisher := NewOutboxPublisher("control-service")
	ctx := context.Background()

	t.Run("publishes control event successfully", func(t *testing.T) {
		event := newTestEvent()

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.PublishControlEvent(
				ctx,
				tx,
				event,
				"position_keeping.transaction_suspended.v1",
				"log-123",
				"FinancialPositionLog",
				"position-keeping.control-events.v1",
				"correlation-789",
			)
		})

		require.NoError(t, err)

		var entries []EventOutbox
		db.Where("aggregate_id = ?", "log-123").Find(&entries)
		require.Len(t, entries, 1)

		entry := entries[0]
		assert.Equal(t, "position_keeping.transaction_suspended.v1", entry.EventType)
		assert.Equal(t, "log-123", entry.AggregateID)
		assert.Equal(t, "FinancialPositionLog", entry.AggregateType)
		assert.Equal(t, "position-keeping.control-events.v1", entry.Topic)
		assert.Equal(t, "control-service", entry.ServiceName)
		assert.Equal(t, "correlation-789", entry.CorrelationID)
	})
}

func TestOutboxPublisher_AtomicWithBusinessOperation(t *testing.T) {
	db := setupPublisherTestDB(t)
	publisher := NewOutboxPublisher("test-service")
	ctx := context.Background()

	// Create a simple table for the "business operation"
	type BusinessEntity struct {
		ID     string `gorm:"primaryKey"`
		Status string
	}
	db.AutoMigrate(&BusinessEntity{})

	t.Run("both business operation and event succeed atomically", func(t *testing.T) {
		event := newTestEvent()

		err := db.Transaction(func(tx *gorm.DB) error {
			// Business operation
			entity := &BusinessEntity{ID: "entity-1", Status: "suspended"}
			if err := tx.Create(entity).Error; err != nil {
				return err
			}

			// Event publication
			return publisher.Publish(ctx, tx, event, PublishConfig{
				EventType:     "entity.suspended.v1",
				AggregateID:   "entity-1",
				AggregateType: "BusinessEntity",
				Topic:         "entity-events",
			})
		})

		require.NoError(t, err)

		// Both should exist
		var entity BusinessEntity
		db.First(&entity, "id = ?", "entity-1")
		assert.Equal(t, "suspended", entity.Status)

		var outbox EventOutbox
		db.First(&outbox, "aggregate_id = ?", "entity-1")
		assert.Equal(t, "entity.suspended.v1", outbox.EventType)
	})

	t.Run("event failure rolls back business operation", func(t *testing.T) {
		// Clear tables
		db.Where("1=1").Delete(&BusinessEntity{})
		db.Where("1=1").Delete(&EventOutbox{})

		err := db.Transaction(func(tx *gorm.DB) error {
			// Business operation
			entity := &BusinessEntity{ID: "entity-2", Status: "terminated"}
			if err := tx.Create(entity).Error; err != nil {
				return err
			}

			// Event publication fails (empty event type)
			return publisher.Publish(ctx, tx, newTestEvent(), PublishConfig{
				AggregateID:   "entity-2",
				AggregateType: "BusinessEntity",
				Topic:         "entity-events",
				// Missing EventType - will fail
			})
		})

		require.Error(t, err)

		// Neither should exist
		var entityCount int64
		db.Model(&BusinessEntity{}).Where("id = ?", "entity-2").Count(&entityCount)
		assert.Equal(t, int64(0), entityCount)

		var outboxCount int64
		db.Model(&EventOutbox{}).Where("aggregate_id = ?", "entity-2").Count(&outboxCount)
		assert.Equal(t, int64(0), outboxCount)
	})

	t.Run("business failure rolls back event", func(t *testing.T) {
		// Clear tables
		db.Where("1=1").Delete(&BusinessEntity{})
		db.Where("1=1").Delete(&EventOutbox{})

		err := db.Transaction(func(tx *gorm.DB) error {
			// Event publication first
			if err := publisher.Publish(ctx, tx, newTestEvent(), PublishConfig{
				EventType:     "entity.created.v1",
				AggregateID:   "entity-3",
				AggregateType: "BusinessEntity",
				Topic:         "entity-events",
			}); err != nil {
				return err
			}

			// Business operation fails
			return errBusinessValidationFailed
		})

		require.Error(t, err)

		// Neither should exist
		var outboxCount int64
		db.Model(&EventOutbox{}).Where("aggregate_id = ?", "entity-3").Count(&outboxCount)
		assert.Equal(t, int64(0), outboxCount)
	})
}

// validTransactionCapturedEvent returns a TransactionCapturedEvent that satisfies all
// buf/validate constraints defined in position_keeping_events.proto.
func validTransactionCapturedEvent() *eventsv1.TransactionCapturedEvent {
	return &eventsv1.TransactionCapturedEvent{
		LogId:          "550e8400-e29b-41d4-a716-446655440000",
		AccountId:      "account-123",
		TransactionId:  "550e8400-e29b-41d4-a716-446655440001",
		AmountCents:    100,
		InstrumentCode: "GBP",
		Direction:      "DEBIT",
		Source:         "MANUAL",
		CorrelationId:  "corr-123",
		Timestamp:      timestamppb.Now(),
		Version:        1,
	}
}

func TestOutboxPublisher_ProtovalidateEnforcement(t *testing.T) {
	db := setupPublisherTestDB(t)
	publisher := NewOutboxPublisher("test-service")
	ctx := context.Background()

	baseConfig := PublishConfig{
		EventType:     "events.transaction_captured.v1",
		AggregateID:   "550e8400-e29b-41d4-a716-446655440000",
		AggregateType: "FinancialPositionLog",
		Topic:         "position-keeping.events.v1",
		CorrelationID: "corr-123",
	}

	t.Run("valid event passes validation and is written to outbox", func(t *testing.T) {
		// Clear outbox before test
		db.Where("1=1").Delete(&EventOutbox{})

		event := validTransactionCapturedEvent()

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, baseConfig)
		})

		require.NoError(t, err)

		var count int64
		db.Model(&EventOutbox{}).Count(&count)
		assert.Equal(t, int64(1), count, "valid event should be written to outbox")
	})

	t.Run("invalid UUID field is rejected before outbox insert", func(t *testing.T) {
		// Clear outbox before test
		db.Where("1=1").Delete(&EventOutbox{})

		event := validTransactionCapturedEvent()
		event.LogId = "not-a-uuid" // violates (buf.validate.field).string.uuid = true

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, baseConfig)
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "event payload validation failed")

		// Verify nothing was written to outbox
		var count int64
		db.Model(&EventOutbox{}).Count(&count)
		assert.Equal(t, int64(0), count, "invalid event must not enter the outbox")
	})

	t.Run("required field missing is rejected before outbox insert", func(t *testing.T) {
		// Clear outbox before test
		db.Where("1=1").Delete(&EventOutbox{})

		event := validTransactionCapturedEvent()
		event.Timestamp = nil // violates (buf.validate.field).required = true

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, baseConfig)
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "event payload validation failed")

		var count int64
		db.Model(&EventOutbox{}).Count(&count)
		assert.Equal(t, int64(0), count, "event with missing required field must not enter the outbox")
	})

	t.Run("invalid enum value is rejected before outbox insert", func(t *testing.T) {
		// Clear outbox before test
		db.Where("1=1").Delete(&EventOutbox{})

		event := validTransactionCapturedEvent()
		event.Direction = "SIDEWAYS" // violates (buf.validate.field).string.in constraint

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, baseConfig)
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "event payload validation failed")

		var count int64
		db.Model(&EventOutbox{}).Count(&count)
		assert.Equal(t, int64(0), count, "event with invalid enum value must not enter the outbox")
	})

	t.Run("validation error rolls back business operation atomically", func(t *testing.T) {
		// Create a simple business entity table
		type BizEntity struct {
			ID string `gorm:"primaryKey"`
		}
		db.AutoMigrate(&BizEntity{})
		db.Where("1=1").Delete(&BizEntity{})
		db.Where("1=1").Delete(&EventOutbox{})

		event := validTransactionCapturedEvent()
		event.LogId = "bad-uuid" // will fail validation

		err := db.Transaction(func(tx *gorm.DB) error {
			// Business operation first
			if err := tx.Create(&BizEntity{ID: "entity-val-test"}).Error; err != nil {
				return err
			}
			return publisher.Publish(ctx, tx, event, baseConfig)
		})

		require.Error(t, err)

		// Business entity must also be rolled back
		var entityCount int64
		db.Model(&BizEntity{}).Where("id = ?", "entity-val-test").Count(&entityCount)
		assert.Equal(t, int64(0), entityCount, "business operation must be rolled back on validation failure")
	})
}
