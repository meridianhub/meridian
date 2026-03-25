package audit

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupConsumerTestDB creates an in-memory SQLite database for consumer unit tests.
// MaxOpenConns is set to 1 so that the entire connection pool shares the same
// in-memory database. Without this limit, database/sql may open additional
// connections that each receive their own, empty in-memory database — making
// data written on one connection invisible to others.
// Tables are created with raw SQL to avoid PostgreSQL-specific syntax in GORM struct tags
// (e.g. gen_random_uuid(), jsonb).
func setupConsumerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	err = db.Exec(`CREATE TABLE audit_log (
		id TEXT PRIMARY KEY,
		table_name TEXT NOT NULL,
		operation TEXT NOT NULL,
		record_id TEXT NOT NULL,
		old_values TEXT,
		new_values TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		changed_by TEXT,
		transaction_id TEXT,
		client_ip TEXT,
		user_agent TEXT
	)`).Error
	require.NoError(t, err)

	err = db.Exec(`CREATE TABLE audit_outbox (
		id TEXT PRIMARY KEY,
		table_name TEXT NOT NULL,
		operation TEXT NOT NULL,
		record_id TEXT NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		retry_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by TEXT,
		transaction_id TEXT,
		client_ip TEXT,
		user_agent TEXT
	)`).Error
	require.NoError(t, err)

	return db
}

// newTestConsumer builds a Consumer that only has the db field set.
// Do not call Start/Stop/Close on this consumer.
func newTestConsumer(db *gorm.DB) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Consumer{
		db:     db,
		ctx:    ctx,
		cancel: cancel,
	}
}

// --- ConsumerConfig.applyDefaults ---

func TestConsumerConfig_ApplyDefaults_AllDefaults(t *testing.T) {
	cfg := ConsumerConfig{}
	cfg.applyDefaults()

	assert.Equal(t, kafka.AuditConsumerGroup, cfg.GroupID)
	assert.Equal(t, kafka.AuditEventsTopic, cfg.Topic)
	assert.Equal(t, ".dlq", cfg.DLQTopicSuffix)
	assert.Equal(t, defaults.DefaultRPCTimeout, cfg.HandlerTimeout)
	assert.Equal(t, 3, cfg.MaxRetries)
}

func TestConsumerConfig_ApplyDefaults_PreservesExistingValues(t *testing.T) {
	cfg := ConsumerConfig{
		GroupID:        "my-group",
		Topic:          "my-topic",
		DLQTopicSuffix: ".dead",
		HandlerTimeout: 5 * time.Second,
		MaxRetries:     10,
	}
	cfg.applyDefaults()

	assert.Equal(t, "my-group", cfg.GroupID)
	assert.Equal(t, "my-topic", cfg.Topic)
	assert.Equal(t, ".dead", cfg.DLQTopicSuffix)
	assert.Equal(t, 5*time.Second, cfg.HandlerTimeout)
	assert.Equal(t, 10, cfg.MaxRetries)
}

func TestConsumerConfig_ApplyDefaults_PartialOverride(t *testing.T) {
	cfg := ConsumerConfig{
		GroupID: "custom-group",
	}
	cfg.applyDefaults()

	assert.Equal(t, "custom-group", cfg.GroupID)
	assert.Equal(t, kafka.AuditEventsTopic, cfg.Topic)
	assert.Equal(t, ".dlq", cfg.DLQTopicSuffix)
	assert.Equal(t, defaults.DefaultRPCTimeout, cfg.HandlerTimeout)
	assert.Equal(t, 3, cfg.MaxRetries)
}

// --- handleMessage error paths ---

// TestHandleMessage_UnexpectedType_ReturnsError passes a non-AuditEvent proto to trigger ErrUnexpectedMessageType.
// Uses *timestamppb.Timestamp as a valid proto.Message that is not *auditv1.AuditEvent.
func TestHandleMessage_UnexpectedType_ReturnsError(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	err := c.handleMessage(context.Background(), nil, timestamppb.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnexpectedMessageType)
}

func TestHandleMessage_InvalidOperation_Unspecified(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED,
		TableName: "orders",
		RecordId:  "record-1",
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOperation)
}

func TestHandleMessage_InvalidOperation_UnknownValue(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation(999),
		TableName: "orders",
		RecordId:  "record-1",
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOperation)
}

func TestHandleMessage_InvalidSchemaName(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName:  "orders",
		RecordId:   "record-1",
		SchemaName: "acme'; DROP TABLE--",
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSchemaName)
}

func TestHandleMessage_InvalidSchemaName_StartsWithNumber(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName:  "orders",
		RecordId:   "record-1",
		SchemaName: "123bad",
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSchemaName)
}

func TestHandleMessage_ContextCancelled(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName: "orders",
		RecordId:  "record-1",
	}

	err := c.handleMessage(ctx, nil, event)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
}

// --- handleMessage happy paths (no SchemaName to avoid SET LOCAL) ---

func TestHandleMessage_ValidInsert_CreatesAuditLog(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName: "orders",
		RecordId:  "order-123",
		NewValues: `{"amount": 100}`,
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	assert.Equal(t, "orders", log.Table)
	assert.Equal(t, "INSERT", log.Operation)
	assert.Equal(t, "order-123", log.RecordID)
	assert.Equal(t, `{"amount": 100}`, log.NewValues)
	assert.Nil(t, log.ChangedBy)
	assert.Nil(t, log.TransactionID)
	assert.Nil(t, log.ClientIP)
	assert.Nil(t, log.UserAgent)
}

func TestHandleMessage_ValidUpdate_CreatesAuditLog(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
		TableName: "accounts",
		RecordId:  "account-456",
		OldValues: `{"status": "pending"}`,
		NewValues: `{"status": "active"}`,
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	assert.Equal(t, "accounts", log.Table)
	assert.Equal(t, "UPDATE", log.Operation)
	assert.Equal(t, `{"status": "pending"}`, log.OldValues)
	assert.Equal(t, `{"status": "active"}`, log.NewValues)
}

func TestHandleMessage_ValidDelete_CreatesAuditLog(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
		TableName: "sessions",
		RecordId:  "session-789",
		OldValues: `{"token": "abc"}`,
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	assert.Equal(t, "DELETE", log.Operation)
	assert.Equal(t, "session-789", log.RecordID)
}

func TestHandleMessage_ValidInitialImport_CreatesAuditLog(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT,
		TableName: "customers",
		RecordId:  "customer-1",
		NewValues: `{"name": "Acme Corp"}`,
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	assert.Equal(t, "INITIAL_IMPORT", log.Operation)
}

func TestHandleMessage_NilTimestamp_UsesCurrentTime(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	before := time.Now().Add(-time.Second)

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName: "orders",
		RecordId:  "order-nil-ts",
		Timestamp: nil, // explicitly nil
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	assert.True(t, log.CreatedAt.After(before), "CreatedAt should be after test start")
}

func TestHandleMessage_WithTimestamp_UsesEventTime(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	eventTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	event := &auditv1.AuditEvent{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName: "orders",
		RecordId:  "order-with-ts",
		Timestamp: timestamppb.New(eventTime),
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	// SQLite stores time without nanoseconds; compare at second precision
	assert.Equal(t, eventTime.Unix(), log.CreatedAt.Unix())
}

func TestHandleMessage_WithOptionalFields_PopulatesLog(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	changedBy := "user-123"
	txID := "tx-abc"
	clientIP := "192.168.1.1"
	userAgent := "Mozilla/5.0"

	event := &auditv1.AuditEvent{
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
		TableName:     "ledger",
		RecordId:      "entry-1",
		ChangedBy:     changedBy,
		TransactionId: txID,
		ClientIp:      clientIP,
		UserAgent:     userAgent,
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	require.NotNil(t, log.ChangedBy)
	assert.Equal(t, changedBy, *log.ChangedBy)
	require.NotNil(t, log.TransactionID)
	assert.Equal(t, txID, *log.TransactionID)
	require.NotNil(t, log.ClientIP)
	assert.Equal(t, clientIP, *log.ClientIP)
	require.NotNil(t, log.UserAgent)
	assert.Equal(t, userAgent, *log.UserAgent)
}

func TestHandleMessage_EmptyOptionalFields_NilPointers(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	event := &auditv1.AuditEvent{
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName:     "orders",
		RecordId:      "order-1",
		ChangedBy:     "",
		TransactionId: "",
		ClientIp:      "",
		UserAgent:     "",
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var log AuditLog
	require.NoError(t, db.First(&log).Error)
	assert.Nil(t, log.ChangedBy)
	assert.Nil(t, log.TransactionID)
	assert.Nil(t, log.ClientIP)
	assert.Nil(t, log.UserAgent)
}

func TestHandleMessage_EmptySchemaName_DefaultsToUnknown(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	// When SchemaName is empty, schema variable defaults to "unknown"
	// and no SET LOCAL is issued (compatible with SQLite)
	event := &auditv1.AuditEvent{
		Operation:  auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		TableName:  "orders",
		RecordId:   "order-1",
		SchemaName: "",
	}

	err := c.handleMessage(context.Background(), nil, event)
	require.NoError(t, err)

	var count int64
	db.Model(&AuditLog{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

// --- ProcessOutboxFallback ---

func TestProcessOutboxFallback_InvalidSchemaName(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	_, err := c.ProcessOutboxFallback(context.Background(), "123invalid", 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSchemaName)
}

func TestProcessOutboxFallback_InvalidSchemaName_WithSQLInjection(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	_, err := c.ProcessOutboxFallback(context.Background(), "tenant'; DROP TABLE audit_log--", 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSchemaName)
}

func TestProcessOutboxFallback_DefaultBatchSize(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	// Seed 150 pending entries so the default batch size of 100 is exercised.
	for i := 0; i < 150; i++ {
		err := db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, status, created_at, retry_count)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), "orders", "INSERT", fmt.Sprintf("r-%d", i), "pending", time.Now(), 0,
		).Error
		require.NoError(t, err)
	}

	// batchSize = 0 should apply the default of 100, so exactly 100 rows are processed.
	processed, err := c.ProcessOutboxFallback(context.Background(), "", 0)
	require.NoError(t, err)
	assert.Equal(t, 100, processed)
}

func TestProcessOutboxFallback_NegativeBatchSize_UsesDefault(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	// Seed 150 pending entries so the default of 100 is exercised.
	for i := 0; i < 150; i++ {
		err := db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, status, created_at, retry_count)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), "accounts", "UPDATE", fmt.Sprintf("a-%d", i), "pending", time.Now(), 0,
		).Error
		require.NoError(t, err)
	}

	// Negative batch size should also fall back to 100.
	processed, err := c.ProcessOutboxFallback(context.Background(), "", -5)
	require.NoError(t, err)
	assert.Equal(t, 100, processed)
}

func TestProcessOutboxFallback_NoPendingEntries(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	processed, err := c.ProcessOutboxFallback(context.Background(), "", 10)
	require.NoError(t, err)
	assert.Equal(t, 0, processed)
}

func TestProcessOutboxFallback_ProcessesPendingEntries(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	// Insert pending outbox entries with explicit IDs
	id1 := uuid.New()
	id2 := uuid.New()
	changedBy := "user-abc"
	err := db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, old_values, new_values, status, created_at, retry_count, changed_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id1.String(), "orders", "INSERT", "order-1", "", `{"status":"new"}`, "pending", time.Now(), 0, &changedBy,
	).Error
	require.NoError(t, err)

	err = db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, old_values, new_values, status, created_at, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id2.String(), "accounts", "UPDATE", "account-2", `{"bal":0}`, `{"bal":100}`, "pending", time.Now(), 0,
	).Error
	require.NoError(t, err)

	processed, err := c.ProcessOutboxFallback(context.Background(), "", 10)
	require.NoError(t, err)
	assert.Equal(t, 2, processed)

	// Verify audit_log entries were created
	var count int64
	db.Model(&AuditLog{}).Count(&count)
	assert.Equal(t, int64(2), count)

	// Verify outbox entries were marked completed
	var completedCount int64
	db.Model(&AuditOutbox{}).Where("status = ?", "completed").Count(&completedCount)
	assert.Equal(t, int64(2), completedCount)
}

func TestProcessOutboxFallback_SkipsAlreadyProcessedEntries(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	// Insert a completed entry - should be ignored
	err := db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, status, created_at, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), "orders", "INSERT", "order-done", "completed", time.Now(), 0,
	).Error
	require.NoError(t, err)

	// Insert a failed entry - should also be ignored
	err = db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, status, created_at, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), "orders", "DELETE", "order-failed", "failed", time.Now(), 3,
	).Error
	require.NoError(t, err)

	processed, err := c.ProcessOutboxFallback(context.Background(), "", 10)
	require.NoError(t, err)
	assert.Equal(t, 0, processed)

	var count int64
	db.Model(&AuditLog{}).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestProcessOutboxFallback_RespectsBatchSizeLimit(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	// Insert 5 pending entries
	for i := 0; i < 5; i++ {
		err := db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, status, created_at, retry_count)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), "orders", "INSERT", "order-"+uuid.New().String(), "pending", time.Now(), 0,
		).Error
		require.NoError(t, err)
	}

	// Process only 3 at a time
	processed, err := c.ProcessOutboxFallback(context.Background(), "", 3)
	require.NoError(t, err)
	assert.Equal(t, 3, processed)

	var logCount int64
	db.Model(&AuditLog{}).Count(&logCount)
	assert.Equal(t, int64(3), logCount)
}

func TestProcessOutboxFallback_ContextCancelled(t *testing.T) {
	db := setupConsumerTestDB(t)
	c := newTestConsumer(db)
	defer c.cancel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entryID := uuid.New().String()
	err := db.Exec(`INSERT INTO audit_outbox (id, table_name, operation, record_id, status, created_at, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entryID, "orders", "INSERT", "order-ctx-cancel", "pending", time.Now(), 0,
	).Error
	require.NoError(t, err)

	// A pre-cancelled context should prevent processing; the entry must remain pending.
	// Intentionally ignore the return value — the outcome is verified via DB state below.
	_, _ = c.ProcessOutboxFallback(ctx, "", 10)

	var status string
	err = db.Raw("SELECT status FROM audit_outbox WHERE id = ?", entryID).Scan(&status).Error
	require.NoError(t, err)
	require.NotEmpty(t, status)
	// The entry should not have been moved to 'completed'; it stays 'pending' (or 'failed').
	assert.NotEqual(t, "completed", status, "cancelled context should prevent audit log write")
}

// --- Error types ---

func TestConsumerErrorTypes(t *testing.T) {
	t.Run("sentinel errors are distinct", func(t *testing.T) {
		assert.NotEqual(t, ErrEmptyBootstrapServers, ErrNilDatabase)
		assert.NotEqual(t, ErrEmptyBootstrapServers, ErrUnexpectedMessageType)
		assert.NotEqual(t, ErrEmptyBootstrapServers, ErrInvalidOperation)
		assert.NotEqual(t, ErrEmptyBootstrapServers, ErrInvalidSchemaName)
	})

	t.Run("sentinel errors survive fmt.Errorf wrapping", func(t *testing.T) {
		wrappedSchema := fmt.Errorf("outer: %w", ErrInvalidSchemaName)
		assert.ErrorIs(t, wrappedSchema, ErrInvalidSchemaName)

		wrappedOp := fmt.Errorf("outer: %w", ErrInvalidOperation)
		assert.ErrorIs(t, wrappedOp, ErrInvalidOperation)

		wrappedDB := fmt.Errorf("outer: %w", ErrNilDatabase)
		assert.ErrorIs(t, wrappedDB, ErrNilDatabase)
	})
}
