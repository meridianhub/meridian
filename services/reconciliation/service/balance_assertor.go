package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/observability"
	"github.com/shopspring/decimal"
)

// CallerRole represents the authorization role of the calling user.
type CallerRole string

// Supported caller roles for authorization checks.
const (
	CallerRoleTenantAdmin CallerRole = "TENANT_ADMIN"
	CallerRoleSystem      CallerRole = "SYSTEM"
	CallerRoleAuditor     CallerRole = "AUDITOR"
)

// AssertBalanceRequest is the input for executing a balance assertion.
type AssertBalanceRequest struct {
	AccountID       string
	InstrumentCode  string
	Expression      string
	ExpectedBalance decimal.Decimal
	RunID           *uuid.UUID
	Scope           domain.AssertionScope
	CallerRole      CallerRole
}

// AssertBalanceResult is the output of a balance assertion execution.
type AssertBalanceResult struct {
	Assertion *domain.BalanceAssertion
	Event     *domain.BalanceImbalanceDetectedEvent
}

// ImbalanceEventPublisher publishes balance imbalance domain events.
type ImbalanceEventPublisher interface {
	PublishBalanceImbalanceDetected(ctx context.Context, event *domain.BalanceImbalanceDetectedEvent) error
}

// BalanceAssertor executes balance assertion checks against Position Keeping.
type BalanceAssertor struct {
	assertionRepo domain.BalanceAssertionRepository
	trendRepo     domain.ImbalanceTrendRepository
	pkClient      PositionKeepingClient
	faClient      FinancialAccountingClient
	publisher     ImbalanceEventPublisher
	logger        *slog.Logger
}

// NewBalanceAssertor creates a new BalanceAssertor.
// assertionRepo and pkClient are required; nil values will panic.
// trendRepo, faClient, and publisher are optional (nil-guarded at call sites).
func NewBalanceAssertor(
	assertionRepo domain.BalanceAssertionRepository,
	trendRepo domain.ImbalanceTrendRepository,
	pkClient PositionKeepingClient,
	faClient FinancialAccountingClient,
	publisher ImbalanceEventPublisher,
	logger *slog.Logger,
) *BalanceAssertor {
	if assertionRepo == nil {
		panic("balance assertor: assertionRepo is required")
	}
	if pkClient == nil {
		panic("balance assertor: pkClient is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BalanceAssertor{
		assertionRepo: assertionRepo,
		trendRepo:     trendRepo,
		pkClient:      pkClient,
		faClient:      faClient,
		publisher:     publisher,
		logger:        logger,
	}
}

// ExecuteBalanceAssertion performs a balance assertion check.
// On PK client failure, it returns BOTH a result (with FAILED assertion) and an error.
// This allows callers to access the persisted assertion even when PK is unreachable.
func (ba *BalanceAssertor) ExecuteBalanceAssertion(ctx context.Context, req AssertBalanceRequest) (*AssertBalanceResult, error) {
	// Validate scope
	if !req.Scope.IsValid() {
		req.Scope = domain.AssertionScopePositionLedger
	}

	// NOSTRO_VOSTRO is not yet implemented
	if req.Scope == domain.AssertionScopeNostroVostro {
		return nil, domain.ErrUnimplemented
	}

	// Authorization: CROSS_ACCOUNT requires SYSTEM or AUDITOR role
	if req.Scope == domain.AssertionScopeCrossAccount {
		if req.CallerRole != CallerRoleSystem && req.CallerRole != CallerRoleAuditor {
			ba.logger.Warn("unauthorized cross-account assertion attempt",
				"caller_role", req.CallerRole,
				"instrument_code", req.InstrumentCode)
			return nil, domain.ErrUnauthorized
		}
	}

	// Create the assertion domain entity
	assertion, err := domain.NewBalanceAssertion(
		req.RunID,
		req.AccountID,
		req.InstrumentCode,
		req.Expression,
		req.ExpectedBalance,
	)
	if err != nil {
		return nil, fmt.Errorf("creating balance assertion: %w", err)
	}

	// Persist the pending assertion
	if err := ba.assertionRepo.Create(ctx, assertion); err != nil {
		return nil, fmt.Errorf("persisting assertion: %w", err)
	}

	// Query Position Keeping for aggregated debits/credits
	summary, err := ba.pkClient.GetPositionSummary(ctx, req.AccountID, req.InstrumentCode)
	if err != nil {
		failReason := fmt.Sprintf("failed to query position keeping: %v", err)
		if failErr := assertion.Fail(decimal.Zero, failReason); failErr != nil {
			ba.logger.Error("failed to mark assertion as failed", "error", failErr)
		}
		_ = ba.assertionRepo.Update(ctx, assertion)
		observability.BalanceAssertionTotal.WithLabelValues("FAILED", req.Scope.String()).Inc()
		return &AssertBalanceResult{Assertion: assertion}, fmt.Errorf("querying position keeping: %w", err)
	}

	// Calculate imbalance: total_debits - total_credits.
	// The assertion validates ledger equilibrium (debits == credits), not a user-supplied
	// expected balance. ExpectedBalance is stored for audit trail context only.
	imbalanceAmount := summary.TotalDebits.Sub(summary.TotalCredits)
	actualBalance := summary.TotalCredits.Sub(summary.TotalDebits) // net balance

	result := &AssertBalanceResult{}

	if imbalanceAmount.IsZero() {
		// BALANCED: debits == credits
		if err := assertion.Pass(actualBalance); err != nil {
			return nil, fmt.Errorf("marking assertion passed: %w", err)
		}

		observability.BalanceImbalanceGauge.WithLabelValues(req.InstrumentCode).Set(0)
		observability.BalanceAssertionTotal.WithLabelValues("PASSED", req.Scope.String()).Inc()

		// Resolve any existing trend
		ba.resolveTrend(ctx, req.InstrumentCode)
	} else {
		// IMBALANCED: P1/Critical severity - ledger integrity violation
		failureReason := fmt.Sprintf(
			"CRITICAL: Ledger imbalance detected for instrument %s: total_debits=%s, total_credits=%s, imbalance=%s",
			req.InstrumentCode,
			summary.TotalDebits.String(),
			summary.TotalCredits.String(),
			imbalanceAmount.String(),
		)

		if err := assertion.Fail(actualBalance, failureReason); err != nil {
			return nil, fmt.Errorf("marking assertion failed: %w", err)
		}

		// Set imbalance gauge (absolute value)
		absImbalance, _ := imbalanceAmount.Abs().Float64()
		observability.BalanceImbalanceGauge.WithLabelValues(req.InstrumentCode).Set(absImbalance)
		observability.BalanceAssertionTotal.WithLabelValues("FAILED", req.Scope.String()).Inc()

		// Track trend and check persistence
		trend := ba.updateTrend(ctx, req.InstrumentCode, imbalanceAmount, assertion.AssertionID)

		// Gather FA diagnostics if available
		ba.enrichWithDiagnostics(ctx, assertion, req.AccountID, req.InstrumentCode)

		// Publish critical imbalance event
		event := domain.NewBalanceImbalanceDetectedEvent(
			assertion.AssertionID,
			req.InstrumentCode,
			summary.TotalDebits,
			summary.TotalCredits,
			imbalanceAmount,
			req.Scope,
			trend != nil && trend.IsPersistent(),
			ba.getTrendDays(trend),
		)

		if ba.publisher != nil {
			if pubErr := ba.publisher.PublishBalanceImbalanceDetected(ctx, event); pubErr != nil {
				ba.logger.Error("failed to publish imbalance event",
					"error", pubErr,
					"assertion_id", assertion.AssertionID,
					"instrument_code", req.InstrumentCode)
			}
		}

		result.Event = event

		ba.logger.Error("CRITICAL: Balance imbalance detected",
			"assertion_id", assertion.AssertionID,
			"instrument_code", req.InstrumentCode,
			"total_debits", summary.TotalDebits.String(),
			"total_credits", summary.TotalCredits.String(),
			"imbalance", imbalanceAmount.String(),
			"is_persistent", trend != nil && trend.IsPersistent(),
			"severity", "P1_CRITICAL")
	}

	// Update the assertion with result
	if err := ba.assertionRepo.Update(ctx, assertion); err != nil {
		return nil, fmt.Errorf("updating assertion result: %w", err)
	}

	result.Assertion = assertion
	return result, nil
}

// updateTrend updates or creates an imbalance trend for the instrument code.
func (ba *BalanceAssertor) updateTrend(ctx context.Context, instrumentCode string, imbalanceAmount decimal.Decimal, assertionID uuid.UUID) *domain.ImbalanceTrend {
	if ba.trendRepo == nil {
		return nil
	}

	trend, err := ba.trendRepo.FindByInstrumentCode(ctx, instrumentCode)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			ba.logger.Error("failed to find imbalance trend", "error", err, "instrument_code", instrumentCode)
			return nil
		}
		// Create new trend
		trend = &domain.ImbalanceTrend{
			TrendID:         uuid.New(),
			InstrumentCode:  instrumentCode,
			FirstDetectedAt: time.Now().UTC(),
		}
	}

	trend.RecordImbalance(imbalanceAmount, assertionID)

	if err := ba.trendRepo.Upsert(ctx, trend); err != nil {
		ba.logger.Error("failed to upsert imbalance trend", "error", err, "instrument_code", instrumentCode)
		return trend
	}

	// Update persistent imbalance gauge
	observability.PersistentImbalanceGauge.WithLabelValues(instrumentCode).Set(float64(trend.ConsecutiveDays))

	if trend.IsPersistent() {
		ba.logger.Error("CRITICAL: Persistent imbalance detected",
			"instrument_code", instrumentCode,
			"consecutive_days", trend.ConsecutiveDays,
			"first_detected", trend.FirstDetectedAt,
			"severity", "P1_CRITICAL")
	}

	return trend
}

// resolveTrend resolves any active imbalance trend for the instrument code.
func (ba *BalanceAssertor) resolveTrend(ctx context.Context, instrumentCode string) {
	if ba.trendRepo == nil {
		return
	}

	trend, err := ba.trendRepo.FindByInstrumentCode(ctx, instrumentCode)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			ba.logger.Error("failed to find imbalance trend for resolution", "error", err, "instrument_code", instrumentCode)
		}
		return
	}

	trend.Resolve()
	if err := ba.trendRepo.Upsert(ctx, trend); err != nil {
		ba.logger.Error("failed to resolve imbalance trend", "error", err, "instrument_code", instrumentCode)
	}

	observability.PersistentImbalanceGauge.WithLabelValues(instrumentCode).Set(0)
}

// enrichWithDiagnostics adds FA diagnostic details to the assertion attributes.
func (ba *BalanceAssertor) enrichWithDiagnostics(ctx context.Context, assertion *domain.BalanceAssertion, accountID, instrumentCode string) {
	if ba.faClient == nil {
		return
	}

	detail, err := ba.faClient.GetDiagnosticDetail(ctx, accountID, instrumentCode)
	if err != nil {
		ba.logger.Warn("failed to retrieve FA diagnostics",
			"error", err,
			"account_id", accountID,
			"instrument_code", instrumentCode)
		return
	}

	if assertion.Attributes == nil {
		assertion.Attributes = make(map[string]string)
	}
	assertion.Attributes["fa_diagnostic_message"] = detail.Message
}

// getTrendDays safely extracts consecutive days from a trend that may be nil.
func (ba *BalanceAssertor) getTrendDays(trend *domain.ImbalanceTrend) int {
	if trend == nil {
		return 0
	}
	return trend.ConsecutiveDays
}
