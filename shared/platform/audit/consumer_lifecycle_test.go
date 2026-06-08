package audit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- Consumer.Stop / Consumer.Close lifecycle ---
//
// These exercise the lifecycle methods on a Consumer whose Kafka consumer and
// DLQ producer are nil. Both methods nil-guard those fields, so they run the
// cancel/wait paths without requiring a live Kafka broker. The Kafka-dependent
// constructors (buildDLQProducer, buildKafkaConsumer) and Start cannot be
// covered without a broker and have no injectable seam — see the report.

func TestConsumer_Stop_NilConsumer_CancelsContext(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)

	c.Stop()

	// After Stop, the consumer context must be cancelled.
	require.Error(t, c.ctx.Err())
	assert.ErrorIs(t, c.ctx.Err(), context.Canceled)
}

func TestConsumer_Close_NilConsumer_NoError(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)

	err := c.Close()
	require.NoError(t, err)

	// Close calls Stop internally, so the context is cancelled.
	assert.ErrorIs(t, c.ctx.Err(), context.Canceled)
}

func TestConsumer_Stop_Idempotent_WaitGroupCompletes(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)

	// Calling Stop twice must not panic and must leave the context cancelled.
	c.Stop()
	c.Stop()

	// await confirms the context has settled into the cancelled state.
	err := await.Until(func() bool {
		return c.ctx.Err() != nil
	})
	require.NoError(t, err)
	assert.ErrorIs(t, c.ctx.Err(), context.Canceled)
}

// --- handleMessage DB-insert failure path ---

// TestHandleMessage_InsertFailure_ReturnsError drops the audit_log table so the
// Create call inside the transaction fails, covering the error branch in
// handleMessage (RecordKafkaConsumed failure + wrapped error).
func TestHandleMessage_InsertFailure_ReturnsError(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	require.NoError(t, db.Exec(`DROP TABLE audit_log`).Error)

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName: "orders",
		RecordId:  "order-fail",
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to insert audit log")
}

// --- ProcessOutboxFallback failure path (marks entry as failed) ---

// TestProcessOutboxFallback_InsertFailure_MarksEntryFailed drops the audit_log
// table so processOutboxEntry's Create fails. ProcessOutboxFallback should then
// mark the outbox entry as 'failed' and increment retry_count, covering the
// error-handling branch.
func TestProcessOutboxFallback_InsertFailure_MarksEntryFailed(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	entryID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO audit_outbox (id, table_name, operation, record_id, status, created_at, retry_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entryID.String(), "orders", "INSERT", "order-1", "pending", time.Now(), 0,
	).Error)

	// Drop audit_log so the insert inside processOutboxEntry fails.
	require.NoError(t, db.Exec(`DROP TABLE audit_log`).Error)

	processed, err := c.ProcessOutboxFallback(context.Background(), "", 10)
	require.NoError(t, err)
	assert.Equal(t, 0, processed)

	var status string
	var retryCount int
	require.NoError(t, db.Raw(
		`SELECT status, retry_count FROM audit_outbox WHERE id = ?`, entryID.String(),
	).Row().Scan(&status, &retryCount))
	assert.Equal(t, "failed", status)
	assert.Equal(t, 1, retryCount)
}

// --- processOutboxEntry context cancellation guard ---

func TestProcessOutboxEntry_ContextCancelled_ReturnsError(t *testing.T) {
	db := setupConsumerTestDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := db.Transaction(func(tx *gorm.DB) error {
		return processOutboxEntry(ctx, tx, uuid.New())
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestProcessOutboxEntry_EntryNotFound returns gorm.ErrRecordNotFound when the
// entry id does not match a pending row.
func TestProcessOutboxEntry_EntryNotFound(t *testing.T) {
	db := setupConsumerTestDB(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		return processOutboxEntry(context.Background(), tx, uuid.New())
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
