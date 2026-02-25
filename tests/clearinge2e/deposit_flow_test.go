//go:build integration
// +build integration

package clearinge2e

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/await"
)

// =============================================================================
// Test: Deposit Flow E2E
// =============================================================================

// TestDepositClearingFlow tests the complete deposit flow across all services.
//
// Flow:
// 1. Customer initiates a deposit (Current Account)
// 2. System resolves the deposit clearing account (Internal Account)
// 3. Ledger posting created: Debit Clearing Account, Credit Customer Account (Financial Accounting)
// 4. Position logs recorded for both accounts (Position Keeping)
// 5. Verify: Ledger is balanced
func TestDepositClearingFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupE2EInfra(t)

	t.Run("GBP_deposit_uses_correct_clearing_account", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_deposit_gbp")
		schemaName := tenantID.SchemaName()

		// Step 1: Create deposit clearing account
		depositClearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEPOSIT", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Step 2: Create withdrawal clearing account (for comparison)
		_ = createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-WITHDRAW", "GBP", "CLEARING_PURPOSE_WITHDRAWAL")

		// Step 3: Create customer account
		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Step 4: Simulate deposit operation
		depositAmount := "10.00"
		depositReference := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])

		// The deposit operation should:
		// a) Resolve the deposit clearing account by purpose
		resolvedAccountID, resolvedAccountCode, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found, "deposit clearing account should be found")
		assert.Equal(t, depositClearingAccountID, resolvedAccountID)
		assert.Equal(t, "CLR-GBP-DEPOSIT", resolvedAccountCode)

		// b) Create ledger posting: Debit Clearing, Credit Customer
		postingID := createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositReference,
			depositClearingAccountID, // debit
			customerAccountID,        // credit
			"GBP",
			depositAmount,
			"Customer deposit via bank transfer")

		require.NotEmpty(t, postingID, "ledger posting should be created")

		// c) Record position logs for both accounts
		// Clearing account: negative (debit)
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingAccountID, "GBP", "AVAILABLE",
			"-"+depositAmount, depositReference, "DEPOSIT")

		// Customer account: positive (credit)
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE",
			depositAmount, depositReference, "DEPOSIT")

		// Step 5: Verify the ledger is balanced
		// Sum of all position changes should be zero
		clearingBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, depositClearingAccountID, "GBP")
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")

		assert.Equal(t, "-10.00000000", clearingBalance, "clearing account should be debited")
		assert.Equal(t, "10.00000000", customerBalance, "customer account should be credited")

		// Step 6: Verify ledger posting details
		_, debitAcct, creditAcct, amount, found := getLedgerPostingByReference(t, ctx,
			infra.financialAccountingDB, schemaName, depositReference)
		require.True(t, found, "ledger posting should exist")
		assert.Equal(t, depositClearingAccountID, debitAcct)
		assert.Equal(t, customerAccountID, creditAcct)
		assert.Equal(t, "10.00000000", amount)
	})

	t.Run("deposit_resolves_deposit_account_not_withdrawal", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_deposit_correct_purpose")
		schemaName := tenantID.SchemaName()

		// Create both deposit and withdrawal clearing accounts
		depositAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		withdrawalAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-WDR", "GBP", "CLEARING_PURPOSE_WITHDRAWAL")

		// When resolving for DEPOSIT purpose
		resolvedID, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found)
		assert.Equal(t, depositAccountID, resolvedID, "should resolve to deposit account")
		assert.NotEqual(t, withdrawalAccountID, resolvedID, "should NOT resolve to withdrawal account")

		// When resolving for WITHDRAWAL purpose
		resolvedID, _, found = getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_WITHDRAWAL")
		require.True(t, found)
		assert.Equal(t, withdrawalAccountID, resolvedID, "should resolve to withdrawal account")
		assert.NotEqual(t, depositAccountID, resolvedID, "should NOT resolve to deposit account")
	})

	t.Run("multiple_deposits_accumulate_correctly", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_deposit_multiple")
		schemaName := tenantID.SchemaName()

		// Setup accounts
		clearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Process multiple deposits
		deposits := []string{"100.00", "50.00", "25.50", "75.25"}
		for i, amount := range deposits {
			ref := fmt.Sprintf("DEP-%d-%s", i, uuid.New().String()[:8])

			// Ledger posting
			createLedgerPosting(t, ctx,
				infra.financialAccountingDB, schemaName,
				ref, clearingAccountID, customerAccountID, "GBP", amount,
				fmt.Sprintf("Deposit %d", i+1))

			// Position logs
			recordPosition(t, ctx,
				infra.positionKeepingDB, schemaName,
				clearingAccountID, "GBP", "AVAILABLE", "-"+amount, ref, "DEPOSIT")
			recordPosition(t, ctx,
				infra.positionKeepingDB, schemaName,
				customerAccountID, "GBP", "AVAILABLE", amount, ref, "DEPOSIT")
		}

		// Verify final balances
		// Expected: 100 + 50 + 25.50 + 75.25 = 250.75
		clearingBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, clearingAccountID, "GBP")
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")

		assert.Equal(t, "-250.75000000", clearingBalance)
		assert.Equal(t, "250.75000000", customerBalance)

		// Verify position log count
		clearingLogCount := countPositionLogs(t, ctx,
			infra.positionKeepingDB, schemaName, clearingAccountID)
		customerLogCount := countPositionLogs(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID)

		assert.Equal(t, 4, clearingLogCount)
		assert.Equal(t, 4, customerLogCount)
	})

	t.Run("deposit_with_async_position_update", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_deposit_async")
		schemaName := tenantID.SchemaName()

		// Setup accounts (clearing account created but not directly used in this test)
		_ = createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		depositAmount := "500.00"

		// Simulate async position update using await pattern
		// In a real system, position updates might happen asynchronously via Kafka
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Simulate async delay
			recordPosition(t, ctx,
				infra.positionKeepingDB, schemaName,
				customerAccountID, "GBP", "AVAILABLE", depositAmount, depositRef, "DEPOSIT")
		}()

		// Ensure goroutine completes before test ends (for safe t usage)
		defer wg.Wait()

		// Use await to wait for position to be recorded
		var finalBalance string
		err := await.New().
			AtMost(await.DefaultTimeout).
			PollInterval(await.DefaultPollInterval).
			Until(func() bool {
				balance := getPositionBalance(t, ctx,
					infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
				if balance != "0" {
					finalBalance = balance
					return true
				}
				return false
			})
		require.NoError(t, err, "position should be recorded within timeout")
		assert.Equal(t, "500.00000000", finalBalance)
	})
}

// TestDepositClearingAccountResolution tests various clearing account resolution scenarios.
func TestDepositClearingAccountResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupE2EInfra(t)

	t.Run("missing_clearing_account_detected", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_deposit_missing")
		schemaName := tenantID.SchemaName()

		// Don't create any clearing accounts
		// Try to resolve
		_, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		assert.False(t, found, "should not find clearing account when none exists")
	})

	t.Run("inactive_clearing_account_not_resolved", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_deposit_inactive")
		schemaName := tenantID.SchemaName()

		// Create clearing account
		createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Suspend it
		_, err := infra.internalAccountDB.pool.Exec(ctx, fmt.Sprintf(`
			UPDATE %s.internal_accounts SET status = 'SUSPENDED'
			WHERE account_code = 'CLR-GBP-DEP'
		`, pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Try to resolve - should not find it
		_, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		assert.False(t, found, "should not resolve suspended clearing account")
	})

	t.Run("currency_specific_clearing_accounts", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_deposit_multi_currency")
		schemaName := tenantID.SchemaName()

		// Create clearing accounts for multiple currencies
		gbpAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		usdAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-USD-DEP", "USD", "CLEARING_PURPOSE_DEPOSIT")

		eurAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-EUR-DEP", "EUR", "CLEARING_PURPOSE_DEPOSIT")

		// Verify each currency resolves to correct account
		id, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found)
		assert.Equal(t, gbpAccountID, id)

		id, _, found = getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "USD", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found)
		assert.Equal(t, usdAccountID, id)

		id, _, found = getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "EUR", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found)
		assert.Equal(t, eurAccountID, id)
	})
}
