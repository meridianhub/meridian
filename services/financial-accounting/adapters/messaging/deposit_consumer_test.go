package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"

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
// The deposit consumer uses it only as a best-effort fast-path cache; correctness
// is enforced by the database, so these mocks let us simulate Redis being present,
// stale, or entirely unavailable.
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

func setupTestServices(t *testing.T) (*service.PostingService, *gorm.DB, context.Context, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.LedgerPostingEntity{},
		&persistence.FinancialBookingLogEntity{},
		&persistence.DepositIdempotencyEntity{},
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

	// Per-deposit idempotency marker table (MON-3). The unique primary key on
	// dedupe_key enforces exactly-once deposit processing at the database layer.
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.deposit_idempotency (
		dedupe_key VARCHAR(64) PRIMARY KEY,
		account_id VARCHAR(255) NOT NULL,
		correlation_id VARCHAR(255),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create audit_outbox table for GORM hooks
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
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewLedgerRepository(db)
	svc := service.NewPostingServiceWithConfig(service.PostingServiceConfig{
		Repo:              repo,
		BankCashAccountID: "BANK-CASH-001",
	})
	return svc, db, ctx, cleanup
}

// countPostings returns the total number of ledger postings in the test tenant
// schema. Each deposit writes two postings (debit + credit), so this is used to
// assert the exactly-once guarantee. Each test gets its own isolated database.
func countPostings(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	schema := tenant.TenantID(testTenantID).SchemaName()
	var count int64
	err := db.Table(schema + ".ledger_posting").Count(&count).Error
	require.NoError(t, err)
	return count
}

func validDepositEvent(accountID, correlationID string, amountCents int64) *eventsv1.DepositEvent {
	now := timestamppb.Now()
	return &eventsv1.DepositEvent{
		AccountId:      accountID,
		AmountCents:    amountCents,
		InstrumentCode: "GBP",
		CorrelationId:  correlationID,
		ValueDate:      now,
		Timestamp:      now,
	}
}

func newTestConsumer(t *testing.T, postingService *service.PostingService, idemp idempotency.Service) *DepositConsumer {
	t.Helper()
	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, idemp)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	t.Cleanup(func() { _ = consumer.Close() })
	return consumer
}

func TestNewDepositConsumer(t *testing.T) {
	postingService, _, _, cleanup := setupTestServices(t)
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
	postingService, _, _, cleanup := setupTestServices(t)
	defer cleanup()

	_, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency service cannot be nil")
}

func TestDepositConsumer_HandleDepositEvent(t *testing.T) {
	postingService, _, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	consumer := newTestConsumer(t, postingService, newMockIdempotencyService())

	tests := []struct {
		name    string
		event   *eventsv1.DepositEvent
		wantErr bool
	}{
		{
			name:    "valid deposit event",
			event:   validDepositEvent("ACC-123", "deposit-001", 10000),
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
			testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			err := consumer.handleDepositEvent(testCtx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleDepositEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDepositConsumer_HandleDepositEvent_NilTimestamp(t *testing.T) {
	postingService, _, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	consumer := newTestConsumer(t, postingService, newMockIdempotencyService())

	event := &eventsv1.DepositEvent{
		AccountId:      "ACC-NO-TS",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		CorrelationId:  "deposit-no-ts",
		ValueDate:      timestamppb.Now(),
		Timestamp:      nil,
	}

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := consumer.handleDepositEvent(testCtx, event)
	require.Error(t, err)
}

// TestDepositConsumer_RedisFastPath_Skips verifies that when Redis reports a
// deposit as already processed, the consumer short-circuits and writes no
// postings to the database.
func TestDepositConsumer_RedisFastPath_Skips(t *testing.T) {
	postingService, db, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
		return &idempotency.Result{Status: idempotency.StatusCompleted}, idempotency.ErrOperationAlreadyProcessed
	}

	consumer := newTestConsumer(t, postingService, mockIdemp)

	event := validDepositEvent("ACC-FASTPATH", "deposit-fastpath", 10000)

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	require.NoError(t, consumer.handleDepositEvent(testCtx, event))
	assert.Equal(t, int64(0), countPostings(t, db),
		"Redis fast path should skip the database write entirely")
}

// TestDepositConsumer_RedisDown_StillProcesses proves that idempotency survives
// Redis being unavailable: even when both Check and StoreResult error, the
// deposit is processed via the authoritative database path.
func TestDepositConsumer_RedisDown_StillProcesses(t *testing.T) {
	postingService, db, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	redisErr := errors.New("redis connection refused")
	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
		return nil, redisErr
	}
	mockIdemp.storeResultFunc = func(_ context.Context, _ idempotency.Result) error {
		return redisErr
	}

	consumer := newTestConsumer(t, postingService, mockIdemp)

	event := validDepositEvent("ACC-REDIS-DOWN", "deposit-redis-down", 10000)

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	require.NoError(t, consumer.handleDepositEvent(testCtx, event),
		"deposit must succeed even when Redis is unavailable")
	assert.Equal(t, int64(2), countPostings(t, db),
		"deposit should be posted (debit + credit) despite Redis errors")
}

// TestDepositConsumer_DuplicateRedelivery_NoDoublePosting is the core MON-3
// guarantee: redelivering the same committed deposit produces no second posting.
func TestDepositConsumer_DuplicateRedelivery_NoDoublePosting(t *testing.T) {
	postingService, db, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	// Redis is deliberately blind (always "not processed") so the database marker
	// is the only thing preventing a duplicate.
	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
		return nil, idempotency.ErrResultNotFound
	}

	consumer := newTestConsumer(t, postingService, mockIdemp)

	event := validDepositEvent("ACC-REDELIVER", "deposit-redeliver", 10000)

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// First delivery.
	require.NoError(t, consumer.handleDepositEvent(testCtx, event))
	// Redelivery of the identical event (same bytes -> same dedupe key).
	require.NoError(t, consumer.handleDepositEvent(testCtx, event))

	assert.Equal(t, int64(2), countPostings(t, db),
		"redelivery must not create a second double-entry posting")
}

// TestDepositConsumer_DistinctDepositsSameCorrelation_BothPost verifies that two
// genuinely distinct deposits that happen to share a correlation ID are BOTH
// processed - i.e. the dedupe key is not the correlation ID.
func TestDepositConsumer_DistinctDepositsSameCorrelation_BothPost(t *testing.T) {
	postingService, db, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	consumer := newTestConsumer(t, postingService, newMockIdempotencyService())

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Same account and correlation ID, but different amounts -> distinct deposits.
	first := validDepositEvent("ACC-SAME-CORR", "shared-correlation", 10000)
	second := validDepositEvent("ACC-SAME-CORR", "shared-correlation", 25000)

	require.NoError(t, consumer.handleDepositEvent(testCtx, first))
	require.NoError(t, consumer.handleDepositEvent(testCtx, second))

	assert.Equal(t, int64(4), countPostings(t, db),
		"distinct deposits sharing a correlation ID must both post")
}

// TestDepositConsumer_IdempotencyKeyFormat verifies the Redis fast-path key is
// keyed on the per-deposit dedupe fingerprint, not the correlation ID.
func TestDepositConsumer_IdempotencyKeyFormat(t *testing.T) {
	postingService, _, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	var capturedKey idempotency.Key
	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkFunc = func(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
		capturedKey = key
		return nil, idempotency.ErrResultNotFound
	}

	consumer := newTestConsumer(t, postingService, mockIdemp)

	event := validDepositEvent("ACC-KEY-FORMAT", "correlation-123", 10000)

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_ = consumer.handleDepositEvent(testCtx, event)

	expectedDedupe := buildDepositDedupeKey(testCtx, event)
	assert.Equal(t, "financial-accounting", capturedKey.Namespace)
	assert.Equal(t, "process-deposit", capturedKey.Operation)
	assert.Equal(t, "ACC-KEY-FORMAT", capturedKey.EntityID)
	assert.Equal(t, testTenantID, capturedKey.TenantID)
	assert.Equal(t, expectedDedupe, capturedKey.RequestID,
		"Redis key must use the per-deposit dedupe fingerprint, not the correlation ID")
	assert.NotEqual(t, "correlation-123", capturedKey.RequestID)
}

// TestDepositConsumer_MarksRedisOnSuccess verifies that a successful deposit
// records a completed marker in Redis for the fast path on future deliveries.
func TestDepositConsumer_MarksRedisOnSuccess(t *testing.T) {
	postingService, _, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	var storeResultCalled bool
	var capturedResult idempotency.Result
	mockIdemp := newMockIdempotencyService()
	mockIdemp.storeResultFunc = func(_ context.Context, result idempotency.Result) error {
		storeResultCalled = true
		capturedResult = result
		return nil
	}

	consumer := newTestConsumer(t, postingService, mockIdemp)

	event := validDepositEvent("ACC-SUCCESS", "deposit-success", 10000)

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	require.NoError(t, consumer.handleDepositEvent(testCtx, event))

	assert.True(t, storeResultCalled, "StoreResult should be called on success")
	assert.Equal(t, idempotency.StatusCompleted, capturedResult.Status)
	assert.Equal(t, 24*time.Hour, capturedResult.TTL)
	assert.Nil(t, capturedResult.Data, "Data should be nil for events")
}

func TestBuildDepositDedupeKey(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(testTenantID))
	now := timestamppb.Now()
	later := timestamppb.New(now.AsTime().Add(time.Second))

	// newEvent constructs a fresh DepositEvent so we never copy a proto value
	// (proto messages contain a mutex and must not be copied).
	newEvent := func(amount int64, ts *timestamppb.Timestamp) *eventsv1.DepositEvent {
		return &eventsv1.DepositEvent{
			AccountId:      "ACC-DEDUPE",
			AmountCents:    amount,
			InstrumentCode: "GBP",
			CorrelationId:  "corr-1",
			ValueDate:      now,
			Timestamp:      ts,
		}
	}

	key1 := buildDepositDedupeKey(ctx, newEvent(10000, now))
	key2 := buildDepositDedupeKey(ctx, newEvent(10000, now))
	assert.Equal(t, key1, key2, "identical events must produce identical dedupe keys")
	assert.Len(t, key1, 64, "dedupe key should be a hex-encoded SHA-256 (64 chars)")

	// Differing amount -> different key.
	assert.NotEqual(t, key1, buildDepositDedupeKey(ctx, newEvent(20000, now)))

	// Same correlation ID but different timestamp -> different key.
	assert.NotEqual(t, key1, buildDepositDedupeKey(ctx, newEvent(10000, later)))

	// Different tenant -> different key.
	otherCtx := tenant.WithTenant(context.Background(), tenant.TenantID("other_tenant"))
	assert.NotEqual(t, key1, buildDepositDedupeKey(otherCtx, newEvent(10000, now)))
}

func TestDepositConsumer_LifecycleMethods_Exist(t *testing.T) {
	// Verify Start, Stop, and Close methods exist and have correct signatures.
	// Actual lifecycle testing requires a Kafka broker, so we test the
	// consumer's struct fields are properly initialized instead.
	postingService, _, _, cleanup := setupTestServices(t)
	defer cleanup()

	consumer := newTestConsumer(t, postingService, newMockIdempotencyService())

	// Verify all fields are initialized
	assert.NotNil(t, consumer.consumer, "ProtoConsumer should be initialized")
	assert.NotNil(t, consumer.postingService, "PostingService should be set")
	assert.NotNil(t, consumer.idempotency, "Idempotency service should be set")
	assert.NotNil(t, consumer.validator, "Validator should be initialized")
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
