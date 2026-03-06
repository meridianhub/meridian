package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func valuationTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestValuationEngineAdapter_Valuate_Success(t *testing.T) {
	inner := &mockValuationEngine{}
	adapter := NewValuationEngineAdapter(inner, valuationTestLogger())

	req := &valuation.Request{
		RequestID: uuid.New(),
		MethodID:  uuid.New(),
		Quantity: valuation.Quantity{
			Amount:         decimal.NewFromFloat(100.00),
			InstrumentCode: "GBP",
		},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
	}

	resp, err := adapter.Valuate(context.Background(), req)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
}

func TestValuationEngineAdapter_Valuate_Error(t *testing.T) {
	inner := &mockValuationEngine{err: errors.New("starlark timeout")}
	adapter := NewValuationEngineAdapter(inner, valuationTestLogger())

	req := &valuation.Request{
		RequestID: uuid.New(),
		MethodID:  uuid.New(),
		Quantity: valuation.Quantity{
			Amount:         decimal.NewFromFloat(100.00),
			InstrumentCode: "GBP",
		},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
	}

	resp, err := adapter.Valuate(context.Background(), req)
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valuation engine error")
}
