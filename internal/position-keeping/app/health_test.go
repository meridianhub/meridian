package app

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/pkg/platform/health"
)

const testDatabaseName = "database"

func TestPgxPoolChecker_Name(t *testing.T) {
	// Create minimal config for test pool
	config := &Config{
		Database: DatabaseConfig{
			URL:          "postgres://test:test@localhost:5432/testdb",
			MaxOpenConns: 5,
			MaxIdleConns: 2,
		},
	}

	poolConfig, err := pgxpool.ParseConfig(config.Database.URL)
	if err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Create pool (won't connect until used)
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	checker := NewPgxPoolChecker(pool)

	if got := checker.Name(); got != testDatabaseName {
		t.Errorf("Name() = %q, want %q", got, testDatabaseName)
	}
}

func TestPgxPoolChecker_NilPool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewPgxPoolChecker(nil) did not panic")
		}
	}()

	_ = NewPgxPoolChecker(nil)
}

func TestPgxPoolChecker_Check_ReturnsResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create minimal config for test pool
	config := &Config{
		Database: DatabaseConfig{
			URL:          "postgres://test:test@localhost:5432/testdb",
			MaxOpenConns: 5,
			MaxIdleConns: 2,
		},
	}

	poolConfig, err := pgxpool.ParseConfig(config.Database.URL)
	if err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Create pool (connection will likely fail without real database)
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	checker := NewPgxPoolChecker(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := checker.Check(ctx)

	// Verify result structure (database likely won't connect in test environment)
	if result.Name != testDatabaseName {
		t.Errorf("result.Name = %q, want %q", result.Name, testDatabaseName)
	}

	if result.CheckedAt.IsZero() {
		t.Error("result.CheckedAt is zero, want non-zero timestamp")
	}

	if result.ResponseTime == 0 {
		t.Error("result.ResponseTime is zero, want non-zero duration")
	}

	// Status should be either healthy or unhealthy (not unknown or degraded)
	if result.Status != health.StatusHealthy && result.Status != health.StatusUnhealthy {
		t.Errorf("result.Status = %v, want either StatusHealthy or StatusUnhealthy", result.Status)
	}

	// If unhealthy, should have error and message
	if result.Status == health.StatusUnhealthy {
		if result.Error == nil {
			t.Error("result.Error is nil for unhealthy status")
		}
		if result.Message == "" {
			t.Error("result.Message is empty for unhealthy status")
		}
	}
}
