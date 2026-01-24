// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestSagaMigrations_SchemaCreation verifies that RunSagaMigrations creates
// the saga_instances and saga_step_results tables with correct columns.
func TestSagaMigrations_SchemaCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	// Run the saga migrations
	err := RunSagaMigrations(db)
	require.NoError(t, err, "RunSagaMigrations should not fail")

	// Verify saga_instances table exists with correct columns
	t.Run("saga_instances table has correct columns", func(t *testing.T) {
		var columns []struct {
			ColumnName string `gorm:"column:column_name"`
			DataType   string `gorm:"column:data_type"`
			IsNullable string `gorm:"column:is_nullable"`
		}

		err := db.Raw(`
			SELECT column_name, data_type, is_nullable
			FROM information_schema.columns
			WHERE table_name = 'saga_instances'
			ORDER BY ordinal_position
		`).Scan(&columns).Error
		require.NoError(t, err)

		// Build a map for easier assertions
		columnMap := make(map[string]struct {
			DataType   string
			IsNullable string
		})
		for _, col := range columns {
			columnMap[col.ColumnName] = struct {
				DataType   string
				IsNullable string
			}{col.DataType, col.IsNullable}
		}

		// Required columns from PRD Section 3.1
		requiredColumns := []string{
			"id", "saga_definition_id", "correlation_id", "causation_id",
			"parent_saga_id", "current_step_index", "status",
			"lease_expires_at", "created_at", "updated_at", "completed_at",
		}

		for _, col := range requiredColumns {
			_, exists := columnMap[col]
			assert.True(t, exists, "saga_instances should have column: %s", col)
		}

		// Verify id is UUID type
		assert.Contains(t, columnMap["id"].DataType, "uuid", "id should be UUID type")
		// Verify status has NOT NULL
		assert.Equal(t, "NO", columnMap["status"].IsNullable, "status should be NOT NULL")
	})

	// Verify saga_step_results table exists with correct columns
	t.Run("saga_step_results table has correct columns", func(t *testing.T) {
		var columns []struct {
			ColumnName string `gorm:"column:column_name"`
			DataType   string `gorm:"column:data_type"`
			IsNullable string `gorm:"column:is_nullable"`
		}

		err := db.Raw(`
			SELECT column_name, data_type, is_nullable
			FROM information_schema.columns
			WHERE table_name = 'saga_step_results'
			ORDER BY ordinal_position
		`).Scan(&columns).Error
		require.NoError(t, err)

		columnMap := make(map[string]struct {
			DataType   string
			IsNullable string
		})
		for _, col := range columns {
			columnMap[col.ColumnName] = struct {
				DataType   string
				IsNullable string
			}{col.DataType, col.IsNullable}
		}

		// Required columns from PRD Section 3.1
		requiredColumns := []string{
			"id", "saga_instance_id", "step_index", "idempotency_key",
			"status", "created_at", "updated_at",
		}

		for _, col := range requiredColumns {
			_, exists := columnMap[col]
			assert.True(t, exists, "saga_step_results should have column: %s", col)
		}

		// Verify saga_instance_id is UUID type
		assert.Contains(t, columnMap["saga_instance_id"].DataType, "uuid", "saga_instance_id should be UUID type")
	})
}

// TestSagaMigrations_CascadeDelete verifies that deleting a SagaInstance
// cascades to delete all associated SagaStepResult records.
func TestSagaMigrations_CascadeDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a saga instance
	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		Status:           SagaStatusPending,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err, "Failed to create saga instance")

	// Create associated step results
	stepResult1 := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		IdempotencyKey: "saga_" + instanceID.String() + "_step_0",
		Status:         StepStatusCompleted,
	}
	stepResult2 := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      1,
		IdempotencyKey: "saga_" + instanceID.String() + "_step_1",
		Status:         StepStatusCompleted,
	}
	err = db.Create(stepResult1).Error
	require.NoError(t, err, "Failed to create step result 1")
	err = db.Create(stepResult2).Error
	require.NoError(t, err, "Failed to create step result 2")

	// Verify step results exist
	var count int64
	db.Model(&SagaStepResult{}).Where("saga_instance_id = ?", instanceID).Count(&count)
	require.Equal(t, int64(2), count, "Should have 2 step results before cascade delete")

	// Delete the saga instance
	err = db.Delete(&SagaInstance{}, "id = ?", instanceID).Error
	require.NoError(t, err, "Failed to delete saga instance")

	// Verify step results were cascade deleted
	db.Model(&SagaStepResult{}).Where("saga_instance_id = ?", instanceID).Count(&count)
	assert.Equal(t, int64(0), count, "Step results should be cascade deleted when instance is deleted")
}

// TestSagaMigrations_UniqueConstraints verifies the unique constraints on
// saga_step_results: (saga_instance_id, step_index) and (idempotency_key).
func TestSagaMigrations_UniqueConstraints(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a saga instance
	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		Status:           SagaStatusPending,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	// Create a step result
	idempotencyKey := "saga_" + instanceID.String() + "_step_0"
	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      0,
		IdempotencyKey: idempotencyKey,
		Status:         StepStatusCompleted,
	}
	err = db.Create(stepResult).Error
	require.NoError(t, err)

	t.Run("duplicate saga_instance_id + step_index fails", func(t *testing.T) {
		duplicateStep := &SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: instanceID,
			StepIndex:      0, // Same step index for same instance
			IdempotencyKey: "different_key",
			Status:         StepStatusCompleted,
		}
		err := db.Create(duplicateStep).Error
		assert.Error(t, err, "Should fail on duplicate (saga_instance_id, step_index)")
	})

	t.Run("duplicate idempotency_key fails", func(t *testing.T) {
		// Create another saga instance
		anotherInstance := &SagaInstance{
			ID:               uuid.New(),
			SagaDefinitionID: uuid.New(),
			Status:           SagaStatusPending,
			CorrelationID:    uuid.New(),
			CurrentStepIndex: 0,
		}
		err := db.Create(anotherInstance).Error
		require.NoError(t, err)

		duplicateKey := &SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: anotherInstance.ID,
			StepIndex:      5,              // Different step
			IdempotencyKey: idempotencyKey, // Same idempotency key as first!
			Status:         StepStatusCompleted,
		}
		err = db.Create(duplicateKey).Error
		assert.Error(t, err, "Should fail on duplicate idempotency_key")
	})
}

// TestSagaMigrations_PartialIndex verifies the partial index for orphan queries.
func TestSagaMigrations_PartialIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create test data: one orphaned saga, one completed saga
	now := time.Now()
	expiredLease := now.Add(-10 * time.Minute)
	futureLease := now.Add(10 * time.Minute)

	orphanedSaga := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		Status:           SagaStatusRunning,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 2,
		LeaseExpiresAt:   &expiredLease, // Expired lease - orphaned
	}
	completedSaga := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		Status:           SagaStatusCompleted,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 5,
		LeaseExpiresAt:   &futureLease,
	}
	activeSaga := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		Status:           SagaStatusRunning,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 1,
		LeaseExpiresAt:   &futureLease, // Not expired - not orphaned
	}

	err = db.Create(orphanedSaga).Error
	require.NoError(t, err)
	err = db.Create(completedSaga).Error
	require.NoError(t, err)
	err = db.Create(activeSaga).Error
	require.NoError(t, err)

	// Query for orphaned sagas (the query the partial index optimizes)
	var orphans []SagaInstance
	err = db.Where("status IN ? AND lease_expires_at < ?",
		[]string{string(SagaStatusRunning), string(SagaStatusSuspended)},
		now,
	).Find(&orphans).Error
	require.NoError(t, err)

	assert.Len(t, orphans, 1, "Should find exactly one orphaned saga")
	if len(orphans) > 0 {
		assert.Equal(t, orphanedSaga.ID, orphans[0].ID, "Orphaned saga should be the one with expired lease")
	}

	// Verify the partial index exists via EXPLAIN ANALYZE
	t.Run("partial index is used for orphan query", func(t *testing.T) {
		var explainResult []struct {
			QueryPlan string `gorm:"column:QUERY PLAN"`
		}
		err := db.Raw(`
			EXPLAIN ANALYZE
			SELECT * FROM saga_instances
			WHERE status IN ('RUNNING', 'SUSPENDED')
			AND lease_expires_at < NOW()
		`).Scan(&explainResult).Error
		require.NoError(t, err)

		// Check that the explain plan mentions the partial index
		fullPlan := ""
		for _, row := range explainResult {
			fullPlan += row.QueryPlan + "\n"
		}
		// The index should be named idx_saga_instances_orphaned
		assert.Contains(t, fullPlan, "idx_saga_instances_orphaned",
			"EXPLAIN should show partial index usage. Plan: %s", fullPlan)
	})
}

// setupTestPostgres creates a PostgreSQL testcontainer and returns a GORM DB connection.
func setupTestPostgres(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("saga_test"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Failed to connect to database")

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = pgContainer.Terminate(cleanupCtx)
	}

	return db, cleanup
}

// Suppress unused import warning for sql package
var _ = sql.ErrNoRows
