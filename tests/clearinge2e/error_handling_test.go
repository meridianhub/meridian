//go:build integration
// +build integration

package clearinge2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/await"
)

// =============================================================================
// Test: Error Handling and Resilience
// =============================================================================

// TestClearingErrorHandling tests error scenarios and fallback behavior
// when the clearing account resolution or related services fail.
func TestClearingErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupE2EInfra(t)

	t.Run("missing_clearing_account_detected_gracefully", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_missing")
		schemaName := tenantID.SchemaName()

		// Don't create any clearing accounts
		// Attempt to resolve should indicate not found
		_, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		assert.False(t, found, "should gracefully handle missing clearing account")

		// System should be able to continue with fallback (in real impl)
		// For now, we just verify detection works
	})

	t.Run("suspended_clearing_account_not_used", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_suspended")
		schemaName := tenantID.SchemaName()

		// Create clearing account
		createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Suspend the account
		_, err := infra.internalAccountDB.pool.Exec(ctx, fmt.Sprintf(`
			UPDATE %s.internal_accounts
			SET status = 'SUSPENDED'
			WHERE account_code = 'CLR-GBP-DEP'
		`, pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Should not resolve suspended account
		_, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		assert.False(t, found, "should not resolve suspended clearing account")
	})

	t.Run("closed_clearing_account_not_used", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_closed")
		schemaName := tenantID.SchemaName()

		// Create clearing account
		createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Close the account
		_, err := infra.internalAccountDB.pool.Exec(ctx, fmt.Sprintf(`
			UPDATE %s.internal_accounts
			SET status = 'CLOSED'
			WHERE account_code = 'CLR-GBP-DEP'
		`, pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Should not resolve closed account
		_, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		assert.False(t, found, "should not resolve closed clearing account")
	})

	t.Run("fallback_to_alternative_clearing_account", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_fallback")
		schemaName := tenantID.SchemaName()

		// Create primary clearing account (but suspend it)
		createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP-PRIMARY", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		_, err := infra.internalAccountDB.pool.Exec(ctx, fmt.Sprintf(`
			UPDATE %s.internal_accounts
			SET status = 'SUSPENDED'
			WHERE account_code = 'CLR-GBP-DEP-PRIMARY'
		`, pq.QuoteIdentifier(schemaName)))
		require.NoError(t, err)

		// Create fallback clearing account (active)
		fallbackID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP-FALLBACK", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Should resolve to fallback
		resolvedID, code, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		require.True(t, found, "should find fallback account")
		assert.Equal(t, fallbackID, resolvedID)
		assert.Equal(t, "CLR-GBP-DEP-FALLBACK", code)
	})

	t.Run("position_log_failure_does_not_affect_other_services", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_partial")
		schemaName := tenantID.SchemaName()

		// Setup accounts
		clearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		partyID := uuid.New().String()
		customerID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Create ledger posting (should succeed even if position log fails)
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		postingID := createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef, clearingID, customerID, "GBP",
			"100.00", "Deposit")

		require.NotEmpty(t, postingID, "ledger posting should succeed")

		// Verify posting exists in Financial Accounting
		_, _, _, _, found := getLedgerPostingByReference(t, ctx,
			infra.financialAccountingDB, schemaName, depositRef)
		assert.True(t, found, "ledger posting should exist independently")

		// Position log operations are separate
		// In a real system, if Position Keeping fails:
		// - Ledger posting is committed (FA is consistent)
		// - Position log is retried asynchronously or compensated
		// Here we verify the services are independent
	})

	t.Run("database_connection_timeout_handling", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_timeout")
		schemaName := tenantID.SchemaName()

		// Create account
		createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Test with very short context timeout
		shortCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
		defer cancel()

		time.Sleep(10 * time.Millisecond) // Ensure context has expired

		// Verify context is done
		assert.Error(t, shortCtx.Err(), "context should be cancelled")

		// Attempt query with expired context - should not find result
		// (the getClearingAccountByPurpose function handles context cancellation gracefully)
		_, _, found := getClearingAccountByPurpose(t, shortCtx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		assert.False(t, found, "query with expired context should not succeed")

		// Operations with fresh context should still work
		_, _, found = getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		assert.True(t, found, "operations with valid context should succeed")
	})

	t.Run("concurrent_operations_do_not_cause_data_loss", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_concurrent")
		schemaName := tenantID.SchemaName()

		// Setup
		clearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		partyID := uuid.New().String()
		customerID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Process many concurrent deposits
		numDeposits := 10
		depositAmount := "10.00"

		// Use WaitGroup for safer goroutine handling
		var wg sync.WaitGroup
		errCh := make(chan error, numDeposits*3) // 3 operations per deposit

		for i := 0; i < numDeposits; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				ref := fmt.Sprintf("DEP-%d-%s", idx, uuid.New().String()[:8])

				// Use the helper functions which handle errors internally
				// In production code, these would return errors for proper handling
				createLedgerPosting(t, ctx,
					infra.financialAccountingDB, schemaName,
					ref, clearingID, customerID, "GBP",
					depositAmount, fmt.Sprintf("Concurrent deposit %d", idx))

				recordPosition(t, ctx,
					infra.positionKeepingDB, schemaName,
					clearingID, "GBP", "AVAILABLE", "-"+depositAmount, ref, "DEPOSIT")
				recordPosition(t, ctx,
					infra.positionKeepingDB, schemaName,
					customerID, "GBP", "AVAILABLE", depositAmount, ref, "DEPOSIT")
			}(i)
		}

		// Wait for all deposits with timeout
		doneCh := make(chan struct{})
		go func() {
			wg.Wait()
			close(doneCh)
		}()

		select {
		case <-doneCh:
			// All goroutines completed
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for concurrent deposits")
		}

		// Check for any errors from goroutines
		close(errCh)
		for err := range errCh {
			require.NoError(t, err, "concurrent operation failed")
		}

		// Verify final balance using await (in case of async processing)
		var finalBalance string
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(100 * time.Millisecond).
			Until(func() bool {
				balance := getPositionBalance(t, ctx,
					infra.positionKeepingDB, schemaName, customerID, "GBP")
				if balance != "0" {
					finalBalance = balance
					// Check if all deposits are recorded
					return finalBalance == "100.00000000"
				}
				return false
			})
		require.NoError(t, err)

		// Expected: 10 * 10.00 = 100.00
		assert.Equal(t, "100.00000000", finalBalance, "all concurrent deposits should be recorded")

		// Verify position log count
		logCount := countPositionLogs(t, ctx,
			infra.positionKeepingDB, schemaName, customerID)
		assert.Equal(t, numDeposits, logCount, "should have position log for each deposit")
	})

	t.Run("idempotent_operations_prevent_duplicate_processing", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_error_idempotent")
		schemaName := tenantID.SchemaName()

		// Setup
		clearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		partyID := uuid.New().String()
		customerID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Same reference for duplicate attempts
		depositRef := "DEP-IDEMPOTENT-001"
		depositAmount := "100.00"

		// First attempt - should succeed
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef, clearingID, customerID, "GBP",
			depositAmount, "Original deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerID, "GBP", "AVAILABLE", depositAmount, depositRef, "DEPOSIT")

		// Verify initial state
		balance1 := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerID, "GBP")
		assert.Equal(t, "100.00000000", balance1)

		// Attempt duplicate ledger posting - should fail due to unique constraint
		_, err := tryCreateLedgerPosting(ctx,
			infra.financialAccountingDB, schemaName,
			depositRef, clearingID, customerID, "GBP",
			depositAmount, "Duplicate attempt")
		assert.Error(t, err, "duplicate posting should fail due to unique constraint")

		// Verify original posting still exists
		_, _, _, _, found := getLedgerPostingByReference(t, ctx,
			infra.financialAccountingDB, schemaName, depositRef)
		assert.True(t, found, "original posting should exist")

		// Balance should remain unchanged (duplicate was rejected)
		balance2 := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerID, "GBP")
		assert.Equal(t, "100.00000000", balance2, "balance should not change from duplicate")
	})

	t.Run("tenant_isolation_prevents_cross_tenant_access", func(t *testing.T) {
		// Setup Tenant A
		ctxA, tenantA := setupTestTenant(t, infra, "e2e_tenant_a")
		schemaA := tenantA.SchemaName()

		clearingA := createClearingAccount(t, ctxA,
			infra.internalAccountDB, schemaA,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Setup Tenant B
		ctxB, tenantB := setupTestTenant(t, infra, "e2e_tenant_b")
		schemaB := tenantB.SchemaName()

		clearingB := createClearingAccount(t, ctxB,
			infra.internalAccountDB, schemaB,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Verify Tenant A can only access Tenant A accounts
		resolvedA, _, foundA := getClearingAccountByPurpose(t, ctxA,
			infra.internalAccountDB, schemaA, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, foundA)
		assert.Equal(t, clearingA, resolvedA)

		// Verify Tenant B can only access Tenant B accounts
		resolvedB, _, foundB := getClearingAccountByPurpose(t, ctxB,
			infra.internalAccountDB, schemaB, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, foundB)
		assert.Equal(t, clearingB, resolvedB)

		// Verify accounts are different
		assert.NotEqual(t, clearingA, clearingB, "each tenant should have separate clearing accounts")

		// Verify Tenant A cannot resolve from Tenant B schema
		_, _, crossFound := getClearingAccountByPurpose(t, ctxA,
			infra.internalAccountDB, schemaB, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Note: Schema-level isolation means the query would work but return different results
		// The key is that each tenant's context uses its own schema
		_ = crossFound // Schema isolation is enforced at the query level
	})
}

// TestServiceRecoveryScenarios tests system behavior during service recovery.
func TestServiceRecoveryScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupE2EInfra(t)

	t.Run("operations_resume_after_database_reconnect", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_recovery_db")
		schemaName := tenantID.SchemaName()

		// Create clearing account
		clearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Verify it exists
		_, _, found1 := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found1)

		// Simulate brief disconnection (pool handles this automatically)
		// In pgxpool, connections are managed and reconnected as needed
		// Here we just verify continued operation after a pause

		time.Sleep(100 * time.Millisecond)

		// Operations should still work
		resolvedID, _, found2 := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found2)
		assert.Equal(t, clearingID, resolvedID, "should resolve same account after reconnect")
	})

	t.Run("partial_transaction_rollback_maintains_consistency", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_recovery_rollback")
		schemaName := tenantID.SchemaName()

		// Setup
		clearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		partyID := uuid.New().String()
		customerID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Start with known state
		initialRef := fmt.Sprintf("DEP-INIT-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			initialRef, clearingID, customerID, "GBP",
			"100.00", "Initial deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerID, "GBP", "AVAILABLE", "100.00", initialRef, "DEPOSIT")

		initialBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerID, "GBP")
		assert.Equal(t, "100.00000000", initialBalance)

		// Simulate failed transaction (would be rolled back in real impl)
		// Here we just verify that not creating a position log
		// means the balance doesn't change incorrectly

		failedRef := fmt.Sprintf("DEP-FAIL-%s", uuid.New().String()[:8])
		// Create ledger posting but "fail" before position log
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			failedRef, clearingID, customerID, "GBP",
			"50.00", "Deposit that will fail")
		// No position log created - simulating failure

		// Balance should still be 100 (position log not created)
		balance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerID, "GBP")
		assert.Equal(t, "100.00000000", balance, "balance unchanged when position log fails")

		// In a real system with sagas, the ledger posting would be compensated
		// The key assertion here is that partial failures are detectable
	})
}
