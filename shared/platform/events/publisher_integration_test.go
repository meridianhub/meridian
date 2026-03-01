package events

import (
	"context"
	"testing"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// TestOutboxPublisher_ProtovalidateIntegration verifies protovalidate enforcement at the
// outbox boundary using a real CockroachDB instance (production parity).
func TestOutboxPublisher_ProtovalidateIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := testdb.SetupCockroachDB(t, []interface{}{&EventOutbox{}})
	defer cleanup()

	publisher := NewOutboxPublisher("integration-test-service")
	ctx := context.Background()

	baseConfig := PublishConfig{
		EventType:     "events.transaction_captured.v1",
		AggregateID:   "550e8400-e29b-41d4-a716-446655440000",
		AggregateType: "FinancialPositionLog",
		Topic:         "position-keeping.events.v1",
		CorrelationID: "integ-corr-123",
	}

	validEvent := func() *eventsv1.TransactionCapturedEvent {
		return &eventsv1.TransactionCapturedEvent{
			LogId:          "550e8400-e29b-41d4-a716-446655440000",
			AccountId:      "account-123",
			TransactionId:  "550e8400-e29b-41d4-a716-446655440001",
			AmountCents:    100,
			InstrumentCode: "GBP",
			Direction:      "DEBIT",
			Source:         "MANUAL",
			CorrelationId:  "integ-corr-123",
			Timestamp:      timestamppb.Now(),
			Version:        1,
		}
	}

	t.Run("valid TransactionCapturedEvent is written to CockroachDB outbox", func(t *testing.T) {
		db.Where("1=1").Delete(&EventOutbox{})

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, validEvent(), baseConfig)
		})

		require.NoError(t, err)

		var count int64
		db.Model(&EventOutbox{}).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("invalid UUID rejected and no entry written to CockroachDB outbox", func(t *testing.T) {
		db.Where("1=1").Delete(&EventOutbox{})

		event := validEvent()
		event.LogId = "not-a-valid-uuid"

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, baseConfig)
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "event payload validation failed")

		var count int64
		db.Model(&EventOutbox{}).Count(&count)
		assert.Equal(t, int64(0), count, "no outbox entry must exist after validation failure")
	})

	t.Run("missing required timestamp rejected and no entry written to CockroachDB outbox", func(t *testing.T) {
		db.Where("1=1").Delete(&EventOutbox{})

		event := validEvent()
		event.Timestamp = nil

		err := db.Transaction(func(tx *gorm.DB) error {
			return publisher.Publish(ctx, tx, event, baseConfig)
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "event payload validation failed")

		var count int64
		db.Model(&EventOutbox{}).Count(&count)
		assert.Equal(t, int64(0), count, "no outbox entry must exist after validation failure")
	})
}
