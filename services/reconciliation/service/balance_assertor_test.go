package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock implementations ---

type mockAssertionRepo struct {
	assertions map[uuid.UUID]*domain.BalanceAssertion
	createErr  error
	updateErr  error
}

func newMockAssertionRepo() *mockAssertionRepo {
	return &mockAssertionRepo{
		assertions: make(map[uuid.UUID]*domain.BalanceAssertion),
	}
}

func (m *mockAssertionRepo) Create(_ context.Context, a *domain.BalanceAssertion) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.assertions[a.AssertionID] = a
	return nil
}

func (m *mockAssertionRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.BalanceAssertion, error) {
	a, ok := m.assertions[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return a, nil
}

func (m *mockAssertionRepo) FindByRunID(_ context.Context, _ uuid.UUID) ([]*domain.BalanceAssertion, error) {
	return nil, nil
}

func (m *mockAssertionRepo) Update(_ context.Context, a *domain.BalanceAssertion) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.assertions[a.AssertionID] = a
	return nil
}

func (m *mockAssertionRepo) List(_ context.Context, _ domain.AssertionFilter) ([]*domain.BalanceAssertion, error) {
	return nil, nil
}

type mockTrendRepo struct {
	trends   map[string]*domain.ImbalanceTrend
	upserted []*domain.ImbalanceTrend
}

func newMockTrendRepo() *mockTrendRepo {
	return &mockTrendRepo{
		trends: make(map[string]*domain.ImbalanceTrend),
	}
}

func (m *mockTrendRepo) Upsert(_ context.Context, trend *domain.ImbalanceTrend) error {
	m.trends[trend.InstrumentCode] = trend
	m.upserted = append(m.upserted, trend)
	return nil
}

func (m *mockTrendRepo) FindByInstrumentCode(_ context.Context, code string) (*domain.ImbalanceTrend, error) {
	t, ok := m.trends[code]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if t.ResolvedAt != nil {
		return nil, domain.ErrNotFound
	}
	return t, nil
}

type mockPKClient struct {
	summary *PositionSummary
	err     error
}

func (m *mockPKClient) GetPositionSummary(_ context.Context, _, _ string) (*PositionSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.summary, nil
}

type mockFAClient struct {
	detail *DiagnosticDetail
	err    error
}

func (m *mockFAClient) GetDiagnosticDetail(_ context.Context, _, _ string) (*DiagnosticDetail, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.detail, nil
}

type mockEventPublisher struct {
	events []*domain.BalanceImbalanceDetectedEvent
	err    error
}

func (m *mockEventPublisher) PublishBalanceImbalanceDetected(_ context.Context, event *domain.BalanceImbalanceDetectedEvent) error {
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, event)
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// --- Tests ---

func TestExecuteBalanceAssertion_Balanced(t *testing.T) {
	repo := newMockAssertionRepo()
	trendRepo := newMockTrendRepo()
	publisher := &mockEventPublisher{}

	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(50000.00),
		},
	}

	assertor := NewBalanceAssertor(repo, trendRepo, pkClient, nil, publisher, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Assertion)
	assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)
	assert.Nil(t, result.Event, "no imbalance event for balanced assertion")
	assert.Empty(t, publisher.events, "no events published for balanced assertion")
}

func TestExecuteBalanceAssertion_Imbalanced(t *testing.T) {
	repo := newMockAssertionRepo()
	trendRepo := newMockTrendRepo()
	publisher := &mockEventPublisher{}

	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(49500.00),
		},
	}

	assertor := NewBalanceAssertor(repo, trendRepo, pkClient, nil, publisher, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Assertion)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
	assert.Contains(t, result.Assertion.FailureReason, "CRITICAL")
	assert.Contains(t, result.Assertion.FailureReason, "imbalance=500")

	// Event should be published
	require.NotNil(t, result.Event)
	assert.Equal(t, "P1_CRITICAL", result.Event.Severity)
	assert.Equal(t, "GBP", result.Event.InstrumentCode)
	assert.True(t, decimal.NewFromFloat(500.00).Equal(result.Event.ImbalanceAmount))

	require.Len(t, publisher.events, 1)
	assert.Equal(t, "P1_CRITICAL", publisher.events[0].Severity)
}

func TestExecuteBalanceAssertion_DecimalPrecision(t *testing.T) {
	repo := newMockAssertionRepo()
	trendRepo := newMockTrendRepo()

	// Test with very precise decimal amounts
	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "KWH",
			TotalDebits:    decimal.RequireFromString("123456.789012345678"),
			TotalCredits:   decimal.RequireFromString("123456.789012345678"),
		},
	}

	assertor := NewBalanceAssertor(repo, trendRepo, pkClient, nil, nil, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "KWH",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)
}

func TestExecuteBalanceAssertion_SmallImbalance(t *testing.T) {
	repo := newMockAssertionRepo()
	trendRepo := newMockTrendRepo()

	// Test with tiny precision difference - should still detect
	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.RequireFromString("100000.000000000001"),
			TotalCredits:   decimal.RequireFromString("100000.000000000000"),
		},
	}

	assertor := NewBalanceAssertor(repo, trendRepo, pkClient, nil, nil, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status,
		"even tiny imbalances must be detected")
}

func TestExecuteBalanceAssertion_CrossAccountAuthorization(t *testing.T) {
	repo := newMockAssertionRepo()
	trendRepo := newMockTrendRepo()
	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(50000.00),
		},
	}

	assertor := NewBalanceAssertor(repo, trendRepo, pkClient, nil, nil, testLogger())

	t.Run("tenant admin denied for cross-account", func(t *testing.T) {
		_, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
			AccountID:       "ACC-001",
			InstrumentCode:  "GBP",
			Expression:      "total_debits == total_credits",
			ExpectedBalance: decimal.Zero,
			Scope:           domain.AssertionScopeCrossAccount,
			CallerRole:      CallerRoleTenantAdmin,
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrUnauthorized)
	})

	t.Run("system role allowed for cross-account", func(t *testing.T) {
		result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
			AccountID:       "ACC-001",
			InstrumentCode:  "GBP",
			Expression:      "total_debits == total_credits",
			ExpectedBalance: decimal.Zero,
			Scope:           domain.AssertionScopeCrossAccount,
			CallerRole:      CallerRoleSystem,
		})

		require.NoError(t, err)
		assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)
	})

	t.Run("auditor role allowed for cross-account", func(t *testing.T) {
		result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
			AccountID:       "ACC-001",
			InstrumentCode:  "GBP",
			Expression:      "total_debits == total_credits",
			ExpectedBalance: decimal.Zero,
			Scope:           domain.AssertionScopeCrossAccount,
			CallerRole:      CallerRoleAuditor,
		})

		require.NoError(t, err)
		assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)
	})
}

func TestExecuteBalanceAssertion_NostroVostroUnimplemented(t *testing.T) {
	repo := newMockAssertionRepo()
	pkClient := &mockPKClient{} // not used; scope check returns before PK call
	assertor := NewBalanceAssertor(repo, nil, pkClient, nil, nil, testLogger())

	_, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopeNostroVostro,
		CallerRole:      CallerRoleSystem,
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnimplemented)
}

func TestExecuteBalanceAssertion_PKClientError(t *testing.T) {
	repo := newMockAssertionRepo()
	pkClient := &mockPKClient{
		err: errors.New("connection refused"),
	}

	assertor := NewBalanceAssertor(repo, nil, pkClient, nil, nil, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "querying position keeping")

	// The assertion should still be persisted with FAILED status
	require.NotNil(t, result)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
}

func TestExecuteBalanceAssertion_PKClientError_WithRepoUpdateError(t *testing.T) {
	repo := newMockAssertionRepo()
	repo.updateErr = errors.New("db write failed")
	pkClient := &mockPKClient{
		err: errors.New("connection refused"),
	}

	assertor := NewBalanceAssertor(repo, nil, pkClient, nil, nil, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "querying position keeping")
	assert.Contains(t, err.Error(), "persisting FAILED assertion")

	// Result is still returned with the failed assertion
	require.NotNil(t, result)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
}

func TestExecuteBalanceAssertion_TrendTracking(t *testing.T) {
	trendRepo := newMockTrendRepo()
	publisher := &mockEventPublisher{}

	imbalancedPK := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(49000.00),
		},
	}

	assertor := NewBalanceAssertor(newMockAssertionRepo(), trendRepo, imbalancedPK, nil, publisher, testLogger())

	// First assertion creates trend with 1 consecutive day
	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)

	// Trend should exist with 1 day (same-day calls don't increment)
	trend := trendRepo.trends["GBP"]
	require.NotNil(t, trend)
	assert.Equal(t, 1, trend.ConsecutiveDays)

	// Simulate multi-day persistence by backdating the last detection
	trend.LastDetectedAt = trend.LastDetectedAt.AddDate(0, 0, -1)

	// Second assertion (now appears to be next day) increments to 2
	_, err = assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, trend.ConsecutiveDays)
	assert.False(t, trend.IsPersistent())

	// Backdate again
	trend.LastDetectedAt = trend.LastDetectedAt.AddDate(0, 0, -1)

	// Third assertion reaches threshold
	_, err = assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	assert.Equal(t, 3, trend.ConsecutiveDays)
	assert.True(t, trend.IsPersistent())

	// The last event should indicate persistence
	require.True(t, len(publisher.events) >= 3)
	lastEvent := publisher.events[len(publisher.events)-1]
	assert.True(t, lastEvent.IsPersistent)
	assert.Equal(t, 3, lastEvent.ConsecutiveDays)
}

func TestExecuteBalanceAssertion_SameDayDoesNotIncrementTrend(t *testing.T) {
	trendRepo := newMockTrendRepo()

	imbalancedPK := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(49000.00),
		},
	}

	assertor := NewBalanceAssertor(newMockAssertionRepo(), trendRepo, imbalancedPK, nil, nil, testLogger())

	// Run multiple assertions on the same day
	for i := 0; i < 5; i++ {
		_, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
			AccountID:       "ACC-001",
			InstrumentCode:  "GBP",
			Expression:      "total_debits == total_credits",
			ExpectedBalance: decimal.Zero,
			Scope:           domain.AssertionScopePositionLedger,
			CallerRole:      CallerRoleTenantAdmin,
		})
		require.NoError(t, err)
	}

	// Should only count as 1 consecutive day despite 5 assertions
	trend := trendRepo.trends["GBP"]
	require.NotNil(t, trend)
	assert.Equal(t, 1, trend.ConsecutiveDays)
}

func TestExecuteBalanceAssertion_TrendResolution(t *testing.T) {
	trendRepo := newMockTrendRepo()

	imbalancedPK := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(49000.00),
		},
	}

	assertor := NewBalanceAssertor(newMockAssertionRepo(), trendRepo, imbalancedPK, nil, nil, testLogger())

	// Create imbalance
	_, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})
	require.NoError(t, err)

	trend := trendRepo.trends["GBP"]
	require.NotNil(t, trend)
	assert.Equal(t, 1, trend.ConsecutiveDays)

	// Now resolve with balanced PK
	balancedPK := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(50000.00),
		},
	}

	assertor2 := NewBalanceAssertor(newMockAssertionRepo(), trendRepo, balancedPK, nil, nil, testLogger())

	result, err := assertor2.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)

	// Trend should be resolved
	trend = trendRepo.trends["GBP"]
	require.NotNil(t, trend)
	assert.NotNil(t, trend.ResolvedAt)
	assert.Equal(t, 0, trend.ConsecutiveDays)
}

func TestExecuteBalanceAssertion_FADiagnosticEnrichment(t *testing.T) {
	repo := newMockAssertionRepo()

	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(49000.00),
		},
	}

	faClient := &mockFAClient{
		detail: &DiagnosticDetail{
			AccountID:       "ACC-001",
			InstrumentCode:  "GBP",
			JournalEntryIDs: []string{"JE-001", "JE-002"},
			Message:         "Missing contra-entry for JE-001",
		},
	}

	assertor := NewBalanceAssertor(repo, newMockTrendRepo(), pkClient, faClient, nil, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)

	// FA diagnostics should be in attributes
	require.NotNil(t, result.Assertion.Attributes)
	assert.Equal(t, "Missing contra-entry for JE-001", result.Assertion.Attributes["fa_diagnostic_message"])
}

func TestExecuteBalanceAssertion_FAClientError(t *testing.T) {
	repo := newMockAssertionRepo()

	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(49000.00),
		},
	}

	faClient := &mockFAClient{
		err: errors.New("FA service unavailable"),
	}

	assertor := NewBalanceAssertor(repo, newMockTrendRepo(), pkClient, faClient, nil, testLogger())

	// FA failure should not prevent assertion from completing
	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
}

func TestExecuteBalanceAssertion_WithRunID(t *testing.T) {
	repo := newMockAssertionRepo()

	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(50000.00),
		},
	}

	runID := uuid.New()
	assertor := NewBalanceAssertor(repo, nil, pkClient, nil, nil, testLogger())

	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		RunID:           &runID,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	assert.Equal(t, &runID, result.Assertion.RunID)
}

func TestExecuteBalanceAssertion_ValidationErrors(t *testing.T) {
	assertor := NewBalanceAssertor(newMockAssertionRepo(), nil, &mockPKClient{}, nil, nil, testLogger())

	t.Run("empty account ID", func(t *testing.T) {
		_, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
			AccountID:       "",
			InstrumentCode:  "GBP",
			Expression:      "expr",
			ExpectedBalance: decimal.Zero,
			Scope:           domain.AssertionScopePositionLedger,
			CallerRole:      CallerRoleTenantAdmin,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrEmptyAccountID)
	})

	t.Run("empty instrument code", func(t *testing.T) {
		_, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
			AccountID:       "ACC-001",
			InstrumentCode:  "",
			Expression:      "expr",
			ExpectedBalance: decimal.Zero,
			Scope:           domain.AssertionScopePositionLedger,
			CallerRole:      CallerRoleTenantAdmin,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrEmptyInstrumentCode)
	})

	t.Run("empty expression", func(t *testing.T) {
		_, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
			AccountID:       "ACC-001",
			InstrumentCode:  "GBP",
			Expression:      "",
			ExpectedBalance: decimal.Zero,
			Scope:           domain.AssertionScopePositionLedger,
			CallerRole:      CallerRoleTenantAdmin,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrEmptyAssertionExpression)
	})
}

func TestExecuteBalanceAssertion_EventPublisherError(t *testing.T) {
	repo := newMockAssertionRepo()
	publisher := &mockEventPublisher{
		err: errors.New("kafka unavailable"),
	}

	pkClient := &mockPKClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    decimal.NewFromFloat(50000.00),
			TotalCredits:   decimal.NewFromFloat(49000.00),
		},
	}

	assertor := NewBalanceAssertor(repo, newMockTrendRepo(), pkClient, nil, publisher, testLogger())

	// Publisher error should not prevent assertion from completing
	result, err := assertor.ExecuteBalanceAssertion(context.Background(), AssertBalanceRequest{
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.Zero,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      CallerRoleTenantAdmin,
	})

	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)
}

func TestImbalanceTrend_IsPersistent(t *testing.T) {
	trend := &domain.ImbalanceTrend{
		InstrumentCode:  "GBP",
		ConsecutiveDays: 2,
	}
	assert.False(t, trend.IsPersistent())

	trend.ConsecutiveDays = 3
	assert.True(t, trend.IsPersistent())

	trend.ConsecutiveDays = 10
	assert.True(t, trend.IsPersistent())
}

func TestImbalanceTrend_RecordAndResolve(t *testing.T) {
	trend := &domain.ImbalanceTrend{
		TrendID:        uuid.New(),
		InstrumentCode: "GBP",
	}

	assertionID := uuid.New()
	trend.RecordImbalance(decimal.NewFromFloat(500), assertionID)

	assert.Equal(t, 1, trend.ConsecutiveDays)
	assert.Equal(t, assertionID, trend.LastAssertionID)
	assert.Nil(t, trend.ResolvedAt)

	// Same day call should not increment
	assertionID2 := uuid.New()
	trend.RecordImbalance(decimal.NewFromFloat(600), assertionID2)
	assert.Equal(t, 1, trend.ConsecutiveDays, "same-day call should not increment")
	assert.Equal(t, assertionID2, trend.LastAssertionID)

	trend.Resolve()
	assert.Equal(t, 0, trend.ConsecutiveDays)
	assert.NotNil(t, trend.ResolvedAt)
}

func TestInferScope(t *testing.T) {
	assert.Equal(t, domain.AssertionScopeCrossAccount, inferScope("any", "SYSTEM"))
	assert.Equal(t, domain.AssertionScopeCrossAccount, inferScope("any", "*"))
	assert.Equal(t, domain.AssertionScopePositionLedger, inferScope("any", "ACC-001"))
}
