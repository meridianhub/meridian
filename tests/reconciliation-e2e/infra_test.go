//go:build integration
// +build integration

package reconciliatione2e

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"
)

// =============================================================================
// Test Infrastructure
// =============================================================================

// e2eTestInfra holds all test infrastructure for reconciliation E2E tests.
type e2eTestInfra struct {
	db      *gorm.DB
	cleanup func()
	ctx     context.Context // tenant-scoped context

	// gRPC transport (bufconn)
	grpcClient reconciliationv1.AccountReconciliationServiceClient

	// Repositories
	runRepo       domain.SettlementRunRepository
	snapRepo      domain.SettlementSnapshotRepository
	varianceRepo  domain.VarianceRepository
	disputeRepo   *persistence.DisputeRepository
	varianceFetch *persistence.VarianceRepository
	assertionRepo domain.BalanceAssertionRepository
	trendRepo     domain.ImbalanceTrendRepository

	// Service components
	capturer    *service.SnapshotCapturer
	detector    *service.VarianceDetector
	valuator    *service.VarianceValuator
	finalizer   *service.SettlementFinalizer
	assertor    *service.BalanceAssertor
	grpcService *service.AccountReconciliationService

	// Mocks
	mockPKProvider *mockPositionDataProvider
	mockPKClient   *mockPositionKeepingClient
	mockFAClient   *mockFinancialAccountingClient
	mockRefData    *mockReferenceDataProvider
	mockValEngine  *mockValuationEngine
	mockLockClient *mockPositionLockClient
	mockSagaRT     *mockSagaRuntime
	mockPublisher  *mockEventPublisher
}

// setupE2EInfra creates all test infrastructure.
func setupE2EInfra(t *testing.T) *e2eTestInfra {
	t.Helper()

	infra := &e2eTestInfra{}

	// Set up CockroachDB testcontainer
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	infra.db = db
	infra.cleanup = cleanup

	// Limit the connection pool to 1 so all operations (including concurrent
	// errgroup goroutines) share the same connection with search_path set.
	// Without this, new pool connections would miss the tenant search_path.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// Set up tenant schema and apply migrations
	tc := testdb.SetupTenantSchema(t, db, "e2e_recon_tenant")
	t.Cleanup(tc.Cleanup)

	// Apply reconciliation service schema DDL
	applyMigrations(t, db, tc.Tenant)

	// Store tenant context
	infra.ctx = tc.Ctx

	// Create repositories
	infra.disputeRepo = persistence.NewDisputeRepository(db)
	infra.varianceFetch = persistence.NewVarianceRepository(db)

	// Create GORM-based repositories for other entities
	infra.runRepo = newGormRunRepository(db)
	infra.snapRepo = newGormSnapshotRepository(db)
	infra.varianceRepo = newGormVarianceRepository(db)
	infra.assertionRepo = newGormAssertionRepository(db)
	infra.trendRepo = newGormTrendRepository(db)

	// Create mocks
	infra.mockPKProvider = &mockPositionDataProvider{}
	infra.mockPKClient = &mockPositionKeepingClient{}
	infra.mockFAClient = &mockFinancialAccountingClient{}
	infra.mockRefData = &mockReferenceDataProvider{
		methods:    make(map[string]uuid.UUID),
		thresholds: make(map[string]decimal.Decimal),
	}
	infra.mockValEngine = &mockValuationEngine{}
	infra.mockLockClient = &mockPositionLockClient{}
	infra.mockSagaRT = &mockSagaRuntime{}
	infra.mockPublisher = &mockEventPublisher{}

	// Wire up service components
	logger := slog.Default()

	infra.capturer = service.NewSnapshotCapturer(
		infra.mockPKProvider,
		infra.runRepo,
		infra.snapRepo,
	)

	infra.detector = service.NewVarianceDetector(
		infra.runRepo,
		infra.snapRepo,
		infra.varianceRepo,
	)

	infra.valuator = service.NewVarianceValuator(
		infra.mockValEngine,
		infra.mockRefData,
		nil, // party resolver not needed in e2e tests (falls back to account ID)
		infra.varianceRepo,
		infra.runRepo,
	)

	infra.finalizer = service.NewSettlementFinalizer(
		infra.runRepo,
		infra.snapRepo,
		infra.mockLockClient,
		infra.mockPublisher,
		logger,
	)

	infra.assertor = service.NewBalanceAssertor(
		infra.assertionRepo,
		infra.trendRepo,
		infra.mockPKClient,
		infra.mockFAClient,
		infra.mockPublisher,
		logger,
	)

	infra.grpcService = service.NewAccountReconciliationService(
		service.WithSettlementRunRepository(infra.runRepo),
		service.WithDisputeRepository(infra.disputeRepo),
		service.WithVarianceRepository(infra.varianceFetch),
		service.WithVarianceListRepository(infra.varianceFetch),
		service.WithSagaRuntime(infra.mockSagaRT),
		service.WithEventPublisher(infra.mockPublisher),
		service.WithBalanceAssertor(infra.assertor),
		service.WithSnapshotCapturer(infra.capturer.CaptureSnapshots),
		service.WithVarianceDetector(infra.detector.DetectVariances),
		service.WithVarianceValuator(infra.valuator.ValueVariances),
		service.WithLogger(logger),
	)

	// Start in-process gRPC server via bufconn
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	reconciliationv1.RegisterAccountReconciliationServiceServer(grpcServer, infra.grpcService)

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = grpcServer.Serve(listener)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	infra.grpcClient = reconciliationv1.NewAccountReconciliationServiceClient(conn)

	t.Cleanup(func() {
		conn.Close()
		grpcServer.GracefulStop()
		<-serveDone
		listener.Close()
		cleanup()
	})

	return infra
}

// applyMigrations applies reconciliation service SQL migrations to the tenant schema.
// Since search_path is already set by SetupTenantSchema, we use unqualified table names.
func applyMigrations(t *testing.T, db *gorm.DB, _ tenant.TenantID) {
	t.Helper()

	ddls := []string{
		// settlement_run
		`CREATE TABLE IF NOT EXISTS settlement_run (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			run_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			scope character varying(20) NOT NULL,
			settlement_type character varying(20) NOT NULL,
			status character varying(20) NOT NULL DEFAULT 'PENDING',
			period_start timestamptz NOT NULL,
			period_end timestamptz NOT NULL,
			initiated_by character varying(100) NOT NULL,
			completed_at timestamptz NULL,
			variance_count integer NOT NULL DEFAULT 0,
			failure_reason text NULL,
			last_completed_phase character varying(30) NULL,
			attributes jsonb NULL,
			version bigint NOT NULL DEFAULT 1,
			PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sr_run_id ON settlement_run (run_id)`,

		// settlement_snapshot
		`CREATE TABLE IF NOT EXISTS settlement_snapshot (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			snapshot_id uuid NOT NULL,
			run_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			instrument_code character varying(20) NOT NULL,
			expected_balance decimal(38, 18) NOT NULL,
			actual_balance decimal(38, 18) NOT NULL,
			variance_amount decimal(38, 18) NOT NULL,
			source_system character varying(100) NOT NULL,
			attributes jsonb NULL,
			captured_at timestamptz NOT NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ss_snapshot_id ON settlement_snapshot (snapshot_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ss_run_id ON settlement_snapshot (run_id)`,

		// variance
		`CREATE TABLE IF NOT EXISTS variance (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			variance_id uuid NOT NULL,
			run_id uuid NOT NULL,
			snapshot_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			instrument_code character varying(20) NOT NULL,
			expected_amount decimal(38, 18) NOT NULL,
			actual_amount decimal(38, 18) NOT NULL,
			variance_amount decimal(38, 18) NOT NULL,
			value_delta decimal(38, 18) NOT NULL DEFAULT 0,
			currency character varying(10) NOT NULL DEFAULT '',
			reason character varying(30) NOT NULL,
			status character varying(20) NOT NULL DEFAULT 'DETECTED',
			resolution_note text NULL,
			resolved_by character varying(100) NULL,
			resolved_at timestamptz NULL,
			attributes jsonb NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_v_variance_id ON variance (variance_id)`,
		`CREATE INDEX IF NOT EXISTS idx_v_run_id ON variance (run_id)`,

		// dispute
		`CREATE TABLE IF NOT EXISTS dispute (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			dispute_id uuid NOT NULL,
			variance_id uuid NOT NULL,
			run_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			status character varying(20) NOT NULL DEFAULT 'OPEN',
			reason text NOT NULL,
			resolution text NULL,
			raised_by character varying(100) NOT NULL,
			resolved_by character varying(100) NULL,
			resolved_at timestamptz NULL,
			attributes jsonb NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_d_dispute_id ON dispute (dispute_id)`,

		// balance_assertion
		`CREATE TABLE IF NOT EXISTS balance_assertion (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			assertion_id uuid NOT NULL,
			run_id uuid NULL,
			account_id character varying(34) NOT NULL,
			instrument_code character varying(20) NOT NULL,
			expression text NOT NULL,
			expected_balance decimal(38, 18) NOT NULL,
			actual_balance decimal(38, 18) NOT NULL DEFAULT 0,
			status character varying(20) NOT NULL DEFAULT 'PENDING',
			failure_reason text NULL,
			override_reason text NULL,
			attributes jsonb NULL,
			asserted_at timestamptz NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ba_assertion_id ON balance_assertion (assertion_id)`,

		// imbalance_trend
		`CREATE TABLE IF NOT EXISTS imbalance_trend (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			trend_id uuid NOT NULL,
			instrument_code character varying(20) NOT NULL,
			consecutive_days integer NOT NULL DEFAULT 0,
			last_imbalance_amount decimal(38, 18) NOT NULL DEFAULT 0,
			last_assertion_id uuid NULL,
			first_detected_at timestamptz NOT NULL,
			last_detected_at timestamptz NOT NULL,
			resolved_at timestamptz NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_it_trend_id ON imbalance_trend (trend_id)`,
	}

	for _, ddl := range ddls {
		err := db.Exec(ddl).Error
		require.NoError(t, err, "failed to execute DDL: %s", ddl[:min(len(ddl), 80)])
	}
}

// =============================================================================
// Mock Implementations
// =============================================================================

// mockPositionDataProvider implements service.PositionDataProvider for snapshot capture.
type mockPositionDataProvider struct {
	mu       sync.RWMutex
	pages    []service.PositionPage
	fetchErr error
}

func (m *mockPositionDataProvider) FetchPositions(_ context.Context, _ string, _ int32, pageToken string) (*service.PositionPage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.fetchErr != nil {
		return nil, m.fetchErr
	}

	if len(m.pages) == 0 {
		return &service.PositionPage{}, nil
	}

	pageIdx := 0
	if pageToken != "" {
		for i, p := range m.pages {
			if p.NextPageToken == pageToken {
				pageIdx = i + 1
				break
			}
		}
	}

	if pageIdx >= len(m.pages) {
		return &service.PositionPage{}, nil
	}

	return &m.pages[pageIdx], nil
}

func (m *mockPositionDataProvider) setPages(pages []service.PositionPage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pages = pages
}

func (m *mockPositionDataProvider) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetchErr = err
}

// mockPositionKeepingClient implements service.PositionKeepingClient for balance assertions.
type mockPositionKeepingClient struct {
	mu        sync.RWMutex
	summaries map[string]*service.PositionSummary
	err       error
}

func (m *mockPositionKeepingClient) GetPositionSummary(_ context.Context, accountID, instrumentCode string) (*service.PositionSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return nil, m.err
	}

	key := accountID + "|" + instrumentCode
	if s, ok := m.summaries[key]; ok {
		return s, nil
	}

	return &service.PositionSummary{
		InstrumentCode: instrumentCode,
		TotalDebits:    decimal.Zero,
		TotalCredits:   decimal.Zero,
	}, nil
}

func (m *mockPositionKeepingClient) setSummary(accountID, instrumentCode string, debits, credits decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.summaries == nil {
		m.summaries = make(map[string]*service.PositionSummary)
	}
	m.summaries[accountID+"|"+instrumentCode] = &service.PositionSummary{
		InstrumentCode: instrumentCode,
		TotalDebits:    debits,
		TotalCredits:   credits,
	}
}

func (m *mockPositionKeepingClient) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// mockFinancialAccountingClient implements service.FinancialAccountingClient.
type mockFinancialAccountingClient struct {
	mu      sync.RWMutex
	details map[string]*service.DiagnosticDetail
	err     error
}

func (m *mockFinancialAccountingClient) GetDiagnosticDetail(_ context.Context, accountID, instrumentCode string) (*service.DiagnosticDetail, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return nil, m.err
	}

	key := accountID + "|" + instrumentCode
	if d, ok := m.details[key]; ok {
		return d, nil
	}

	return &service.DiagnosticDetail{
		AccountID:      accountID,
		InstrumentCode: instrumentCode,
		Message:        "no diagnostic detail available",
	}, nil
}

// mockReferenceDataProvider implements service.ReferenceDataProvider.
type mockReferenceDataProvider struct {
	mu         sync.RWMutex
	methods    map[string]uuid.UUID
	thresholds map[string]decimal.Decimal
	err        error
}

func (m *mockReferenceDataProvider) GetValuationMethodID(_ context.Context, instrumentCode string) (uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return uuid.Nil, m.err
	}

	if id, ok := m.methods[instrumentCode]; ok {
		return id, nil
	}

	return uuid.New(), nil
}

func (m *mockReferenceDataProvider) GetMaterialityThreshold(_ context.Context, instrumentCode string) (decimal.Decimal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return decimal.Zero, m.err
	}

	if t, ok := m.thresholds[instrumentCode]; ok {
		return t, nil
	}

	return decimal.NewFromFloat(0.01), nil
}

func (m *mockReferenceDataProvider) setThreshold(instrumentCode string, threshold decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.thresholds[instrumentCode] = threshold
}

// mockValuationEngine implements valuation.Engine.
type mockValuationEngine struct {
	mu  sync.RWMutex
	fn  func(req *valuation.Request) (*valuation.Response, error)
	err error
}

func (m *mockValuationEngine) Valuate(_ context.Context, req *valuation.Request) (*valuation.Response, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return nil, m.err
	}

	if m.fn != nil {
		return m.fn(req)
	}

	// Default: return the variance amount converted to GBP at 1:1
	return &valuation.Response{
		ValuedAmount: valuation.Quantity{
			Amount:         req.Quantity.Amount,
			InstrumentCode: "GBP",
		},
		ComputedAt: time.Now().UTC(),
	}, nil
}

// mockPositionLockClient implements service.PositionLockClient.
type mockPositionLockClient struct {
	mu        sync.RWMutex
	lockErr   error
	pendingOp int
	checkErr  error
	calls     []service.PositionLockRequest
}

func (m *mockPositionLockClient) RequestPositionLock(_ context.Context, req service.PositionLockRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, req)
	return m.lockErr
}

func (m *mockPositionLockClient) CheckPendingOperations(_ context.Context, _ string, _, _ time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pendingOp, m.checkErr
}

func (m *mockPositionLockClient) getLockCalls() []service.PositionLockRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]service.PositionLockRequest, len(m.calls))
	copy(result, m.calls)
	return result
}

// mockSagaRuntime implements service.SagaRuntime.
type mockSagaRuntime struct {
	mu    sync.RWMutex
	calls []sagaCall
	err   error
}

type sagaCall struct {
	Name   string
	Params map[string]interface{}
}

func (m *mockSagaRuntime) InvokeSaga(_ context.Context, sagaName string, params map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, sagaCall{Name: sagaName, Params: params})
	return m.err
}

func (m *mockSagaRuntime) getCalls() []sagaCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]sagaCall, len(m.calls))
	copy(result, m.calls)
	return result
}

// mockEventPublisher implements service.EventPublisher and service.ImbalanceEventPublisher.
type mockEventPublisher struct {
	mu     sync.RWMutex
	events []publishedEvent
	err    error
}

type publishedEvent struct {
	Topic string
	Event interface{}
}

func (m *mockEventPublisher) Publish(_ context.Context, topic string, event interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, publishedEvent{Topic: topic, Event: event})
	return m.err
}

func (m *mockEventPublisher) PublishBalanceImbalanceDetected(_ context.Context, event *domain.BalanceImbalanceDetectedEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, publishedEvent{
		Topic: "reconciliation.balance-imbalance-detected.v1",
		Event: event,
	})
	return m.err
}

func (m *mockEventPublisher) getEvents() []publishedEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]publishedEvent, len(m.events))
	copy(result, m.events)
	return result
}

func (m *mockEventPublisher) getEventsByTopic(topic string) []publishedEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []publishedEvent
	for _, e := range m.events {
		if e.Topic == topic {
			result = append(result, e)
		}
	}
	return result
}

func (m *mockEventPublisher) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
}

// tenantCtx returns the tenant-scoped context.
func (infra *e2eTestInfra) tenantCtx() context.Context {
	return infra.ctx
}

// =============================================================================
// Context Helpers
// =============================================================================

// contextWithAdminClaims creates a context with admin role claims.
func contextWithAdminClaims(ctx context.Context) context.Context {
	claims := &auth.Claims{
		UserID: "e2e-admin",
		Roles:  []string{"admin"},
	}
	return context.WithValue(ctx, auth.ClaimsContextKey, claims)
}

// contextWithServiceClaims creates a context with service role claims.
func contextWithServiceClaims(ctx context.Context) context.Context {
	claims := &auth.Claims{
		UserID: "e2e-service",
		Roles:  []string{"service"},
	}
	return context.WithValue(ctx, auth.ClaimsContextKey, claims)
}

// contextWithAuditorClaims creates a context with auditor role claims.
func contextWithAuditorClaims(ctx context.Context) context.Context {
	claims := &auth.Claims{
		UserID: "e2e-auditor",
		Roles:  []string{"auditor"},
	}
	return context.WithValue(ctx, auth.ClaimsContextKey, claims)
}

// =============================================================================
// Test Data Helpers
// =============================================================================

// createSettlementRun creates and persists a settlement run.
func createSettlementRun(
	t *testing.T,
	ctx context.Context,
	infra *e2eTestInfra,
	accountID string,
	scope domain.ReconciliationScope,
	settlementType domain.SettlementType,
	periodStart, periodEnd time.Time,
	initiatedBy string,
) *domain.SettlementRun {
	t.Helper()

	run, err := domain.NewSettlementRun(
		accountID, scope, settlementType,
		periodStart, periodEnd, initiatedBy,
	)
	require.NoError(t, err)

	err = infra.runRepo.Create(ctx, run)
	require.NoError(t, err)

	return run
}

// createSnapshot creates and persists a settlement snapshot.
func createSnapshot(
	t *testing.T,
	ctx context.Context,
	infra *e2eTestInfra,
	runID uuid.UUID,
	accountID, instrumentCode string,
	expected, actual decimal.Decimal,
	sourceSystem string,
	attrs map[string]string,
) *domain.SettlementSnapshot {
	t.Helper()

	snap, err := domain.NewSettlementSnapshot(
		runID, accountID, instrumentCode,
		expected, actual, sourceSystem, attrs,
	)
	require.NoError(t, err)

	err = infra.snapRepo.Create(ctx, snap)
	require.NoError(t, err)

	return snap
}

// createVariance creates and persists a variance.
func createVariance(
	t *testing.T,
	ctx context.Context,
	infra *e2eTestInfra,
	runID, snapshotID uuid.UUID,
	accountID, instrumentCode string,
	expected, actual decimal.Decimal,
	reason domain.VarianceReason,
) *domain.Variance {
	t.Helper()

	v, err := domain.NewVariance(
		runID, snapshotID, accountID, instrumentCode,
		expected, actual, reason,
	)
	require.NoError(t, err)

	err = infra.varianceRepo.Create(ctx, v)
	require.NoError(t, err)

	return v
}

// defaultPeriod returns a standard test period for settlement runs.
func defaultPeriod() (time.Time, time.Time) {
	now := time.Now().UTC()
	start := now.Add(-24 * time.Hour).Truncate(time.Hour)
	end := now.Truncate(time.Hour)
	return start, end
}
