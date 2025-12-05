package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver for testing
	"github.com/meridianhub/meridian/shared/pkg/health"
)

const testDatabaseName = "database"

func TestGormDBChecker_Name(t *testing.T) {
	// Create a minimal database connection for testing
	db, err := sql.Open("postgres", "postgres://test:test@localhost:5432/testdb?sslmode=disable")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	checker := NewGormDBChecker(db)

	if got := checker.Name(); got != testDatabaseName {
		t.Errorf("Name() = %q, want %q", got, testDatabaseName)
	}
}

func TestGormDBChecker_NilDB(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewGormDBChecker(nil) did not panic")
		}
	}()

	_ = NewGormDBChecker(nil)
}

func TestGormDBChecker_Check_ReturnsResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a minimal database connection for testing
	db, err := sql.Open("postgres", "postgres://test:test@localhost:5432/testdb?sslmode=disable")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	checker := NewGormDBChecker(db)
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
