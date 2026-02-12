package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubValuationEngine_IdentityValuation(t *testing.T) {
	engine := NewStubValuationEngine()

	req := &valuation.Request{
		RequestID: uuid.New(),
		MethodID:  uuid.New(),
		Quantity: valuation.Quantity{
			Amount:         decimal.NewFromFloat(100.50),
			InstrumentCode: "GBP",
		},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
	}

	resp, err := engine.Valuate(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.True(t, req.Quantity.Amount.Equal(resp.ValuedAmount.Amount),
		"stub engine should return input amount unchanged, got %s", resp.ValuedAmount.Amount)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode,
		"output instrument should match input instrument")
	assert.NotZero(t, resp.ComputedAt, "computed_at should be set")
}

func TestStubValuationEngine_PreservesInstrumentCode(t *testing.T) {
	engine := NewStubValuationEngine()

	instruments := []string{"GBP", "KWH", "USD", "TONNE_CO2E", "GPU_HOUR"}
	for _, code := range instruments {
		req := &valuation.Request{
			RequestID: uuid.New(),
			MethodID:  uuid.New(),
			Quantity: valuation.Quantity{
				Amount:         decimal.NewFromFloat(42.00),
				InstrumentCode: code,
			},
			AccountID:   uuid.New(),
			PartyID:     uuid.New(),
			KnowledgeAt: time.Now(),
		}

		resp, err := engine.Valuate(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, code, resp.ValuedAmount.InstrumentCode,
			"instrument code should be preserved for %s", code)
	}
}

func TestStubValuationEngine_VariousAmounts(t *testing.T) {
	engine := NewStubValuationEngine()

	amounts := []decimal.Decimal{
		decimal.NewFromFloat(0.001),
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(-25.50),
		decimal.NewFromFloat(999999.99),
		decimal.Zero,
	}

	for _, amount := range amounts {
		req := &valuation.Request{
			RequestID: uuid.New(),
			MethodID:  uuid.New(),
			Quantity: valuation.Quantity{
				Amount:         amount,
				InstrumentCode: "GBP",
			},
			AccountID:   uuid.New(),
			PartyID:     uuid.New(),
			KnowledgeAt: time.Now(),
		}

		resp, err := engine.Valuate(context.Background(), req)
		require.NoError(t, err)
		assert.True(t, amount.Equal(resp.ValuedAmount.Amount),
			"amount %s should pass through unchanged", amount)
	}
}

func TestStubReferenceDataProvider_GetValuationMethodID(t *testing.T) {
	provider := NewStubReferenceDataProvider()

	id, err := provider.GetValuationMethodID(context.Background(), "GBP")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id, "should return a non-nil UUID")
}

func TestStubReferenceDataProvider_GetMaterialityThreshold(t *testing.T) {
	provider := NewStubReferenceDataProvider()

	threshold, err := provider.GetMaterialityThreshold(context.Background(), "GBP")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(0.01).Equal(threshold),
		"default materiality threshold should be 0.01, got %s", threshold)
}

func TestStubValuationEngine_IntegrationWithVarianceValuator(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create a detected variance with amount 100.00 GBP
	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(100.00))
	varianceRepo.variances = []*domain.Variance{v}

	stubEngine := NewStubValuationEngine()
	stubRefData := NewStubReferenceDataProvider()

	valuator := NewVarianceValuator(stubEngine, stubRefData, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Identity valuation: ValueDelta should match the variance amount
	assert.Equal(t, domain.VarianceStatusValued, varianceRepo.variances[0].Status,
		"variance should transition DETECTED -> VALUED")
	assert.True(t, varianceRepo.variances[0].ValueDelta.Equal(v.VarianceAmount),
		"value delta should match variance amount for identity valuation")
	assert.Equal(t, "GBP", varianceRepo.variances[0].Currency)
}

func TestVarianceValuator_MaterialityAutoAccept(t *testing.T) {
	runRepo := newMockRunRepo()
	varianceRepo := &mockVarianceRepoFull{}

	run := newRunningTestRun(t)
	_ = runRepo.Create(context.Background(), run)

	// Create a variance with amount below default threshold (0.01)
	v := createDetectedVariance(t, run.RunID, "ACC-001", "GBP", decimal.NewFromFloat(0.005))
	varianceRepo.variances = []*domain.Variance{v}

	stubEngine := NewStubValuationEngine()
	stubRefData := NewStubReferenceDataProvider()

	valuator := NewVarianceValuator(stubEngine, stubRefData, varianceRepo, runRepo)
	err := valuator.ValueVariances(context.Background(), run.RunID)
	require.NoError(t, err)

	// Below materiality threshold: should be auto-accepted
	assert.Equal(t, domain.VarianceStatusAccepted, varianceRepo.variances[0].Status,
		"variance below materiality should be auto-accepted")
	assert.Contains(t, varianceRepo.variances[0].ResolutionNote, "auto-accepted")
}
