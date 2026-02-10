package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSagaHandler_ValuationAnalysis_StoredInAttributes verifies that valuation_analysis
// parameter from saga scripts is marshaled and passed to Position Keeping via attributes.
func TestSagaHandler_ValuationAnalysis_StoredInAttributes(t *testing.T) {
	// Mock Position Keeping client that captures the request
	mockPosKeeping := &mockPositionKeepingClient{}

	// Setup handler dependencies
	baseCtx := context.Background()
	baseCtx = context.WithValue(baseCtx, ContextKeyHandlerDeps, &CurrentAccountHandlerDeps{
		Logger:           testLogger(),
		PosKeepingClient: mockPosKeeping,
	})
	ctx := &saga.StarlarkContext{
		Context: baseCtx,
	}

	// Create valuation_analysis data (simulating what a saga script would pass)
	valuationAnalysis := map[string]interface{}{
		"method_id":        "method-abc-123",
		"method_version":   "2.1.0",
		"degraded_mode":    false,
		"applied_rates":    map[string]interface{}{"fx_rate": "1.25", "spread": "0.02"},
		"knowledge_at":     "2026-02-07T12:00:00Z",
		"computed_at":      "2026-02-07T12:00:01Z",
		"calculation_path": []interface{}{"fetch_market_data", "apply_spread", "round"},
	}

	// Call handler with valuation_analysis
	params := map[string]any{
		"position_id":        "test-account-123",
		"amount":             decimal.NewFromFloat(100.50),
		"currency":           "GBP",
		"direction":          "DEBIT",
		"transaction_id":     uuid.New().String(),
		"valuation_analysis": valuationAnalysis,
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)

	// Assertions
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, mockPosKeeping.lastInitiateRequest, "Position Keeping should have been called")

	capturedEntry := mockPosKeeping.lastInitiateRequest.InitialEntry
	require.NotNil(t, capturedEntry, "InitialEntry should be present")

	// Verify attributes were passed
	require.NotNil(t, capturedEntry.Attributes, "Attributes should be present")
	require.Contains(t, capturedEntry.Attributes, "valuation_analysis", "Should contain valuation_analysis key")

	// Verify JSON structure
	valuationJSON := capturedEntry.Attributes["valuation_analysis"]
	require.NotEmpty(t, valuationJSON)

	// Parse and verify the stored valuation_analysis
	var stored map[string]interface{}
	unmarshalErr := json.Unmarshal([]byte(valuationJSON), &stored)
	require.NoError(t, unmarshalErr, "Should be valid JSON")

	assert.Equal(t, "method-abc-123", stored["method_id"])
	assert.Equal(t, "2.1.0", stored["method_version"])
	assert.Equal(t, false, stored["degraded_mode"])

	// Verify nested structures
	appliedRates, ok := stored["applied_rates"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "1.25", appliedRates["fx_rate"])
	assert.Equal(t, "0.02", appliedRates["spread"])

	calcPath, ok := stored["calculation_path"].([]interface{})
	require.True(t, ok)
	assert.Len(t, calcPath, 3)
	assert.Equal(t, "fetch_market_data", calcPath[0])
}

// TestSagaHandler_ValuationAnalysis_DegradedMode verifies degraded_mode flag is preserved.
func TestSagaHandler_ValuationAnalysis_DegradedMode(t *testing.T) {
	mockPosKeeping := &mockPositionKeepingClient{}

	baseCtx := context.Background()
	baseCtx = context.WithValue(baseCtx, ContextKeyHandlerDeps, &CurrentAccountHandlerDeps{
		Logger:           testLogger(),
		PosKeepingClient: mockPosKeeping,
	})
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Degraded valuation (using stale market data)
	valuationAnalysis := map[string]interface{}{
		"method_id":       "method-xyz-789",
		"degraded_mode":   true,
		"degraded_reason": "market data unavailable, using 24h cached rates",
	}

	params := map[string]any{
		"position_id":        "test-account-456",
		"amount":             decimal.NewFromFloat(50.00),
		"currency":           "GBP",
		"direction":          "CREDIT",
		"transaction_id":     uuid.New().String(),
		"valuation_analysis": valuationAnalysis,
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, mockPosKeeping.lastInitiateRequest)

	capturedEntry := mockPosKeeping.lastInitiateRequest.InitialEntry
	require.NotNil(t, capturedEntry)
	require.NotNil(t, capturedEntry.Attributes)

	valuationJSON := capturedEntry.Attributes["valuation_analysis"]
	var stored map[string]interface{}
	unmarshalErr := json.Unmarshal([]byte(valuationJSON), &stored)
	require.NoError(t, unmarshalErr)

	// Verify degraded mode flags
	assert.Equal(t, true, stored["degraded_mode"])
	assert.Equal(t, "market data unavailable, using 24h cached rates", stored["degraded_reason"])
}

// TestSagaHandler_ValuationAnalysis_BackwardCompatibility ensures handler works without valuation_analysis.
func TestSagaHandler_ValuationAnalysis_BackwardCompatibility(t *testing.T) {
	mockPosKeeping := &mockPositionKeepingClient{}

	baseCtx := context.Background()
	baseCtx = context.WithValue(baseCtx, ContextKeyHandlerDeps, &CurrentAccountHandlerDeps{
		Logger:           testLogger(),
		PosKeepingClient: mockPosKeeping,
	})
	ctx := &saga.StarlarkContext{Context: baseCtx}

	// Call WITHOUT valuation_analysis (backward compatibility)
	params := map[string]any{
		"position_id":    "test-account-789",
		"amount":         decimal.NewFromFloat(25.00),
		"currency":       "GBP",
		"direction":      "DEBIT",
		"transaction_id": uuid.New().String(),
		// NO valuation_analysis parameter
	}

	result, err := currentAccountPositionKeepingInitiateLog(ctx, params)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, mockPosKeeping.lastInitiateRequest)

	capturedEntry := mockPosKeeping.lastInitiateRequest.InitialEntry
	require.NotNil(t, capturedEntry)

	// Attributes should be nil (not present)
	assert.Nil(t, capturedEntry.Attributes, "Attributes should be nil when valuation_analysis not provided")
}
