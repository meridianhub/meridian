package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/messaging"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB creates an in-memory SQLite database with the event_outbox table.
// Used for testing that outbox entries are written correctly.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	require.NoError(t, db.AutoMigrate(&events.EventOutbox{}))
	return db
}

func newTestInstruction(t *testing.T) *domain.Instruction {
	t.Helper()
	instr, err := domain.NewInstruction(
		uuid.New(),
		"payment.initiate",
		uuid.New().String(),
		map[string]any{"amount": 100.0},
		domain.WithCorrelationID(uuid.New().String()),
		domain.WithCausationID(uuid.New().String()),
	)
	require.NoError(t, err)
	instr.ID = uuid.New()
	instr.Version = 1
	return instr
}

func TestInstructionEventPublisher_PublishCreated(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishCreated(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "operational_gateway.instruction_created.v1", entry.EventType)
	assert.Equal(t, "operational-gateway.instruction-created.v1", entry.Topic)
	assert.Equal(t, "Instruction", entry.AggregateType)
	assert.Equal(t, instr.ID.String(), entry.AggregateID)
	assert.Equal(t, instr.CorrelationID, entry.CorrelationID)
	assert.Equal(t, events.StatusPending, entry.Status)
	assert.Equal(t, "operational-gateway", entry.ServiceName)
	assert.NotEmpty(t, entry.EventPayload)
}

func TestInstructionEventPublisher_PublishDispatched(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)
	instr.AttemptCount = 1

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishDispatched(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	require.Len(t, entries, 1)

	assert.Equal(t, "operational_gateway.instruction_dispatched.v1", entries[0].EventType)
	assert.Equal(t, "operational-gateway.instruction-dispatched.v1", entries[0].Topic)
}

func TestInstructionEventPublisher_PublishDelivered(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishDelivered(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	assert.Equal(t, "operational_gateway.instruction_delivered.v1", entries[0].EventType)
}

func TestInstructionEventPublisher_PublishAcknowledged(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishAcknowledged(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	assert.Equal(t, "operational_gateway.instruction_acknowledged.v1", entries[0].EventType)
}

func TestInstructionEventPublisher_PublishFailed(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)
	instr.FailureReason = "connection timeout"
	instr.ErrorCode = "TIMEOUT"
	instr.AttemptCount = 3

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishFailed(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	assert.Equal(t, "operational_gateway.instruction_failed.v1", entries[0].EventType)
}

func TestInstructionEventPublisher_PublishExpired(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)
	expiry := time.Now().Add(-time.Minute)
	instr.ExpiresAt = &expiry

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishExpired(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	assert.Equal(t, "operational_gateway.instruction_expired.v1", entries[0].EventType)
}

func TestInstructionEventPublisher_PublishCancelled(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishCancelled(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	assert.Equal(t, "operational_gateway.instruction_cancelled.v1", entries[0].EventType)
}

func TestInstructionEventPublisher_RollbackOnError(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)

	// Simulate a transaction that writes the event but then fails (rollback)
	_ = db.Transaction(func(tx *gorm.DB) error {
		if err := ep.PublishCreated(context.Background(), tx, instr); err != nil {
			return err
		}
		// Force rollback
		return assert.AnError
	})

	// The event should NOT be in the outbox since the transaction was rolled back
	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	assert.Empty(t, entries, "event outbox should be empty after rollback")
}

func TestInstructionEventPublisher_PartitionKeyIsInstructionID(t *testing.T) {
	db := setupTestDB(t)
	publisher := events.NewOutboxPublisher("operational-gateway")
	ep := messaging.NewInstructionEventPublisher(publisher)
	instr := newTestInstruction(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		return ep.PublishCreated(context.Background(), tx, instr)
	})
	require.NoError(t, err)

	var entries []events.EventOutbox
	require.NoError(t, db.Find(&entries).Error)
	require.Len(t, entries, 1)

	// Partition key should be the instruction ID for ordered delivery per instruction
	assert.Equal(t, instr.ID.String(), entries[0].PartitionKey)
}
