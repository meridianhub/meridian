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

// requestWithParams builds a valid Request whose Parameters map is exposed to the
// script via ctx, allowing the various Go->Starlark conversions to be exercised.
func requestWithParams(params map[string]interface{}) *valuation.Request {
	return &valuation.Request{
		RequestID:   uuid.New(),
		MethodID:    uuid.New(),
		Quantity:    valuation.Quantity{Amount: decimal.NewFromInt(1), InstrumentCode: "KWH"},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
		Parameters:  params,
	}
}

// TestStarlarkRuntime_ValuedAmountFromString verifies that a string valued_amount
// is parsed into a decimal (exercises the string branch of parseResult).
func TestStarlarkRuntime_ValuedAmountFromString(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `result = {"valued_amount": "123.45", "instrument": "GBP"}`
	resp, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(123.45).Equal(resp.ValuedAmount.Amount),
		"expected 123.45, got %s", resp.ValuedAmount.Amount)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
}

// TestStarlarkRuntime_ValuedAmountFromInt verifies that an integer valued_amount
// is converted via the int64 branch of parseResult.
func TestStarlarkRuntime_ValuedAmountFromInt(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `result = {"valued_amount": 42, "instrument": "GBP"}`
	resp, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(42).Equal(resp.ValuedAmount.Amount),
		"expected 42, got %s", resp.ValuedAmount.Amount)
}

// TestStarlarkRuntime_ValuedAmountInvalidString verifies that a non-numeric string
// valued_amount yields ErrStarlarkInvalidResult.
func TestStarlarkRuntime_ValuedAmountInvalidString(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `result = {"valued_amount": "not-a-number", "instrument": "GBP"}`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, valuation.ErrStarlarkInvalidResult)
	assert.Contains(t, strings.ToLower(err.Error()), "valued_amount")
}

// TestStarlarkRuntime_MissingValuedAmount verifies that a result dict without a
// valued_amount field is rejected.
func TestStarlarkRuntime_MissingValuedAmount(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `result = {"instrument": "GBP"}`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, valuation.ErrStarlarkInvalidResult)
	assert.Contains(t, err.Error(), "valued_amount")
}

// TestStarlarkRuntime_ValuedAmountWrongType verifies that a non-numeric, non-string
// valued_amount (e.g. a bool) is rejected via the default branch of parseResult.
func TestStarlarkRuntime_ValuedAmountWrongType(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `result = {"valued_amount": True, "instrument": "GBP"}`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, valuation.ErrStarlarkInvalidResult)
	assert.Contains(t, strings.ToLower(err.Error()), "numeric")
}

// TestStarlarkRuntime_MissingInstrument verifies that a result without a valid
// instrument string is rejected.
func TestStarlarkRuntime_MissingInstrument(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `result = {"valued_amount": 10}`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, valuation.ErrStarlarkInvalidResult)
	assert.Contains(t, err.Error(), "instrument")
}

// TestStarlarkRuntime_InstrumentWrongType verifies that a non-string instrument
// value is rejected.
func TestStarlarkRuntime_InstrumentWrongType(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `result = {"valued_amount": 10, "instrument": 99}`
	_, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, valuation.ErrStarlarkInvalidResult)
	assert.Contains(t, err.Error(), "instrument")
}

// TestStarlarkRuntime_RecordPathPopulatesAnalysis exercises extractPathEntries by
// recording calculation steps that must surface on the Response.Analysis.
func TestStarlarkRuntime_RecordPathPopulatesAnalysis(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `
record_path("step one", {"price": 45.5, "currency": "GBP"})
record_path("step two", {"factor": 2})
result = {"valued_amount": 91, "instrument": "GBP"}
`
	resp, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.NoError(t, err)
	require.NotNil(t, resp.Analysis)
	require.Len(t, resp.Analysis.CalculationPath, 2)
	assert.Equal(t, "step one", resp.Analysis.CalculationPath[0].Description)
	assert.Equal(t, "step two", resp.Analysis.CalculationPath[1].Description)
	// Data should round-trip through toGoValue's dict branch.
	assert.Equal(t, "GBP", resp.Analysis.CalculationPath[0].Data["currency"])
}

// TestStarlarkRuntime_RecordPathEmptyData exercises extractPathEntries when the
// recorded data is an empty dict (the entry.Data non-nil branch with no keys).
// Note: record_path requires a dict argument, so the entry.Data == nil branch in
// extractPathEntries is not reachable from scripts; an empty dict is the closest
// observable case.
func TestStarlarkRuntime_RecordPathEmptyData(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `
record_path("empty data step", {})
result = {"valued_amount": 5, "instrument": "GBP"}
`
	resp, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.NoError(t, err)
	require.NotNil(t, resp.Analysis)
	require.Len(t, resp.Analysis.CalculationPath, 1)
	assert.Equal(t, "empty data step", resp.Analysis.CalculationPath[0].Description)
}

// TestStarlarkRuntime_ContextParamConversions feeds heterogeneous Parameters into
// the script context so toStarlarkValue's list/int/float/bool/string/nil branches
// are exercised, then reads them back through toGoValue.
func TestStarlarkRuntime_ContextParamConversions(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	params := map[string]interface{}{
		"a_string": "hello",
		"an_int":   int(7),
		"an_int64": int64(11),
		"a_float":  3.5,
		"a_bool":   true,
		"a_nil":    nil,
		"a_list":   []interface{}{1, "two", 3.0, false},
		"a_nested": map[string]interface{}{"inner": "value"},
	}

	// The script reads each ctx field and echoes them back into the result dict so
	// the conversions are observable, then returns a fixed valuation.
	script := `
result = {
    "valued_amount": 1,
    "instrument": "GBP",
    "echo_string": ctx["a_string"],
    "echo_int": ctx["an_int"],
    "echo_int64": ctx["an_int64"],
    "echo_float": ctx["a_float"],
    "echo_bool": ctx["a_bool"],
    "echo_nil": ctx["a_nil"],
    "echo_list": ctx["a_list"],
    "echo_nested": ctx["a_nested"],
}
`
	resp, err := runtime.Execute(context.Background(), script, requestWithParams(params))
	require.NoError(t, err)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
	assert.True(t, decimal.NewFromInt(1).Equal(resp.ValuedAmount.Amount))
}

// TestStarlarkRuntime_ResultGoValueRoundTrip records a path entry whose data
// contains every Starlark scalar/collection kind so toGoValue's branches (string,
// int, float, bool, dict, list, none) are all exercised when extracted.
func TestStarlarkRuntime_ResultGoValueRoundTrip(t *testing.T) {
	runtime := newSecurityRuntime(5 * time.Second)

	script := `
record_path("mixed", {
    "s": "text",
    "i": 12,
    "f": 1.5,
    "b": False,
    "n": None,
    "lst": [1, 2, 3],
    "nested": {"deep": "val"},
})
result = {"valued_amount": 1, "instrument": "GBP"}
`
	resp, err := runtime.Execute(context.Background(), script, minimalRequest())
	require.NoError(t, err)
	require.NotNil(t, resp.Analysis)
	require.Len(t, resp.Analysis.CalculationPath, 1)

	data := resp.Analysis.CalculationPath[0].Data
	require.NotNil(t, data)
	assert.Equal(t, "text", data["s"])
	assert.Equal(t, int64(12), data["i"])
	assert.InDelta(t, 1.5, data["f"], 0.0001)
	assert.Equal(t, false, data["b"])
	assert.Nil(t, data["n"])
	assert.Equal(t, []interface{}{int64(1), int64(2), int64(3)}, data["lst"])
	nested, ok := data["nested"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "val", nested["deep"])
}

// TestStarlarkRuntime_CancelledContextClassified drives classifyExecError when the
// parent context is already cancelled (non-deadline). The runtime wraps the parent
// in a WithTimeout, so a pre-cancelled parent propagates Canceled to the child,
// taking classifyExecError's ctx.Done() branch without DeadlineExceeded.
func TestStarlarkRuntime_CancelledContextClassified(t *testing.T) {
	runtime := newSecurityRuntime(10 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Execute runs

	script := `
def run():
    x = 0
    for i in range(1000000000):
        x = x + 1
    return x
result = {"valued_amount": run(), "instrument": "GBP"}
`
	_, err := runtime.Execute(ctx, script, minimalRequest())
	require.Error(t, err)
	// classifyExecError returns the execution sentinel wrapping context.Canceled.
	assert.ErrorIs(t, err, valuation.ErrStarlarkExecution)
	assert.True(t,
		strings.Contains(err.Error(), "cancel") ||
			strings.Contains(err.Error(), "context"),
		"expected cancellation context error, got: %v", err)
}
