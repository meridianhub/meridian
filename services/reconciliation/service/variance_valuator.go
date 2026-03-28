package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

// ErrAllValuationsFailed is returned when every variance in a run fails valuation.
var ErrAllValuationsFailed = errors.New("all variance valuations failed")

const (
	// defaultValuationConcurrency limits concurrent in-process valuation calls.
	defaultValuationConcurrency = 10

	// valuationTimeout is the maximum time allowed for the entire valuation phase.
	valuationTimeout = 5 * time.Minute
)

// ReferenceDataProvider abstracts fetching valuation configuration from Reference Data.
type ReferenceDataProvider interface {
	// GetValuationMethodID returns the valuation method ID for the given instrument code.
	GetValuationMethodID(ctx context.Context, instrumentCode string) (uuid.UUID, error)

	// GetMaterialityThreshold returns the minimum variance amount that requires action
	// for the given instrument code. Variances below this threshold are auto-accepted.
	GetMaterialityThreshold(ctx context.Context, instrumentCode string) (decimal.Decimal, error)
}

// VarianceValuator values detected variances using the shared valuation engine.
// It calls the valuation engine in-process (not via gRPC) to convert quantity deltas
// into monetary value deltas in settlement currency.
type VarianceValuator struct {
	engine        valuation.Engine
	refData       ReferenceDataProvider
	partyResolver AccountPartyResolver
	varianceRepo  domain.VarianceRepository
	runRepo       domain.SettlementRunRepository
	concurrency   int
}

// NewVarianceValuator creates a new VarianceValuator.
func NewVarianceValuator(
	engine valuation.Engine,
	refData ReferenceDataProvider,
	partyResolver AccountPartyResolver,
	varianceRepo domain.VarianceRepository,
	runRepo domain.SettlementRunRepository,
) *VarianceValuator {
	return &VarianceValuator{
		engine:        engine,
		refData:       refData,
		partyResolver: partyResolver,
		varianceRepo:  varianceRepo,
		runRepo:       runRepo,
		concurrency:   defaultValuationConcurrency,
	}
}

// ValueVariances values all DETECTED variances for a settlement run.
//
// For each detected variance it:
//  1. Looks up the valuation method from Reference Data
//  2. Calls the valuation engine in-process to compute value_delta
//  3. Applies materiality filtering (auto-accepts below threshold)
//  4. Updates the variance status to VALUED
//
// Variances are valued concurrently with bounded parallelism.
func (vv *VarianceValuator) ValueVariances(ctx context.Context, runID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, valuationTimeout)
	defer cancel()

	detected, err := vv.fetchDetectedVariances(ctx, runID)
	if err != nil {
		return err
	}

	if len(detected) == 0 {
		slog.InfoContext(ctx, "no detected variances to value", "run_id", runID)
		return nil
	}

	slog.InfoContext(ctx, "starting variance valuation",
		"run_id", runID,
		"variance_count", len(detected),
	)

	partyIDs := vv.resolvePartyIDs(ctx, detected)

	valuedCount, failedCount, totalValue, err := vv.valueConcurrently(ctx, detected, partyIDs)
	if err != nil {
		return fmt.Errorf("variance valuation failed: %w", err)
	}

	if failedCount > 0 && valuedCount == 0 {
		return fmt.Errorf("%d variances: %w", failedCount, ErrAllValuationsFailed)
	}

	if err := vv.updateRunSummary(ctx, runID, valuedCount); err != nil {
		return err
	}

	slog.InfoContext(ctx, "variance valuation completed",
		"run_id", runID,
		"valued_count", valuedCount,
		"total_value", totalValue.String(),
	)

	return nil
}

// fetchDetectedVariances retrieves variances for a run and filters to DETECTED status.
func (vv *VarianceValuator) fetchDetectedVariances(ctx context.Context, runID uuid.UUID) ([]*domain.Variance, error) {
	variances, err := vv.varianceRepo.FindByRunID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch variances for run %s: %w", runID, err)
	}

	detected := make([]*domain.Variance, 0, len(variances))
	for _, v := range variances {
		if v.Status == domain.VarianceStatusDetected {
			detected = append(detected, v)
		}
	}

	return detected, nil
}

// resolvePartyIDs pre-resolves party IDs for all unique accounts to avoid N+1 lookups.
func (vv *VarianceValuator) resolvePartyIDs(ctx context.Context, detected []*domain.Variance) map[string]uuid.UUID {
	partyIDs := make(map[string]uuid.UUID)
	for _, v := range detected {
		if _, ok := partyIDs[v.AccountID]; !ok {
			pid, pidErr := vv.resolvePartyID(ctx, v.AccountID)
			if pidErr != nil {
				slog.WarnContext(ctx, "failed to resolve party for account, falling back to account ID",
					"account_id", v.AccountID,
					"error", pidErr,
				)
				pid = uuidFromString(v.AccountID)
			}
			partyIDs[v.AccountID] = pid
		}
	}
	return partyIDs
}

// valueConcurrently values all detected variances with bounded parallelism.
func (vv *VarianceValuator) valueConcurrently(ctx context.Context, detected []*domain.Variance, partyIDs map[string]uuid.UUID) (int, int, decimal.Decimal, error) {
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(vv.concurrency)

	var (
		mu          sync.Mutex
		valuedCount int
		failedCount int
		totalValue  decimal.Decimal
	)

	for _, v := range detected {
		v := v // capture for closure
		partyID := partyIDs[v.AccountID]
		g.Go(func() error {
			valued, valueDelta, err := vv.valueVariance(gCtx, v, partyID)
			if err != nil {
				slog.WarnContext(gCtx, "failed to value variance",
					"variance_id", v.VarianceID,
					"error", err,
				)
				mu.Lock()
				failedCount++
				mu.Unlock()
				return nil
			}

			if err := vv.varianceRepo.Update(gCtx, valued); err != nil {
				return fmt.Errorf("failed to update variance %s: %w", v.VarianceID, err)
			}

			mu.Lock()
			valuedCount++
			totalValue = totalValue.Add(valueDelta.Abs())
			mu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return valuedCount, failedCount, totalValue, err
	}

	return valuedCount, failedCount, totalValue, nil
}

// updateRunSummary updates the run's variance count after valuation completes.
func (vv *VarianceValuator) updateRunSummary(ctx context.Context, runID uuid.UUID, valuedCount int) error {
	run, err := vv.runRepo.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to find run for summary update: %w", err)
	}
	run.SetVarianceCount(valuedCount)
	if err := vv.runRepo.Update(ctx, run); err != nil {
		return fmt.Errorf("failed to update run variance summary: %w", err)
	}
	return nil
}

// valueVariance values a single variance using the in-process valuation engine.
// partyID is pre-resolved by the caller to avoid N+1 gRPC lookups.
func (vv *VarianceValuator) valueVariance(ctx context.Context, v *domain.Variance, partyID uuid.UUID) (*domain.Variance, decimal.Decimal, error) {
	// Look up the valuation method for this instrument
	methodID, err := vv.refData.GetValuationMethodID(ctx, v.InstrumentCode)
	if err != nil {
		return nil, decimal.Zero, fmt.Errorf("failed to get valuation method for %s: %w", v.InstrumentCode, err)
	}

	// Build valuation request
	req := &valuation.Request{
		RequestID: uuid.New(),
		MethodID:  methodID,
		Quantity: valuation.Quantity{
			Amount:         v.VarianceAmount,
			InstrumentCode: v.InstrumentCode,
			Attributes:     v.Attributes,
		},
		AccountID:   uuidFromString(v.AccountID),
		PartyID:     partyID,
		KnowledgeAt: v.CreatedAt,
	}

	resp, err := vv.engine.Valuate(ctx, req)
	if err != nil {
		return nil, decimal.Zero, fmt.Errorf("valuation failed: %w", err)
	}

	valueDelta := resp.ValuedAmount.Amount

	// Check materiality threshold in the settlement currency (output instrument)
	settlementCurrency := resp.ValuedAmount.InstrumentCode
	threshold, err := vv.refData.GetMaterialityThreshold(ctx, settlementCurrency)
	if err != nil {
		slog.WarnContext(ctx, "failed to get materiality threshold, skipping filter",
			"settlement_currency", settlementCurrency,
			"error", err,
		)
	} else if valueDelta.Abs().LessThan(threshold) {
		// Below materiality: auto-accept via domain transition
		v.ValueDelta = valueDelta
		v.Currency = resp.ValuedAmount.InstrumentCode
		note := fmt.Sprintf("auto-accepted: value delta %s below materiality threshold %s",
			valueDelta.Abs().String(), threshold.String())
		if err := v.Accept(note, "system:materiality-filter"); err != nil {
			return nil, decimal.Zero, fmt.Errorf("failed to auto-accept variance: %w", err)
		}
		return v, valueDelta, nil
	}

	// Above materiality: mark as VALUED via domain transition
	if err := v.Value(valueDelta, resp.ValuedAmount.InstrumentCode); err != nil {
		return nil, decimal.Zero, fmt.Errorf("failed to transition variance to VALUED: %w", err)
	}

	return v, valueDelta, nil
}

// resolvePartyID resolves the owning party ID for an account.
// If no resolver is configured, it falls back to parsing the account ID as a UUID.
func (vv *VarianceValuator) resolvePartyID(ctx context.Context, accountID string) (uuid.UUID, error) {
	if vv.partyResolver == nil {
		return uuidFromString(accountID), nil
	}
	return vv.partyResolver.ResolvePartyID(ctx, accountID)
}

// uuidFromString tries to parse a string as UUID, returns uuid.Nil on failure.
func uuidFromString(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}
