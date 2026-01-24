package saga

import (
	"fmt"

	"gorm.io/gorm"
)

// RunSagaMigrations executes the saga persistence schema migrations using GORM AutoMigrate.
// It creates the saga_instances and saga_step_results tables with all required indexes
// and constraints.
//
// This function should be called by each service during startup to ensure the saga
// persistence schema exists. The schema is service-local per the Durable Execution
// Engine design (PRD Section 3.1).
//
// Usage:
//
//	func main() {
//	    db, _ := gorm.Open(postgres.Open(dsn), &gorm.Config{})
//	    if err := saga.RunSagaMigrations(db); err != nil {
//	        log.Fatal("Failed to migrate saga schema:", err)
//	    }
//	}
func RunSagaMigrations(db *gorm.DB) error {
	// Run AutoMigrate for the saga models
	// This creates the tables and standard indexes defined in struct tags
	if err := db.AutoMigrate(&SagaInstance{}, &SagaStepResult{}); err != nil {
		return fmt.Errorf("failed to auto-migrate saga models: %w", err)
	}

	// Create the partial index for orphan detection
	// This index is critical for the recovery worker to efficiently find
	// sagas with expired leases (orphaned due to pod crash)
	//
	// Per PRD Section 3.1:
	// CREATE INDEX idx_saga_instances_orphaned
	//     ON saga_instances(status, lease_expires_at)
	//     WHERE status IN ('RUNNING', 'SUSPENDED')
	//
	// GORM AutoMigrate doesn't support partial indexes, so we create it via raw SQL
	if err := createPartialIndexForOrphanDetection(db); err != nil {
		return fmt.Errorf("failed to create partial index for orphan detection: %w", err)
	}

	// Create composite unique constraint for (saga_instance_id, step_index)
	// This prevents duplicate step results for the same saga step
	if err := createCompositeUniqueConstraint(db); err != nil {
		return fmt.Errorf("failed to create composite unique constraint: %w", err)
	}

	// Create the orphan notification trigger (FR-23)
	// This enables reactive orphan wake-up with <10 second latency target
	if err := createOrphanNotifyTrigger(db); err != nil {
		return fmt.Errorf("failed to create orphan notify trigger: %w", err)
	}

	return nil
}

// createPartialIndexForOrphanDetection creates the idx_saga_instances_orphaned partial index.
// This index optimizes the orphan detection query:
//
//	SELECT * FROM saga_instances
//	WHERE status IN ('RUNNING', 'SUSPENDED')
//	AND lease_expires_at < NOW()
//
// The partial index only includes rows where status is RUNNING or SUSPENDED,
// making it much smaller and faster than a full index.
func createPartialIndexForOrphanDetection(db *gorm.DB) error {
	// Use CREATE INDEX IF NOT EXISTS to make the migration idempotent
	sql := `
		CREATE INDEX IF NOT EXISTS idx_saga_instances_orphaned
		ON saga_instances (status, lease_expires_at)
		WHERE status IN ('RUNNING', 'SUSPENDED')
	`
	return db.Exec(sql).Error
}

// createCompositeUniqueConstraint creates a unique constraint on (saga_instance_id, step_index).
// This ensures that each step in a saga instance can only have one result.
func createCompositeUniqueConstraint(db *gorm.DB) error {
	// Check if constraint already exists to make migration idempotent
	// Query is schema-aware for multi-schema deployments
	var count int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM pg_constraint c
		JOIN pg_class r ON r.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = r.relnamespace
		WHERE c.conname = 'uq_saga_step_results_instance_step'
		  AND r.relname = 'saga_step_results'
		  AND n.nspname = current_schema()
	`).Scan(&count).Error; err != nil {
		return fmt.Errorf("failed to check constraint existence: %w", err)
	}

	if count > 0 {
		return nil // Constraint already exists
	}

	sql := `
		ALTER TABLE saga_step_results
		ADD CONSTRAINT uq_saga_step_results_instance_step
		UNIQUE (saga_instance_id, step_index)
	`
	return db.Exec(sql).Error
}

// createOrphanNotifyTrigger creates the PostgreSQL trigger function and trigger
// that fires NOTIFY 'saga_orphaned' when a saga's lease is released.
// This enables reactive orphan detection with <10 second latency target (FR-23).
//
// The trigger fires when:
// - claimed_by_pod changes from a non-NULL value to NULL
// - This typically happens when a pod crashes (lease expires) or gracefully releases
//
// The notification payload includes the saga instance ID for targeted handling.
func createOrphanNotifyTrigger(db *gorm.DB) error {
	// Create the trigger function
	functionSQL := `
		CREATE OR REPLACE FUNCTION notify_saga_orphaned()
		RETURNS TRIGGER AS $$
		BEGIN
			-- Fire notification when claimed_by_pod is set to NULL from a non-NULL value
			IF OLD.claimed_by_pod IS NOT NULL AND NEW.claimed_by_pod IS NULL THEN
				PERFORM pg_notify('saga_orphaned', NEW.id::text);
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
	`
	if err := db.Exec(functionSQL).Error; err != nil {
		return fmt.Errorf("failed to create notify_saga_orphaned function: %w", err)
	}

	// Create the trigger (use IF NOT EXISTS pattern via conditional drop/create)
	// First drop if exists to ensure clean state, then create
	dropTriggerSQL := `
		DROP TRIGGER IF EXISTS saga_orphaned_trigger ON saga_instances;
	`
	if err := db.Exec(dropTriggerSQL).Error; err != nil {
		return fmt.Errorf("failed to drop existing saga_orphaned_trigger: %w", err)
	}

	createTriggerSQL := `
		CREATE TRIGGER saga_orphaned_trigger
		AFTER UPDATE ON saga_instances
		FOR EACH ROW
		EXECUTE FUNCTION notify_saga_orphaned();
	`
	if err := db.Exec(createTriggerSQL).Error; err != nil {
		return fmt.Errorf("failed to create saga_orphaned_trigger: %w", err)
	}

	return nil
}

// DropSagaTables removes all saga-related tables.
// This is useful for testing and development, but should NEVER be called in production.
func DropSagaTables(db *gorm.DB) error {
	// Drop in reverse order due to foreign key constraints
	if err := db.Migrator().DropTable(&SagaStepResult{}); err != nil {
		return fmt.Errorf("failed to drop saga_step_results: %w", err)
	}
	if err := db.Migrator().DropTable(&SagaInstance{}); err != nil {
		return fmt.Errorf("failed to drop saga_instances: %w", err)
	}
	return nil
}
