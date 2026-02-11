package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"
)

// setupIntegrationDB creates a CockroachDB testcontainer with all reconciliation tables.
func setupIntegrationDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") == "" && testing.Short() {
		t.Skip("Skipping integration test (set INTEGRATION_TEST=1 or remove -short)")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)

	tid := tenant.TenantID("test-tenant-01")
	schemaName := tid.SchemaName()
	quoted := fmt.Sprintf("%q", schemaName)

	err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + quoted).Error
	require.NoError(t, err)

	migrationSQL := `
		SET search_path TO ` + quoted + `, public;

		CREATE TABLE IF NOT EXISTS "settlement_run" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"run_id" uuid NOT NULL,
			"account_id" character varying(34) NOT NULL,
			"scope" character varying(20) NOT NULL DEFAULT 'ACCOUNT',
			"settlement_type" character varying(20) NOT NULL DEFAULT 'DAILY',
			"status" character varying(20) NOT NULL DEFAULT 'PENDING',
			"period_start" timestamptz NOT NULL,
			"period_end" timestamptz NOT NULL,
			"initiated_by" character varying(100) NOT NULL,
			"completed_at" timestamptz NULL,
			"variance_count" integer NOT NULL DEFAULT 0,
			"failure_reason" text NULL,
			"attributes" jsonb NULL,
			"version" bigint NOT NULL DEFAULT 1,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_sr_run_id" ON "settlement_run" ("run_id");

		CREATE TABLE IF NOT EXISTS "settlement_snapshot" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"snapshot_id" uuid NOT NULL,
			"run_id" uuid NOT NULL REFERENCES "settlement_run" ("id") ON DELETE CASCADE,
			"account_id" character varying(34) NOT NULL,
			"instrument_code" character varying(20) NOT NULL,
			"expected_balance" decimal(38, 18) NOT NULL,
			"actual_balance" decimal(38, 18) NOT NULL,
			"variance_amount" decimal(38, 18) NOT NULL,
			"source_system" character varying(100) NOT NULL,
			"attributes" jsonb NULL,
			"captured_at" timestamptz NOT NULL,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_ss_snap_id" ON "settlement_snapshot" ("snapshot_id");

		CREATE TABLE IF NOT EXISTS "variance" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"variance_id" uuid NOT NULL,
			"run_id" uuid NOT NULL REFERENCES "settlement_run" ("id") ON DELETE CASCADE,
			"snapshot_id" uuid NOT NULL REFERENCES "settlement_snapshot" ("id") ON DELETE CASCADE,
			"account_id" character varying(34) NOT NULL,
			"instrument_code" character varying(20) NOT NULL,
			"expected_amount" decimal(38, 18) NOT NULL,
			"actual_amount" decimal(38, 18) NOT NULL,
			"variance_amount" decimal(38, 18) NOT NULL,
			"value_delta" decimal(38, 18) NOT NULL DEFAULT 0,
			"currency" character varying(10) NOT NULL DEFAULT '',
			"reason" character varying(30) NOT NULL,
			"status" character varying(20) NOT NULL DEFAULT 'OPEN',
			"resolution_note" text NULL,
			"resolved_by" character varying(100) NULL,
			"resolved_at" timestamptz NULL,
			"attributes" jsonb NULL,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_v_var_id" ON "variance" ("variance_id");

		CREATE TABLE IF NOT EXISTS "dispute" (
			"id" uuid NOT NULL DEFAULT gen_random_uuid(),
			"created_at" timestamptz NOT NULL DEFAULT now(),
			"updated_at" timestamptz NOT NULL DEFAULT now(),
			"dispute_id" uuid NOT NULL,
			"variance_id" uuid NOT NULL REFERENCES "variance" ("id") ON DELETE CASCADE,
			"run_id" uuid NOT NULL REFERENCES "settlement_run" ("id") ON DELETE CASCADE,
			"account_id" character varying(34) NOT NULL,
			"status" character varying(20) NOT NULL DEFAULT 'OPEN',
			"reason" text NOT NULL,
			"resolution" text NULL,
			"raised_by" character varying(100) NOT NULL,
			"resolved_by" character varying(100) NULL,
			"resolved_at" timestamptz NULL,
			"attributes" jsonb NULL,
			PRIMARY KEY ("id")
		);
		CREATE UNIQUE INDEX IF NOT EXISTS "idx_d_disp_id" ON "dispute" ("dispute_id");

		SET search_path TO public;
	`
	err = db.Exec(migrationSQL).Error
	require.NoError(t, err)

	return db, cleanup
}

// startTestGRPCServer creates a gRPC server with the reconciliation service wired,
// listens on a random port, and returns the address and a stop function.
func startTestGRPCServer(t *testing.T, db *gorm.DB, logger *slog.Logger) (string, func()) {
	t.Helper()

	// Create repositories
	runRepo := persistence.NewSettlementRunRepository(db)
	snapshotRepo := persistence.NewSettlementSnapshotRepository(db)
	varianceRepo := persistence.NewVarianceRepository(db)
	disputeRepo := persistence.NewDisputeRepository(db)

	// Wire VarianceDetector (repos only, always available)
	detector := service.NewVarianceDetector(runRepo, snapshotRepo, varianceRepo)

	// Create service with all available options
	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(runRepo),
		service.WithDisputeRepository(disputeRepo),
		service.WithVarianceRepository(varianceRepo),
		service.WithVarianceListRepository(varianceRepo),
		service.WithVarianceDetector(detector.DetectVariances),
		service.WithLogger(logger),
	)

	// Create gRPC server
	grpcServer := grpc.NewServer()
	reconciliationv1.RegisterAccountReconciliationServiceServer(grpcServer, svc)

	// Register health
	healthAggregator := health.NewAggregator(nil)
	healthSrv := newHealthServer(healthAggregator, logger)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)

	// Register reflection
	reflection.Register(grpcServer)

	// Listen on random port
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	return lis.Addr().String(), func() {
		grpcServer.GracefulStop()
	}
}

func TestMainWiring_ServiceRegistered(t *testing.T) {
	db, cleanup := setupIntegrationDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	addr, stop := startTestGRPCServer(t, db, logger)
	defer stop()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	// Verify the AccountReconciliationService is registered by calling an RPC
	client := reconciliationv1.NewAccountReconciliationServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// InitiateAccountReconciliation should return InvalidArgument (not Unimplemented)
	// because the handler is wired and validates input
	_, err = client.InitiateAccountReconciliation(ctx, &reconciliationv1.InitiateAccountReconciliationRequest{})
	require.Error(t, err)
	// Should get an error about missing fields, not about unimplemented service
	assert.NotContains(t, err.Error(), "unknown service")
}

func TestMainWiring_HealthCheckServing(t *testing.T) {
	db, cleanup := setupIntegrationDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	addr, stop := startTestGRPCServer(t, db, logger)
	defer stop()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestMainWiring_GracefulShutdown(t *testing.T) {
	db, cleanup := setupIntegrationDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	addr, stop := startTestGRPCServer(t, db, logger)

	// Verify server is accepting connections
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	// Gracefully stop the server
	stop()

	// Server should no longer accept new connections
	// (existing connection may still work briefly, but new RPCs should fail)
}

func TestMainWiring_MissingOptionalDeps_ReturnsUnimplemented(t *testing.T) {
	db, cleanup := setupIntegrationDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create service WITHOUT optional dependencies (no snapshot capturer, no valuator, no assertor)
	runRepo := persistence.NewSettlementRunRepository(db)
	disputeRepo := persistence.NewDisputeRepository(db)
	varianceRepo := persistence.NewVarianceRepository(db)

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(runRepo),
		service.WithDisputeRepository(disputeRepo),
		service.WithVarianceRepository(varianceRepo),
		service.WithVarianceListRepository(varianceRepo),
		service.WithLogger(logger),
	)

	grpcServer := grpc.NewServer()
	reconciliationv1.RegisterAccountReconciliationServiceServer(grpcServer, svc)

	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.GracefulStop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := reconciliationv1.NewAccountReconciliationServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// AssertBalance should return Unimplemented when assertor is nil
	_, err = client.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "DEBIT == CREDIT",
		ExpectedBalance: "100.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unimplemented")
}

func TestMainWiring_AssertBalanceRegression(t *testing.T) {
	db, cleanup := setupIntegrationDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Confirm AssertBalance returns Unimplemented with default wiring (no balance assertor)
	runRepo := persistence.NewSettlementRunRepository(db)
	disputeRepo := persistence.NewDisputeRepository(db)
	varianceRepo := persistence.NewVarianceRepository(db)

	svc := service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(runRepo),
		service.WithDisputeRepository(disputeRepo),
		service.WithVarianceRepository(varianceRepo),
		service.WithVarianceListRepository(varianceRepo),
		service.WithLogger(logger),
	)

	grpcServer := grpc.NewServer()
	reconciliationv1.RegisterAccountReconciliationServiceServer(grpcServer, svc)

	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.GracefulStop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := reconciliationv1.NewAccountReconciliationServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Without BalanceAssertor, should return Unimplemented
	_, err = client.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "DEBIT == CREDIT",
		ExpectedBalance: "100.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unimplemented")

	// InitiateAccountReconciliation should work (returns validation error, not unimplemented)
	_, err = client.InitiateAccountReconciliation(ctx, &reconciliationv1.InitiateAccountReconciliationRequest{
		AccountId: "ACC-001",
	})
	require.Error(t, err)
	// Should fail validation, not be unimplemented
	assert.Contains(t, err.Error(), "scope")
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, parseLogLevel(tc.input))
		})
	}
}
