package saga

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

// sharedDB holds the shared CockroachDB connection for all integration tests.
// Lazily initialized on first use via sync.Once to avoid starting the container
// when only unit tests run (e.g. -short mode).
var (
	sharedDB      *gorm.DB
	sharedOnce    sync.Once
	sharedInitErr error
	sharedCleanup func()
)

func TestMain(m *testing.M) {
	code := m.Run()

	if sharedCleanup != nil {
		sharedCleanup()
	}
	os.Exit(code)
}

func initSharedContainer() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	if err != nil {
		_ = crdbContainer.Terminate(ctx)
		return fmt.Errorf("connection config: %w", err)
	}

	db, err := gorm.Open(gormpg.Open(connConfig.ConnString()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = crdbContainer.Terminate(ctx)
		return fmt.Errorf("gorm open: %w", err)
	}

	// Run migrations once — subsequent RunSagaMigrations calls in tests
	// are idempotent no-ops (tables/indexes already exist).
	if err := RunSagaMigrations(db); err != nil {
		_ = crdbContainer.Terminate(ctx)
		return fmt.Errorf("migrations: %w", err)
	}

	sharedDB = db
	sharedCleanup = func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = crdbContainer.Terminate(cleanupCtx)
	}

	return nil
}

// setupTestPostgres returns the shared CockroachDB connection with per-test
// data cleanup. Named setupTestPostgres for historical compatibility —
// CockroachDB is wire-compatible with PostgreSQL and is our production database.
//
// Previously each test started its own CockroachDB container (~15s startup).
// With ~60 integration tests the package exceeded the 15-minute timeout.
// Sharing one container reduces total overhead from ~15 minutes to ~15 seconds.
func setupTestPostgres(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	sharedOnce.Do(func() {
		sharedInitErr = initSharedContainer()
	})
	if sharedInitErr != nil {
		t.Fatalf("shared CockroachDB setup failed: %v", sharedInitErr)
	}

	// Clean tables before each test (FK order: children first).
	if err := sharedDB.Exec("DELETE FROM saga_step_results").Error; err != nil {
		t.Fatalf("Failed to clean saga_step_results: %v", err)
	}
	if err := sharedDB.Exec("DELETE FROM saga_instances").Error; err != nil {
		t.Fatalf("Failed to clean saga_instances: %v", err)
	}
	if err := sharedDB.Exec("DELETE FROM saga_definitions").Error; err != nil {
		t.Fatalf("Failed to clean saga_definitions: %v", err)
	}

	return sharedDB, func() {
		// No-op: container lifecycle managed by TestMain.
	}
}
