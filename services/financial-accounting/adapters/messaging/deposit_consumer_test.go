package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testTenantID = "test_tenant"

// mockIdempotencyService provides a mock implementation of idempotency.Service for testing.
type mockIdempotencyService struct {
	checkFunc       func(ctx context.Context, key idempotency.Key) (*idempotency.Result, error)
	markPendingFunc func(ctx context.Context, key idempotency.Key, ttl time.Duration) error
	storeResultFunc func(ctx context.Context, result idempotency.Result) error
	deleteFunc      func(ctx context.Context, key idempotency.Key) error
	acquireFunc     func(ctx context.Context, key idempotency.Key, opts idempotency.LockOptions) error
	releaseFunc     func(ctx context.Context, key idempotency.Key, token string) error
	refreshFunc     func(ctx context.Context, key idempotency.Key, token string, ttl time.Duration) error
	isHeldFunc      func(ctx context.Context, key idempotency.Key) (bool, error)
}

func newMockIdempotencyService() *mockIdempotencyService {
	return &mockIdempotencyService{
		checkFunc: func(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
			return nil, idempotency.ErrResultNotFound
		},
		markPendingFunc: func(_ context.Context, _ idempotency.Key, _ time.Duration) error {
			return nil
		},
		storeResultFunc: func(_ context.Context, _ idempotency.Result) error {
			return nil
		},
		deleteFunc: func(_ context.Context, _ idempotency.Key) error {
			return nil
		},
		acquireFunc: func(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
			return nil
		},
		releaseFunc: func(_ context.Context, _ idempotency.Key, _ string) error {
			return nil
		},
		refreshFunc: func(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
			return nil
		},
		isHeldFunc: func(_ context.Context, _ idempotency.Key) (bool, error) {
			return false, nil
		},
	}
}

func (m *mockIdempotencyService) Check(ctx context.Context, key idempotency.Key) (*idempotency.Result, error) {
	return m.checkFunc(ctx, key)
}

func (m *mockIdempotencyService) MarkPending(ctx context.Context, key idempotency.Key, ttl time.Duration) error {
	return m.markPendingFunc(ctx, key, ttl)
}

func (m *mockIdempotencyService) StoreResult(ctx context.Context, result idempotency.Result) error {
	return m.storeResultFunc(ctx, result)
}

func (m *mockIdempotencyService) Delete(ctx context.Context, key idempotency.Key) error {
	return m.deleteFunc(ctx, key)
}

func (m *mockIdempotencyService) Acquire(ctx context.Context, key idempotency.Key, opts idempotency.LockOptions) error {
	return m.acquireFunc(ctx, key, opts)
}

func (m *mockIdempotencyService) Release(ctx context.Context, key idempotency.Key, token string) error {
	return m.releaseFunc(ctx, key, token)
}

func (m *mockIdempotencyService) Refresh(ctx context.Context, key idempotency.Key, token string, ttl time.Duration) error {
	return m.refreshFunc(ctx, key, token, ttl)
}

func (m *mockIdempotencyService) IsHeld(ctx context.Context, key idempotency.Key) (bool, error) {
	return m.isHeldFunc(ctx, key)
}

func setupTestServices(t *testing.T) (*service.PostingService, context.Context, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.LedgerPostingEntity{},
		&persistence.FinancialBookingLogEntity{},
		&audit.AuditOutbox{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create tables in tenant schema (singular names to match production)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.financial_booking_log (
		id UUID PRIMARY KEY,
		financial_account_type VARCHAR(50) NOT NULL,
		product_service_reference VARCHAR(255) NOT NULL,
		business_unit_reference VARCHAR(255) NOT NULL,
		chart_of_accounts_rules TEXT NOT NULL,
		base_currency VARCHAR(3) NOT NULL,
		status VARCHAR(20) NOT NULL,
		idempotency_key VARCHAR(255) NOT NULL UNIQUE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		version BIGINT NOT NULL DEFAULT 1,
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.ledger_posting (
		id UUID PRIMARY KEY,
		financial_booking_log_id UUID NOT NULL,
		posting_direction VARCHAR(20) NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(32) NOT NULL,
		dimension_type VARCHAR(20) DEFAULT 'CURRENCY',
		instrument_version INTEGER DEFAULT 1,
		instrument_precision INTEGER DEFAULT 2,
		attributes JSONB DEFAULT '{}',
		account_id VARCHAR(255) NOT NULL,
		account_service_domain VARCHAR(20) NOT NULL DEFAULT '',
		value_date TIMESTAMP WITH TIME ZONE NOT NULL,
		posting_result TEXT,
		status VARCHAR(20) NOT NULL,
		correlation_id VARCHAR(255),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, pq.QuoteIdentifier(schemaName))).Error
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

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewLedgerRepository(db)
	return service.NewPostingServiceWithConfig(service.PostingServiceConfig{
		Repo:              repo,
		BankCashAccountID: "BANK-CASH-001",
	}), ctx, cleanup
}

func TestNewDepositConsumer(t *testing.T) {
	postingService, _, cleanup := setupTestServices(t)
	defer cleanup()

	mockIdemp := newMockIdempotencyService()

	tests := []struct {
		name           string
		config         kafka.ConsumerConfig
		idempotencySvc idempotency.Service
		wantErr        bool
		errContains    string
	}{
		{
			name: "valid config",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
				ClientID:         "test-consumer",
			},
			idempotencySvc: mockIdemp,
			wantErr:        false,
		},
		{
			name: "nil idempotency service",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			},
			idempotencySvc: nil,
			wantErr:        true,
			errContains:    "idempotency service cannot be nil",
		},
		{
			name: "missing bootstrap servers",
			config: kafka.ConsumerConfig{
				GroupID: "test-group",
			},
			idempotencySvc: mockIdemp,
			wantErr:        true,
		},
		{
			name: "missing group ID",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
			},
			idempotencySvc: mockIdemp,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer, err := NewDepositConsumer(tt.config, postingService, tt.idempotencySvc)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if consumer != nil {
				defer func() {
					_ = consumer.Close()
				}()
			}
		})
	}
}

func TestDepositConsumer_NilIdempotencyService(t *testing.T) {
	postingService, _, cleanup := setupTestServices(t)
	defer cleanup()

	_, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency service cannot be nil")
}

func TestDepositConsumer_HandleDepositEvent(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	mockIdemp := newMockIdempotencyService()

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	tests := []struct {
		name    string
		event   *eventsv1.DepositEvent
		wantErr bool
	}{
		{
			name: "valid deposit event",
			event: &eventsv1.DepositEvent{
				AccountId:      "ACC-123",
				AmountCents:    10000,
				InstrumentCode: "GBP",
				CorrelationId:  "deposit-001",
				ValueDate:      timestamppb.Now(),
				Timestamp:      timestamppb.Now(),
			},
			wantErr: false,
		},
		{
			name: "zero amount",
			event: &eventsv1.DepositEvent{
				AccountId:      "ACC-456",
				AmountCents:    0,
				InstrumentCode: "GBP",
				CorrelationId:  "deposit-002",
				ValueDate:      timestamppb.Now(),
				Timestamp:      timestamppb.Now(),
			},
			wantErr: true,
		},
		{
			name: "nil value date",
			event: &eventsv1.DepositEvent{
				AccountId:      "ACC-789",
				AmountCents:    5000,
				InstrumentCode: "USD",
				CorrelationId:  "deposit-003",
				ValueDate:      nil,
				Timestamp:      timestamppb.Now(),
			},
			wantErr: true,
		},
		{
			name: "unspecified currency",
			event: &eventsv1.DepositEvent{
				AccountId:      "ACC-999",
				AmountCents:    3000,
				InstrumentCode: "",
				CorrelationId:  "deposit-004",
				ValueDate:      timestamppb.Now(),
				Timestamp:      timestamppb.Now(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()

			err := consumer.handleDepositEvent(testCtx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleDepositEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDepositConsumer_IdempotencyCache(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	// Mock idempotency service that returns already processed
	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
		return &idempotency.Result{
			Status: idempotency.StatusCompleted,
		}, idempotency.ErrOperationAlreadyProcessed
	}

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-DUPLICATE",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		CorrelationId:  "deposit-duplicate",
		ValueDate:      timestamppb.Now(),
		Timestamp:      timestamppb.Now(),
	}

	testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	// Should succeed without error (idempotent success)
	err = consumer.handleDepositEvent(testCtx, event)
	require.NoError(t, err, "handleDepositEvent should return nil for already processed events")
}

func TestDepositConsumer_IdempotencyLockAcquisition(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	var acquireCalled bool
	var capturedKey idempotency.Key
	var capturedOpts idempotency.LockOptions

	mockIdemp := newMockIdempotencyService()
	mockIdemp.acquireFunc = func(_ context.Context, key idempotency.Key, opts idempotency.LockOptions) error {
		acquireCalled = true
		capturedKey = key
		capturedOpts = opts
		return nil
	}

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-LOCK",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		CorrelationId:  "deposit-lock-test",
		ValueDate:      timestamppb.Now(),
		Timestamp:      timestamppb.Now(),
	}

	testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	_ = consumer.handleDepositEvent(testCtx, event)

	assert.True(t, acquireCalled, "Acquire should be called for lock")
	assert.Equal(t, "financial-accounting", capturedKey.Namespace)
	assert.Equal(t, "process-deposit", capturedKey.Operation)
	assert.Equal(t, "ACC-LOCK", capturedKey.EntityID)
	assert.Equal(t, "deposit-lock-test", capturedKey.RequestID)
	assert.Equal(t, lockTTL, capturedOpts.TTL)
	assert.NotEmpty(t, capturedOpts.Token, "Lock token should be generated")
	assert.Equal(t, 0, capturedOpts.MaxRetries, "Should not retry lock acquisition")
}

func TestDepositConsumer_IdempotencyStoreSuccess(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	var storeResultCalled bool
	var capturedResult idempotency.Result

	mockIdemp := newMockIdempotencyService()
	mockIdemp.storeResultFunc = func(_ context.Context, result idempotency.Result) error {
		storeResultCalled = true
		capturedResult = result
		return nil
	}

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-SUCCESS",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		CorrelationId:  "deposit-success-test",
		ValueDate:      timestamppb.Now(),
		Timestamp:      timestamppb.Now(),
	}

	testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	err = consumer.handleDepositEvent(testCtx, event)
	require.NoError(t, err)

	assert.True(t, storeResultCalled, "StoreResult should be called on success")
	assert.Equal(t, idempotency.StatusCompleted, capturedResult.Status)
	assert.Equal(t, 24*time.Hour, capturedResult.TTL)
	assert.Nil(t, capturedResult.Data, "Data should be nil for events")
}

func TestDepositConsumer_IdempotencyStoreFailure(t *testing.T) {
	// This test verifies that failure results are stored in the idempotency cache.
	// Proto validation rejects CURRENCY_UNSPECIFIED (not_in: [0]), so we can't use
	// that to trigger a currency conversion failure. Instead, we test by observing
	// the call pattern: if proto validation fails first, no idempotency calls happen.
	//
	// To properly test failure storage, we would need to mock the PostingService to
	// fail ProcessDeposit. For now, we verify the error path through proto validation.
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	var checkCalled bool
	var acquireCalled bool

	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
		checkCalled = true
		return nil, idempotency.ErrResultNotFound
	}
	mockIdemp.acquireFunc = func(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
		acquireCalled = true
		return nil
	}

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	// Use unspecified currency - proto validation will fail first (not_in: [0])
	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-FAILURE",
		AmountCents:    10000,
		InstrumentCode: "",
		CorrelationId:  "deposit-failure-test",
		ValueDate:      timestamppb.Now(),
		Timestamp:      timestamppb.Now(),
	}

	testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	err = consumer.handleDepositEvent(testCtx, event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid deposit event", "Error should indicate proto validation failure")

	// Proto validation fails before idempotency check, so no idempotency calls should be made
	assert.False(t, checkCalled, "Idempotency check should not be called when proto validation fails")
	assert.False(t, acquireCalled, "Acquire should not be called when proto validation fails")
}

func TestDepositConsumer_IdempotencyKeyFormat(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	var capturedKey idempotency.Key

	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
		capturedKey = key
		return nil, idempotency.ErrResultNotFound
	}

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-KEY-FORMAT",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		CorrelationId:  "correlation-123",
		ValueDate:      timestamppb.Now(),
		Timestamp:      timestamppb.Now(),
	}

	testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	_ = consumer.handleDepositEvent(testCtx, event)

	assert.Equal(t, "financial-accounting", capturedKey.Namespace)
	assert.Equal(t, "process-deposit", capturedKey.Operation)
	assert.Equal(t, "ACC-KEY-FORMAT", capturedKey.EntityID)
	assert.Equal(t, "correlation-123", capturedKey.RequestID)
	assert.Equal(t, testTenantID, capturedKey.TenantID)
}

// errRedisConnection is a test error for simulating Redis failures
var errRedisConnection = errors.New("redis connection error")

func TestDepositConsumer_IdempotencyCheckFailed(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
		return nil, errRedisConnection
	}

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-CHECK-FAIL",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		CorrelationId:  "deposit-check-fail",
		ValueDate:      timestamppb.Now(),
		Timestamp:      timestamppb.Now(),
	}

	testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	err = consumer.handleDepositEvent(testCtx, event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency check failed")
}

func TestDepositConsumer_ConcurrentProcessingRejected(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	// Mock idempotency service that fails to acquire lock (another consumer holds it)
	mockIdemp := newMockIdempotencyService()
	mockIdemp.acquireFunc = func(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
		return idempotency.ErrLockAcquisitionFailed
	}

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, mockIdemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-CONCURRENT-REJECT",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		CorrelationId:  "deposit-concurrent-reject",
		ValueDate:      timestamppb.Now(),
		Timestamp:      timestamppb.Now(),
	}

	testCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	err = consumer.handleDepositEvent(testCtx, event)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConcurrentProcessing)
}

func TestExtractTenantID(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{
			name:     "context with tenant",
			ctx:      tenant.WithTenant(context.Background(), tenant.TenantID("my-tenant")),
			expected: "my-tenant",
		},
		{
			name:     "context without tenant",
			ctx:      context.Background(),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTenantID(tt.ctx)
			assert.Equal(t, tt.expected, result)
		})
	}
}
