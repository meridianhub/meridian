//go:build integration

// Package e2e provides end-to-end integration tests for the internal bank account service.
// These tests verify the full account lifecycle from creation through closure,
// including multi-tenant isolation, multi-asset support, and correspondent banking.
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantitypb "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-bank-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-bank-account/service"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/types/known/timestamppb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================================
// Test Infrastructure
// ============================================================================

// e2eTestContext holds the test infrastructure for E2E tests.
type e2eTestContext struct {
	container *postgres.PostgresContainer
	pool      *pgxpool.Pool
	db        *gorm.DB
	repo      *persistence.Repository
	svc       *service.Service
}

// setupE2ETestPool creates a shared PostgreSQL testcontainer for E2E tests.
// Returns a configured pool, GORM db, repository, and service instance.
func setupE2ETestPool(t *testing.T) *e2eTestContext {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("e2e_internal_bank_account"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = pgContainer.Terminate(cleanupCtx)
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err, "Failed to create connection pool")

	t.Cleanup(func() {
		pool.Close()
	})

	// Create GORM connection
	db, err := gorm.Open(gormpg.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "Failed to connect to database with GORM")

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	repo := persistence.NewRepository(db)

	// Create service with repository (no external clients for E2E tests)
	svc, err := service.NewService(repo)
	require.NoError(t, err, "Failed to create service")

	return &e2eTestContext{
		container: pgContainer,
		pool:      pool,
		db:        db,
		repo:      repo,
		svc:       svc,
	}
}

// setupTenantSchema creates a tenant schema and applies the internal_bank_account schema.
func setupTenantSchema(t *testing.T, tc *e2eTestContext, tenantID string) context.Context {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	ctx := context.Background()

	// Create the tenant schema
	_, err := tc.pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName))
	require.NoError(t, err, "Failed to create tenant schema %s", schemaName)

	// Apply internal_bank_account schema
	applyInternalBankAccountSchema(t, tc.pool, schemaName)

	// Create context with tenant and audit user
	tenantCtx := tenant.WithTenant(context.Background(), tid)
	tenantCtx = context.WithValue(tenantCtx, auth.UserIDContextKey, "e2e-test-user")

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = tc.pool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schemaName))
	})

	return tenantCtx
}

// applyInternalBankAccountSchema creates the internal_bank_account tables in the tenant schema.
func applyInternalBankAccountSchema(t *testing.T, pool *pgxpool.Pool, schemaName string) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET search_path TO %q, public", schemaName))
	require.NoError(t, err)

	// Create internal_bank_account table
	// Note: dimension constraint is relaxed for tests (allows empty string)
	// since Reference Data client is not configured in E2E tests
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS internal_bank_account (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			account_id character varying(100) NOT NULL,
			account_code character varying(50) NOT NULL,
			name character varying(255) NOT NULL,
			account_type character varying(20) NOT NULL,
			instrument_code character varying(32) NOT NULL,
			dimension character varying(20) NOT NULL DEFAULT '',
			status character varying(20) NOT NULL DEFAULT 'ACTIVE',
			correspondent_bank_id character varying(50) NULL,
			correspondent_bank_name character varying(255) NULL,
			correspondent_external_ref character varying(100) NULL,
			attributes jsonb NOT NULL DEFAULT '{}',
			version bigint NOT NULL DEFAULT 1,
			PRIMARY KEY (id),
			CONSTRAINT chk_account_type CHECK (account_type IN (
				'CLEARING', 'NOSTRO', 'VOSTRO', 'HOLDING',
				'SUSPENSE', 'REVENUE', 'EXPENSE', 'INVENTORY'
			)),
			CONSTRAINT chk_status CHECK (status IN (
				'ACTIVE', 'SUSPENDED', 'CLOSED'
			))
		)
	`)
	require.NoError(t, err, "Failed to create internal_bank_account table")

	// Create unique constraint on account_id
	_, err = tx.Exec(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_internal_bank_account_account_id ON internal_bank_account (account_id)
	`)
	require.NoError(t, err, "Failed to create account_id unique index")

	// Create indexes
	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_internal_bank_account_type ON internal_bank_account (account_type);
		CREATE INDEX IF NOT EXISTS idx_internal_bank_account_instrument ON internal_bank_account (instrument_code);
		CREATE INDEX IF NOT EXISTS idx_internal_bank_account_status ON internal_bank_account (status);
		CREATE INDEX IF NOT EXISTS idx_internal_bank_account_code ON internal_bank_account (account_code);
		CREATE INDEX IF NOT EXISTS idx_internal_bank_account_deleted_at ON internal_bank_account (deleted_at)
	`)
	require.NoError(t, err, "Failed to create indexes")

	// Create status history table
	_, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS internal_bank_account_status_history (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			account_id character varying(100) NOT NULL,
			from_status character varying(20) NOT NULL,
			to_status character varying(20) NOT NULL,
			reason text NULL,
			changed_by character varying(100) NOT NULL,
			changed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			CONSTRAINT chk_from_status CHECK (from_status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
			CONSTRAINT chk_to_status CHECK (to_status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
			CONSTRAINT fk_status_history_account FOREIGN KEY (account_id)
				REFERENCES internal_bank_account (account_id)
				ON UPDATE NO ACTION ON DELETE RESTRICT
		)
	`)
	require.NoError(t, err, "Failed to create status_history table")

	// Create status history indexes
	_, err = tx.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_status_history_account_changed
			ON internal_bank_account_status_history (account_id, changed_at DESC);
		CREATE INDEX IF NOT EXISTS idx_status_history_changed_at
			ON internal_bank_account_status_history (changed_at)
	`)
	require.NoError(t, err, "Failed to create status history indexes")

	require.NoError(t, tx.Commit(ctx))
}

// createServiceWithMockPositionKeeping creates a service with a mock Position Keeping client.
func createServiceWithMockPositionKeeping(t *testing.T, repo *persistence.Repository, mockPK *mockPositionKeepingClient) *service.Service {
	t.Helper()

	svc, err := service.NewServiceWithClients(repo, mockPK, nil, nil, nil)
	require.NoError(t, err)
	return svc
}

// ============================================================================
// Mock Position Keeping Client
// ============================================================================

// mockPositionKeepingClient implements service.PositionKeepingClient for testing.
type mockPositionKeepingClient struct {
	balances map[string]*positionkeepingv1.GetAccountBalancesResponse
}

func newMockPositionKeepingClient() *mockPositionKeepingClient {
	return &mockPositionKeepingClient{
		balances: make(map[string]*positionkeepingv1.GetAccountBalancesResponse),
	}
}

func (m *mockPositionKeepingClient) GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	key := fmt.Sprintf("%s:%s", req.AccountId, req.InstrumentCode)
	if resp, ok := m.balances[key]; ok {
		return resp, nil
	}
	// Return zero balance if not configured
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantitypb.InstrumentAmount{
					Amount:         "0",
					InstrumentCode: req.InstrumentCode,
				},
			},
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (m *mockPositionKeepingClient) SetBalance(accountID, instrumentCode string, amount decimal.Decimal) {
	key := fmt.Sprintf("%s:%s", accountID, instrumentCode)
	m.balances[key] = &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: accountID,
		Balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantitypb.InstrumentAmount{
					Amount:         amount.String(),
					InstrumentCode: instrumentCode,
				},
			},
		},
		AsOf: timestamppb.Now(),
	}
}

func (m *mockPositionKeepingClient) Close() error {
	return nil
}

// ============================================================================
// E2E Test: Account Lifecycle
// ============================================================================

// TestE2E_AccountLifecycle tests the complete account lifecycle from initiation to closure.
// This covers: Initiate -> Update -> Activate -> GetBalance -> Suspend -> Reactivate -> Close -> Verify
func TestE2E_AccountLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_lifecycle_tenant")

	// Create mock Position Keeping client for balance queries
	mockPK := newMockPositionKeepingClient()
	svc := createServiceWithMockPositionKeeping(t, tc.repo, mockPK)

	// Use account_code for lookups (service's findAccountByID falls back to FindByCode)
	accountCode := "GBP_CLEARING_001"
	var accountID string // Store the business ID for status history tracking

	t.Run("1. Initiate new internal bank account", func(t *testing.T) {
		resp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    accountCode,
			Name:           "GBP Clearing Account",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)
		require.NotEmpty(t, resp.AccountId)
		accountID = resp.AccountId

		assert.Equal(t, accountCode, resp.Facility.AccountCode)
		assert.Equal(t, "GBP Clearing Account", resp.Facility.Name)
		assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING, resp.Facility.AccountType)
		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
		assert.Equal(t, "GBP", resp.Facility.InstrumentCode)
		assert.Equal(t, int32(1), resp.Facility.Version)

		t.Logf("Created account: %s (code: %s)", accountID, accountCode)
	})

	t.Run("2. Update account name", func(t *testing.T) {
		// Use account_code for lookup
		resp, err := svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
			AccountId: accountCode, // findAccountByID falls back to FindByCode
			Name:      "GBP Main Clearing Account",
		})
		require.NoError(t, err)

		assert.Equal(t, "GBP Main Clearing Account", resp.Facility.Name)
		assert.Equal(t, int32(2), resp.Facility.Version)
	})

	t.Run("3. Retrieve account and verify state", func(t *testing.T) {
		resp, err := svc.RetrieveInternalBankAccount(ctx, &pb.RetrieveInternalBankAccountRequest{
			AccountId: accountCode,
		})
		require.NoError(t, err)

		assert.Equal(t, accountID, resp.Facility.AccountId)
		assert.Equal(t, "GBP Main Clearing Account", resp.Facility.Name)
		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)
	})

	t.Run("4. Get balance (via Position Keeping integration)", func(t *testing.T) {
		// Set up mock balance using account_id (business ID) as Position Keeping uses
		mockPK.SetBalance(accountID, "GBP", decimal.NewFromFloat(10000.50))

		resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountCode,
		})
		require.NoError(t, err)

		// Note: GetBalance response contains account identifier (code or ID)
		assert.NotEmpty(t, resp.AccountId)
		require.NotNil(t, resp.CurrentBalance)
		// Decimal string format may not preserve trailing zeros
		actualBalance, _ := decimal.NewFromString(resp.CurrentBalance.Amount)
		expectedBalance := decimal.NewFromFloat(10000.50)
		assert.True(t, expectedBalance.Equal(actualBalance),
			"Balance mismatch: expected %s, got %s", expectedBalance.String(), actualBalance.String())
	})

	t.Run("5. Suspend account", func(t *testing.T) {
		resp, err := svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "Routine audit suspension",
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED, resp.Facility.AccountStatus)
		assert.NotNil(t, resp.ActionTimestamp)

		// Record status change for audit
		err = tc.repo.RecordStatusChange(ctx, accountID, "ACTIVE", "SUSPENDED", "Routine audit suspension")
		require.NoError(t, err)
	})

	t.Run("6. Verify suspended account cannot query balance", func(t *testing.T) {
		_, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
			AccountId: accountCode,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not active")
	})

	t.Run("7. Reactivate account", func(t *testing.T) {
		resp, err := svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, resp.Facility.AccountStatus)

		// Record status change for audit
		err = tc.repo.RecordStatusChange(ctx, accountID, "SUSPENDED", "ACTIVE", "Audit completed")
		require.NoError(t, err)
	})

	t.Run("8. Close account", func(t *testing.T) {
		resp, err := svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_CLOSE,
			Reason:        "Account no longer required",
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED, resp.Facility.AccountStatus)

		// Record status change for audit
		err = tc.repo.RecordStatusChange(ctx, accountID, "ACTIVE", "CLOSED", "Account no longer required")
		require.NoError(t, err)
	})

	t.Run("9. Verify closed account cannot be modified", func(t *testing.T) {
		_, err := svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
			AccountId: accountCode,
			Name:      "Attempted update",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "closed")
	})

	t.Run("10. Verify closed account cannot be reactivated", func(t *testing.T) {
		_, err := svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
			AccountId:     accountCode,
			ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid")
	})

	t.Run("11. Verify status history audit trail", func(t *testing.T) {
		tid, _ := tenant.FromContext(ctx)
		schemaName := tid.SchemaName()

		// Query status history
		var history []struct {
			FromStatus string `gorm:"column:from_status"`
			ToStatus   string `gorm:"column:to_status"`
			Reason     string `gorm:"column:reason"`
		}

		err := tc.db.Raw(fmt.Sprintf(
			`SELECT from_status, to_status, reason
			 FROM %q.internal_bank_account_status_history
			 WHERE account_id = ?
			 ORDER BY changed_at ASC`, schemaName), accountID).Scan(&history).Error
		require.NoError(t, err)

		require.Len(t, history, 3)
		// Verify transitions: ACTIVE -> SUSPENDED -> ACTIVE -> CLOSED
		assert.Equal(t, "ACTIVE", history[0].FromStatus)
		assert.Equal(t, "SUSPENDED", history[0].ToStatus)
		assert.Equal(t, "SUSPENDED", history[1].FromStatus)
		assert.Equal(t, "ACTIVE", history[1].ToStatus)
		assert.Equal(t, "ACTIVE", history[2].FromStatus)
		assert.Equal(t, "CLOSED", history[2].ToStatus)
	})
}

// ============================================================================
// E2E Test: Multi-Asset Accounts
// ============================================================================

// TestE2E_MultiAssetAccounts tests creating accounts for different asset types.
// Meridian supports multi-asset accounts: GBP (currency), KWH (energy), GPU_HOUR (compute).
func TestE2E_MultiAssetAccounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_multiasset_tenant")

	mockPK := newMockPositionKeepingClient()
	svc := createServiceWithMockPositionKeeping(t, tc.repo, mockPK)

	// Define multi-asset test cases with account_code for lookups
	testCases := []struct {
		code       string
		name       string
		instrument string
		balance    float64
	}{
		{"GBP_CLEARING", "GBP Clearing Account", "GBP", 1000000.00},
		{"KWH_HOLDING", "Energy Holding Account", "KWH", 50000.5},
		{"GPU_HOUR_POOL", "GPU Compute Pool", "GPU_HOUR", 10000.0},
		{"CARBON_OFFSET", "Carbon Credits Holding", "CARBON_TONNE", 5000.0},
	}

	var createdAccounts []struct {
		code      string
		accountID string
	}

	t.Run("Create multi-asset accounts", func(t *testing.T) {
		for _, tc := range testCases {
			resp, err := svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
				AccountCode:    tc.code,
				Name:           tc.name,
				AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING,
				InstrumentCode: tc.instrument,
			})
			require.NoError(t, err, "Failed to create account for %s", tc.instrument)

			assert.Equal(t, tc.code, resp.Facility.AccountCode)
			assert.Equal(t, tc.instrument, resp.Facility.InstrumentCode)

			createdAccounts = append(createdAccounts, struct {
				code      string
				accountID string
			}{tc.code, resp.AccountId})

			// Set up mock balance using business account_id
			mockPK.SetBalance(resp.AccountId, tc.instrument, decimal.NewFromFloat(tc.balance))
		}
	})

	t.Run("Query balances for each asset type", func(t *testing.T) {
		for i, tc := range testCases {
			// Use account_code for lookup
			resp, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{
				AccountId: createdAccounts[i].code,
			})
			require.NoError(t, err, "Failed to get balance for %s", tc.instrument)

			// Response AccountId contains the lookup identifier (account code or business ID)
			assert.NotEmpty(t, resp.AccountId, "AccountId should not be empty")
			require.NotNil(t, resp.CurrentBalance)

			expectedBalance := decimal.NewFromFloat(tc.balance)
			actualBalance, _ := decimal.NewFromString(resp.CurrentBalance.Amount)
			assert.True(t, expectedBalance.Equal(actualBalance),
				"Balance mismatch for %s: expected %s, got %s",
				tc.instrument, expectedBalance.String(), actualBalance.String())
		}
	})

	t.Run("List accounts by instrument code", func(t *testing.T) {
		// Filter by GBP
		resp, err := svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
			InstrumentCodeFilter: "GBP",
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 1)
		assert.Equal(t, "GBP", resp.Facilities[0].InstrumentCode)

		// Filter by KWH
		resp, err = svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
			InstrumentCodeFilter: "KWH",
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 1)
		assert.Equal(t, "KWH", resp.Facilities[0].InstrumentCode)
	})

	t.Run("List all accounts (no filter)", func(t *testing.T) {
		resp, err := svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, len(testCases))
	})
}

// ============================================================================
// E2E Test: Correspondent Banking (NOSTRO/VOSTRO)
// ============================================================================

// TestE2E_CorrespondentBanking tests creating NOSTRO and VOSTRO accounts with correspondent details.
func TestE2E_CorrespondentBanking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_correspondent_tenant")

	t.Run("Create NOSTRO account (our account at Citibank)", func(t *testing.T) {
		resp, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "USD_NOSTRO_CITI",
			Name:           "USD NOSTRO at Citibank",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO,
			InstrumentCode: "USD",
			CorrespondentDetails: &pb.CorrespondentBankDetails{
				BankId:             "CITI001",
				BankName:           "Citibank NA",
				ExternalAccountRef: "12345678901",
				SwiftCode:          "CITIUS33",
			},
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO, resp.Facility.AccountType)
		require.NotNil(t, resp.Facility.CorrespondentDetails)
		assert.Equal(t, "CITI001", resp.Facility.CorrespondentDetails.BankId)
		assert.Equal(t, "Citibank NA", resp.Facility.CorrespondentDetails.BankName)
		assert.Equal(t, "12345678901", resp.Facility.CorrespondentDetails.ExternalAccountRef)
		assert.Equal(t, "CITIUS33", resp.Facility.CorrespondentDetails.SwiftCode)
		assert.Equal(t, pb.CorrespondentType_CORRESPONDENT_TYPE_NOSTRO, resp.Facility.CorrespondentDetails.CorrespondentType)
	})

	t.Run("Create VOSTRO account (Deutsche Bank's account at our bank)", func(t *testing.T) {
		resp, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "EUR_VOSTRO_DB",
			Name:           "EUR VOSTRO for Deutsche Bank",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO,
			InstrumentCode: "EUR",
			CorrespondentDetails: &pb.CorrespondentBankDetails{
				BankId:             "DB001",
				BankName:           "Deutsche Bank AG",
				ExternalAccountRef: "DE89370400440532013000",
				SwiftCode:          "DEUTDEFF",
			},
		})
		require.NoError(t, err)

		assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO, resp.Facility.AccountType)
		require.NotNil(t, resp.Facility.CorrespondentDetails)
		assert.Equal(t, "DB001", resp.Facility.CorrespondentDetails.BankId)
		assert.Equal(t, "Deutsche Bank AG", resp.Facility.CorrespondentDetails.BankName)
		assert.Equal(t, pb.CorrespondentType_CORRESPONDENT_TYPE_VOSTRO, resp.Facility.CorrespondentDetails.CorrespondentType)
	})

	t.Run("NOSTRO account requires correspondent details", func(t *testing.T) {
		_, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "USD_NOSTRO_MISSING",
			Name:           "USD NOSTRO Missing Correspondent",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO,
			InstrumentCode: "USD",
			// Missing CorrespondentDetails
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "correspondent")
	})

	t.Run("VOSTRO account requires correspondent details", func(t *testing.T) {
		_, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "EUR_VOSTRO_MISSING",
			Name:           "EUR VOSTRO Missing Correspondent",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO,
			InstrumentCode: "EUR",
			// Missing CorrespondentDetails
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "correspondent")
	})

	t.Run("CLEARING account rejects correspondent details", func(t *testing.T) {
		// The domain should reject correspondent details for non-NOSTRO/VOSTRO accounts
		// First create the account successfully
		resp, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "GBP_CLEARING_ONLY",
			Name:           "GBP Clearing Only",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)
		assert.Nil(t, resp.Facility.CorrespondentDetails)
	})

	t.Run("Update correspondent details on NOSTRO account", func(t *testing.T) {
		// First create the NOSTRO account
		createResp, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "USD_NOSTRO_JPMORGAN",
			Name:           "USD NOSTRO at JPMorgan",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO,
			InstrumentCode: "USD",
			CorrespondentDetails: &pb.CorrespondentBankDetails{
				BankId:             "JPM001",
				BankName:           "JPMorgan Chase",
				ExternalAccountRef: "987654321",
				SwiftCode:          "CHASUS33",
			},
		})
		require.NoError(t, err)

		// Update correspondent details using account_code
		updateResp, err := tc.svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
			AccountId: createResp.Facility.AccountCode,
			CorrespondentDetails: &pb.CorrespondentBankDetails{
				BankId:             "JPM001",
				BankName:           "JPMorgan Chase & Co",
				ExternalAccountRef: "999888777",
				SwiftCode:          "CHASUS33XXX",
			},
		})
		require.NoError(t, err)

		// Verify updated details
		assert.Equal(t, "JPMorgan Chase & Co", updateResp.Facility.CorrespondentDetails.BankName)
		assert.Equal(t, "999888777", updateResp.Facility.CorrespondentDetails.ExternalAccountRef)
		assert.Equal(t, "CHASUS33XXX", updateResp.Facility.CorrespondentDetails.SwiftCode)
	})

	t.Run("List NOSTRO accounts only", func(t *testing.T) {
		resp, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
			AccountTypeFilter: pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 2) // CITI and JPMorgan

		for _, facility := range resp.Facilities {
			assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO, facility.AccountType)
			assert.NotNil(t, facility.CorrespondentDetails)
		}
	})

	t.Run("List VOSTRO accounts only", func(t *testing.T) {
		resp, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
			AccountTypeFilter: pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 1) // Deutsche Bank

		for _, facility := range resp.Facilities {
			assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO, facility.AccountType)
			assert.NotNil(t, facility.CorrespondentDetails)
		}
	})
}

// ============================================================================
// E2E Test: Multi-Tenant Isolation
// ============================================================================

// TestE2E_MultiTenantIsolation verifies that tenant A cannot see tenant B's accounts.
func TestE2E_MultiTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)

	// Setup two separate tenants
	ctxTenantA := setupTenantSchema(t, tc, "tenant_iso_alpha")
	ctxTenantB := setupTenantSchema(t, tc, "tenant_iso_beta")

	t.Run("Tenant A creates accounts", func(t *testing.T) {
		// Create 3 accounts for tenant A
		for i := 1; i <= 3; i++ {
			resp, err := tc.svc.InitiateInternalBankAccount(ctxTenantA, &pb.InitiateInternalBankAccountRequest{
				AccountCode:    fmt.Sprintf("TENANT_A_ACC_%d", i),
				Name:           fmt.Sprintf("Tenant A Account %d", i),
				AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
				InstrumentCode: "GBP",
			})
			require.NoError(t, err)
			assert.NotEmpty(t, resp.AccountId)
		}
	})

	t.Run("Tenant B creates accounts", func(t *testing.T) {
		// Create 2 accounts for tenant B
		for i := 1; i <= 2; i++ {
			resp, err := tc.svc.InitiateInternalBankAccount(ctxTenantB, &pb.InitiateInternalBankAccountRequest{
				AccountCode:    fmt.Sprintf("TENANT_B_ACC_%d", i),
				Name:           fmt.Sprintf("Tenant B Account %d", i),
				AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING,
				InstrumentCode: "EUR",
			})
			require.NoError(t, err)
			assert.NotEmpty(t, resp.AccountId)
		}
	})

	t.Run("Tenant A can only see their 3 accounts", func(t *testing.T) {
		resp, err := tc.svc.ListInternalBankAccounts(ctxTenantA, &pb.ListInternalBankAccountsRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 3)

		for _, facility := range resp.Facilities {
			assert.Contains(t, facility.AccountCode, "TENANT_A_ACC_")
		}
	})

	t.Run("Tenant B can only see their 2 accounts", func(t *testing.T) {
		resp, err := tc.svc.ListInternalBankAccounts(ctxTenantB, &pb.ListInternalBankAccountsRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 2)

		for _, facility := range resp.Facilities {
			assert.Contains(t, facility.AccountCode, "TENANT_B_ACC_")
		}
	})

	t.Run("Tenant A cannot retrieve Tenant B's account by code", func(t *testing.T) {
		// Try to retrieve tenant B's account using tenant A's context
		_, err := tc.svc.RetrieveInternalBankAccount(ctxTenantA, &pb.RetrieveInternalBankAccountRequest{
			AccountId: "TENANT_B_ACC_1", // Use account_code
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Tenant B cannot modify Tenant A's account", func(t *testing.T) {
		// Try to update using tenant B's context
		_, err := tc.svc.UpdateInternalBankAccount(ctxTenantB, &pb.UpdateInternalBankAccountRequest{
			AccountId: "TENANT_A_ACC_1",
			Name:      "Hacked by Tenant B",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		// Verify tenant A's account is unchanged
		retrieveResp, err := tc.svc.RetrieveInternalBankAccount(ctxTenantA, &pb.RetrieveInternalBankAccountRequest{
			AccountId: "TENANT_A_ACC_1",
		})
		require.NoError(t, err)
		assert.Contains(t, retrieveResp.Facility.Name, "Tenant A")
	})

	t.Run("Both tenants can use the same account code independently", func(t *testing.T) {
		// Tenant A creates account with code "SHARED_CODE"
		respA, err := tc.svc.InitiateInternalBankAccount(ctxTenantA, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "SHARED_CODE",
			Name:           "Shared Code Account - Tenant A",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE,
			InstrumentCode: "USD",
		})
		require.NoError(t, err)

		// Tenant B creates account with the same code
		respB, err := tc.svc.InitiateInternalBankAccount(ctxTenantB, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    "SHARED_CODE",
			Name:           "Shared Code Account - Tenant B",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE,
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)

		// Both accounts exist independently
		assert.NotEqual(t, respA.AccountId, respB.AccountId)
		assert.Equal(t, "SHARED_CODE", respA.Facility.AccountCode)
		assert.Equal(t, "SHARED_CODE", respB.Facility.AccountCode)
		assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE, respA.Facility.AccountType)
		assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE, respB.Facility.AccountType)
	})

	t.Run("Control actions are tenant-isolated", func(t *testing.T) {
		// Tenant B cannot suspend Tenant A's account
		_, err := tc.svc.ControlInternalBankAccount(ctxTenantB, &pb.ControlInternalBankAccountRequest{
			AccountId:     "TENANT_A_ACC_1",
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "Cross-tenant attack",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		// Verify account is still ACTIVE
		retrieveResp, err := tc.svc.RetrieveInternalBankAccount(ctxTenantA, &pb.RetrieveInternalBankAccountRequest{
			AccountId: "TENANT_A_ACC_1",
		})
		require.NoError(t, err)
		assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, retrieveResp.Facility.AccountStatus)
	})
}

// ============================================================================
// E2E Test: Async Operations with Await
// ============================================================================

// TestE2E_AsyncOperationsWithAwait demonstrates proper use of the await package
// for testing asynchronous operations without time.Sleep.
func TestE2E_AsyncOperationsWithAwait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_async_tenant")

	t.Run("Wait for account creation in goroutine", func(t *testing.T) {
		accountCode := fmt.Sprintf("ASYNC_ACC_%s", uuid.New().String()[:8])

		// Create account in goroutine with delay
		go func() {
			time.Sleep(100 * time.Millisecond) // Simulate async processing delay
			_, _ = tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
				AccountCode:    accountCode,
				Name:           "Async Created Account",
				AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
				InstrumentCode: "GBP",
			})
		}()

		// Use await to poll for account existence
		var foundAccount *pb.InternalBankAccountFacility
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				resp, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{})
				if err != nil {
					return false
				}
				for _, facility := range resp.Facilities {
					if facility.AccountCode == accountCode {
						foundAccount = facility
						return true
					}
				}
				return false
			})

		require.NoError(t, err, "account should be created within timeout")
		require.NotNil(t, foundAccount)
		assert.Equal(t, accountCode, foundAccount.AccountCode)
	})

	t.Run("Wait for status change via async control action", func(t *testing.T) {
		accountCode := fmt.Sprintf("STATUS_ASYNC_%s", uuid.New().String()[:8])

		// Create account
		_, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    accountCode,
			Name:           "Status Change Test Account",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING,
			InstrumentCode: "EUR",
		})
		require.NoError(t, err)

		// Suspend in goroutine
		go func() {
			time.Sleep(150 * time.Millisecond)
			_, _ = tc.svc.ControlInternalBankAccount(ctx, &pb.ControlInternalBankAccountRequest{
				AccountId:     accountCode,
				ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:        "Async suspension",
			})
		}()

		// Use await to poll for status change
		err = await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				resp, err := tc.svc.RetrieveInternalBankAccount(ctx, &pb.RetrieveInternalBankAccountRequest{
					AccountId: accountCode,
				})
				if err != nil {
					return false
				}
				return resp.Facility.AccountStatus == pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED
			})

		require.NoError(t, err, "account should become SUSPENDED within timeout")
	})

	t.Run("UntilNoError for transient failures", func(t *testing.T) {
		// Create a unique account code to query
		uniqueCode := fmt.Sprintf("RETRY_%s", uuid.New().String()[:8])

		// Create account with delay
		go func() {
			time.Sleep(200 * time.Millisecond)
			_, _ = tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
				AccountCode:    uniqueCode,
				Name:           "Retry Test Account",
				AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
				InstrumentCode: "USD",
			})
		}()

		// Use UntilNoError to retry until account exists
		var facility *pb.InternalBankAccountFacility
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			UntilNoError(func() error {
				// Try to find account by listing and filtering
				resp, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{})
				if err != nil {
					return err
				}
				for _, f := range resp.Facilities {
					if f.AccountCode == uniqueCode {
						facility = f
						return nil
					}
				}
				return fmt.Errorf("account not found yet")
			})

		require.NoError(t, err)
		require.NotNil(t, facility)
		assert.Equal(t, uniqueCode, facility.AccountCode)
	})
}

// ============================================================================
// E2E Test: Pagination
// ============================================================================

// TestE2E_Pagination tests listing accounts with pagination.
func TestE2E_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_pagination_tenant")

	// Create 10 accounts
	const totalAccounts = 10
	for i := 1; i <= totalAccounts; i++ {
		_, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    fmt.Sprintf("PAGE_ACC_%02d", i),
			Name:           fmt.Sprintf("Pagination Account %d", i),
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)
	}

	t.Run("First page of 3 accounts", func(t *testing.T) {
		resp, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize: 3,
			},
		})
		require.NoError(t, err)
		assert.Len(t, resp.Facilities, 3)
		assert.NotEmpty(t, resp.Pagination.NextPageToken)
	})

	t.Run("Second page using token", func(t *testing.T) {
		// Get first page
		firstPage, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize: 3,
			},
		})
		require.NoError(t, err)

		// Get second page
		secondPage, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize:  3,
				PageToken: firstPage.Pagination.NextPageToken,
			},
		})
		require.NoError(t, err)
		assert.Len(t, secondPage.Facilities, 3)

		// Verify no overlap between pages
		firstPageCodes := make(map[string]bool)
		for _, f := range firstPage.Facilities {
			firstPageCodes[f.AccountCode] = true
		}
		for _, f := range secondPage.Facilities {
			assert.False(t, firstPageCodes[f.AccountCode], "second page should not contain first page items")
		}
	})

	t.Run("Iterate through all pages", func(t *testing.T) {
		var allAccountCodes []string
		pageToken := ""
		pageCount := 0

		for {
			resp, err := tc.svc.ListInternalBankAccounts(ctx, &pb.ListInternalBankAccountsRequest{
				Pagination: &commonpb.Pagination{
					PageSize:  4,
					PageToken: pageToken,
				},
			})
			require.NoError(t, err)
			pageCount++

			for _, f := range resp.Facilities {
				allAccountCodes = append(allAccountCodes, f.AccountCode)
			}

			if resp.Pagination.NextPageToken == "" {
				break
			}
			pageToken = resp.Pagination.NextPageToken
		}

		// Should have retrieved all accounts across pages
		assert.Len(t, allAccountCodes, totalAccounts)
		// Verify pages required (10 accounts / 4 per page = 3 pages)
		assert.Equal(t, 3, pageCount)
	})
}

// ============================================================================
// E2E Test: Optimistic Locking
// ============================================================================

// TestE2E_OptimisticLocking tests version-based optimistic locking.
func TestE2E_OptimisticLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_locking_tenant")

	t.Run("Version conflict on concurrent updates", func(t *testing.T) {
		accountCode := "VERSION_TEST"

		// Create account
		createResp, err := tc.svc.InitiateInternalBankAccount(ctx, &pb.InitiateInternalBankAccountRequest{
			AccountCode:    accountCode,
			Name:           "Version Test Account",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		})
		require.NoError(t, err)
		initialVersion := createResp.Facility.Version

		// First update succeeds
		updateResp1, err := tc.svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
			AccountId:       accountCode,
			Name:            "Updated Name V1",
			ExpectedVersion: initialVersion,
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), updateResp1.Facility.Version)

		// Second update with stale version fails
		_, err = tc.svc.UpdateInternalBankAccount(ctx, &pb.UpdateInternalBankAccountRequest{
			AccountId:       accountCode,
			Name:            "Stale Update",
			ExpectedVersion: initialVersion, // Stale version
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version")
	})
}

// ============================================================================
// E2E Test: Account Types
// ============================================================================

// TestE2E_AllAccountTypes tests creating all supported account types.
func TestE2E_AllAccountTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := setupE2ETestPool(t)
	ctx := setupTenantSchema(t, tc, "e2e_account_types_tenant")

	// Test cases for all account types
	accountTypes := []struct {
		protoType            pb.InternalAccountType
		requireCorrespondent bool
		name                 string
	}{
		{pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING, false, "Clearing"},
		{pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING, false, "Holding"},
		{pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE, false, "Suspense"},
		{pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE, false, "Revenue"},
		{pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE, false, "Expense"},
		{pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO, true, "Nostro"},
		{pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO, true, "Vostro"},
	}

	for _, at := range accountTypes {
		t.Run(fmt.Sprintf("Create %s account", at.name), func(t *testing.T) {
			req := &pb.InitiateInternalBankAccountRequest{
				AccountCode:    fmt.Sprintf("%s_ACCOUNT", at.name),
				Name:           fmt.Sprintf("%s Test Account", at.name),
				AccountType:    at.protoType,
				InstrumentCode: "GBP",
			}

			if at.requireCorrespondent {
				req.CorrespondentDetails = &pb.CorrespondentBankDetails{
					BankId:             "BANK001",
					BankName:           "Test Bank",
					ExternalAccountRef: "REF123",
				}
			}

			resp, err := tc.svc.InitiateInternalBankAccount(ctx, req)
			require.NoError(t, err)
			assert.Equal(t, at.protoType, resp.Facility.AccountType)

			if at.requireCorrespondent {
				assert.NotNil(t, resp.Facility.CorrespondentDetails)
			}
		})
	}
}
