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
	// Only run AutoMigrate when tables don't yet exist.
	// GORM's AutoMigrate is not idempotent on CockroachDB: re-running it
	// fails with SQLSTATE 42704 because CockroachDB names unique constraints
	// differently than what GORM expects when checking existing schema.
	// The partial indexes and composite constraint below are already
	// idempotent (IF NOT EXISTS / information_schema checks).
	migrator := db.Migrator()
	if !migrator.HasTable(&SagaDefinition{}) {
		if err := db.AutoMigrate(&SagaDefinition{}); err != nil {
			return fmt.Errorf("failed to auto-migrate saga_definitions: %w", err)
		}
	}
	if !migrator.HasTable(&SagaInstance{}) || !migrator.HasTable(&SagaStepResult{}) {
		if err := db.AutoMigrate(&SagaInstance{}, &SagaStepResult{}); err != nil {
			return fmt.Errorf("failed to auto-migrate saga models: %w", err)
		}
	}

	// Create the unique (name, version) constraint on saga_definitions.
	// Two definitions with the same (name, version) are not allowed: per-version
	// scripts are immutable, and FindOrCreate enforces hash match for reuse.
	if err := createSagaDefinitionsNameVersionIndex(db); err != nil {
		return fmt.Errorf("failed to create saga_definitions (name, version) index: %w", err)
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

	// Add next_retry_at column + partial index for exponential backoff on transient
	// failures (see migrateNextRetryAtBackoff for the CockroachDB ordering rule).
	if err := migrateNextRetryAtBackoff(db); err != nil {
		return err
	}

	// Create composite unique constraint for (saga_instance_id, step_index)
	// This prevents duplicate step results for the same saga step
	if err := createCompositeUniqueConstraint(db); err != nil {
		return fmt.Errorf("failed to create composite unique constraint: %w", err)
	}

	return nil
}

// createSagaDefinitionsNameVersionIndex creates a unique index on (name, version).
// Each (name, version) combination resolves to exactly one immutable definition row.
func createSagaDefinitionsNameVersionIndex(db *gorm.DB) error {
	sql := `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_saga_definitions_name_version
		ON saga_definitions (name, version)
	`
	return db.Exec(sql).Error
}

// migrateNextRetryAtBackoff adds the next_retry_at column AND its supporting
// partial index for exponential-backoff orphan claiming.
//
// The column is nullable: NULL means "no backoff in effect, immediately eligible
// for reclaim by the orphan watcher". A non-NULL value is the earliest wall-clock
// time at which the orphan watcher may reclaim the saga.
//
// CRITICAL CockroachDB ordering: the ALTER TABLE and the partial CREATE INDEX
// MUST commit in separate transactions. Each db.Exec call here runs in
// autocommit mode, so issuing them as two separate calls satisfies the rule.
// Combining them inside a single Transaction(...) block would fail because the
// new column is not yet "public" when the index references it.
//
// Both statements are idempotent (IF NOT EXISTS) and safe to re-run.
func migrateNextRetryAtBackoff(db *gorm.DB) error {
	if err := db.Exec(`
		ALTER TABLE saga_instances
		ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ NULL
	`).Error; err != nil {
		return fmt.Errorf("failed to add next_retry_at column: %w", err)
	}

	// Partial index keeps the index compact: only sagas currently in backoff
	// appear here. Sagas with next_retry_at IS NULL are served from the
	// existing idx_saga_instances_orphaned index.
	if err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_saga_instances_next_retry_at
		ON saga_instances (next_retry_at)
		WHERE next_retry_at IS NOT NULL
	`).Error; err != nil {
		return fmt.Errorf("failed to create partial index for next_retry_at: %w", err)
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
	// Uses information_schema which is compatible with both PostgreSQL and CockroachDB
	var count int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM information_schema.table_constraints
		WHERE constraint_name = 'uq_saga_step_results_instance_step'
		  AND table_name = 'saga_step_results'
		  AND constraint_type = 'UNIQUE'
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
	if err := db.Migrator().DropTable(&SagaDefinition{}); err != nil {
		return fmt.Errorf("failed to drop saga_definitions: %w", err)
	}
	return nil
}
