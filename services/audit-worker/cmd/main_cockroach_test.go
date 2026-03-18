package main

import (
	"context"
	"os"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

func TestSetupDatabase_WithRealDB(t *testing.T) {
	container, cleanup := testdb.StartCockroachContainer(t, "cmd_test_db")
	t.Cleanup(cleanup)

	dsn := testdb.CockroachDSN(t, container)

	orig := os.Getenv("DATABASE_URL")
	defer func() {
		if orig != "" {
			_ = os.Setenv("DATABASE_URL", orig)
		} else {
			_ = os.Unsetenv("DATABASE_URL")
		}
	}()

	_ = os.Setenv("DATABASE_URL", dsn)

	ctx := context.Background()
	db, err := setupDatabase(ctx)
	if err != nil {
		t.Fatalf("setupDatabase() unexpected error: %v", err)
	}
	if db == nil {
		t.Fatal("setupDatabase() returned nil db")
	}

	// Close the db
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error: %v", err)
	}
	_ = sqlDB.Close()
}
