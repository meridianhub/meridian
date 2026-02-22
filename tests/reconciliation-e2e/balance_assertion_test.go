//go:build integration
// +build integration

package reconciliatione2e

import (
	"testing"

	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBalanceAssertion_Balanced verifies that when debits == credits,
// the assertion passes with PASSED status.
func TestBalanceAssertion_Balanced(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Set up mock PK to return balanced positions
	infra.mockPKClient.setSummary("ACC-BAL", "GBP",
		decimal.NewFromFloat(5000.00), // total debits
		decimal.NewFromFloat(5000.00), // total credits (balanced)
	)

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-BAL",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(5000.00),
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)
	assert.Nil(t, result.Event, "no imbalance event should be published for balanced positions")

	// Verify assertion was persisted
	persisted, err := infra.assertionRepo.FindByID(ctx, result.Assertion.AssertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, persisted.Status)
}

// TestBalanceAssertion_Imbalanced verifies that when debits != credits,
// the assertion fails and publishes a P1 critical event.
func TestBalanceAssertion_Imbalanced(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Set up mock PK with imbalance
	infra.mockPKClient.setSummary("ACC-IMBAL", "GBP",
		decimal.NewFromFloat(5000.00), // total debits
		decimal.NewFromFloat(4800.00), // total credits (imbalanced by 200)
	)

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-IMBAL",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(5000.00),
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
	assert.Contains(t, result.Assertion.FailureReason, "CRITICAL")
	assert.Contains(t, result.Assertion.FailureReason, "imbalance")

	// Verify imbalance event was published
	require.NotNil(t, result.Event, "imbalance event should be published")
	assert.Equal(t, "GBP", result.Event.InstrumentCode)
	assert.True(t, result.Event.ImbalanceAmount.Equal(decimal.NewFromFloat(200.00)))

	// Verify event was published to mock publisher
	imbalanceEvents := infra.mockPublisher.getEventsByTopic("reconciliation.balance-imbalance-detected.v1")
	assert.Len(t, imbalanceEvents, 1)

	// Verify assertion was persisted
	persisted, err := infra.assertionRepo.FindByID(ctx, result.Assertion.AssertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, persisted.Status)
}

// TestBalanceAssertion_ImbalanceTrend verifies that persistent imbalances
// across multiple assertions trigger trend tracking.
func TestBalanceAssertion_ImbalanceTrend(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Set up persistent imbalance
	infra.mockPKClient.setSummary("ACC-TREND", "EUR",
		decimal.NewFromFloat(10000.00),
		decimal.NewFromFloat(9900.00), // 100 EUR imbalance
	)

	// Run multiple assertions to build a trend
	for i := 0; i < 3; i++ {
		result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
			AccountID:       "ACC-TREND",
			InstrumentCode:  "EUR",
			Expression:      "total_debits == total_credits",
			ExpectedBalance: decimal.NewFromFloat(10000.00),
			Scope:           domain.AssertionScopePositionLedger,
			CallerRole:      service.CallerRoleTenantAdmin,
		})
		require.NoError(t, err)
		assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
	}

	// Verify trend was created and tracks consecutive days
	trend, err := infra.trendRepo.FindByInstrumentCode(ctx, "EUR")
	require.NoError(t, err)
	assert.Equal(t, "EUR", trend.InstrumentCode)
	assert.Greater(t, trend.ConsecutiveDays, 0, "consecutive days should increase")
	assert.True(t, trend.LastImbalanceAmount.Equal(decimal.NewFromFloat(100.00)))
}

// TestBalanceAssertion_TrendResolvesOnBalance verifies that a balanced assertion
// resolves any existing imbalance trend.
func TestBalanceAssertion_TrendResolvesOnBalance(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Create an imbalance first
	infra.mockPKClient.setSummary("ACC-RESOLVE", "USD",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(900.00),
	)

	_, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-RESOLVE",
		InstrumentCode:  "USD",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(1000.00),
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.NoError(t, err)

	// Verify trend exists
	trend, err := infra.trendRepo.FindByInstrumentCode(ctx, "USD")
	require.NoError(t, err)
	assert.Nil(t, trend.ResolvedAt, "trend should be unresolved")

	// Now fix the imbalance
	infra.mockPKClient.setSummary("ACC-RESOLVE", "USD",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1000.00), // Balanced now
	)

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-RESOLVE",
		InstrumentCode:  "USD",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(1000.00),
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)

	// Trend should be resolved (FindByInstrumentCode only returns unresolved)
	_, err = infra.trendRepo.FindByInstrumentCode(ctx, "USD")
	assert.ErrorIs(t, err, domain.ErrNotFound, "trend should be resolved and not found by active query")
}

// TestBalanceAssertion_CrossAccountRequiresSystemRole verifies RBAC for
// cross-account balance assertions.
func TestBalanceAssertion_CrossAccountRequiresSystemRole(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Tenant admin should not be able to do cross-account assertions
	_, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "SYSTEM",
		InstrumentCode:  "GBP",
		Expression:      "sum(all_accounts.debits) == sum(all_accounts.credits)",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopeCrossAccount,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)

	// System role should succeed
	infra.mockPKClient.setSummary("SYSTEM", "GBP",
		decimal.NewFromFloat(100000.00),
		decimal.NewFromFloat(100000.00),
	)

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "SYSTEM",
		InstrumentCode:  "GBP",
		Expression:      "sum(all_accounts.debits) == sum(all_accounts.credits)",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopeCrossAccount,
		CallerRole:      service.CallerRoleSystem,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)
}

// TestBalanceAssertion_PKClientFailure verifies graceful degradation when
// Position Keeping is unavailable.
func TestBalanceAssertion_PKClientFailure(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Mock PK to return error
	infra.mockPKClient.setError(assert.AnError)

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-PKFAIL",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(1000.00),
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})

	// Should return both result AND error
	require.Error(t, err)
	require.NotNil(t, result, "result should be returned even on PK failure")
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
	assert.Contains(t, result.Assertion.FailureReason, "failed to query position keeping")

	// Reset PK error for other tests
	infra.mockPKClient.setError(nil)
}

// TestBalanceAssertion_OverrideWorkflow verifies that a failed assertion
// can be overridden by an operator.
func TestBalanceAssertion_OverrideWorkflow(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Create failed assertion via imbalance
	infra.mockPKClient.setSummary("ACC-OVRD", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(999.00),
	)

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-OVRD",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(1000.00),
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)

	// Override the assertion via domain model
	require.NoError(t, result.Assertion.Override("Approved by risk committee"))
	require.NoError(t, infra.assertionRepo.Update(ctx, result.Assertion))

	// Verify override persisted
	overridden, err := infra.assertionRepo.FindByID(ctx, result.Assertion.AssertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusOverride, overridden.Status)
	assert.Equal(t, "Approved by risk committee", overridden.OverrideReason)
	// Original failure reason preserved
	assert.NotEmpty(t, overridden.FailureReason)
}

// TestBalanceAssertion_NostroVostroUnimplemented verifies that NOSTRO_VOSTRO scope
// returns an ErrUnimplemented error.
func TestBalanceAssertion_NostroVostroUnimplemented(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	_, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-NV",
		InstrumentCode:  "GBP",
		Expression:      "nostro == vostro",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopeNostroVostro,
		CallerRole:      service.CallerRoleSystem,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnimplemented)
}
