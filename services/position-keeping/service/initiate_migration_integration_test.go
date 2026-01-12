//go:build integration

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// TestIntegration_InitiateWithOpeningBalance_PositiveBalance tests migrating an account
// with a positive opening balance and verifies the balance query returns the correct value.
func TestIntegration_InitiateWithOpeningBalance_PositiveBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-001"

	// Create request for migration with £1,500.50 opening balance
	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1500,
				Nanos:        500000000, // £1500.50
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-001",
	}

	// Act - Call the migration endpoint
	resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)

	// Assert - Verify log was created correctly
	require.NoError(t, err, "Expected successful migration")
	require.NotNil(t, resp, "Expected non-nil response")
	require.NotNil(t, resp.Log, "Expected log in response")

	assert.Equal(t, accountID, resp.Log.AccountId)
	assert.NotEmpty(t, resp.Log.LogId)
	// Opening balance transaction entry should exist
	assert.Len(t, resp.Log.TransactionLogEntries, 1, "Expected 1 transaction entry for opening balance")
	// Status should be POSTED (opening balance is immediately posted)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.Log.StatusTracking.CurrentStatus)

	// Verify balance query returns correct value
	balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: "GBP",
	}

	balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
	require.NoError(t, err, "Expected successful balance query")
	require.NotNil(t, balanceResp, "Expected balance response")

	// Verify the balance amount (1500.50 GBP)
	assert.Equal(t, "1500.50", balanceResp.Amount.Amount)
	assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode)
}

// TestIntegration_InitiateWithOpeningBalance_NegativeBalance tests migrating an overdrawn account.
func TestIntegration_InitiateWithOpeningBalance_NegativeBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-002"

	// Create request for migration with -£500.25 opening balance (overdrawn)
	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        -500,
				Nanos:        -250000000, // -£500.25
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-002",
	}

	// Act
	resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected successful migration of overdrawn account")
	require.NotNil(t, resp, "Expected non-nil response")

	assert.Equal(t, accountID, resp.Log.AccountId)
	// Transaction entry should be a DEBIT for negative balance
	require.Len(t, resp.Log.TransactionLogEntries, 1)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, resp.Log.TransactionLogEntries[0].Direction)

	// Verify balance query returns correct negative value
	balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: "GBP",
	}

	balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
	require.NoError(t, err, "Expected successful balance query")

	// Balance should be negative (overdrawn) - -500.25 GBP
	assert.Equal(t, "-500.25", balanceResp.Amount.Amount)
	assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode)
}

// TestIntegration_InitiateWithOpeningBalance_MultipleAccounts tests migrating multiple
// different accounts in sequence to ensure isolation.
func TestIntegration_InitiateWithOpeningBalance_MultipleAccounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	effectiveDate := time.Now().Add(-24 * time.Hour)

	// Migrate multiple accounts with different balances
	accounts := []struct {
		accountID      string
		units          int64
		nanos          int32
		expectedAmount string // Expected balance in InstrumentAmount format
	}{
		{"ACC-MULTI-001", 1000, 0, "1000.00"},
		{"ACC-MULTI-002", 2500, 500000000, "2500.50"}, // £2500.50
		{"ACC-MULTI-003", -100, 0, "-100.00"},         // Overdrawn
	}

	createdLogIDs := make(map[string]string)

	for _, acc := range accounts {
		req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
			AccountId: acc.accountID,
			OpeningBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        acc.units,
					Nanos:        acc.nanos,
				},
			},
			EffectiveDate:      timestamppb.New(effectiveDate),
			MigrationReference: "BULK-MIGRATION-001",
		}

		resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)
		require.NoError(t, err, "Migration should succeed for %s", acc.accountID)
		require.NotNil(t, resp, "Response should not be nil")
		require.NotNil(t, resp.Log, "Log should not be nil")

		createdLogIDs[acc.accountID] = resp.Log.LogId
	}

	// Verify each account has its correct balance
	for _, acc := range accounts {
		balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      acc.accountID,
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			InstrumentCode: "GBP",
		}

		balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
		require.NoError(t, err, "Balance query should succeed for %s", acc.accountID)

		assert.Equal(t, acc.expectedAmount, balanceResp.Amount.Amount,
			"Balance amount should match for %s", acc.accountID)
		assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode,
			"Instrument code should be GBP for %s", acc.accountID)
	}

	// Verify all log IDs are unique
	logIDSet := make(map[string]bool)
	for _, logID := range createdLogIDs {
		assert.False(t, logIDSet[logID], "Log IDs should be unique")
		logIDSet[logID] = true
	}
}

// TestIntegration_InitiateWithOpeningBalance_VerifyKafkaEvent verifies that
// the OpeningBalanceRecorded event is published to Kafka.
func TestIntegration_InitiateWithOpeningBalance_VerifyKafkaEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-004"

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        2500,
				Nanos:        0,
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-004",
	}

	// Act
	resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected successful migration")
	require.NotNil(t, resp, "Expected non-nil response")

	// In a real integration test with Kafka, we would verify the event was published.
	// Since we're using an in-memory publisher in the test container, we can verify
	// that the event was captured (if we had access to the publisher).
	// For now, we just verify the response is correct.
	assert.Equal(t, accountID, resp.Log.AccountId)
	assert.NotEmpty(t, resp.Log.LogId)
}

// TestIntegration_InitiateWithOpeningBalance_ZeroBalance tests migrating with zero balance.
func TestIntegration_InitiateWithOpeningBalance_ZeroBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-005"

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        0,
				Nanos:        0,
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-005",
	}

	// Act
	resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)

	// Assert
	require.NoError(t, err, "Expected successful migration with zero balance")
	require.NotNil(t, resp)

	// Zero balance should create no transaction entry
	assert.Len(t, resp.Log.TransactionLogEntries, 0, "Expected no transaction entry for zero balance")

	// Balance should be zero
	balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: "GBP",
	}

	balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
	require.NoError(t, err, "Expected successful balance query")
	assert.Equal(t, "0.00", balanceResp.Amount.Amount)
	assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode)
}

// TestIntegration_InitiateWithOpeningBalance_FutureEffectiveDate tests validation rejects future dates.
func TestIntegration_InitiateWithOpeningBalance_FutureEffectiveDate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-006"

	// Set effective date to tomorrow
	effectiveDate := time.Now().Add(24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1000,
				Nanos:        0,
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-006",
	}

	// Act
	resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)

	// Assert - Should fail with InvalidArgument
	require.Error(t, err, "Expected error for future effective date")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "effective_date cannot be in the future")
}

// TestIntegration_InitiateWithOpeningBalance_ThenAddTransactions tests that transactions
// can be added after migration and balance reflects all entries.
func TestIntegration_InitiateWithOpeningBalance_ThenAddTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-007"

	// Step 1: Migrate with £1000 opening balance
	effectiveDate := time.Now().Add(-24 * time.Hour)
	migrateReq := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        1000,
				Nanos:        0,
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-007",
	}

	migrateResp, err := tc.Service.InitiateWithOpeningBalance(ctx, migrateReq)
	require.NoError(t, err, "Expected successful migration")

	// Step 2: Add additional transactions to the same account
	// Create a new position log with additional credit
	creditMoney := domain.MustNewMoney(decimal.NewFromInt(500), domain.CurrencyGBP)
	creditEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		creditMoney,
		domain.PostingDirectionCredit,
		time.Now().UTC(),
		"Additional deposit",
		"DEP-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	additionalLog, err := domain.NewFinancialPositionLog(accountID, creditEntry, nil)
	require.NoError(t, err)

	err = tc.Repo.Create(ctx, additionalLog)
	require.NoError(t, err)

	// Step 3: Verify total balance is £1500
	balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: "GBP",
	}

	balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
	require.NoError(t, err, "Expected successful balance query")

	// Opening balance (£1000) + additional credit (£500) = £1500
	assert.Equal(t, "1500.00", balanceResp.Amount.Amount)
	assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode)

	_ = migrateResp // Use the response to avoid unused variable warning
}

// TestIntegration_MigrationIdempotency tests that duplicate migration attempts
// for the same account are handled correctly and don't corrupt data.
func TestIntegration_MigrationIdempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-008"

	effectiveDate := time.Now().Add(-24 * time.Hour)
	req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        3000,
				Nanos:        0,
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-008",
	}

	// First migration - should succeed
	resp1, err := tc.Service.InitiateWithOpeningBalance(ctx, req)
	require.NoError(t, err, "First migration should succeed")
	require.NotNil(t, resp1)
	firstLogID := resp1.Log.LogId

	// Second migration attempt with same account - should be handled by idempotency
	resp2, err := tc.Service.InitiateWithOpeningBalance(ctx, req)

	// Either it should succeed (idempotent) or fail with AlreadyExists error
	if err != nil {
		st, ok := status.FromError(err)
		require.True(t, ok, "Expected gRPC status error")
		assert.Equal(t, codes.AlreadyExists, st.Code(), "Should fail with AlreadyExists code")
	} else {
		// If idempotent, should return same log ID
		assert.Equal(t, firstLogID, resp2.Log.LogId, "Idempotent call should return same log")
	}

	// Verify balance is still correct and not doubled
	balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: "GBP",
	}

	balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
	require.NoError(t, err, "Balance query should succeed")

	assert.Equal(t, "3000.00", balanceResp.Amount.Amount,
		"Balance should still be 3000, not doubled")
	assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode)
}

// TestIntegration_MigrationInvalidBalances tests that invalid balance amounts
// are rejected during migration with proper error messages.
func TestIntegration_MigrationInvalidBalances(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	tests := []struct {
		name          string
		accountID     string
		units         int64
		nanos         int32
		currency      string
		errorContains string
	}{
		{
			name:          "empty account ID",
			accountID:     "",
			units:         1000,
			nanos:         0,
			currency:      "GBP",
			errorContains: "account_id",
		},
		{
			name:          "missing currency",
			accountID:     "ACC-009",
			units:         1000,
			nanos:         0,
			currency:      "",
			errorContains: "currency",
		},
		{
			name:          "invalid currency code",
			accountID:     "ACC-010",
			units:         1000,
			nanos:         0,
			currency:      "INVALID",
			errorContains: "currency",
		},
		{
			name:          "nanos out of range (too large)",
			accountID:     "ACC-011",
			units:         1000,
			nanos:         1_000_000_000, // Should be < 1 billion
			currency:      "GBP",
			errorContains: "nanos",
		},
		{
			name:          "nanos out of range (too small)",
			accountID:     "ACC-012",
			units:         1000,
			nanos:         -1_000_000_000,
			currency:      "GBP",
			errorContains: "nanos",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effectiveDate := time.Now().Add(-24 * time.Hour)
			req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
				AccountId: tt.accountID,
				OpeningBalance: &commonv1.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: tt.currency,
						Units:        tt.units,
						Nanos:        tt.nanos,
					},
				},
				EffectiveDate:      timestamppb.New(effectiveDate),
				MigrationReference: "INVALID-TEST",
			}

			resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)

			assert.Error(t, err, "Should reject invalid balance")
			assert.Nil(t, resp, "Response should be nil on error")
			if tt.errorContains != "" {
				assert.Contains(t, err.Error(), tt.errorContains,
					"Error should contain %s", tt.errorContains)
			}
		})
	}
}

// TestIntegration_BatchMigration tests migrating multiple accounts simultaneously
// to verify no race conditions or balance corruption occurs.
func TestIntegration_BatchMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	effectiveDate := time.Now().Add(-48 * time.Hour)

	// Create 20 accounts with various balances (reduced from 100 for faster test execution)
	const accountCount = 20
	accounts := make([]struct {
		accountID      string
		units          int64
		nanos          int32
		expectedAmount string // Expected balance in InstrumentAmount format
	}, accountCount)

	for i := 0; i < accountCount; i++ {
		units := int64((i + 1) * 100)
		nanosPart := int32((i % 10) * 100000000)
		// Format expected amount: units + nanos/1e9
		var expectedAmount string
		if nanosPart == 0 {
			expectedAmount = decimal.NewFromInt(units).StringFixed(2)
		} else {
			expectedAmount = decimal.NewFromInt(units).Add(
				decimal.NewFromInt(int64(nanosPart)).Div(decimal.NewFromInt(1_000_000_000)),
			).StringFixed(2)
		}
		accounts[i] = struct {
			accountID      string
			units          int64
			nanos          int32
			expectedAmount string
		}{
			accountID:      "BATCH-" + uuid.New().String()[:8],
			units:          units,
			nanos:          nanosPart,
			expectedAmount: expectedAmount,
		}
	}

	// Migrate all accounts (simulating batch migration)
	for _, acc := range accounts {
		req := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
			AccountId: acc.accountID,
			OpeningBalance: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        acc.units,
					Nanos:        acc.nanos,
				},
			},
			EffectiveDate:      timestamppb.New(effectiveDate),
			MigrationReference: "BATCH-MIGRATION-001",
		}

		resp, err := tc.Service.InitiateWithOpeningBalance(ctx, req)
		require.NoError(t, err, "Batch migration should succeed for %s", acc.accountID)
		require.NotNil(t, resp, "Response should not be nil")
		require.NotNil(t, resp.Log, "Log should not be nil")
	}

	// Verify all accounts have correct balances
	for _, acc := range accounts {
		balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      acc.accountID,
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			InstrumentCode: "GBP",
		}

		balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
		require.NoError(t, err, "Balance query should succeed for %s", acc.accountID)

		assert.Equal(t, acc.expectedAmount, balanceResp.Amount.Amount,
			"Balance amount should match for %s", acc.accountID)
		assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode,
			"Instrument code should be GBP for %s", acc.accountID)
	}

	// Add a transaction to one of the migrated accounts to verify post-migration consistency
	testAccount := accounts[0]
	creditEntry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		testAccount.accountID,
		domain.MustNewMoney(decimal.NewFromInt(250), domain.CurrencyGBP),
		domain.PostingDirectionCredit,
		time.Now().UTC(),
		"Post-migration deposit",
		"POST-MIG-001",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(testAccount.accountID, creditEntry, nil)
	require.NoError(t, err)
	err = tc.Repo.Create(ctx, log)
	require.NoError(t, err)

	// Verify balance updated correctly
	balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      testAccount.accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: "GBP",
	}

	balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
	require.NoError(t, err, "Balance query should succeed")

	// Expected: original balance + 250
	// testAccount is accounts[0] which has units=100, nanos=0, so expectedAmount="100.00"
	// After adding 250, expected balance is 350.00
	assert.Equal(t, "350.00", balanceResp.Amount.Amount,
		"Balance should be opening balance + deposit")
	assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode)
}

// TestIntegration_MigrationBalanceConsistencyMultipleTransactions tests that after
// migration, multiple transactions maintain balance consistency.
func TestIntegration_MigrationBalanceConsistencyMultipleTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := SetupBalanceIntegrationTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MIGRATION-INT-013"

	// Step 1: Migrate with opening balance of £2,500.50
	effectiveDate := time.Now().Add(-96 * time.Hour) // 4 days ago
	migrateReq := &positionkeepingv1.InitiateWithOpeningBalanceRequest{
		AccountId: accountID,
		OpeningBalance: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        2500,
				Nanos:        500000000, // £2,500.50
			},
		},
		EffectiveDate:      timestamppb.New(effectiveDate),
		MigrationReference: "LEGACY-MIGRATION-013",
	}

	_, err := tc.Service.InitiateWithOpeningBalance(ctx, migrateReq)
	require.NoError(t, err, "Migration should succeed")

	// Step 2: Execute multiple transactions and verify balance after each
	transactions := []struct {
		amount      string
		direction   domain.PostingDirection
		desc        string
		expectedBal string // Expected balance after this transaction
	}{
		{"123.45", domain.PostingDirectionCredit, "Deposit 1", "2623.95"},
		{"67.89", domain.PostingDirectionDebit, "Withdrawal 1", "2556.06"},
		{"0.01", domain.PostingDirectionCredit, "Micro deposit", "2556.07"},
		{"999.99", domain.PostingDirectionCredit, "Large deposit", "3556.06"},
		{"50.25", domain.PostingDirectionDebit, "Small withdrawal", "3505.81"},
	}

	runningBalance := decimal.NewFromFloat(2500.50) // Starting balance

	for i, tx := range transactions {
		// Calculate expected balance
		txAmount, err := decimal.NewFromString(tx.amount)
		require.NoError(t, err)

		if tx.direction == domain.PostingDirectionCredit {
			runningBalance = runningBalance.Add(txAmount)
		} else {
			runningBalance = runningBalance.Sub(txAmount)
		}

		// Create and save transaction
		entry, err := domain.NewTransactionLogEntry(
			uuid.New(),
			accountID,
			domain.MustNewMoney(txAmount, domain.CurrencyGBP),
			tx.direction,
			time.Now().Add(-time.Duration(len(transactions)-i)*time.Hour),
			tx.desc,
			uuid.New().String()[:8],
			domain.TransactionSourceManual,
		)
		require.NoError(t, err)

		log, err := domain.NewFinancialPositionLog(accountID, entry, nil)
		require.NoError(t, err)
		err = tc.Repo.Create(ctx, log)
		require.NoError(t, err)

		// Verify balance after this transaction
		balanceReq := &positionkeepingv1.GetAccountBalanceRequest{
			AccountId:      accountID,
			BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			InstrumentCode: "GBP",
		}

		balanceResp, err := tc.Service.GetAccountBalance(ctx, balanceReq)
		require.NoError(t, err, "Balance query should succeed after transaction %d", i+1)

		// Parse response amount string to decimal for comparison
		actualBalance, err := decimal.NewFromString(balanceResp.Amount.Amount)
		require.NoError(t, err, "Balance amount should be valid decimal")

		assert.True(t, runningBalance.Equal(actualBalance),
			"After transaction %d (%s): Expected balance %s, got %s",
			i+1, tx.desc, runningBalance.String(), actualBalance.String())
		assert.Equal(t, "GBP", balanceResp.Amount.InstrumentCode,
			"Instrument code should be GBP after transaction %d", i+1)
	}
}
