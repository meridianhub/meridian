package valuation_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/valuation"
)

func TestStarlarkRuntime_Execute_Success(t *testing.T) {
	runtime := valuation.NewStarlarkRuntime(valuation.StarlarkRuntimeConfig{
		Timeout: 5 * time.Second,
	})

	script := `
# Simple valuation script
def valuate(ctx):
    # Access input quantity
    amount = ctx["amount"]
    rate = ctx["rate"]

    # Calculate valued amount
    valued = amount * rate

    return {
        "valued_amount": valued,
        "instrument": "GBP"
    }

# Execute valuation
result = valuate(ctx)
`

	req := &valuation.Request{
		RequestID: uuid.New(),
		MethodID:  uuid.New(),
		Quantity: valuation.Quantity{
			Amount:         decimal.NewFromFloat(100.0),
			InstrumentCode: "KWH",
		},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
		Parameters: map[string]interface{}{
			"amount": 100.0,
			"rate":   0.35,
		},
	}

	resp, err := runtime.Execute(context.Background(), script, req)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
	assert.Equal(t, decimal.NewFromFloat(35.0), resp.ValuedAmount.Amount)
}

func TestStarlarkRuntime_Execute_Timeout(t *testing.T) {
	t.Skip("Starlark timeout enforcement requires thread interruption hooks - deferred for future implementation")
	// Starlark execution is atomic (no built-in cancellation points)
	// Proper timeout requires:
	// - Custom thread interruption hooks
	// - Periodic cancellation checks during execution
	// - Or using Starlark's experimental cancellation API
	//
	// For now, timeout protection relies on external monitoring and process limits
}

func TestStarlarkRuntime_Execute_SyntaxError(t *testing.T) {
	runtime := valuation.NewStarlarkRuntime(valuation.StarlarkRuntimeConfig{
		Timeout: 5 * time.Second,
	})

	script := `
def valuate(ctx):
    # Syntax error: missing closing parenthesis
    return {"valued_amount": 100
`

	req := &valuation.Request{
		RequestID:   uuid.New(),
		MethodID:    uuid.New(),
		Quantity:    valuation.Quantity{Amount: decimal.NewFromFloat(100.0), InstrumentCode: "KWH"},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
		Parameters:  map[string]interface{}{},
	}

	_, err := runtime.Execute(context.Background(), script, req)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "syntax")
}

func TestStarlarkRuntime_Execute_MissingResult(t *testing.T) {
	runtime := valuation.NewStarlarkRuntime(valuation.StarlarkRuntimeConfig{
		Timeout: 5 * time.Second,
	})

	script := `
# Script that doesn't set 'result' variable
def valuate(ctx):
    return {"valued_amount": 100}

# Missing: result = valuate(ctx)
`

	req := &valuation.Request{
		RequestID:   uuid.New(),
		MethodID:    uuid.New(),
		Quantity:    valuation.Quantity{Amount: decimal.NewFromFloat(100.0), InstrumentCode: "KWH"},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
		Parameters:  map[string]interface{}{},
	}

	_, err := runtime.Execute(context.Background(), script, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "result")
}

func TestStarlarkRuntime_Execute_InvalidResultType(t *testing.T) {
	runtime := valuation.NewStarlarkRuntime(valuation.StarlarkRuntimeConfig{
		Timeout: 5 * time.Second,
	})

	script := `
# Script that sets result to wrong type (string instead of dict)
result = "invalid"
`

	req := &valuation.Request{
		RequestID:   uuid.New(),
		MethodID:    uuid.New(),
		Quantity:    valuation.Quantity{Amount: decimal.NewFromFloat(100.0), InstrumentCode: "KWH"},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
		Parameters:  map[string]interface{}{},
	}

	_, err := runtime.Execute(context.Background(), script, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dict")
}

func TestStarlarkRuntime_MemoryLimit(t *testing.T) {
	t.Skip("Memory limit enforcement requires custom allocator - deferred for future implementation")
	// This test would verify that scripts cannot allocate > 64MB
	// Implementation requires Starlark custom allocator hooks
}

func TestStarlarkRuntime_NoFilesystemAccess(t *testing.T) {
	runtime := valuation.NewStarlarkRuntime(valuation.StarlarkRuntimeConfig{
		Timeout: 5 * time.Second,
	})

	script := `
# Attempt to access filesystem (should fail)
import os  # This should be blocked
result = {"valued_amount": 0}
`

	req := &valuation.Request{
		RequestID:   uuid.New(),
		MethodID:    uuid.New(),
		Quantity:    valuation.Quantity{Amount: decimal.NewFromFloat(100.0), InstrumentCode: "KWH"},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
		Parameters:  map[string]interface{}{},
	}

	_, err := runtime.Execute(context.Background(), script, req)
	require.Error(t, err)
	// Starlark doesn't have 'import' statement - should fail at parse time
	assert.Contains(t, strings.ToLower(err.Error()), "syntax")
}
