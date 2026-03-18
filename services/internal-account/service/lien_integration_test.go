package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const lienIntegrationTenantID = "lien_integration_tenant"

// setupLienIntegrationDB sets up a CockroachDB testcontainer with both
// internal_account and lien tables, returning a service ready for lien tests.
func setupLienIntegrationDB(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, nil)

	tid := tenant.TenantID(lienIntegrationTenantID)
	schemaName := tid.SchemaName()

	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.internal_account (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_code VARCHAR(50) NOT NULL,
		name VARCHAR(255) NOT NULL,
		account_type VARCHAR(20) NOT NULL DEFAULT 'CLEARING',
		clearing_purpose VARCHAR(32) NULL,
		org_party_id UUID NULL,
		product_type_code VARCHAR(100) NULL,
		product_type_version INTEGER NULL,
		instrument_code VARCHAR(32) NOT NULL DEFAULT 'GBP',
		dimension VARCHAR(32) NOT NULL DEFAULT 'CURRENCY',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		counterparty_id VARCHAR(50),
		counterparty_name VARCHAR(255),
		counterparty_external_ref VARCHAR(100),
		attributes JSONB NOT NULL DEFAULT '{}',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		created_by VARCHAR(100) NOT NULL DEFAULT 'system',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_by VARCHAR(100) NOT NULL DEFAULT 'system',
		deleted_at TIMESTAMPTZ,
		version BIGINT NOT NULL DEFAULT 1
	)`, schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.lien (
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		amount_cents BIGINT NOT NULL,
		instrument_code VARCHAR(32) NOT NULL,
		bucket_id VARCHAR(255) NOT NULL DEFAULT '',
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		payment_order_reference VARCHAR(255) NOT NULL,
		termination_reason VARCHAR(1000) NOT NULL DEFAULT '',
		expires_at TIMESTAMPTZ,
		reserved_quantity JSONB,
		valued_amount JSONB,
		valuation_analysis JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		version INTEGER NOT NULL DEFAULT 1,
		CONSTRAINT idx_lien_payment_order UNIQUE (payment_order_reference)
	)`, schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	lienRepo := persistence.NewLienRepository(db)
	refClient := newInstrumentMap(map[string]int32{"GBP": 2, "USD": 2, "EUR": 2})

	svc, err := NewServiceFull(repo, nil, refClient, testLogger(), nil,
		WithLienRepo(lienRepo),
	)
	require.NoError(t, err)

	return svc, db, ctx, cleanup
}

// insertTestAccount inserts a minimal CLEARING account and returns its UUID.
func insertTestAccount(t *testing.T, db *gorm.DB, accountID, accountCode string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	err := db.Exec(`INSERT INTO internal_account (id, account_id, account_code, name, account_type, clearing_purpose, instrument_code, dimension, status, created_by, updated_by)
		VALUES (?, ?, ?, 'Test Account', 'CLEARING', 'GENERAL', 'GBP', 'CURRENCY', 'ACTIVE', 'test', 'test')`,
		id, accountID, accountCode).Error
	require.NoError(t, err)
	return id
}

// ---------------------------------------------------------------------------
// InitiateLien integration tests
// ---------------------------------------------------------------------------

func TestInitiateLien_Integration_Success(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-LIEN-INT-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("LIENINT-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	resp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-INT-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "100.00",
			InstrumentCode: "GBP",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Lien)
	assert.NotEmpty(t, resp.Lien.LienId)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, resp.Lien.Status)
	assert.NotEmpty(t, resp.Lien.Amount.Amount)
}

func TestInitiateLien_Integration_Idempotent(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-LIEN-IDEMP-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("LIENIDEMP-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	ref := "PAY-IDEMP-001"
	resp1, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: ref,
		Input:                 &quantityv1.InstrumentAmount{Amount: "50.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp1)

	// Second call with same reference → idempotent, returns same lien
	resp2, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: ref,
		Input:                 &quantityv1.InstrumentAmount{Amount: "50.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp2)
	assert.Equal(t, resp1.Lien.LienId, resp2.Lien.LienId)
}

func TestInitiateLien_Integration_DuplicateRefDifferentAccount(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	acc1ID := fmt.Sprintf("IBA-LIENDUP1-%s", uuid.New().String()[:8])
	acc1Code := fmt.Sprintf("LIENDUP1-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, acc1ID, acc1Code)

	acc2ID := fmt.Sprintf("IBA-LIENDUP2-%s", uuid.New().String()[:8])
	acc2Code := fmt.Sprintf("LIENDUP2-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, acc2ID, acc2Code)

	ref := "PAY-DUPREF-001"

	// Create lien for account 1
	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             acc1ID,
		PaymentOrderReference: ref,
		Input:                 &quantityv1.InstrumentAmount{Amount: "25.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)

	// Same reference but different account → InvalidArgument
	_, err = svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             acc2ID,
		PaymentOrderReference: ref,
		Input:                 &quantityv1.InstrumentAmount{Amount: "25.00", InstrumentCode: "GBP"},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestInitiateLien_Integration_LienRepoInternalError(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-LIENERR-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("LIENERR-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	// Use an amount with too many decimal places for GBP (precision=2)
	_, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-ERRDECIMAL-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.001",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// RetrieveLien integration tests
// ---------------------------------------------------------------------------

func TestRetrieveLien_Integration_Success(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-RETLIEN-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("RETLIEN-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	// Create a lien first
	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-RETRIEVE-001",
		Input:                 &quantityv1.InstrumentAmount{Amount: "75.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	// Retrieve it
	retResp, err := svc.RetrieveLien(ctx, &pb.RetrieveLienRequest{LienId: lienID})
	require.NoError(t, err)
	require.NotNil(t, retResp)
	assert.Equal(t, lienID, retResp.Lien.LienId)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_ACTIVE, retResp.Lien.Status)
}

func TestRetrieveLien_Integration_NotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	_, err := svc.RetrieveLien(ctx, &pb.RetrieveLienRequest{LienId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// ExecuteLien integration tests
// ---------------------------------------------------------------------------

func TestExecuteLien_Integration_Success(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-EXECLIEN-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("EXECLIEN-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-EXEC-001",
		Input:                 &quantityv1.InstrumentAmount{Amount: "200.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	execResp, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lienID})
	require.NoError(t, err)
	require.NotNil(t, execResp)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, execResp.Lien.Status)
}

func TestExecuteLien_Integration_AlreadyExecuted_Idempotent(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-EXECIDEM-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("EXECIDEM-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-EXEC-IDEM-001",
		Input:                 &quantityv1.InstrumentAmount{Amount: "30.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	// Execute once
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lienID})
	require.NoError(t, err)

	// Execute again → idempotent (already executed)
	execResp2, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lienID})
	require.NoError(t, err)
	require.NotNil(t, execResp2)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, execResp2.Lien.Status)
}

func TestExecuteLien_Integration_NotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	_, err := svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestExecuteLien_Integration_LienTerminated_NotActive(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-EXECTERM-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("EXECTERM-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-EXECTERM-001",
		Input:                 &quantityv1.InstrumentAmount{Amount: "15.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	// Terminate the lien first
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lienID,
		Reason: "test termination",
	})
	require.NoError(t, err)

	// Now try to execute → FailedPrecondition (not active)
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lienID})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// TerminateLien integration tests
// ---------------------------------------------------------------------------

func TestTerminateLien_Integration_Success(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-TERMLIEN-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("TERMLIEN-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-TERM-001",
		Input:                 &quantityv1.InstrumentAmount{Amount: "50.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	termResp, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: lienID,
		Reason: "payment cancelled",
	})
	require.NoError(t, err)
	require.NotNil(t, termResp)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, termResp.Lien.Status)
}

func TestTerminateLien_Integration_AlreadyTerminated_Idempotent(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-TERMIDEMP-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("TERMIDEMP-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-TERM-IDEM-001",
		Input:                 &quantityv1.InstrumentAmount{Amount: "10.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	// Terminate once
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{LienId: lienID, Reason: "first"})
	require.NoError(t, err)

	// Terminate again → idempotent
	termResp2, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{LienId: lienID, Reason: "second"})
	require.NoError(t, err)
	require.NotNil(t, termResp2)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, termResp2.Lien.Status)
}

func TestTerminateLien_Integration_NotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	_, err := svc.TerminateLien(ctx, &pb.TerminateLienRequest{
		LienId: uuid.New().String(),
		Reason: "test",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestTerminateLien_Integration_AlreadyExecuted_NotActive(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-TERMEXEC-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("TERMEXEC-%s", uuid.New().String()[:6])
	insertTestAccount(t, db, accountID, accountCode)

	initResp, err := svc.InitiateLien(ctx, &pb.InitiateLienRequest{
		AccountId:             accountID,
		PaymentOrderReference: "PAY-TERMEXEC-001",
		Input:                 &quantityv1.InstrumentAmount{Amount: "40.00", InstrumentCode: "GBP"},
	})
	require.NoError(t, err)
	lienID := initResp.Lien.LienId

	// Execute the lien first
	_, err = svc.ExecuteLien(ctx, &pb.ExecuteLienRequest{LienId: lienID})
	require.NoError(t, err)

	// Now try to terminate → FailedPrecondition (not active)
	_, err = svc.TerminateLien(ctx, &pb.TerminateLienRequest{LienId: lienID, Reason: "too late"})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// HealthChecker.Check – all-components path (triggers logHealthCheck + DatabaseHealthChecker.Check)
// Uses a real DB so DatabaseHealthChecker.Ping() does not panic.
// ---------------------------------------------------------------------------

func TestHealthChecker_Check_AllComponents_WithRealDB(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	repo := persistence.NewRepository(db)
	pkHealthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		Repository:                  repo,
		PositionKeepingHealthClient: pkHealthClient,
		Logger:                      testLogger(),
	})
	require.NoError(t, err)

	// Empty service name → checks all components, triggers logHealthCheck + DatabaseHealthChecker.Check
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	// Database is available (testcontainer), so should be SERVING
	assert.NotEqual(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}

func TestHealthChecker_Check_ServiceName_Match(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	repo := persistence.NewRepository(db)
	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		Repository:  repo,
		ServiceName: "internal-account",
		Logger:      testLogger(),
	})
	require.NoError(t, err)

	// Service name matches → checks all components
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "internal-account",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
}
