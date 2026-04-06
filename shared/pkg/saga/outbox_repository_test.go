package saga

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestGormDB creates an in-memory SQLite database with simplified tables
// compatible with the GORM models used by the outbox repository.
func setupTestGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=5000"), &gorm.Config{})
	require.NoError(t, err)

	// Create tables with SQLite-compatible types (no uuid, jsonb, gen_random_uuid())
	sqls := []string{
		`CREATE TABLE IF NOT EXISTS saga_instances (
			id TEXT PRIMARY KEY,
			saga_definition_id TEXT NOT NULL,
			saga_name TEXT,
			saga_version INTEGER,
			script_hash_at_start TEXT,
			input_snapshot TEXT,
			status TEXT NOT NULL DEFAULT 'PENDING',
			current_step_index INTEGER DEFAULT 0,
			correlation_id TEXT,
			causation_id TEXT,
			tenant_id TEXT NOT NULL DEFAULT '',
			party_type TEXT,
			party_id TEXT,
			knowledge_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			claimed_by TEXT,
			claimed_at DATETIME,
			lease_expires_at DATETIME,
			completed_at DATETIME,
			timeout_seconds INTEGER DEFAULT 0,
			original_input TEXT,
			max_retries INTEGER DEFAULT 0,
			retry_count INTEGER DEFAULT 0,
			last_error TEXT,
			parent_saga_instance_id TEXT,
			is_child INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS saga_step_results (
			id TEXT PRIMARY KEY,
			saga_instance_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			step_name TEXT,
			idempotency_key TEXT NOT NULL UNIQUE,
			result TEXT,
			error TEXT,
			status TEXT NOT NULL,
			error_category TEXT,
			causation_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (saga_instance_id) REFERENCES saga_instances(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS event_outbox (
			id TEXT PRIMARY KEY,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			aggregate_type TEXT NOT NULL,
			event_payload BLOB NOT NULL,
			correlation_id TEXT,
			causation_id TEXT,
			status TEXT NOT NULL,
			topic TEXT NOT NULL,
			partition_key TEXT,
			created_at DATETIME NOT NULL,
			processed_at DATETIME,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			service_name TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT ''
		)`,
	}

	for _, sql := range sqls {
		require.NoError(t, db.Exec(sql).Error)
	}

	return db
}

// ─── Constructor tests ─────────────────────────────────────────────────────

func TestNewGormTxContextWithOutbox(t *testing.T) {
	ctx := NewGormTxContextWithOutbox(nil, "test-service")
	assert.NotNil(t, ctx)
	assert.Equal(t, "test-service", ctx.serviceName)
}

func TestNewGormTransactionalRepositoryWithOutbox(t *testing.T) {
	repo := NewGormTransactionalRepositoryWithOutbox(nil, "test-service")
	assert.NotNil(t, repo)
	assert.Equal(t, "test-service", repo.serviceName)
}

// ─── SaveStepResult tests ──────────────────────────────────────────────────

func TestSaveStepResult_Success(t *testing.T) {
	db := setupTestGormDB(t)

	// Insert a saga instance for the FK
	instanceID := uuid.New()
	db.Exec("INSERT INTO saga_instances (id, saga_definition_id, saga_name, status) VALUES (?, ?, ?, ?)",
		instanceID.String(), uuid.New().String(), "test_saga", "RUNNING")

	tx := db.Begin()
	txCtx := NewGormTxContextWithOutbox(tx, "test-service")

	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		StepName:       "register_instrument",
		IdempotencyKey: "saga_" + instanceID.String() + "_step_0",
		Status:         "COMPLETED",
	}

	err := txCtx.SaveStepResult(context.Background(), stepResult)
	require.NoError(t, err)

	err = txCtx.Commit()
	require.NoError(t, err)

	// Verify persisted
	var count int64
	db.Model(&SagaStepResult{}).Where("id = ?", stepResult.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestSaveStepResult_DuplicateIdempotencyKey(t *testing.T) {
	db := setupTestGormDB(t)

	instanceID := uuid.New()
	db.Exec("INSERT INTO saga_instances (id, saga_definition_id, saga_name, status) VALUES (?, ?, ?, ?)",
		instanceID.String(), uuid.New().String(), "test_saga", "RUNNING")

	key := "saga_" + instanceID.String() + "_step_0"

	// Insert first
	tx1 := db.Begin()
	txCtx1 := NewGormTxContextWithOutbox(tx1, "test-service")
	err := txCtx1.SaveStepResult(context.Background(), &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		StepName:       "step_one",
		IdempotencyKey: key,
		Status:         "COMPLETED",
	})
	require.NoError(t, err)
	require.NoError(t, txCtx1.Commit())

	// Insert duplicate - should fail on unique constraint
	tx2 := db.Begin()
	txCtx2 := NewGormTxContextWithOutbox(tx2, "test-service")
	err = txCtx2.SaveStepResult(context.Background(), &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		StepName:       "step_one",
		IdempotencyKey: key,
		Status:         "COMPLETED",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save step result")
	_ = txCtx2.Rollback()
}

// ─── UpdateStepIndex tests ─────────────────────────────────────────────────

func TestUpdateStepIndex_Success(t *testing.T) {
	db := setupTestGormDB(t)

	instanceID := uuid.New()
	db.Exec("INSERT INTO saga_instances (id, saga_definition_id, saga_name, status, current_step_index) VALUES (?, ?, ?, ?, ?)",
		instanceID.String(), uuid.New().String(), "test_saga", "RUNNING", 0)

	tx := db.Begin()
	txCtx := NewGormTxContextWithOutbox(tx, "test-service")

	err := txCtx.UpdateStepIndex(context.Background(), instanceID, 3)
	require.NoError(t, err)
	require.NoError(t, txCtx.Commit())

	// Verify updated
	var instance SagaInstance
	db.First(&instance, "id = ?", instanceID)
	assert.Equal(t, 3, instance.CurrentStepIndex)
}

func TestUpdateStepIndex_NotFound(t *testing.T) {
	db := setupTestGormDB(t)
	tx := db.Begin()
	txCtx := NewGormTxContextWithOutbox(tx, "test-service")

	err := txCtx.UpdateStepIndex(context.Background(), uuid.New(), 1)
	assert.ErrorIs(t, err, ErrSagaInstanceNotFound)
	_ = txCtx.Rollback()
}

// ─── WriteOutboxEntry tests ────────────────────────────────────────────────

func TestWriteOutboxEntry_Success(t *testing.T) {
	db := setupTestGormDB(t)
	tx := db.Begin()
	txCtx := NewGormTxContextWithOutbox(tx, "saga-service")

	entryID := uuid.New()
	entry := &OutboxEntry{
		ID:            entryID,
		EventType:     "saga.step.completed",
		AggregateID:   "agg-123",
		AggregateType: "saga_instance",
		EventPayload:  []byte(`{"step":"register_instrument"}`),
		CorrelationID: uuid.New().String(),
		CausationID:   uuid.New().String(),
		Topic:         "saga-events",
	}

	err := txCtx.WriteOutboxEntry(context.Background(), entry)
	require.NoError(t, err)
	require.NoError(t, txCtx.Commit())

	// Verify the platform EventOutbox was created
	var outbox events.EventOutbox
	result := db.First(&outbox, "id = ?", entryID)
	require.NoError(t, result.Error)
	assert.Equal(t, "saga.step.completed", outbox.EventType)
	assert.Equal(t, "agg-123", outbox.AggregateID)
	assert.Equal(t, "saga_instance", outbox.AggregateType)
	assert.Equal(t, "saga-events", outbox.Topic)
	assert.Equal(t, events.StatusPending, outbox.Status)
	assert.Equal(t, "saga-service", outbox.ServiceName)
	assert.Equal(t, "agg-123", outbox.PartitionKey) // Uses aggregate ID
	assert.Equal(t, 0, outbox.RetryCount)
}

// ─── Commit / Rollback tests ──────────────────────────────────────────────

func TestCommit_Success(t *testing.T) {
	db := setupTestGormDB(t)

	instanceID := uuid.New()
	db.Exec("INSERT INTO saga_instances (id, saga_definition_id, saga_name, status) VALUES (?, ?, ?, ?)",
		instanceID.String(), uuid.New().String(), "test_saga", "RUNNING")

	tx := db.Begin()
	txCtx := NewGormTxContextWithOutbox(tx, "test-service")

	err := txCtx.SaveStepResult(context.Background(), &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		StepName:       "test_step",
		IdempotencyKey: "key-commit-" + uuid.New().String(),
		Status:         "COMPLETED",
	})
	require.NoError(t, err)

	err = txCtx.Commit()
	require.NoError(t, err)

	// Verify data is visible
	var count int64
	db.Model(&SagaStepResult{}).Where("saga_instance_id = ?", instanceID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestRollback_DiscardsChanges(t *testing.T) {
	db := setupTestGormDB(t)

	instanceID := uuid.New()
	db.Exec("INSERT INTO saga_instances (id, saga_definition_id, saga_name, status) VALUES (?, ?, ?, ?)",
		instanceID.String(), uuid.New().String(), "test_saga", "RUNNING")

	tx := db.Begin()
	txCtx := NewGormTxContextWithOutbox(tx, "test-service")

	err := txCtx.SaveStepResult(context.Background(), &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		StepName:       "test_step",
		IdempotencyKey: "key-rollback-" + uuid.New().String(),
		Status:         "COMPLETED",
	})
	require.NoError(t, err)

	err = txCtx.Rollback()
	require.NoError(t, err)

	// Verify data is NOT visible
	var count int64
	db.Model(&SagaStepResult{}).Where("saga_instance_id = ?", instanceID).Count(&count)
	assert.Equal(t, int64(0), count)
}

// ─── BeginTxWithOutbox tests ───────────────────────────────────────────────

func TestBeginTxWithOutbox_Success(t *testing.T) {
	db := setupTestGormDB(t)
	repo := NewGormTransactionalRepositoryWithOutbox(db, "test-service")

	txCtx, err := repo.BeginTxWithOutbox(context.Background())
	require.NoError(t, err)
	require.NotNil(t, txCtx)

	// Should be able to rollback without error
	err = txCtx.Rollback()
	require.NoError(t, err)
}

func TestBeginTxWithOutbox_FullWorkflow(t *testing.T) {
	db := setupTestGormDB(t)
	repo := NewGormTransactionalRepositoryWithOutbox(db, "workflow-service")

	instanceID := uuid.New()
	db.Exec("INSERT INTO saga_instances (id, saga_definition_id, saga_name, status) VALUES (?, ?, ?, ?)",
		instanceID.String(), uuid.New().String(), "test_saga", "RUNNING")

	txCtx, err := repo.BeginTxWithOutbox(context.Background())
	require.NoError(t, err)

	// Save step result
	err = txCtx.SaveStepResult(context.Background(), &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		StepName:       "full_workflow_step",
		IdempotencyKey: "key-workflow-" + uuid.New().String(),
		Status:         "COMPLETED",
	})
	require.NoError(t, err)

	// Write outbox entry
	err = txCtx.WriteOutboxEntry(context.Background(), &OutboxEntry{
		ID:            uuid.New(),
		EventType:     "saga.step.completed",
		AggregateID:   instanceID.String(),
		AggregateType: "saga_instance",
		EventPayload:  []byte(`{}`),
		Topic:         "saga-events",
	})
	require.NoError(t, err)

	// Commit
	err = txCtx.Commit()
	require.NoError(t, err)

	// Verify both records exist
	var stepCount int64
	db.Model(&SagaStepResult{}).Where("saga_instance_id = ?", instanceID).Count(&stepCount)
	assert.Equal(t, int64(1), stepCount)

	var outboxCount int64
	db.Model(&events.EventOutbox{}).Where("aggregate_id = ?", instanceID.String()).Count(&outboxCount)
	assert.Equal(t, int64(1), outboxCount)
}

// ─── Error sentinel tests ──────────────────────────────────────────────────

func TestErrSagaInstanceNotFound(t *testing.T) {
	assert.EqualError(t, ErrSagaInstanceNotFound, "saga instance not found")
}
