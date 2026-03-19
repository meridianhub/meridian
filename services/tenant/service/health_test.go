package service

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestNewHealthChecker(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	defer cleanup()
	testdb.CreateAuditTables(t, db)
	repo := persistence.NewRepository(db)

	hc := NewHealthChecker(HealthCheckerConfig{
		Repository:  repo,
		ServiceName: "tenant",
		Timeout:     5 * time.Second,
	})

	if hc == nil {
		t.Fatal("Expected non-nil HealthChecker")
	}
	if hc.timeout != 5*time.Second {
		t.Errorf("Expected timeout 5s, got %v", hc.timeout)
	}
	if hc.serviceName != "tenant" {
		t.Errorf("Expected service name 'tenant', got %s", hc.serviceName)
	}
}

func TestNewHealthChecker_Defaults(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	defer cleanup()
	testdb.CreateAuditTables(t, db)
	repo := persistence.NewRepository(db)

	hc := NewHealthChecker(HealthCheckerConfig{
		Repository: repo,
	})

	if hc.timeout <= 0 {
		t.Error("Expected positive default timeout")
	}
	if hc.logger == nil {
		t.Error("Expected non-nil default logger")
	}
}

func TestHealthChecker_Check_Healthy(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	defer cleanup()
	testdb.CreateAuditTables(t, db)
	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	hc := NewHealthChecker(HealthCheckerConfig{
		Repository:  repo,
		Logger:      logger,
		ServiceName: "tenant",
		Timeout:     5 * time.Second,
	})

	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("Expected SERVING, got %v", resp.Status)
	}
}
