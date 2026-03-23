// Package service provides gRPC integration tests for the financial-accounting service.
// These tests use testcontainers to spin up real PostgreSQL instances,
// verifying end-to-end gRPC behavior with actual database operations.
package service

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// testServer holds the test gRPC server and its dependencies
type testServer struct {
	db         *gorm.DB
	repo       *persistence.LedgerRepository
	server     *grpc.Server
	listener   net.Listener
	address    string
	cleanup    func()
	healthSrv  *health.Server
	grpcClient financialaccountingv1.FinancialAccountingServiceClient
	healthCli  grpc_health_v1.HealthClient
	conn       *grpc.ClientConn
	ctx        context.Context
}

// setupIntegrationTest creates a complete test environment with:
// - PostgreSQL testcontainer
// - gRPC server with FinancialAccountingService
// - Health check service
// - gRPC client connections
func setupIntegrationTest(t *testing.T) (*testServer, context.Context) {
	t.Helper()

	// Create PostgreSQL testcontainer with schema and migrations
	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.FinancialBookingLogEntity{},
		&persistence.LedgerPostingEntity{},
		&audit.AuditOutbox{},
	})

	// Create tenant schema
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create tables in tenant schema (singular names to match production)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.financial_booking_log (
		id UUID PRIMARY KEY,
		financial_account_type TEXT NOT NULL,
		product_service_reference TEXT NOT NULL,
		business_unit_reference TEXT NOT NULL,
		chart_of_accounts_rules TEXT,
		base_currency TEXT NOT NULL,
		status TEXT NOT NULL,
		idempotency_key TEXT UNIQUE NOT NULL,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		version INT NOT NULL DEFAULT 1,
		deleted_at TIMESTAMP
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.ledger_posting (
		id UUID PRIMARY KEY,
		financial_booking_log_id UUID NOT NULL REFERENCES %s.financial_booking_log(id) ON DELETE RESTRICT,
		posting_direction TEXT NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(32) NOT NULL,
		dimension_type VARCHAR(20) DEFAULT 'CURRENCY',
		instrument_version INTEGER DEFAULT 1,
		instrument_precision INTEGER DEFAULT 2,
		attributes JSONB DEFAULT '{}',
		account_id TEXT NOT NULL,
		account_service_domain VARCHAR(20) NOT NULL DEFAULT '',
		value_date TIMESTAMP NOT NULL,
		posting_result TEXT,
		correlation_id TEXT,
		status TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		deleted_at TIMESTAMP
	)`, schemaName, schemaName)).Error
	require.NoError(t, err)

	// Create audit_outbox table for GORM hooks
	// Note: Uses TEXT instead of JSONB for old_values/new_values for compatibility with
	// the shared audit infrastructure which writes empty strings when values are nil.
	// record_id is VARCHAR(50) to match the shared AuditOutbox which uses string IDs.
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
		record_id VARCHAR(50) NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set search_path to tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	// Create repository
	repo := persistence.NewLedgerRepository(db)

	// Create service dependencies
	eventPublisher := &noopEventPublisher{}
	idempotencySvc := &inMemoryIdempotencyService{
		store: make(map[string]*idempotency.Result),
	}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create the financial accounting service
	service, err := NewFinancialAccountingService(repo, eventPublisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err, "Failed to create financial accounting service")

	// Create gRPC server with tenant interceptor
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.TenantExtractionInterceptor()),
	)
	financialaccountingv1.RegisterFinancialAccountingServiceServer(grpcServer, service)

	// Create and register health service
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("financial-accounting", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Create listener on random available port using ListenConfig for context support
	var lc net.ListenConfig
	listener, err := lc.Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err, "Failed to create listener")

	address := listener.Addr().String()

	// Start server in background
	go func() {
		// Note: Cannot use t.Logf here as test may have already finished
		// Server errors during graceful shutdown are expected and can be ignored
		_ = grpcServer.Serve(listener)
	}()

	// Create client interceptor that adds tenant ID to outgoing metadata
	tenantInterceptor := func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		// Add tenant ID to outgoing metadata
		md := metadata.New(map[string]string{
			tenant.TenantIDKey: testTenantID,
		})
		ctx = metadata.NewOutgoingContext(ctx, md)
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	// Create client connection using NewClient (grpc.DialContext is deprecated)
	// Note: NewClient creates a lazy client that doesn't connect until first RPC
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(tenantInterceptor),
	)
	require.NoError(t, err, "Failed to create gRPC client")

	// Create clients
	grpcClient := financialaccountingv1.NewFinancialAccountingServiceClient(conn)
	healthClient := grpc_health_v1.NewHealthClient(conn)

	ts := &testServer{
		db:         db,
		repo:       repo,
		server:     grpcServer,
		listener:   listener,
		address:    address,
		healthSrv:  healthServer,
		grpcClient: grpcClient,
		healthCli:  healthClient,
		conn:       conn,
		ctx:        ctx,
	}

	ts.cleanup = func() {
		conn.Close()
		grpcServer.GracefulStop()
		dbCleanup()
	}

	return ts, ctx
}

// createTestBookingLog creates a booking log in the database for testing
func createTestBookingLog(t *testing.T, db *gorm.DB, ctx context.Context) uuid.UUID {
	t.Helper()

	bookingLogID := uuid.New()
	bookingLog := &persistence.FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.WithContext(ctx).Create(bookingLog).Error)

	return bookingLogID
}

// noopEventPublisher is a no-op implementation for testing
type noopEventPublisher struct{}

func (n *noopEventPublisher) Publish(_ context.Context, _ DomainEvent) error {
	return nil
}

func (n *noopEventPublisher) PublishBatch(_ context.Context, _ []DomainEvent) error {
	return nil
}

// inMemoryIdempotencyService provides a thread-safe in-memory idempotency service for integration tests.
// Uses sync.RWMutex to allow concurrent reads while ensuring safe writes.
type inMemoryIdempotencyService struct {
	mu    sync.RWMutex
	store map[string]*idempotency.Result
}

func (s *inMemoryIdempotencyService) Check(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keyStr := key.String()
	if result, ok := s.store[keyStr]; ok {
		if result.Status == idempotency.StatusCompleted {
			return result, idempotency.ErrOperationAlreadyProcessed
		}
		return result, nil
	}
	return nil, idempotency.ErrResultNotFound
}

func (s *inMemoryIdempotencyService) MarkPending(_ context.Context, key idempotency.Key, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := key.String()
	s.store[keyStr] = &idempotency.Result{
		Key:    key,
		Status: idempotency.StatusPending,
	}
	return nil
}

func (s *inMemoryIdempotencyService) StoreResult(_ context.Context, result idempotency.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := result.Key.String()
	s.store[keyStr] = &result
	return nil
}

func (s *inMemoryIdempotencyService) Delete(_ context.Context, key idempotency.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.store, key.String())
	return nil
}

func (s *inMemoryIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (s *inMemoryIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (s *inMemoryIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (s *inMemoryIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}

// ============================================================================
// Health Check Integration Tests
// ============================================================================

func TestHealthCheck_Integration_DefaultService(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 5*time.Second)
	defer cancel()

	// Test default service health check (used by K8s probes)
	resp, err := ts.healthCli.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthCheck_Integration_NamedService(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 5*time.Second)
	defer cancel()

	// Test named service health check
	resp, err := ts.healthCli.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "financial-accounting",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthCheck_Integration_UnknownService(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 5*time.Second)
	defer cancel()

	// Test unknown service health check
	_, err := ts.healthCli.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "unknown-service",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHealthCheck_Integration_StatusChange(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 5*time.Second)
	defer cancel()

	// Initially SERVING
	resp, err := ts.healthCli.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "financial-accounting",
	})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	// Simulate shutdown - change to NOT_SERVING
	ts.healthSrv.SetServingStatus("financial-accounting", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	// Verify status changed
	resp, err = ts.healthCli.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "financial-accounting",
	})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)
}

// ============================================================================
// CaptureLedgerPosting Integration Tests
// ============================================================================

func TestCaptureLedgerPosting_Integration_Success(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create a booking log first (required for FK constraint)
	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)

	// Create posting request
	req := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        500000000, // 100.50 GBP
		},
		AccountId: "ACC-123",
		ValueDate: timestamppb.Now(),
	}

	// Execute
	resp, err := ts.grpcClient.CaptureLedgerPosting(ctx, req)

	// Verify
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.LedgerPosting)

	assert.NotEmpty(t, resp.LedgerPosting.Id, "posting ID should be generated")
	assert.Equal(t, bookingLogID.String(), resp.LedgerPosting.FinancialBookingLogId)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, resp.LedgerPosting.PostingDirection)
	assert.Equal(t, "ACC-123", resp.LedgerPosting.AccountId)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, resp.LedgerPosting.Status)

	// Verify posting was persisted in database
	postingID, err := uuid.Parse(resp.LedgerPosting.Id)
	require.NoError(t, err)

	savedPosting, err := ts.repo.GetPosting(ctx, postingID)
	require.NoError(t, err)
	assert.Equal(t, domain.PostingDirectionDebit, savedPosting.Direction)
	assert.Equal(t, "ACC-123", savedPosting.AccountID)
}

func TestCaptureLedgerPosting_Integration_CreditDirection(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)

	req := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        50,
			Nanos:        0,
		},
		AccountId: "ACC-456",
		ValueDate: timestamppb.Now(),
	}

	resp, err := ts.grpcClient.CaptureLedgerPosting(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_CREDIT, resp.LedgerPosting.PostingDirection)
}

func TestCaptureLedgerPosting_Integration_WithIdempotencyKey(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)

	req := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        0,
		},
		AccountId: "ACC-123",
		ValueDate: timestamppb.Now(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "unique-key-" + uuid.New().String(),
			TtlSeconds: 3600,
		},
	}

	// First request should succeed
	resp1, err := ts.grpcClient.CaptureLedgerPosting(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp1)
	require.NotNil(t, resp1.LedgerPosting)
	originalPostingID := resp1.LedgerPosting.Id

	// Second request with same idempotency key should return cached response (idempotent)
	resp2, err := ts.grpcClient.CaptureLedgerPosting(ctx, req)
	require.NoError(t, err, "idempotent request should succeed with cached response")
	require.NotNil(t, resp2)
	require.NotNil(t, resp2.LedgerPosting)
	assert.Equal(t, originalPostingID, resp2.LedgerPosting.Id, "should return same posting ID from cache")
	assert.Equal(t, resp1.LedgerPosting.PostingDirection, resp2.LedgerPosting.PostingDirection)
	assert.Equal(t, resp1.LedgerPosting.AccountId, resp2.LedgerPosting.AccountId)
}

func TestCaptureLedgerPosting_Integration_InvalidBookingLogID(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	req := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "not-a-uuid",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        0,
		},
		AccountId: "ACC-123",
		ValueDate: timestamppb.Now(),
	}

	_, err := ts.grpcClient.CaptureLedgerPosting(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCaptureLedgerPosting_Integration_ZeroAmount(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)

	req := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        0,
			Nanos:        0,
		},
		AccountId: "ACC-123",
		ValueDate: timestamppb.Now(),
	}

	_, err := ts.grpcClient.CaptureLedgerPosting(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCaptureLedgerPosting_Integration_MissingAccountID(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)

	req := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        0,
		},
		AccountId: "", // Missing
		ValueDate: timestamppb.Now(),
	}

	_, err := ts.grpcClient.CaptureLedgerPosting(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ============================================================================
// RetrieveLedgerPosting Integration Tests
// ============================================================================

func TestRetrieveLedgerPosting_Integration_Success(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create booking log and posting
	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromFloat(100.50), gbpInstrument)
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                amount,
		AccountID:             "ACC-123",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPending,
		CreatedAt:             time.Now(),
	}
	require.NoError(t, ts.repo.SavePosting(ctx, posting))

	// Retrieve via gRPC
	resp, err := ts.grpcClient.RetrieveLedgerPosting(ctx, &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: posting.ID.String(),
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.LedgerPosting)

	assert.Equal(t, posting.ID.String(), resp.LedgerPosting.Id)
	assert.Equal(t, bookingLogID.String(), resp.LedgerPosting.FinancialBookingLogId)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, resp.LedgerPosting.PostingDirection)
	assert.Equal(t, "ACC-123", resp.LedgerPosting.AccountId)
}

func TestRetrieveLedgerPosting_Integration_NotFound(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Try to retrieve non-existent posting
	_, err := ts.grpcClient.RetrieveLedgerPosting(ctx, &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: uuid.New().String(),
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestRetrieveLedgerPosting_Integration_InvalidUUID(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	_, err := ts.grpcClient.RetrieveLedgerPosting(ctx, &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: "not-a-uuid",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ============================================================================
// UpdateLedgerPosting Integration Tests
// ============================================================================

func TestUpdateLedgerPosting_Integration_PendingToPosted(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create booking log and pending posting
	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                amount,
		AccountID:             "ACC-123",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPending,
		CreatedAt:             time.Now(),
	}
	require.NoError(t, ts.repo.SavePosting(ctx, posting))

	// Update to POSTED
	resp, err := ts.grpcClient.UpdateLedgerPosting(ctx, &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:            posting.ID.String(),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		PostingResult: "Successfully posted to ledger",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.LedgerPosting.Status)
	assert.Equal(t, "Successfully posted to ledger", resp.LedgerPosting.PostingResult)

	// Verify persisted in database
	savedPosting, err := ts.repo.GetPosting(ctx, posting.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusPosted, savedPosting.Status)
}

func TestUpdateLedgerPosting_Integration_PendingToFailed(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                amount,
		AccountID:             "ACC-123",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPending,
		CreatedAt:             time.Now(),
	}
	require.NoError(t, ts.repo.SavePosting(ctx, posting))

	resp, err := ts.grpcClient.UpdateLedgerPosting(ctx, &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:            posting.ID.String(),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
		PostingResult: "Insufficient funds",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, resp.LedgerPosting.Status)
}

func TestUpdateLedgerPosting_Integration_InvalidTransition(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create already POSTED posting
	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                amount,
		AccountID:             "ACC-123",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted, // Already posted
		PostingResult:         "Previously posted",
		CreatedAt:             time.Now(),
	}
	require.NoError(t, ts.repo.SavePosting(ctx, posting))

	// Try to fail an already posted transaction
	_, err := ts.grpcClient.UpdateLedgerPosting(ctx, &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:            posting.ID.String(),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
		PostingResult: "Trying to fail posted",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestUpdateLedgerPosting_Integration_NotFound(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	_, err := ts.grpcClient.UpdateLedgerPosting(ctx, &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:            uuid.New().String(),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		PostingResult: "test",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ============================================================================
// ListLedgerPostings Integration Tests
// ============================================================================

func TestListLedgerPostings_Integration_Success(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create booking log and multiple postings
	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

	for i := 0; i < 5; i++ {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                amount,
			AccountID:             "ACC-123",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
		// Intentional sleep: Ensure different timestamps for ordering tests
		time.Sleep(time.Millisecond) //nolint:forbidigo // ensures distinct timestamps
	}

	// List all postings
	resp, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 10,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.LedgerPostings, 5)
}

func TestListLedgerPostings_Integration_FilterByBookingLogID(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create two booking logs
	bookingLogID1 := createTestBookingLog(t, ts.db, ts.ctx)
	bookingLogID2 := createTestBookingLog(t, ts.db, ts.ctx)

	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

	// Create 3 postings for first booking log
	for i := 0; i < 3; i++ {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID1,
			Direction:             domain.PostingDirectionDebit,
			Amount:                amount,
			AccountID:             "ACC-123",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
	}

	// Create 2 postings for second booking log
	for i := 0; i < 2; i++ {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID2,
			Direction:             domain.PostingDirectionCredit,
			Amount:                amount,
			AccountID:             "ACC-456",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
	}

	// List postings for first booking log only
	resp, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		FinancialBookingLogId: bookingLogID1.String(),
		Pagination: &commonv1.Pagination{
			PageSize: 10,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.LedgerPostings, 3)

	// Verify all returned postings belong to the first booking log
	for _, posting := range resp.LedgerPostings {
		assert.Equal(t, bookingLogID1.String(), posting.FinancialBookingLogId)
	}
}

func TestListLedgerPostings_Integration_FilterByDirection(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

	// Create 2 debit and 3 credit postings
	for i := 0; i < 2; i++ {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                amount,
			AccountID:             "ACC-123",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
	}

	for i := 0; i < 3; i++ {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionCredit,
			Amount:                amount,
			AccountID:             "ACC-456",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
	}

	// Filter by debit only
	resp, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		Pagination: &commonv1.Pagination{
			PageSize: 10,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.LedgerPostings, 2)

	for _, posting := range resp.LedgerPostings {
		assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, posting.PostingDirection)
	}
}

func TestListLedgerPostings_Integration_Pagination(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

	// Create 10 postings
	for i := 0; i < 10; i++ {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                amount,
			AccountID:             "ACC-123",
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
		// Intentional sleep: Ensure different timestamps for pagination ordering
		time.Sleep(time.Millisecond) //nolint:forbidigo // ensures distinct timestamps
	}

	// Get first page
	resp1, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 5,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp1)
	assert.Len(t, resp1.LedgerPostings, 5)
	assert.NotEmpty(t, resp1.Pagination.NextPageToken, "should have next page token")

	// Get second page using the page token
	resp2, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		Pagination: &commonv1.Pagination{
			PageSize:  5,
			PageToken: resp1.Pagination.NextPageToken,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp2)
	assert.Len(t, resp2.LedgerPostings, 5, "second page should have 5 postings")

	// Verify no overlap between pages - cursor pagination should return distinct sets
	page1IDs := make(map[string]bool)
	for _, posting := range resp1.LedgerPostings {
		page1IDs[posting.Id] = true
	}
	for _, posting := range resp2.LedgerPostings {
		assert.False(t, page1IDs[posting.Id], "pages should not have overlapping postings")
	}

	// Verify total count reflects all records
	assert.Equal(t, int64(10), resp1.Pagination.TotalCount)
}

func TestListLedgerPostings_Integration_InvalidPageSize(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Page size 0 should fail
	_, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 0,
		},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ============================================================================
// ListFinancialBookingLogs Integration Tests
// ============================================================================

func TestListFinancialBookingLogs_Integration_Success(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create multiple booking logs
	for i := 0; i < 3; i++ {
		createTestBookingLog(t, ts.db, ts.ctx)
	}

	resp, err := ts.grpcClient.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 10,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.FinancialBookingLogs, 3)
}

func TestListFinancialBookingLogs_Integration_FilterByBusinessUnit(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create booking logs with different business units
	bookingLog1 := &persistence.FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-RETAIL",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, ts.db.WithContext(ts.ctx).Create(bookingLog1).Error)

	bookingLog2 := &persistence.FinancialBookingLogEntity{
		ID:                      uuid.New(),
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-002",
		BusinessUnitReference:   "BU-CORPORATE",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  "ACTIVE",
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, ts.db.WithContext(ts.ctx).Create(bookingLog2).Error)

	// Filter by business unit
	resp, err := ts.grpcClient.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
		BusinessUnitReference: "BU-RETAIL",
		Pagination: &commonv1.Pagination{
			PageSize: 10,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.FinancialBookingLogs, 1)
	assert.Equal(t, "BU-RETAIL", resp.FinancialBookingLogs[0].BusinessUnitReference)
}

// ============================================================================
// End-to-End Workflow Tests
// ============================================================================

func TestEndToEnd_CreateAndRetrievePosting(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)

	// Step 1: Create posting
	createResp, err := ts.grpcClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        250,
			Nanos:        750000000,
		},
		AccountId: "ACC-E2E-TEST",
		ValueDate: timestamppb.Now(),
	})
	require.NoError(t, err)
	postingID := createResp.LedgerPosting.Id

	// Step 2: Retrieve posting
	retrieveResp, err := ts.grpcClient.RetrieveLedgerPosting(ctx, &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: postingID,
	})
	require.NoError(t, err)

	assert.Equal(t, postingID, retrieveResp.LedgerPosting.Id)
	assert.Equal(t, "ACC-E2E-TEST", retrieveResp.LedgerPosting.AccountId)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, retrieveResp.LedgerPosting.Status)
}

func TestEndToEnd_PostingLifecycle(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)

	// Step 1: Create posting (PENDING)
	createResp, err := ts.grpcClient.CaptureLedgerPosting(ctx, &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        500,
			Nanos:        0,
		},
		AccountId: "ACC-LIFECYCLE",
		ValueDate: timestamppb.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, createResp.LedgerPosting.Status)

	postingID := createResp.LedgerPosting.Id

	// Step 2: Update to POSTED
	updateResp, err := ts.grpcClient.UpdateLedgerPosting(ctx, &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:            postingID,
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		PostingResult: "Posted successfully via lifecycle test",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, updateResp.LedgerPosting.Status)

	// Step 3: Verify via retrieve
	retrieveResp, err := ts.grpcClient.RetrieveLedgerPosting(ctx, &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: postingID,
	})
	require.NoError(t, err)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, retrieveResp.LedgerPosting.Status)
	assert.Equal(t, "Posted successfully via lifecycle test", retrieveResp.LedgerPosting.PostingResult)

	// Step 4: Verify in list
	listResp, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		FinancialBookingLogId: bookingLogID.String(),
		Pagination: &commonv1.Pagination{
			PageSize: 10,
		},
	})
	require.NoError(t, err)
	require.Len(t, listResp.LedgerPostings, 1)
	assert.Equal(t, postingID, listResp.LedgerPostings[0].Id)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, listResp.LedgerPostings[0].Status)
}

// ============================================================================
// UpdateFinancialBookingLog Integration Tests - Double-Entry Balance Validation
// ============================================================================

// createTestBookingLogWithStatus creates a booking log with specified status for testing
func createTestBookingLogWithStatus(t *testing.T, db *gorm.DB, ctx context.Context, status string) uuid.UUID {
	t.Helper()

	bookingLogID := uuid.New()
	bookingLog := &persistence.FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "{}",
		BaseCurrency:            "GBP",
		Status:                  status,
		IdempotencyKey:          "test-key-" + uuid.New().String(),
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
		Version:                 1,
	}
	require.NoError(t, db.WithContext(ctx).Create(bookingLog).Error)

	return bookingLogID
}

// createTestPosting creates a ledger posting for testing double-entry validation
func createTestPosting(t *testing.T, db *gorm.DB, ctx context.Context, bookingLogID uuid.UUID, direction string, amountCents int64) uuid.UUID {
	t.Helper()

	postingID := uuid.New()
	posting := &persistence.LedgerPostingEntity{
		ID:                    postingID,
		FinancialBookingLogID: bookingLogID,
		PostingDirection:      direction,
		AmountMinorUnits:      amountCents,
		Currency:              "GBP",
		DimensionType:         "CURRENCY",
		InstrumentVersion:     1,
		InstrumentPrecision:   2,
		AccountID:             "ACC-" + uuid.New().String()[:8],
		ValueDate:             time.Now(),
		Status:                "PENDING",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}
	require.NoError(t, db.WithContext(ctx).Create(posting).Error)

	return postingID
}

// TestUpdateFinancialBookingLog_Integration_BalancedPostings_Success tests that
// transitioning to POSTED status succeeds when postings are balanced (debits == credits).
func TestUpdateFinancialBookingLog_Integration_BalancedPostings_Success(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create a booking log in PENDING status
	bookingLogID := createTestBookingLogWithStatus(t, ts.db, ts.ctx, "PENDING")

	// Create balanced postings: 100.00 debit + 100.00 credit
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "DEBIT", 10000)  // 100.00
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "CREDIT", 10000) // 100.00

	// Attempt to transition to POSTED
	resp, err := ts.grpcClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID.String(),
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	// Should succeed with balanced postings
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.FinancialBookingLog.Status)
}

// TestUpdateFinancialBookingLog_Integration_MultipleBalancedPostings tests that
// multiple postings that sum to balanced amounts allow POSTED transition.
func TestUpdateFinancialBookingLog_Integration_MultipleBalancedPostings(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create a booking log in PENDING status
	bookingLogID := createTestBookingLogWithStatus(t, ts.db, ts.ctx, "PENDING")

	// Create multiple postings: 2 debits (50 + 50) = 1 credit (100)
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "DEBIT", 5000)   // 50.00
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "DEBIT", 5000)   // 50.00
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "CREDIT", 10000) // 100.00

	// Attempt to transition to POSTED
	resp, err := ts.grpcClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID.String(),
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	// Should succeed with balanced postings
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.FinancialBookingLog.Status)
}

// TestUpdateFinancialBookingLog_Integration_UnbalancedPostings_FailedPrecondition tests that
// transitioning to POSTED status fails when debits != credits.
func TestUpdateFinancialBookingLog_Integration_UnbalancedPostings_FailedPrecondition(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create a booking log in PENDING status
	bookingLogID := createTestBookingLogWithStatus(t, ts.db, ts.ctx, "PENDING")

	// Create unbalanced postings: 100.00 debit + 50.00 credit
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "DEBIT", 10000) // 100.00
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "CREDIT", 5000) // 50.00

	// Attempt to transition to POSTED
	resp, err := ts.grpcClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID.String(),
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	// Should fail with FailedPrecondition
	require.Error(t, err)
	require.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot post unbalanced booking log")
	assert.Contains(t, st.Message(), "debits=")
	assert.Contains(t, st.Message(), "credits=")
	assert.Contains(t, st.Message(), "imbalance=")
}

// TestUpdateFinancialBookingLog_Integration_NoPostings_FailedPrecondition tests that
// transitioning to POSTED status fails when there are no postings.
func TestUpdateFinancialBookingLog_Integration_NoPostings_FailedPrecondition(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create a booking log in PENDING status with no postings
	bookingLogID := createTestBookingLogWithStatus(t, ts.db, ts.ctx, "PENDING")

	// Attempt to transition to POSTED with no postings
	resp, err := ts.grpcClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID.String(),
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	// Should fail with FailedPrecondition
	require.Error(t, err)
	require.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "cannot post booking log with no postings")
}

// TestUpdateFinancialBookingLog_Integration_NonPostedTransition_SkipsValidation tests that
// transitions to non-POSTED statuses (FAILED, CANCELLED) skip balance validation.
func TestUpdateFinancialBookingLog_Integration_NonPostedTransition_SkipsValidation(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create a booking log in PENDING status with unbalanced postings
	bookingLogID := createTestBookingLogWithStatus(t, ts.db, ts.ctx, "PENDING")
	createTestPosting(t, ts.db, ts.ctx, bookingLogID, "DEBIT", 10000) // 100.00, no credit

	// Transition to FAILED should succeed (balance validation not required)
	resp, err := ts.grpcClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID.String(),
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, resp.FinancialBookingLog.Status)
}

// TestListLedgerPostings_Integration_FilterByAccountIDs tests filtering by multiple account IDs.
func TestListLedgerPostings_Integration_FilterByAccountIDs(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

	// Create postings for three different accounts
	for _, accID := range []string{"ACC-A1", "ACC-A2", "ACC-OTHER"} {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                amount,
			AccountID:             accID,
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
	}

	// Filter for two accounts only
	resp, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		AccountIds: []string{"ACC-A1", "ACC-A2"},
		Pagination: &commonv1.Pagination{PageSize: 10},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.LedgerPostings, 2)
	for _, p := range resp.LedgerPostings {
		assert.Contains(t, []string{"ACC-A1", "ACC-A2"}, p.AccountId)
	}
}

// TestListLedgerPostings_Integration_AccountIDs_TakesPrecedence verifies account_ids takes
// precedence over account_id when both are provided.
func TestListLedgerPostings_Integration_AccountIDs_TakesPrecedence(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	bookingLogID := createTestBookingLog(t, ts.db, ts.ctx)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

	for _, accID := range []string{"ACC-B1", "ACC-B2", "ACC-IGNORED"} {
		posting := &domain.LedgerPosting{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                amount,
			AccountID:             accID,
			ValueDate:             time.Now(),
			Status:                domain.TransactionStatusPending,
			CreatedAt:             time.Now(),
		}
		require.NoError(t, ts.repo.SavePosting(ctx, posting))
	}

	// account_ids should take precedence over account_id
	resp, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		AccountId:  "ACC-IGNORED",
		AccountIds: []string{"ACC-B1", "ACC-B2"},
		Pagination: &commonv1.Pagination{PageSize: 10},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.LedgerPostings, 2)
	for _, p := range resp.LedgerPostings {
		assert.Contains(t, []string{"ACC-B1", "ACC-B2"}, p.AccountId)
	}
}

// TestListLedgerPostings_Integration_AccountIDs_MaxLimitRejected tests that >100 account_ids
// is rejected with InvalidArgument.
func TestListLedgerPostings_Integration_AccountIDs_MaxLimitRejected(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "ACC-001"
	}

	_, err := ts.grpcClient.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		AccountIds: tooMany,
		Pagination: &commonv1.Pagination{PageSize: 10},
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestUpdateFinancialBookingLog_Integration_CancelledTransition_SkipsValidation tests that
// transitions to CANCELLED skip balance validation.
func TestUpdateFinancialBookingLog_Integration_CancelledTransition_SkipsValidation(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	defer ts.cleanup()

	ctx, cancel := context.WithTimeout(ts.ctx, 10*time.Second)
	defer cancel()

	// Create a booking log in PENDING status with no postings
	bookingLogID := createTestBookingLogWithStatus(t, ts.db, ts.ctx, "PENDING")

	// Transition to CANCELLED should succeed without postings
	resp, err := ts.grpcClient.UpdateFinancialBookingLog(ctx, &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id:     bookingLogID.String(),
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, resp.FinancialBookingLog.Status)
}
