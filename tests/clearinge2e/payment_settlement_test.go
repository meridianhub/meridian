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
// Test: Payment Settlement Flow E2E
// =============================================================================

// TestPaymentSettlementFlow tests the complete payment settlement flow across services.
//
// Flow:
// 1. Payment Order: Initiate outbound payment
// 2. Current Account: Create lien on customer account
// 3. Internal Account: Resolve settlement clearing account
// 4. Financial Accounting: Create multi-leg posting (Customer → Clearing → Gateway)
// 5. Position Keeping: Record all position movements
// 6. Verify: All legs balanced
func TestPaymentSettlementFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupE2EInfra(t)

	t.Run("outbound_payment_settlement_full_flow", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_payment_outbound")
		schemaName := tenantID.SchemaName()

		// Step 1: Setup accounts
		// Deposit clearing (to fund the customer)
		depositClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Settlement clearing (for payment processing)
		settlementClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-SETTLEMENT", "GBP", "CLEARING_PURPOSE_SETTLEMENT")

		// Gateway account (represents external payment network)
		gatewayAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"GATEWAY-GBP-FPS", "GBP", "CLEARING_PURPOSE_UNSPECIFIED")

		// Customer account
		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Step 2: Fund customer account
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef,
			depositClearingID, customerAccountID, "GBP",
			"500.00", "Initial deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingID, "GBP", "AVAILABLE", "-500.00", depositRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "500.00", depositRef, "DEPOSIT")

		// Step 3: Create payment order
		paymentAmount := "100.00"
		paymentRef := fmt.Sprintf("PAY-%s", uuid.New().String()[:8])

		// Create lien on customer account
		lienID := createLien(t, ctx,
			infra.currentAccountDB, schemaName,
			customerAccountID, paymentAmount, "PAYMENT", paymentRef)
		require.NotEmpty(t, lienID)

		// Step 4: Process settlement (multi-leg posting)
		// Leg 1: Debit Customer → Credit Clearing
		leg1Ref := paymentRef + "-L1"
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			leg1Ref,
			customerAccountID, settlementClearingID, "GBP",
			paymentAmount, "Payment settlement - Customer to Clearing")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "-"+paymentAmount, leg1Ref, "PAYMENT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			settlementClearingID, "GBP", "AVAILABLE", paymentAmount, leg1Ref, "PAYMENT")

		// Leg 2: Debit Clearing → Credit Gateway
		leg2Ref := paymentRef + "-L2"
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			leg2Ref,
			settlementClearingID, gatewayAccountID, "GBP",
			paymentAmount, "Payment settlement - Clearing to Gateway")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			settlementClearingID, "GBP", "AVAILABLE", "-"+paymentAmount, leg2Ref, "PAYMENT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			gatewayAccountID, "GBP", "AVAILABLE", paymentAmount, leg2Ref, "PAYMENT")

		// Step 5: Release lien after settlement
		releaseLien(t, ctx, infra.currentAccountDB, schemaName, lienID)

		// Step 6: Verify all balances
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
		settlementBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, settlementClearingID, "GBP")
		gatewayBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, gatewayAccountID, "GBP")

		// Customer: 500 - 100 = 400
		assert.Equal(t, "400.00000000", customerBalance, "customer balance after payment")
		// Settlement clearing: 100 - 100 = 0 (balanced)
		assert.Equal(t, "0", settlementBalance, "settlement clearing should be zero-balanced")
		// Gateway: 100 (funds sent to external network)
		assert.Equal(t, "100.00000000", gatewayBalance, "gateway received funds")

		// Verify lien is released
		activeLiens := getActiveLiens(t, ctx, infra.currentAccountDB, schemaName, customerAccountID)
		assert.Empty(t, activeLiens, "all liens should be released")
	})

	t.Run("payment_with_multiple_legs_stays_balanced", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_payment_multi_leg")
		schemaName := tenantID.SchemaName()

		// Setup accounts
		depositClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		settlementClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-SETTLE", "GBP", "CLEARING_PURPOSE_SETTLEMENT")

		feeClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-FEE", "GBP", "CLEARING_PURPOSE_UNSPECIFIED")

		gatewayAccountID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"GATEWAY-GBP", "GBP", "CLEARING_PURPOSE_UNSPECIFIED")

		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Fund customer
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef, depositClearingID, customerAccountID, "GBP",
			"1000.00", "Initial deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingID, "GBP", "AVAILABLE", "-1000.00", depositRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "1000.00", depositRef, "DEPOSIT")

		// Process payment with fee
		paymentAmount := "200.00"
		feeAmount := "2.50"
		totalAmount := "202.50"
		paymentRef := fmt.Sprintf("PAY-%s", uuid.New().String()[:8])

		// Create lien for total (payment + fee)
		lienID := createLien(t, ctx,
			infra.currentAccountDB, schemaName,
			customerAccountID, totalAmount, "PAYMENT", paymentRef)

		// Leg 1: Customer → Settlement Clearing (payment amount)
		leg1Ref := paymentRef + "-PRINCIPAL"
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			leg1Ref, customerAccountID, settlementClearingID, "GBP",
			paymentAmount, "Payment principal")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "-"+paymentAmount, leg1Ref, "PAYMENT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			settlementClearingID, "GBP", "AVAILABLE", paymentAmount, leg1Ref, "PAYMENT")

		// Leg 2: Customer → Fee Clearing (fee amount)
		leg2Ref := paymentRef + "-FEE"
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			leg2Ref, customerAccountID, feeClearingID, "GBP",
			feeAmount, "Payment fee")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "-"+feeAmount, leg2Ref, "FEE")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			feeClearingID, "GBP", "AVAILABLE", feeAmount, leg2Ref, "FEE")

		// Leg 3: Settlement Clearing → Gateway (payment to external)
		leg3Ref := paymentRef + "-GATEWAY"
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			leg3Ref, settlementClearingID, gatewayAccountID, "GBP",
			paymentAmount, "Gateway settlement")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			settlementClearingID, "GBP", "AVAILABLE", "-"+paymentAmount, leg3Ref, "PAYMENT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			gatewayAccountID, "GBP", "AVAILABLE", paymentAmount, leg3Ref, "PAYMENT")

		// Release lien
		releaseLien(t, ctx, infra.currentAccountDB, schemaName, lienID)

		// Verify balances
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
		settlementBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, settlementClearingID, "GBP")
		feeBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, feeClearingID, "GBP")
		gatewayBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, gatewayAccountID, "GBP")

		// Customer: 1000 - 200 - 2.50 = 797.50
		assert.Equal(t, "797.50000000", customerBalance)
		// Settlement: 200 - 200 = 0 (pass-through)
		assert.Equal(t, "0", settlementBalance)
		// Fee: 2.50 (revenue captured)
		assert.Equal(t, "2.50000000", feeBalance)
		// Gateway: 200 (external payment)
		assert.Equal(t, "200.00000000", gatewayBalance)
	})

	t.Run("payment_settlement_with_async_confirmation", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_payment_async")
		schemaName := tenantID.SchemaName()

		// Setup
		depositClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		settlementClearingID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-SETTLE", "GBP", "CLEARING_PURPOSE_SETTLEMENT")

		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-CUST-001", partyID, "GBP")

		// Fund customer
		depositRef := fmt.Sprintf("DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef, depositClearingID, customerAccountID, "GBP",
			"500.00", "Initial deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			depositClearingID, "GBP", "AVAILABLE", "-500.00", depositRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GBP", "AVAILABLE", "500.00", depositRef, "DEPOSIT")

		// Initiate payment
		paymentRef := fmt.Sprintf("PAY-%s", uuid.New().String()[:8])
		paymentAmount := "75.00"

		// Create lien
		lienID := createLien(t, ctx,
			infra.currentAccountDB, schemaName,
			customerAccountID, paymentAmount, "PAYMENT", paymentRef)

		// Simulate async settlement processing
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond) //nolint:forbidigo // simulates async settlement processing delay

			// Debit customer, credit clearing
			legRef := paymentRef + "-SETTLE"
			createLedgerPosting(t, ctx,
				infra.financialAccountingDB, schemaName,
				legRef, customerAccountID, settlementClearingID, "GBP",
				paymentAmount, "Settlement")

			recordPosition(t, ctx,
				infra.positionKeepingDB, schemaName,
				customerAccountID, "GBP", "AVAILABLE", "-"+paymentAmount, legRef, "PAYMENT")
			recordPosition(t, ctx,
				infra.positionKeepingDB, schemaName,
				settlementClearingID, "GBP", "AVAILABLE", paymentAmount, legRef, "PAYMENT")

			// Release lien
			releaseLien(t, ctx, infra.currentAccountDB, schemaName, lienID)
		}()

		// Ensure goroutine completes before test ends (for safe t usage)
		defer wg.Wait()

		// Wait for settlement using await
		var finalCustomerBalance string
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				balance := getPositionBalance(t, ctx,
					infra.positionKeepingDB, schemaName, customerAccountID, "GBP")
				if balance != "500.00000000" {
					finalCustomerBalance = balance
					return true
				}
				return false
			})
		require.NoError(t, err, "settlement should complete within timeout")
		assert.Equal(t, "425.00000000", finalCustomerBalance)

		// Verify lien is released
		err = await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			Until(func() bool {
				liens := getActiveLiens(t, ctx, infra.currentAccountDB, schemaName, customerAccountID)
				return len(liens) == 0
			})
		require.NoError(t, err, "lien should be released")
	})
}

// =============================================================================
// Lien Helpers
// =============================================================================

// createLien creates a lien on an account.
func createLien(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	accountID string,
	amount string,
	reason string,
	referenceID string,
) string {
	t.Helper()

	var lienID string
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s.liens (account_id, amount, reason, reference_id, status)
		VALUES ($1, $2, $3, $4, 'ACTIVE')
		RETURNING id
	`, pq.QuoteIdentifier(schemaName))

	err := db.pool.QueryRow(ctx, insertSQL, accountID, amount, reason, referenceID).Scan(&lienID)
	require.NoError(t, err, "failed to create lien")

	return lienID
}

// releaseLien releases a lien.
func releaseLien(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	lienID string,
) {
	t.Helper()

	updateSQL := fmt.Sprintf(`
		UPDATE %s.liens
		SET status = 'RELEASED', released_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, pq.QuoteIdentifier(schemaName))

	_, err := db.pool.Exec(ctx, updateSQL, lienID)
	require.NoError(t, err, "failed to release lien")
}

// getActiveLiens returns active liens for an account.
func getActiveLiens(
	t *testing.T,
	ctx context.Context,
	db *serviceDB,
	schemaName string,
	accountID string,
) []string {
	t.Helper()

	querySQL := fmt.Sprintf(`
		SELECT id FROM %s.liens
		WHERE account_id = $1 AND status = 'ACTIVE'
	`, pq.QuoteIdentifier(schemaName))

	rows, err := db.pool.Query(ctx, querySQL, accountID)
	require.NoError(t, err)
	defer rows.Close()

	var liens []string
	for rows.Next() {
		var id string
		err := rows.Scan(&id)
		require.NoError(t, err)
		liens = append(liens, id)
	}

	return liens
}
