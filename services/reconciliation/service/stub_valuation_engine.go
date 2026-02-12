package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/shopspring/decimal"
)

// TODO: Replace StubValuationEngine and StubReferenceDataProvider with real
// implementations when shared/pkg/valuation has a concrete Engine and the
// Reference Data service exposes a gRPC client for valuation method lookups.

// StubValuationEngine is a minimal valuation.Engine that performs identity
// valuation: it returns the input amount unchanged in the same instrument.
// This unblocks VarianceValuator wiring without waiting for the full
// valuation service implementation.
type StubValuationEngine struct{}

// NewStubValuationEngine creates a new StubValuationEngine.
func NewStubValuationEngine() *StubValuationEngine {
	return &StubValuationEngine{}
}

// Valuate returns the input quantity as-is (identity valuation).
func (e *StubValuationEngine) Valuate(_ context.Context, req *valuation.Request) (*valuation.Response, error) {
	return &valuation.Response{
		ValuedAmount: valuation.Quantity{
			Amount:         req.Quantity.Amount,
			InstrumentCode: req.Quantity.InstrumentCode,
		},
		ComputedAt: time.Now(),
	}, nil
}

// StubReferenceDataProvider is a minimal ReferenceDataProvider that returns
// stub values: a random method ID and a default materiality threshold of 0.01.
type StubReferenceDataProvider struct{}

// NewStubReferenceDataProvider creates a new StubReferenceDataProvider.
func NewStubReferenceDataProvider() *StubReferenceDataProvider {
	return &StubReferenceDataProvider{}
}

// GetValuationMethodID returns a new random UUID as the valuation method ID.
func (p *StubReferenceDataProvider) GetValuationMethodID(_ context.Context, _ string) (uuid.UUID, error) {
	return uuid.New(), nil
}

// GetMaterialityThreshold returns 0.01 as the default materiality threshold.
func (p *StubReferenceDataProvider) GetMaterialityThreshold(_ context.Context, _ string) (decimal.Decimal, error) {
	return decimal.NewFromFloat(0.01), nil
}
