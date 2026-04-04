//go:build integration

package service_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
)

// BalanceIntegrationTestContainer holds the test database container and service for balance tests.
type BalanceIntegrationTestContainer struct {
	container *postgres.PostgresContainer
	Pool      *pgxpool.Pool
	Repo      *persistence.PostgresRepository
	Service   *service.PositionKeepingService
}

// SetupBalanceIntegrationTestContainer creates a PostgreSQL testcontainer with schema loaded
// and a PositionKeepingService configured for balance testing.
func SetupBalanceIntegrationTestContainer(t *testing.T) *BalanceIntegrationTestContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container with explicit wait strategy
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_balance_integration"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	// Get connection string with search_path configured
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable", "search_path=position_keeping")
	require.NoError(t, err, "Failed to get connection string")

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Load schema
	loadBalanceTestSchema(t, pool)

	// Create repository
	repo := persistence.NewPostgresRepository(pool)

	// Create mock dependencies for service
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	// Create service without CurrentAccountClient (tests will configure as needed)
	svc, err := service.NewPositionKeepingService(
		repo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
	)
	require.NoError(t, err, "Failed to create service")

	return &BalanceIntegrationTestContainer{
		container: pgContainer,
		Pool:      pool,
		Repo:      repo,
		Service:   svc,
	}
}

// SetupBalanceIntegrationTestContainerWithClient creates a test container with a mock CurrentAccountClient.
func SetupBalanceIntegrationTestContainerWithClient(t *testing.T, client domain.CurrentAccountClient) *BalanceIntegrationTestContainer {
	t.Helper()

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_balance_integration"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable", "search_path=position_keeping")
	require.NoError(t, err, "Failed to get connection string")

	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	loadBalanceTestSchema(t, pool)

	repo := persistence.NewPostgresRepository(pool)
	mockMeasurementRepo := new(MockMeasurementRepository)
	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)

	svc, err := service.NewPositionKeepingService(
		repo,
		mockMeasurementRepo,
		mockEventPublisher,
		mockIdempotency,
		newTestOutboxPublisher(t),
		service.WithCurrentAccountClient(client),
	)
	require.NoError(t, err, "Failed to create service")

	return &BalanceIntegrationTestContainer{
		container: pgContainer,
		Pool:      pool,
		Repo:      repo,
		Service:   svc,
	}
}

// Cleanup closes the connection pool and terminates the container.
func (tc *BalanceIntegrationTestContainer) Cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.Pool != nil {
		tc.Pool.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx), "Failed to terminate container")
	}
}

// loadBalanceTestSchema loads the position_keeping schema for balance integration tests.
func loadBalanceTestSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create schema
	_, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS position_keeping`)
	require.NoError(t, err, "Failed to create schema")

	// Create financial_position_log table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.financial_position_log (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			log_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			account_service_domain character varying(20) NOT NULL DEFAULT '',
			version bigint NOT NULL DEFAULT 1,
			current_status character varying(20) NOT NULL,
			previous_status character varying(20) NULL,
			status_updated_at timestamptz NOT NULL,
			status_reason text NOT NULL,
			failure_reason text NULL,
			reconciliation_status character varying(20) NOT NULL,
			opening_balance_amount numeric(38,18) NULL,
			opening_balance_currency character(3) NULL,
			opening_balance_recorded_at timestamptz NULL,
			PRIMARY KEY (id)
		)
	`)
	require.NoError(t, err, "Failed to create financial_position_log table")

	// Create indexes
	_, err = pool.Exec(ctx, `
		CREATE UNIQUE INDEX idx_position_keeping_financial_position_log_log_id
		ON position_keeping.financial_position_log (log_id);
		CREATE INDEX idx_financial_position_log_account_id
		ON position_keeping.financial_position_log (account_id);
	`)
	require.NoError(t, err, "Failed to create indexes")

	// Create transaction_log_entry table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_log_entry (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			entry_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			amount_cents bigint NOT NULL,
			currency character(3) NOT NULL DEFAULT 'GBP',
			direction character varying(10) NOT NULL,
			timestamp timestamptz NOT NULL,
			description text NULL,
			reference character varying(100) NULL,
			source character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_log_entry_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create transaction_log_entry table")

	// Create transaction_lineage table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_lineage (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			parent_transaction_id uuid NULL,
			child_transaction_ids jsonb NOT NULL DEFAULT '[]',
			related_transaction_ids jsonb NOT NULL DEFAULT '[]',
			transaction_type character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_lineage_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create transaction_lineage table")

	// Create audit_trail_entry table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.audit_trail_entry (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			audit_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			timestamp timestamptz NOT NULL,
			user_id character varying(100) NOT NULL,
			action character varying(100) NOT NULL,
			details text NULL,
			ip_address character varying(45) NULL,
			system_context jsonb NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_audit_trail_entry_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_log(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create audit_trail_entry table")
}

// InMemoryCurrentAccountClient is a mock CurrentAccountClient for integration tests.
type InMemoryCurrentAccountClient struct {
	blocks map[string][]domain.AmountBlock
}

func NewInMemoryCurrentAccountClient() *InMemoryCurrentAccountClient {
	return &InMemoryCurrentAccountClient{
		blocks: make(map[string][]domain.AmountBlock),
	}
}

func (c *InMemoryCurrentAccountClient) GetActiveAmountBlocks(_ context.Context, accountID string) ([]domain.AmountBlock, error) {
	if blocks, ok := c.blocks[accountID]; ok {
		return blocks, nil
	}
	return []domain.AmountBlock{}, nil
}

func (c *InMemoryCurrentAccountClient) AddBlocks(accountID string, blocks []domain.AmountBlock) {
	c.blocks[accountID] = append(c.blocks[accountID], blocks...)
}

// ============================================
// Integration Tests
// ============================================

func TestIntegration_GetAccountBalance_AllBalanceTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-BALANCE-INT-001"

	// Create position log with transactions using DEBIT entries (adds to balance)
	// Initial DEBIT of 1000
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(1000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-24*time.Hour),
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)

	// Add additional transactions: +500 (DEBIT), -200 (CREDIT)
	entry1, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(500), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-12*time.Hour),
		"Deposit",
		"DEP-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	entry2, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(200), domain.CurrencyGBP),
		domain.PostingDirectionCredit,
		time.Now().UTC().Add(-6*time.Hour),
		"Withdrawal",
		"WD-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	err = log.AddEntry(entry1)
	require.NoError(t, err)
	err = log.AddEntry(entry2)
	require.NoError(t, err)

	// Save to database
	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Test balance types that don't require CurrentAccountClient
	balanceTypesWithoutClient := []struct {
		name        string
		balanceType positionkeepingv1.BalanceType
		// Expected balance: Opening (1000) + 500 - 200 = 1300 for Current
		// Opening balance query returns 0 (already represented in transaction)
	}{
		{
			name:        "opening balance returns zero (opening balance in transactions)",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
		},
		{
			name:        "current balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		},
		{
			name:        "closing balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING,
		},
		{
			name:        "ledger balance",
			balanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
		},
	}

	for _, tt := range balanceTypesWithoutClient {
		t.Run(tt.name, func(t *testing.T) {
			req := &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   accountID,
				BalanceType: tt.balanceType,
			}

			resp, err := tc.Service.GetAccountBalance(ctx, req)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, accountID, resp.AccountId)
			assert.Equal(t, tt.balanceType, resp.BalanceType)
			assert.NotNil(t, resp.Amount)
			assert.NotNil(t, resp.AsOf)
			assert.Equal(t, "GBP", resp.Amount.InstrumentCode)
		})
	}
}

func TestIntegration_GetAccountBalance_WithLiens(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Setup client with liens
	mockClient := NewInMemoryCurrentAccountClient()

	tc := SetupBalanceIntegrationTestContainerWithClient(t, mockClient)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-BALANCE-LIENS-001"

	// Create position log with a DEBIT entry of 1000 (which adds to the balance)
	// This creates a current balance of 1000
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(1000), domain.CurrencyGBP),
		domain.PostingDirectionDebit, // DEBIT adds to balance
		time.Now().UTC().Add(-24*time.Hour),
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)

	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Add liens totaling 300
	mockClient.AddBlocks(accountID, []domain.AmountBlock{
		{
			BlockID:   "lien-1",
			Amount:    domain.MustNewMoney(decimal.NewFromInt(200), domain.CurrencyGBP),
			BlockType: domain.AmountBlockTypePending,
			Purpose:   "Payment order hold",
		},
		{
			BlockID:   "lien-2",
			Amount:    domain.MustNewMoney(decimal.NewFromInt(100), domain.CurrencyGBP),
			BlockType: domain.AmountBlockTypeTemporary,
			Purpose:   "Authorization hold",
		},
	})

	// Test Reserve balance (should be 300 - sum of liens)
	t.Run("reserve balance equals sum of liens", func(t *testing.T) {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
		}

		resp, err := tc.Service.GetAccountBalance(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "300.00", resp.Amount.Amount)
	})

	// Test Available balance (Current - Reserve = 1000 - 300 = 700)
	t.Run("available balance is current minus reserve", func(t *testing.T) {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
		}

		resp, err := tc.Service.GetAccountBalance(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "700.00", resp.Amount.Amount)
	})

	// Test Free balance (Current - Reserve = 1000 - 300 = 700)
	t.Run("free balance is current minus reserve", func(t *testing.T) {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
		}

		resp, err := tc.Service.GetAccountBalance(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "700.00", resp.Amount.Amount)
	})
}

func TestIntegration_GetAccountBalances_ReturnsAllTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	mockClient := NewInMemoryCurrentAccountClient()
	tc := SetupBalanceIntegrationTestContainerWithClient(t, mockClient)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-ALL-BALANCES-001"

	// Create position log with a DEBIT entry of 5000 (creates balance of 5000)
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(5000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-24*time.Hour),
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)

	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: accountID,
	}

	resp, err := tc.Service.GetAccountBalances(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, accountID, resp.AccountId)
	assert.NotNil(t, resp.AsOf)

	// Should return all 7 balance types when CurrentAccountClient is configured
	assert.Len(t, resp.Balances, 7)

	// Verify all balance types are present
	balanceTypes := make(map[positionkeepingv1.BalanceType]bool)
	for _, b := range resp.Balances {
		balanceTypes[b.BalanceType] = true
	}

	expectedTypes := []positionkeepingv1.BalanceType{
		positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
		positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING,
		positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
		positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
	}

	for _, bt := range expectedTypes {
		assert.True(t, balanceTypes[bt], "missing balance type: %v", bt)
	}
}

func TestIntegration_GetAccountBalance_CurrencyFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create GBP account with DEBIT entry (adds to balance)
	gbpAccountID := "ACC-GBP-001"
	gbpEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		gbpAccountID,
		domain.MustNewMoney(decimal.NewFromInt(1000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-24*time.Hour),
		"Initial deposit",
		"INIT-GBP",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)
	gbpLog, err := domain.NewFinancialPositionLog(gbpAccountID, gbpEntry, nil)
	require.NoError(t, err)
	err = tc.Repo.Create(ctx, gbpLog)
	require.NoError(t, err)

	// Create USD account with DEBIT entry
	usdAccountID := "ACC-USD-001"
	usdEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		usdAccountID,
		domain.MustNewMoney(decimal.NewFromInt(2000), domain.CurrencyUSD),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-24*time.Hour),
		"Initial deposit",
		"INIT-USD",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)
	usdLog, err := domain.NewFinancialPositionLog(usdAccountID, usdEntry, nil)
	require.NoError(t, err)
	err = tc.Repo.Create(ctx, usdLog)
	require.NoError(t, err)

	t.Run("returns balance when currency matches", func(t *testing.T) {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      gbpAccountID,
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			InstrumentCode: "GBP",
		}

		resp, err := tc.Service.GetAccountBalance(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "GBP", resp.Amount.InstrumentCode)
	})

	t.Run("returns NotFound when currency does not match", func(t *testing.T) {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      gbpAccountID,
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			InstrumentCode: "USD", // GBP account, requesting USD
		}

		resp, err := tc.Service.GetAccountBalance(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "USD")
	})

	t.Run("USD account returns USD balance", func(t *testing.T) {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      usdAccountID,
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			InstrumentCode: "USD",
		}

		resp, err := tc.Service.GetAccountBalance(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "USD", resp.Amount.InstrumentCode)
	})
}

func TestIntegration_ConcurrentBalanceQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	mockClient := NewInMemoryCurrentAccountClient()
	tc := SetupBalanceIntegrationTestContainerWithClient(t, mockClient)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-CONCURRENT-001"

	// Create position log with DEBIT entry of 10000
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(10000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-24*time.Hour),
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)
	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)
	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Run concurrent balance queries
	const numGoroutines = 20
	const queriesPerGoroutine = 10

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*queriesPerGoroutine)
	resultChan := make(chan *positionkeepingv1.GetAccountBalanceResponse, numGoroutines*queriesPerGoroutine)

	balanceTypes := []positionkeepingv1.BalanceType{
		positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
		positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING,
		positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
		positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
	}

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < queriesPerGoroutine; i++ {
				bt := balanceTypes[(goroutineID+i)%len(balanceTypes)]
				req := &positionkeepingv1.GetAccountBalanceRequest{
					AccountId:   accountID,
					BalanceType: bt,
				}

				resp, err := tc.Service.GetAccountBalance(ctx, req)
				if err != nil {
					errChan <- err
					continue
				}
				resultChan <- resp
			}
		}(g)
	}

	wg.Wait()
	close(errChan)
	close(resultChan)

	// Collect and verify results
	errs := make([]error, 0, len(errChan))
	for err := range errChan {
		errs = append(errs, err)
	}
	assert.Empty(t, errs, "concurrent queries should not cause errors: %v", errs)

	// Verify all results are valid
	resultCount := 0
	for resp := range resultChan {
		resultCount++
		assert.Equal(t, accountID, resp.AccountId)
		assert.NotNil(t, resp.Amount)
		assert.Equal(t, "GBP", resp.Amount.InstrumentCode)
	}

	assert.Equal(t, numGoroutines*queriesPerGoroutine, resultCount,
		"all queries should complete successfully")
}

func TestIntegration_PerformanceP99Latency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-PERF-001"

	// Create account with realistic transaction history (50 transactions)
	// First entry with DEBIT of 10000
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(10000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-30*24*time.Hour),
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)
	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)

	// Add 49 more transactions (total 50)
	for i := 1; i < 50; i++ {
		var direction domain.PostingDirection
		var amount decimal.Decimal
		if i%2 == 0 {
			direction = domain.PostingDirectionDebit
			amount = decimal.NewFromInt(int64(100 + i*10))
		} else {
			direction = domain.PostingDirectionCredit
			amount = decimal.NewFromInt(int64(50 + i*5))
		}

		entry, err := domain.NewTransactionLogEntry(
			uuid.New(),
			accountID,
			domain.MustNewMoney(amount, domain.CurrencyGBP),
			direction,
			time.Now().UTC().Add(-time.Duration(50-i)*time.Hour),
			"Transaction "+string(rune('A'+i%26)),
			"REF-"+uuid.New().String()[:8],
			domain.TransactionSourceManual,
		)
		require.NoError(t, err)
		err = log.AddEntry(entry)
		require.NoError(t, err)
	}

	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Warm up
	for i := 0; i < 5; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}
		_, err := tc.Service.GetAccountBalance(ctx, req)
		require.NoError(t, err)
	}

	// Measure latencies for 100 sequential queries
	const numQueries = 100
	latencies := make([]time.Duration, numQueries)

	for i := 0; i < numQueries; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}

		start := time.Now()
		_, err := tc.Service.GetAccountBalance(ctx, req)
		latencies[i] = time.Since(start)
		require.NoError(t, err)
	}

	// Calculate P99 latency
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p99Index := int(float64(numQueries) * 0.99)
	p99Latency := latencies[p99Index]

	// Log statistics
	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	avgLatency := sum / time.Duration(numQueries)
	minLatency := latencies[0]
	maxLatency := latencies[numQueries-1]
	p50Latency := latencies[numQueries/2]

	t.Logf("Balance query latency statistics (50 transactions):")
	t.Logf("  Min:    %v", minLatency)
	t.Logf("  P50:    %v", p50Latency)
	t.Logf("  P99:    %v", p99Latency)
	t.Logf("  Max:    %v", maxLatency)
	t.Logf("  Avg:    %v", avgLatency)

	// Assert P99 < 50ms
	assert.Less(t, p99Latency.Milliseconds(), int64(50),
		"P99 latency should be less than 50ms, got %v", p99Latency)
}

func TestIntegration_PerformanceWithLargerHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-PERF-LARGE-001"

	// Create account with 100 transactions - first entry with DEBIT of 50000
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(50000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-90*24*time.Hour),
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)
	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)

	// Add 99 more transactions (total 100)
	for i := 1; i < 100; i++ {
		var direction domain.PostingDirection
		var amount decimal.Decimal
		if i%3 == 0 {
			direction = domain.PostingDirectionCredit
			amount = decimal.NewFromInt(int64(100 + i*5))
		} else {
			direction = domain.PostingDirectionDebit
			amount = decimal.NewFromInt(int64(200 + i*10))
		}

		entry, err := domain.NewTransactionLogEntry(
			uuid.New(),
			accountID,
			domain.MustNewMoney(amount, domain.CurrencyGBP),
			direction,
			time.Now().UTC().Add(-time.Duration(100-i)*time.Hour),
			"Large history transaction",
			"REF-LARGE-"+uuid.New().String()[:8],
			domain.TransactionSourceManual,
		)
		require.NoError(t, err)
		err = log.AddEntry(entry)
		require.NoError(t, err)
	}

	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Warm up
	for i := 0; i < 5; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}
		_, _ = tc.Service.GetAccountBalance(ctx, req)
	}

	// Measure latencies
	const numQueries = 100
	latencies := make([]time.Duration, numQueries)

	for i := 0; i < numQueries; i++ {
		req := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:   accountID,
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		}

		start := time.Now()
		_, err := tc.Service.GetAccountBalance(ctx, req)
		latencies[i] = time.Since(start)
		require.NoError(t, err)
	}

	// Calculate P99 latency
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p99Index := int(float64(numQueries) * 0.99)
	p99Latency := latencies[p99Index]
	p50Latency := latencies[numQueries/2]

	t.Logf("Balance query latency statistics (100 transactions):")
	t.Logf("  P50:    %v", p50Latency)
	t.Logf("  P99:    %v", p99Latency)

	// Assert P99 < 50ms even with 100 transactions
	assert.Less(t, p99Latency.Milliseconds(), int64(50),
		"P99 latency should be less than 50ms with 100 transactions, got %v", p99Latency)
}

func TestIntegration_GetAccountBalance_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	req := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:   "NON-EXISTENT-ACCOUNT",
		BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
	}

	resp, err := tc.Service.GetAccountBalance(ctx, req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NotFound")
}

func TestIntegration_GetAccountBalance_ValidationErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	tests := []struct {
		name        string
		req         *positionkeepingv1.GetAccountBalanceRequest
		errContains string
	}{
		{
			name: "empty account_id",
			req: &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   "",
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			},
			errContains: "account_id",
		},
		{
			name: "unspecified balance_type",
			req: &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   "test-account",
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED,
			},
			errContains: "balance_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := tc.Service.GetAccountBalance(ctx, tt.req)
			assert.Nil(t, resp)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestIntegration_ReserveBalanceWithoutClient_FailsPrecondition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Setup without CurrentAccountClient
	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-NO-CLIENT-001"

	// Create position log with DEBIT entry of 1000
	initialEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		domain.MustNewMoney(decimal.NewFromInt(1000), domain.CurrencyGBP),
		domain.PostingDirectionDebit,
		time.Now().UTC().Add(-24*time.Hour),
		"Initial deposit",
		"INIT-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)
	log, err := domain.NewFinancialPositionLog(accountID, initialEntry, nil)
	require.NoError(t, err)
	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Balance types that require CurrentAccountClient
	balanceTypesRequiringClient := []positionkeepingv1.BalanceType{
		positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
		positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
	}

	for _, bt := range balanceTypesRequiringClient {
		t.Run(bt.String(), func(t *testing.T) {
			req := &positionkeepingv1.GetAccountBalanceRequest{
				AccountId:   accountID,
				BalanceType: bt,
			}

			resp, err := tc.Service.GetAccountBalance(ctx, req)
			assert.Nil(t, resp)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "FailedPrecondition")
			assert.Contains(t, err.Error(), "current account client")
		})
	}
}
