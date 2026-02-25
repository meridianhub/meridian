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
// Test: Multi-Asset Clearing Flows
// =============================================================================

// TestMultiAssetClearingFlows tests clearing account resolution and operations
// for different asset types: fiat (GBP, USD, EUR) and non-fiat (KWH, GPU-HOUR, CARBON).
func TestMultiAssetClearingFlows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	infra := setupE2EInfra(t)

	t.Run("GBP_deposit_uses_GBP_clearing_account", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_multi_asset_gbp")
		schemaName := tenantID.SchemaName()

		// Create GBP clearing accounts
		gbpDepositID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		// Create USD clearing account (should NOT be used)
		_ = createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-USD-DEP", "USD", "CLEARING_PURPOSE_DEPOSIT")

		// Resolve GBP deposit clearing account
		resolvedID, code, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "GBP", "CLEARING_PURPOSE_DEPOSIT")

		require.True(t, found, "GBP deposit clearing account should be found")
		assert.Equal(t, gbpDepositID, resolvedID)
		assert.Equal(t, "CLR-GBP-DEP", code)
	})

	t.Run("KWH_deposit_uses_KWH_clearing_account", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_multi_asset_kwh")
		schemaName := tenantID.SchemaName()

		// Create KWH clearing accounts for energy trading
		kwhDepositID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-KWH-DEP", "KWH", "CLEARING_PURPOSE_DEPOSIT")

		kwhWithdrawID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-KWH-WDR", "KWH", "CLEARING_PURPOSE_WITHDRAWAL")

		// Create customer account for energy
		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-ENERGY-001", partyID, "KWH")

		// Process energy deposit (e.g., solar generation credit)
		depositRef := fmt.Sprintf("ENERGY-DEP-%s", uuid.New().String()[:8])
		energyAmount := "150.5" // 150.5 kWh

		// Resolve deposit clearing account
		resolvedID, _, found := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "KWH", "CLEARING_PURPOSE_DEPOSIT")
		require.True(t, found)
		assert.Equal(t, kwhDepositID, resolvedID)

		// Create ledger posting
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			depositRef, kwhDepositID, customerAccountID, "KWH",
			energyAmount, "Solar generation credit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			kwhDepositID, "KWH", "AVAILABLE", "-"+energyAmount, depositRef, "ENERGY_DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "KWH", "AVAILABLE", energyAmount, depositRef, "ENERGY_DEPOSIT")

		// Verify balances
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "KWH")
		assert.Equal(t, "150.50000000", customerBalance)

		// Verify withdrawal clearing account is separate
		_, _, withdrawFound := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "KWH", "CLEARING_PURPOSE_WITHDRAWAL")
		require.True(t, withdrawFound)

		withdrawResolvedID, _, _ := getClearingAccountByPurpose(t, ctx,
			infra.internalAccountDB, schemaName, "KWH", "CLEARING_PURPOSE_WITHDRAWAL")
		assert.Equal(t, kwhWithdrawID, withdrawResolvedID)
		assert.NotEqual(t, kwhDepositID, kwhWithdrawID, "deposit and withdrawal should be different accounts")
	})

	t.Run("GPU_HOUR_clearing_for_compute_credits", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_multi_asset_gpu")
		schemaName := tenantID.SchemaName()

		// Create GPU-HOUR clearing accounts
		gpuDepositID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GPU-DEP", "GPU-HOUR", "CLEARING_PURPOSE_DEPOSIT")

		gpuWithdrawID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GPU-WDR", "GPU-HOUR", "CLEARING_PURPOSE_WITHDRAWAL")

		// Create AI company account
		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-AI-001", partyID, "GPU-HOUR")

		// Purchase GPU compute credits
		purchaseRef := fmt.Sprintf("GPU-PURCHASE-%s", uuid.New().String()[:8])
		gpuHours := "1000.00" // 1000 GPU hours

		// Deposit GPU hours to customer
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			purchaseRef, gpuDepositID, customerAccountID, "GPU-HOUR",
			gpuHours, "GPU compute credits purchase")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			gpuDepositID, "GPU-HOUR", "AVAILABLE", "-"+gpuHours, purchaseRef, "COMPUTE_PURCHASE")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GPU-HOUR", "AVAILABLE", gpuHours, purchaseRef, "COMPUTE_PURCHASE")

		// Consume some GPU hours (training job)
		consumeRef := fmt.Sprintf("GPU-CONSUME-%s", uuid.New().String()[:8])
		consumedHours := "250.75"

		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			consumeRef, customerAccountID, gpuWithdrawID, "GPU-HOUR",
			consumedHours, "Model training job")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "GPU-HOUR", "AVAILABLE", "-"+consumedHours, consumeRef, "COMPUTE_CONSUME")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			gpuWithdrawID, "GPU-HOUR", "AVAILABLE", consumedHours, consumeRef, "COMPUTE_CONSUME")

		// Verify balances
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "GPU-HOUR")
		depositBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, gpuDepositID, "GPU-HOUR")
		withdrawBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, gpuWithdrawID, "GPU-HOUR")

		// Customer: 1000 - 250.75 = 749.25
		assert.Equal(t, "749.25000000", customerBalance)
		assert.Equal(t, "-1000.00000000", depositBalance)
		assert.Equal(t, "250.75000000", withdrawBalance)
	})

	t.Run("CARBON_credits_clearing", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_multi_asset_carbon")
		schemaName := tenantID.SchemaName()

		// Create CARBON clearing accounts
		carbonDepositID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-CARBON-DEP", "CARBON", "CLEARING_PURPOSE_DEPOSIT")

		carbonRetireID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-CARBON-RETIRE", "CARBON", "CLEARING_PURPOSE_WITHDRAWAL")

		// Create company account
		partyID := uuid.New().String()
		customerAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-ESG-001", partyID, "CARBON")

		// Issue carbon credits (from verified project)
		issueRef := fmt.Sprintf("CARBON-ISSUE-%s", uuid.New().String()[:8])
		carbonCredits := "5000.00" // 5000 tonnes CO2e

		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			issueRef, carbonDepositID, customerAccountID, "CARBON",
			carbonCredits, "Carbon credits from reforestation project")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			carbonDepositID, "CARBON", "AVAILABLE", "-"+carbonCredits, issueRef, "CARBON_ISSUE")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "CARBON", "AVAILABLE", carbonCredits, issueRef, "CARBON_ISSUE")

		// Retire carbon credits (offset emissions)
		retireRef := fmt.Sprintf("CARBON-RETIRE-%s", uuid.New().String()[:8])
		retiredCredits := "1500.00"

		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			retireRef, customerAccountID, carbonRetireID, "CARBON",
			retiredCredits, "Carbon retirement for Q4 emissions offset")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			customerAccountID, "CARBON", "AVAILABLE", "-"+retiredCredits, retireRef, "CARBON_RETIRE")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			carbonRetireID, "CARBON", "AVAILABLE", retiredCredits, retireRef, "CARBON_RETIRE")

		// Verify balances
		customerBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, customerAccountID, "CARBON")
		retireBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, carbonRetireID, "CARBON")

		// Customer: 5000 - 1500 = 3500
		assert.Equal(t, "3500.00000000", customerBalance)
		// Retired: 1500 (permanently offset)
		assert.Equal(t, "1500.00000000", retireBalance)
	})

	t.Run("mixed_asset_types_isolated_correctly", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_multi_asset_mixed")
		schemaName := tenantID.SchemaName()

		// Create clearing accounts for all asset types
		assets := []struct {
			code   string
			prefix string
		}{
			{"GBP", "CLR-GBP"},
			{"USD", "CLR-USD"},
			{"EUR", "CLR-EUR"},
			{"KWH", "CLR-KWH"},
			{"GPU-HOUR", "CLR-GPU"},
			{"CARBON", "CLR-CARBON"},
		}

		clearingAccounts := make(map[string]string)
		for _, asset := range assets {
			depositCode := asset.prefix + "-DEP"
			depositID := createClearingAccount(t, ctx,
				infra.internalAccountDB, schemaName,
				depositCode, asset.code, "CLEARING_PURPOSE_DEPOSIT")
			clearingAccounts[asset.code+"-DEPOSIT"] = depositID

			withdrawCode := asset.prefix + "-WDR"
			withdrawID := createClearingAccount(t, ctx,
				infra.internalAccountDB, schemaName,
				withdrawCode, asset.code, "CLEARING_PURPOSE_WITHDRAWAL")
			clearingAccounts[asset.code+"-WITHDRAWAL"] = withdrawID
		}

		// Verify each asset type resolves to its own clearing account
		for _, asset := range assets {
			depositID, _, found := getClearingAccountByPurpose(t, ctx,
				infra.internalAccountDB, schemaName, asset.code, "CLEARING_PURPOSE_DEPOSIT")
			require.True(t, found, "%s deposit clearing should exist", asset.code)
			assert.Equal(t, clearingAccounts[asset.code+"-DEPOSIT"], depositID,
				"%s should resolve to correct deposit clearing account", asset.code)

			withdrawID, _, found := getClearingAccountByPurpose(t, ctx,
				infra.internalAccountDB, schemaName, asset.code, "CLEARING_PURPOSE_WITHDRAWAL")
			require.True(t, found, "%s withdrawal clearing should exist", asset.code)
			assert.Equal(t, clearingAccounts[asset.code+"-WITHDRAWAL"], withdrawID,
				"%s should resolve to correct withdrawal clearing account", asset.code)

			// Verify deposit and withdrawal are different accounts
			assert.NotEqual(t, depositID, withdrawID,
				"%s deposit and withdrawal should be different accounts", asset.code)
		}
	})

	t.Run("cross_asset_operations_stay_isolated", func(t *testing.T) {
		ctx, tenantID := setupTestTenant(t, infra, "e2e_multi_asset_isolation")
		schemaName := tenantID.SchemaName()

		// Create clearing accounts
		gbpDepositID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-GBP-DEP", "GBP", "CLEARING_PURPOSE_DEPOSIT")

		kwhDepositID := createClearingAccount(t, ctx,
			infra.internalAccountDB, schemaName,
			"CLR-KWH-DEP", "KWH", "CLEARING_PURPOSE_DEPOSIT")

		// Create customer accounts (one per asset type)
		partyID := uuid.New().String()
		gbpAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-GBP-001", partyID, "GBP")

		kwhAccountID := createCustomerAccount(t, ctx,
			infra.currentAccountDB, schemaName,
			"ACC-KWH-001", partyID, "KWH")

		// Deposit GBP
		gbpRef := fmt.Sprintf("GBP-DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			gbpRef, gbpDepositID, gbpAccountID, "GBP",
			"1000.00", "GBP deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			gbpDepositID, "GBP", "AVAILABLE", "-1000.00", gbpRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			gbpAccountID, "GBP", "AVAILABLE", "1000.00", gbpRef, "DEPOSIT")

		// Deposit KWH
		kwhRef := fmt.Sprintf("KWH-DEP-%s", uuid.New().String()[:8])
		createLedgerPosting(t, ctx,
			infra.financialAccountingDB, schemaName,
			kwhRef, kwhDepositID, kwhAccountID, "KWH",
			"500.00", "KWH deposit")

		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			kwhDepositID, "KWH", "AVAILABLE", "-500.00", kwhRef, "DEPOSIT")
		recordPosition(t, ctx,
			infra.positionKeepingDB, schemaName,
			kwhAccountID, "KWH", "AVAILABLE", "500.00", kwhRef, "DEPOSIT")

		// Verify GBP account only has GBP balance
		gbpBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, gbpAccountID, "GBP")
		gbpKwhBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, gbpAccountID, "KWH")

		assert.Equal(t, "1000.00000000", gbpBalance, "GBP account should have GBP")
		assert.Equal(t, "0", gbpKwhBalance, "GBP account should not have KWH")

		// Verify KWH account only has KWH balance
		kwhBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, kwhAccountID, "KWH")
		kwhGbpBalance := getPositionBalance(t, ctx,
			infra.positionKeepingDB, schemaName, kwhAccountID, "GBP")

		assert.Equal(t, "500.00000000", kwhBalance, "KWH account should have KWH")
		assert.Equal(t, "0", kwhGbpBalance, "KWH account should not have GBP")
	})
}
