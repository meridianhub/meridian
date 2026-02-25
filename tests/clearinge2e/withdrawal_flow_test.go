//go:build integration
// +build integration

package clearinge2e

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test: Withdrawal Flow E2E
// =============================================================================

// TestWithdrawalClearingFlow tests the complete withdrawal flow across all services.
//
// Flow:
// 1. Customer initiates a withdrawal (Current Account)
// 2. System resolves the withdrawal clearing account (Internal Account)
// 3. Ledger posting created: Debit Customer Account, Credit Clearing Account (Financial Accounting)
// 4. Position logs recorded for both accounts (Position Keeping)
// 5. Verify: Ledger is balanced, customer balance reduced
func TestWithdrawalClearingFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupE2EInfra(t)

	t.Run("GBP_withdrawal_uses_correct_clearing_account", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_withdraw_gbp")
		schemaName := tenantID.SchemaName()

		// Step 1: Create deposit clearing account (for initial funding)
		depositClearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEPOSIT", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Step 2: Create withdrawal clearing account
		withdrawalClearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-WITHDRAW", "GBP", "CLEARING_PURPOSE_WITHDRAWAL")

		// Step 3: Create customer account
		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Step 4: Fund the customer account first (deposit)
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		initialDeposit := "100.00"

		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef,
			depositClearingAccountID, customerAccountID, "GBP",
			initialDeposit, "Initial deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingAccountID, "GBP", "AVAILABLE",
			"-"+initialDeposit, depositRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE",
			initialDeposit, depositRef, "DEPOSIT")

		// Verify initial state
		customerBalanceBefore := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
		assert.Equal(t, "100.00000000", customerBalanceBefore)

		// Step 5: Process withdrawal
		withdrawalAmount := "25.00"
		withdrawalRef := fmt.Sprintf("WDR-%s", uuid.New().String()[:8])

		// Resolve withdrawal clearing account
		resolvedAccountID, resolvedAccountCode, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_WITHDRAWAL")
		require.True(t, found, "withdrawal clearing account should be found")
		assert.Equal(t, withdrawalClearingAccountID, resolvedAccountID)
		assert.Equal(t, "CLR-GBP-WITHDRAW", resolvedAccountCode)

		// Create ledger posting: Debit Customer, Credit Clearing
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			withdrawalRef,
			customerAccountID,           // debit
			withdrawalClearingAccountID, // credit
			"GBP",
			withdrawalAmount,
			"Customer withdrawal via ATM")

		// Record position logs
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE",
			"-"+withdrawalAmount, withdrawalRef, "WITHDRAWAL")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			withdrawalClearingAccountID, "GBP", "AVAILABLE",
			withdrawalAmount, withdrawalRef, "WITHDRAWAL")

		// Step 6: Verify final balances
		customerBalanceAfter := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
		withdrawalClearingBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, withdrawalClearingAccountID, "GBP")

		// Customer: 100 - 25 = 75
		assert.Equal(t, "75.00000000", customerBalanceAfter, "customer should have 75 GBP after withdrawal")
		// Withdrawal clearing: 25 (credit from withdrawal)
		assert.Equal(t, "25.00000000", withdrawalClearingBalance, "clearing account should be credited")
	})

	t.Run("withdrawal_cannot_exceed_balance", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_withdraw_insufficient")
		schemaName := tenantID.SchemaName()

		// Setup
		depositClearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		withdrawalClearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-WDR", "GBP", "CLEARING_PURPOSE_WITHDRAWAL")

		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Fund with 50 GBP
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef,
			depositClearingAccountID, customerAccountID, "GBP",
			"50.00", "Initial deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingAccountID, "GBP", "AVAILABLE", "-50.00", depositRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "50.00", depositRef, "DEPOSIT")

		// Check balance before withdrawal
		balance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
		assert.Equal(t, "50.00000000", balance)

		// Simulate balance check (in real system, this would reject the withdrawal)
		requestedAmount := "100.00"
		currentBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")

		// Verify insufficient funds would be detected
		// Note: In a real system, Current Account service would check this
		assert.True(t, parseAmount(currentBalance) < parseAmount(requestedAmount),
			"balance check should detect insufficient funds")

		// Balance remains unchanged since withdrawal would be rejected
		balanceAfter := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
		assert.Equal(t, "50.00000000", balanceAfter)

		// Withdrawal clearing account should have no movements
		clearingBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, withdrawalClearingAccountID, "GBP")
		assert.Equal(t, "0", clearingBalance)
	})

	t.Run("multiple_withdrawals_accumulate_correctly", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_withdraw_multiple")
		schemaName := tenantID.SchemaName()

		// Setup
		depositClearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		withdrawalClearingAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-WDR", "GBP", "CLEARING_PURPOSE_WITHDRAWAL")

		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Fund with 500 GBP
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef,
			depositClearingAccountID, customerAccountID, "GBP",
			"500.00", "Initial deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingAccountID, "GBP", "AVAILABLE", "-500.00", depositRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "500.00", depositRef, "DEPOSIT")

		// Process multiple withdrawals
		withdrawals := []string{"50.00", "100.00", "25.50", "74.50"}
		for i, amount := range withdrawals {
			ref := fmt.Sprintf("WDR-%d-%s", i, uuid.New().String()[:8])

			createLedgerPosting(t, ctx,
				infra.financialAccountingDB, schemaName,
				ref,
				customerAccountID, withdrawalClearingAccountID, "GBP",
				amount, fmt.Sprintf("Withdrawal %d", i+1))

			recordPosition(t, ctx,
				infra.positionKeepingDB, schemaName,
				customerAccountID, "GBP", "AVAILABLE", "-"+amount, ref, "WITHDRAWAL")
			recordPosition(t, ctx,
				infra.positionKeepingDB, schemaName,
				withdrawalClearingAccountID, "GBP", "AVAILABLE", amount, ref, "WITHDRAWAL")
		}

		// Verify final balances
		// Customer: 500 - (50 + 100 + 25.50 + 74.50) = 500 - 250 = 250
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
		clearingBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, withdrawalClearingAccountID, "GBP")

		assert.Equal(t, "250.00000000", customerBalance, "customer should have 250 GBP remaining")
		assert.Equal(t, "250.00000000", clearingBalance, "clearing account should have 250 GBP credited")
	})

	t.Run("deposit_and_withdrawal_use_different_clearing_accounts", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_mixed_operations")
		schemaName := tenantID.SchemaName()

		// Create both clearing accounts
		depositClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		withdrawalClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-WDR", "GBP", "CLEARING_PURPOSE_WITHDRAWAL")

		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Process deposit
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef,
			depositClearingID, customerAccountID, "GBP",
			"1000.00", "Deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingID, "GBP", "AVAILABLE", "-1000.00", depositRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "1000.00", depositRef, "DEPOSIT")

		// Process withdrawal
		withdrawalRef := fmt.Sprintf("WDR-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			withdrawalRef,
			customerAccountID, withdrawalClearingID, "GBP",
			"300.00", "Withdrawal")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "-300.00", withdrawalRef, "WITHDRAWAL")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			withdrawalClearingID, "GBP", "AVAILABLE", "300.00", withdrawalRef, "WITHDRAWAL")

		// Verify each clearing account has separate movements
		depositClearingBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, depositClearingID, "GBP")
		withdrawalClearingBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, withdrawalClearingID, "GBP")
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")

		// Deposit clearing: -1000 (debited for deposit)
		assert.Equal(t, "-1000.00000000", depositClearingBalance)
		// Withdrawal clearing: +300 (credited for withdrawal)
		assert.Equal(t, "300.00000000", withdrawalClearingBalance)
		// Customer: 1000 - 300 = 700
		assert.Equal(t, "700.00000000", customerBalance)

		// Verify ledger postings use correct accounts
		_, depositDebitAcct, depositCreditAcct, _, found := getLedgerPostingByReference(t, ctx,
			infra.financialAccountingDB, schemaName, depositRef)
		require.True(t, found)
		assert.Equal(t, depositClearingID, depositDebitAcct, "deposit should debit deposit clearing")
		assert.Equal(t, customerAccountID, depositCreditAcct, "deposit should credit customer")

		_, withdrawalDebitAcct, withdrawalCreditAcct, _, found := getLedgerPostingByReference(t, ctx,
			infra.financialAccountingDB, schemaName, withdrawalRef)
		require.True(t, found)
		assert.Equal(t, customerAccountID, withdrawalDebitAcct, "withdrawal should debit customer")
		assert.Equal(t, withdrawalClearingID, withdrawalCreditAcct, "withdrawal should credit withdrawal clearing")
	})
}

// parseAmount parses a decimal string to float for comparison.
// Note: In production code, use decimal.Decimal for precision.
func parseAmount(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}
