package persistence

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sharedDB holds the single CockroachDB container shared across all tests in this package.
// Lazily initialized on first use via sync.Once to avoid container startup when running unit tests.
var (
	sharedDB      *gorm.DB
	sharedOnce    sync.Once
	sharedInitErr error
	sharedCleanup func()
)

// TestMain manages the lifecycle of the shared CockroachDB container.
func TestMain(m *testing.M) {
	code := m.Run()
	if sharedCleanup != nil {
		sharedCleanup()
	}
	os.Exit(code)
}

// initSharedContainer starts the CockroachDB testcontainer and runs DDL once.
func initSharedContainer() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	connConfig, err := container.ConnectionConfig(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return fmt.Errorf("connection config: %w", err)
	}

	db, err := gorm.Open(gormpg.Open(connConfig.ConnString()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = container.Terminate(ctx)
		return fmt.Errorf("gorm open: %w", err)
	}

	if err := createSchema(db); err != nil {
		_ = container.Terminate(ctx)
		return fmt.Errorf("create schema: %w", err)
	}

	sharedDB = db
	sharedCleanup = func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = container.Terminate(cleanupCtx)
	}
	return nil
}

// createSchema runs DDL to create the operational-gateway tables.
// Uses IF NOT EXISTS so it is safe to call multiple times (idempotent).
func createSchema(db *gorm.DB) error {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS provider_connections (
            tenant_id UUID NOT NULL,
            connection_id UUID NOT NULL,
            provider_name VARCHAR(255) NOT NULL,
            provider_type VARCHAR(128) NOT NULL,
            protocol VARCHAR(20) NOT NULL,
            base_url VARCHAR(2048) NOT NULL,
            auth_config JSONB NOT NULL,
            retry_policy JSONB NULL,
            rate_limit_config JSONB NULL,
            health_status VARCHAR(20) NOT NULL DEFAULT 'UNKNOWN',
            last_health_check_at TIMESTAMPTZ NULL,
            circuit_state VARCHAR(20) NOT NULL DEFAULT 'CLOSED',
            circuit_opened_at TIMESTAMPTZ NULL,
            failure_count INT NOT NULL DEFAULT 0,
            success_count INT NOT NULL DEFAULT 0,
            status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
            deprecated_at TIMESTAMPTZ NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            PRIMARY KEY (tenant_id, connection_id)
        )`,
		`CREATE TABLE IF NOT EXISTS instructions (
            id UUID NOT NULL DEFAULT gen_random_uuid(),
            tenant_id UUID NOT NULL,
            instruction_type VARCHAR(255) NOT NULL,
            provider_connection_id UUID NOT NULL,
            correlation_id VARCHAR(255) NULL,
            causation_id VARCHAR(255) NULL,
            payload JSONB NOT NULL,
            metadata JSONB NULL,
            priority SMALLINT NOT NULL DEFAULT 2,
            status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
            scheduled_at TIMESTAMPTZ NULL,
            expires_at TIMESTAMPTZ NULL,
            attempt_count INTEGER NOT NULL DEFAULT 0,
            max_attempts INTEGER NOT NULL DEFAULT 3,
            next_retry_at TIMESTAMPTZ NULL,
            idempotency_key VARCHAR(255) NOT NULL,
            dispatched_at TIMESTAMPTZ NULL,
            completed_at TIMESTAMPTZ NULL,
            failure_reason TEXT NULL,
            error_code VARCHAR(64) NULL,
            version BIGINT NOT NULL DEFAULT 1,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            PRIMARY KEY (id)
        )`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_instructions_idempotency ON instructions (tenant_id, idempotency_key)`,
		`CREATE TABLE IF NOT EXISTS instruction_attempts (
            id UUID NOT NULL DEFAULT gen_random_uuid(),
            instruction_id UUID NOT NULL,
            attempt_number INTEGER NOT NULL,
            dispatched_at TIMESTAMPTZ NOT NULL,
            completed_at TIMESTAMPTZ NULL,
            response_status_code INTEGER NULL,
            response_body_preview VARCHAR(1024) NULL,
            error_message TEXT NULL,
            duration_ms BIGINT NULL,
            PRIMARY KEY (id)
        )`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_instruction_attempts_instruction ON instruction_attempts (instruction_id, attempt_number)`,
		`CREATE TABLE IF NOT EXISTS instruction_routes (
            tenant_id UUID NOT NULL,
            instruction_type VARCHAR(255) NOT NULL,
            connection_id UUID NOT NULL,
            fallback_connection_id UUID NULL,
            outbound_mapping VARCHAR(255) NOT NULL DEFAULT '',
            inbound_mapping VARCHAR(255) NOT NULL DEFAULT '',
            http_method VARCHAR(10) NOT NULL DEFAULT '',
            path_template VARCHAR(1024) NOT NULL DEFAULT '',
            status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
            deprecated_at TIMESTAMPTZ NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            PRIMARY KEY (tenant_id, instruction_type),
            FOREIGN KEY (tenant_id, connection_id) REFERENCES provider_connections (tenant_id, connection_id) ON DELETE RESTRICT,
            FOREIGN KEY (tenant_id, fallback_connection_id) REFERENCES provider_connections (tenant_id, connection_id) ON DELETE RESTRICT
        )`,
		`CREATE INDEX IF NOT EXISTS idx_instruction_routes_connection_id ON instruction_routes (tenant_id, connection_id)`,
	}

	for _, stmt := range ddl {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("DDL failed: %w\nSQL: %s", err, stmt)
		}
	}
	return nil
}

// getSharedDB returns the shared database, initializing the container on first call.
func getSharedDB(t *testing.T) *gorm.DB {
	t.Helper()
	sharedOnce.Do(func() {
		sharedInitErr = initSharedContainer()
	})
	if sharedInitErr != nil {
		t.Fatalf("shared CockroachDB setup failed: %v", sharedInitErr)
	}
	return sharedDB
}

// cleanTables truncates all data between tests (FK order: children first).
func cleanTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	tables := []string{"instruction_attempts", "instructions", "instruction_routes", "provider_connections"}
	for _, tbl := range tables {
		if err := db.Exec("DELETE FROM " + tbl).Error; err != nil {
			t.Fatalf("failed to clean table %s: %v", tbl, err)
		}
	}
}

// setupTestDB returns the shared DB with a clean slate and a no-op cleanup.
func setupTestDB(t *testing.T) (*gorm.DB, context.Context) {
	t.Helper()
	db := getSharedDB(t)
	cleanTables(t, db)
	return db, context.Background()
}
