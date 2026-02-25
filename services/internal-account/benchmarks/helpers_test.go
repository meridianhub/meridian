// Package benchmarks_test provides performance benchmarks for the Internal Account service.
//
// These benchmarks measure service-level performance including repository persistence,
// gRPC service operations, and Position Keeping integration.
//
// Target metrics from requirements:
//   - Account creation: P99 < 50ms
//   - Balance queries: P99 < 20ms (delegated to Position Keeping)
//   - List operations: P99 < 100ms for 100 accounts
//
// Run with: go test -bench=. -benchmem -benchtime=10s ./services/internal-account/benchmarks/...
package benchmarks_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/lib/pq"

	"github.com/google/uuid"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/services/internal-account/service"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

const (
	benchTenantID = "bench_tenant"
)

// benchStaticLoader implements cache.AccountTypeLoader for benchmark tests.
type benchStaticLoader struct {
	defs map[string]*accounttype.Definition
}

func (l *benchStaticLoader) LoadAccountType(_ context.Context, code string) (*accounttype.Definition, error) {
	def, ok := l.defs[code]
	if !ok {
		return nil, fmt.Errorf("account type not found: %s", code)
	}
	return def, nil
}

func (l *benchStaticLoader) ListActiveAccountTypes(_ context.Context) ([]*accounttype.Definition, error) {
	defs := make([]*accounttype.Definition, 0, len(l.defs))
	for _, def := range l.defs {
		defs = append(defs, def)
	}
	return defs, nil
}

// benchNilCELCompiler is a no-op CEL compiler for benchmark tests.
type benchNilCELCompiler struct{}

func (c *benchNilCELCompiler) CompileValidation(_ string) (cel.Program, error) { return nil, nil }
func (c *benchNilCELCompiler) CompileBucketKey(_ string) (cel.Program, error)  { return nil, nil }
func (c *benchNilCELCompiler) CompileEligibility(_ string) (cel.Program, error) {
	return nil, nil
}

// newBenchAccountTypeCache creates an account type cache with standard benchmark definitions.
func newBenchAccountTypeCache() *cache.LocalAccountTypeCache {
	defs := map[string]*accounttype.Definition{
		"CLEARING_USD": {
			Code: "CLEARING_USD", Version: 1,
			BehaviorClass: accounttype.BehaviorClassClearing, EligibilityCEL: "true",
			Status: accounttype.StatusActive,
		},
		"CLEARING_GBP": {
			Code: "CLEARING_GBP", Version: 1,
			BehaviorClass: accounttype.BehaviorClassClearing, EligibilityCEL: "true",
			Status: accounttype.StatusActive,
		},
		"HOLDING_GBP": {
			Code: "HOLDING_GBP", Version: 1,
			BehaviorClass: accounttype.BehaviorClassHolding, EligibilityCEL: "true",
			Status: accounttype.StatusActive,
		},
	}
	loader := &benchStaticLoader{defs: defs}
	compiler := &benchNilCELCompiler{}
	return cache.NewLocalAccountTypeCache(loader, compiler)
}

// testContainer holds all benchmark infrastructure components.
type testContainer struct {
	ctx                   context.Context
	db                    *gorm.DB
	repo                  domain.Repository
	service               *service.Service
	positionKeepingClient *mockPositionKeepingClient
	cleanup               func()
}

// mockPositionKeepingClient provides a mock implementation of the PositionKeepingClient
// that returns realistic balance data for benchmarking.
type mockPositionKeepingClient struct {
	// latency simulates network latency for more realistic benchmarks
	latency time.Duration
}

// GetAccountBalances returns mock balance data for benchmarking.
func (m *mockPositionKeepingClient) GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	if m.latency > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.latency):
		}
	}

	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					Amount:         "10000.00",
					InstrumentCode: req.InstrumentCode,
					Version:        1,
				},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
				Amount: &quantityv1.InstrumentAmount{
					Amount:         "9500.00",
					InstrumentCode: req.InstrumentCode,
					Version:        1,
				},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
				Amount: &quantityv1.InstrumentAmount{
					Amount:         "10000.00",
					InstrumentCode: req.InstrumentCode,
					Version:        1,
				},
			},
		},
		AsOf: timestamppb.Now(),
	}, nil
}

// GetAccountBalance returns mock balance data for a single balance type.
func (m *mockPositionKeepingClient) GetAccountBalance(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	if m.latency > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.latency):
		}
	}

	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId:   req.AccountId,
		BalanceType: req.BalanceType,
		Amount: &quantityv1.InstrumentAmount{
			Amount:         "10000.00",
			InstrumentCode: req.InstrumentCode,
			Version:        1,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

// Close implements the PositionKeepingClient interface.
func (m *mockPositionKeepingClient) Close() error {
	return nil
}

// newMockPositionKeepingClient creates a mock Position Keeping client for benchmarks.
func newMockPositionKeepingClient() *mockPositionKeepingClient {
	return &mockPositionKeepingClient{
		latency: 0, // No artificial latency by default
	}
}

// setupBenchContainer creates a test container for benchmarking.
// The container is reused across benchmark iterations to avoid setup overhead.
func setupBenchContainer(b *testing.B) *testContainer {
	b.Helper()

	tc := setupTestContainer(&testing.T{})
	b.Cleanup(func() {
		if tc.cleanup != nil {
			tc.cleanup()
		}
	})

	return tc
}

// setupTestContainer creates a fresh test container for validation tests.
func setupTestContainer(t *testing.T) *testContainer {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.InternalAccountEntity{},
		&persistence.StatusHistoryEntity{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(benchTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	// Create the internal_account table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.internal_account (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		created_by VARCHAR(100) NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_by VARCHAR(100) NOT NULL,
		deleted_at TIMESTAMPTZ,
		account_id VARCHAR(100) NOT NULL UNIQUE,
		account_code VARCHAR(50) NOT NULL,
		name VARCHAR(255) NOT NULL,
		account_type VARCHAR(20) NOT NULL,
		product_type_code VARCHAR(100) NULL,
		product_type_version INTEGER NULL,
		instrument_code VARCHAR(32) NOT NULL,
		dimension VARCHAR(20) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		counterparty_id VARCHAR(50),
		counterparty_name VARCHAR(255),
		counterparty_external_ref VARCHAR(100),
		attributes JSONB NOT NULL DEFAULT '{}',
		version BIGINT NOT NULL DEFAULT 1,
		clearing_purpose VARCHAR(32) NULL,
		org_party_id UUID NULL
	)`, pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to create internal_account table: %v", err)
	}

	// Create the status history table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.internal_account_status_history (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL,
		from_status VARCHAR(20) NOT NULL,
		to_status VARCHAR(20) NOT NULL,
		reason TEXT,
		changed_by VARCHAR(100) NOT NULL,
		changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`, pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to create status_history table: %v", err)
	}

	// Create indexes
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_account_code ON %s.internal_account (account_code)`, pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to create account_code index: %v", err)
	}

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_account_type ON %s.internal_account (account_type)`, pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to create account_type index: %v", err)
	}

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_instrument_code ON %s.internal_account (instrument_code)`, pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to create instrument_code index: %v", err)
	}

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_status ON %s.internal_account (status)`, pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to create status index: %v", err)
	}

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	if err != nil {
		t.Fatalf("Failed to set search_path: %v", err)
	}

	// Create repository
	repo := persistence.NewRepository(db)

	// Create context with tenant ID
	ctx := tenant.WithTenant(context.Background(), tid)

	// Create mock Position Keeping client
	pkClient := newMockPositionKeepingClient()

	// Create account type cache for product type resolution
	accountTypeCache := newBenchAccountTypeCache()

	// Create service with mock clients and cache
	svc, err := service.NewServiceFull(repo, pkClient, nil, nil, nil, service.WithAccountTypeCache(accountTypeCache))
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	return &testContainer{
		ctx:                   ctx,
		db:                    db,
		repo:                  repo,
		service:               svc,
		positionKeepingClient: pkClient,
		cleanup:               cleanup,
	}
}

// createBenchAccount creates and saves a single test account for benchmarking.
func createBenchAccount(tb testing.TB, tc *testContainer, accountCode string, accountType domain.AccountType) domain.InternalAccount {
	tb.Helper()

	// CLEARING accounts require a specific purpose; use GENERAL for benchmarks
	clearingPurpose := domain.ClearingPurposeUnspecified
	if accountType == domain.AccountTypeClearing {
		clearingPurpose = domain.ClearingPurposeGeneral
	}

	accountID := fmt.Sprintf("IBA-%s", uuid.New().String())
	account, err := domain.NewInternalAccount(
		accountID,
		accountCode,
		fmt.Sprintf("Benchmark %s Account", accountType),
		accountType,
		clearingPurpose,
		"GBP",
		"CURRENCY",
	)
	if err != nil {
		tb.Fatalf("Failed to create account: %v", err)
	}

	err = tc.repo.Save(tc.ctx, account)
	if err != nil {
		tb.Fatalf("Failed to save account: %v", err)
	}

	return account
}

// createBenchAccounts creates multiple accounts with varied types for benchmarking.
func createBenchAccounts(tb testing.TB, tc *testContainer, count int) []domain.InternalAccount {
	tb.Helper()

	accountTypes := []domain.AccountType{
		domain.AccountTypeClearing,
		domain.AccountTypeHolding,
		domain.AccountTypeSuspense,
		domain.AccountTypeRevenue,
		domain.AccountTypeExpense,
	}

	instrumentCodes := []string{"GBP", "USD", "EUR"}

	accounts := make([]domain.InternalAccount, count)

	for i := 0; i < count; i++ {
		accountType := accountTypes[i%len(accountTypes)]
		instrumentCode := instrumentCodes[i%len(instrumentCodes)]

		// CLEARING accounts require a specific purpose; use GENERAL for benchmarks
		clearingPurpose := domain.ClearingPurposeUnspecified
		if accountType == domain.AccountTypeClearing {
			clearingPurpose = domain.ClearingPurposeGeneral
		}

		accountID := fmt.Sprintf("IBA-%s", uuid.New().String())
		accountCode := fmt.Sprintf("BENCH_%s_%04d", accountType, i)

		account, err := domain.NewInternalAccount(
			accountID,
			accountCode,
			fmt.Sprintf("Benchmark %s Account %d", accountType, i),
			accountType,
			clearingPurpose,
			instrumentCode,
			"CURRENCY",
		)
		if err != nil {
			tb.Fatalf("Failed to create account %d: %v", i, err)
		}

		err = tc.repo.Save(tc.ctx, account)
		if err != nil {
			tb.Fatalf("Failed to save account %d: %v", i, err)
		}

		accounts[i] = account
	}

	return accounts
}
