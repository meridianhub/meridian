// Package benchmarks_test provides performance benchmarks for the Internal Bank Account service.
//
// These benchmarks measure service-level performance including repository persistence,
// gRPC service operations, and Position Keeping integration.
//
// Target metrics from requirements:
//   - Account creation: P99 < 50ms
//   - Balance queries: P99 < 20ms (delegated to Position Keeping)
//   - List operations: P99 < 100ms for 100 accounts
//
// Run with: go test -bench=. -benchmem -benchtime=10s ./services/internal-bank-account/benchmarks/...
package benchmarks_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-bank-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-bank-account/domain"
	"github.com/meridianhub/meridian/services/internal-bank-account/service"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

const (
	benchTenantID = "bench_tenant"
)

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
		&persistence.InternalBankAccountEntity{},
		&persistence.StatusHistoryEntity{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(benchTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	// Create the internal_bank_account table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.internal_bank_account (
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
		instrument_code VARCHAR(32) NOT NULL,
		dimension VARCHAR(20) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		correspondent_bank_id VARCHAR(50),
		correspondent_bank_name VARCHAR(255),
		correspondent_external_ref VARCHAR(100),
		attributes JSONB NOT NULL DEFAULT '{}',
		version BIGINT NOT NULL DEFAULT 1
	)`, schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create internal_bank_account table: %v", err)
	}

	// Create the status history table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.internal_bank_account_status_history (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		account_id VARCHAR(100) NOT NULL,
		from_status VARCHAR(20) NOT NULL,
		to_status VARCHAR(20) NOT NULL,
		reason TEXT,
		changed_by VARCHAR(100) NOT NULL,
		changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`, schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create status_history table: %v", err)
	}

	// Create indexes
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_account_code ON %q.internal_bank_account (account_code)`, schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create account_code index: %v", err)
	}

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_account_type ON %q.internal_bank_account (account_type)`, schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create account_type index: %v", err)
	}

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_instrument_code ON %q.internal_bank_account (instrument_code)`, schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create instrument_code index: %v", err)
	}

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_status ON %q.internal_bank_account (status)`, schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create status index: %v", err)
	}

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to set search_path: %v", err)
	}

	// Create repository
	repo := persistence.NewRepository(db)

	// Create context with tenant ID
	ctx := tenant.WithTenant(context.Background(), tid)

	// Create mock Position Keeping client
	pkClient := newMockPositionKeepingClient()

	// Create service with mock clients
	svc, err := service.NewServiceWithClients(repo, pkClient, nil, nil, nil)
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
func createBenchAccount(tb testing.TB, tc *testContainer, accountCode string, accountType domain.AccountType) domain.InternalBankAccount {
	tb.Helper()

	accountID := fmt.Sprintf("IBA-%s", uuid.New().String())
	account, err := domain.NewInternalBankAccount(
		accountID,
		accountCode,
		fmt.Sprintf("Benchmark %s Account", accountType),
		accountType,
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
func createBenchAccounts(tb testing.TB, tc *testContainer, count int) []domain.InternalBankAccount {
	tb.Helper()

	accountTypes := []domain.AccountType{
		domain.AccountTypeClearing,
		domain.AccountTypeHolding,
		domain.AccountTypeSuspense,
		domain.AccountTypeRevenue,
		domain.AccountTypeExpense,
	}

	instrumentCodes := []string{"GBP", "USD", "EUR"}

	accounts := make([]domain.InternalBankAccount, count)

	for i := 0; i < count; i++ {
		accountType := accountTypes[i%len(accountTypes)]
		instrumentCode := instrumentCodes[i%len(instrumentCodes)]

		accountID := fmt.Sprintf("IBA-%s", uuid.New().String())
		accountCode := fmt.Sprintf("BENCH_%s_%04d", accountType, i)

		account, err := domain.NewInternalBankAccount(
			accountID,
			accountCode,
			fmt.Sprintf("Benchmark %s Account %d", accountType, i),
			accountType,
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
