package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock ValuationEngine ---

type mockValuationEngine struct {
	responses map[uuid.UUID]*valuation.Response
	err       error
	calls     atomic.Int32
}

func (m *mockValuationEngine) Valuate(_ context.Context, req *valuation.Request) (*valuation.Response, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.responses[req.MethodID]; ok {
		return resp, nil
	}
	return &valuation.Response{
		ValuedAmount: valuation.Quantity{
			Amount:         req.Quantity.Amount.Mul(decimal.NewFromFloat(0.10)),
			InstrumentCode: "GBP",
		},
		ComputedAt: time.Now(),
	}, nil
}

// --- Mock ReferenceDataProvider ---

type mockRefData struct {
	methodIDs  map[string]uuid.UUID
	thresholds map[string]decimal.Decimal
	methodErr  error
	threshErr  error
}

func (m *mockRefData) GetValuationMethodID(_ context.Context, instrumentCode string) (uuid.UUID, error) {
	if m.methodErr != nil {
		return uuid.Nil, m.methodErr
	}
	id, ok := m.methodIDs[instrumentCode]
	if !ok {
		return uuid.New(), nil
	}
	return id, nil
}

func (m *mockRefData) GetMaterialityThreshold(_ context.Context, instrumentCode string) (decimal.Decimal, error) {
	if m.threshErr != nil {
		return decimal.Zero, m.threshErr
	}
	thresh, ok := m.thresholds[instrumentCode]
	if !ok {
		return decimal.NewFromFloat(0.01), nil
	}
	return thresh, nil
}

// --- Helper to create detected variances ---

func createDetectedVariance(t *testing.T, runID uuid.UUID, accountID, instrumentCode string, amount decimal.Decimal) *domain.Variance {
	t.Helper()
	v, err := domain.NewVariance(
		runID, uuid.New(), accountID, instrumentCode,
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1000.00).Add(amount),
		domain.VarianceReasonAmountMismatch,
	)
	require.NoError(t, err)
	return v
}

// --- Tests ---

func TestValueVariances_SuccessfulValuation(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create detected variances
	v1 := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	v2 := createDetectedVariance(t, run.RunID, "ACC-002", "KWH", decimal.NewFromFloat(-25.00))
	varianceRepo.variances = []*domain.Variance{v1, v2}

	engine := &mockValuationEngine{}
	refData := &mockRefData{
		thresholds: map[string]decimal.Decimal{
			// Threshold keyed by settlement currency (engine returns GBP for all)
			"GBP": decimal.NewFromFloat(0.01),
		},
	}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Equal(t, int32(2), engine.calls.Load())

	// Verify variances were updated to VALUED
	for _, v := range varianceRepo.variances {
		assert.Equal(t, domain.VarianceStatusValued, v.Status,
			"variance %s should be VALUED", v.VarianceID)
		assert.False(t, v.ValueDelta.IsZero(), "value delta should be non-zero")
		assert.NotEmpty(t, v.Currency, "currency should be set")
	}
}

func TestValueVariances_MaterialityFiltering(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create a variance with very small amount
	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(0.001))
	varianceRepo.variances = []*domain.Variance{v}

	engine := &mockValuationEngine{
		responses: map[uuid.UUID]*valuation.Response{},
	}
	refData := &mockRefData{
		thresholds: map[string]decimal.Decimal{
			"GBP": decimal.NewFromFloat(1.00), // Threshold higher than variance value
		},
	}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Variance should be auto-accepted due to materiality
	assert.Equal(t, domain.VarianceStatusAccepted, varianceRepo.variances[0].Status)
	assert.Contains(t, varianceRepo.variances[0].ResolutionNote, "auto-accepted")
	assert.NotNil(t, varianceRepo.variances[0].ResolvedAt)
}

func TestValueVariances_NoDetectedVariances(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create variance in non-DETECTED state
	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	v.Status = domain.VarianceStatusOpen // already opened
	varianceRepo.variances = []*domain.Variance{v}

	engine := &mockValuationEngine{}
	refData := &mockRefData{}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Engine should not have been called
	assert.Equal(t, int32(0), engine.calls.Load())
}

func TestValueVariances_ValuationEngineError(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	varianceRepo.variances = []*domain.Variance{v}

	engine := &mockValuationEngine{err: errors.New("starlark timeout")}
	refData := &mockRefData{}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	// When all variances fail valuation, the method returns an error
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAllValuationsFailed)

	// Variance should remain in DETECTED state since valuation failed
	assert.Equal(t, domain.VarianceStatusDetected, varianceRepo.variances[0].Status)
}

func TestValueVariances_ReferenceDataError(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	varianceRepo.variances = []*domain.Variance{v}

	engine := &mockValuationEngine{}
	refData := &mockRefData{methodErr: errors.New("reference data unavailable")}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	// When all variances fail valuation, the method returns an error
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAllValuationsFailed)
}

func TestValueVariances_ThresholdError_StillValues(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	varianceRepo.variances = []*domain.Variance{v}

	engine := &mockValuationEngine{}
	refData := &mockRefData{threshErr: errors.New("threshold lookup failed")}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Should still be valued (threshold error is non-fatal, skips filtering)
	assert.Equal(t, domain.VarianceStatusValued, varianceRepo.variances[0].Status)
}

func TestValueVariances_UpdatesRunSummary(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	v1 := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	v2 := createDetectedVariance(t, run.RunID, "ACC-002", "GBP", decimal.NewFromFloat(75.00))
	varianceRepo.variances = []*domain.Variance{v1, v2}

	engine := &mockValuationEngine{}
	refData := &mockRefData{
		thresholds: map[string]decimal.Decimal{
			"GBP": decimal.NewFromFloat(0.01),
		},
	}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Verify run summary was updated
	updatedRun, err := runRepo.FindByID(context.Background(), run.RunID)
	require.NoError(t, err)
	assert.Equal(t, 2, updatedRun.VarianceCount)
}

func TestValueVariances_ConcurrentValuation(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create many variances to test concurrent processing
	for i := 0; i < 20; i++ {
		v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(float64(i+1)))
		varianceRepo.variances = append(varianceRepo.variances, v)
	}

	engine := &mockValuationEngine{}
	refData := &mockRefData{
		thresholds: map[string]decimal.Decimal{
			"GBP": decimal.NewFromFloat(0.01),
		},
	}

	valuator := NewVarianceValuator(engine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Equal(t, int32(20), engine.calls.Load())

	// All should be valued
	for _, v := range varianceRepo.variances {
		assert.Equal(t, domain.VarianceStatusValued, v.Status)
	}
}

// --- Mock AccountPartyResolver ---

type mockPartyResolver struct {
	partyIDs map[string]uuid.UUID
	err      error
}

func (m *mockPartyResolver) ResolvePartyID(_ context.Context, accountID string) (uuid.UUID, error) {
	if m.err != nil {
		return uuid.Nil, m.err
	}
	if id, ok := m.partyIDs[accountID]; ok {
		return id, nil
	}
	return uuid.Nil, errors.New("account not found")
}

func TestValueVariances_UsesPartyResolver(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	partyID := uuid.New()
	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	varianceRepo.variances = []*domain.Variance{v}

	var capturedPartyID uuid.UUID
	engine := &mockValuationEngine{
		responses: map[uuid.UUID]*valuation.Response{},
	}
	// Override to capture the request
	capturingEngine := &partyCapturingEngine{delegate: engine, captured: &capturedPartyID}

	resolver := &mockPartyResolver{
		partyIDs: map[string]uuid.UUID{
			"ACC-001": partyID,
		},
	}
	refData := &mockRefData{
		thresholds: map[string]decimal.Decimal{
			"GBP": decimal.NewFromFloat(0.01),
		},
	}

	valuator := NewVarianceValuator(capturingEngine, refData, resolver, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	assert.Equal(t, partyID, capturedPartyID, "valuator should use resolved party ID")
}

func TestValueVariances_PartyResolverError_FallsBackToAccountID(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	varianceRepo.variances = []*domain.Variance{v}

	var capturedPartyID uuid.UUID
	engine := &mockValuationEngine{}
	capturingEngine := &partyCapturingEngine{delegate: engine, captured: &capturedPartyID}

	resolver := &mockPartyResolver{err: errors.New("service unavailable")}
	refData := &mockRefData{
		thresholds: map[string]decimal.Decimal{
			"GBP": decimal.NewFromFloat(0.01),
		},
	}

	valuator := NewVarianceValuator(capturingEngine, refData, resolver, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Should fall back to uuidFromString("ACC-001") which is uuid.Nil
	assert.Equal(t, uuid.Nil, capturedPartyID, "should fall back to account ID parse on resolver error")
}

func TestValueVariances_NilPartyResolver_FallsBackToAccountID(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(50.00))
	varianceRepo.variances = []*domain.Variance{v}

	var capturedPartyID uuid.UUID
	engine := &mockValuationEngine{}
	capturingEngine := &partyCapturingEngine{delegate: engine, captured: &capturedPartyID}

	refData := &mockRefData{
		thresholds: map[string]decimal.Decimal{
			"GBP": decimal.NewFromFloat(0.01),
		},
	}

	valuator := NewVarianceValuator(capturingEngine, refData, nil, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// nil resolver falls back to uuidFromString("ACC-001") = uuid.Nil
	assert.Equal(t, uuid.Nil, capturedPartyID, "nil resolver should fall back to account ID parse")
}

// partyCapturingEngine wraps a valuation engine and captures the PartyID from requests.
type partyCapturingEngine struct {
	delegate valuation.Engine
	captured *uuid.UUID
}

func (e *partyCapturingEngine) Valuate(ctx context.Context, req *valuation.Request) (*valuation.Response, error) {
	*e.captured = req.PartyID
	return e.delegate.Valuate(ctx, req)
}
