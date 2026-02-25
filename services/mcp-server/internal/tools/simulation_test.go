package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
	"github.com/meridianhub/meridian/shared/pkg/valuation"
)

// --- Mock implementations ---

type mockCELEvaluator struct {
	evaluateFn func(expression, environment string, variables map[string]interface{}) (interface{}, error)
}

func (m *mockCELEvaluator) Evaluate(expression, environment string, variables map[string]interface{}) (interface{}, error) {
	return m.evaluateFn(expression, environment, variables)
}

type mockManifestDiffer struct {
	diffFn func(current, proposed json.RawMessage) (interface{}, error)
}

func (m *mockManifestDiffer) Diff(current, proposed json.RawMessage) (interface{}, error) {
	return m.diffFn(current, proposed)
}

type mockValuationSimulator struct {
	simulateFn func(ctx context.Context, req *valuation.Request) (*valuation.Response, error)
}

func (m *mockValuationSimulator) Simulate(ctx context.Context, req *valuation.Request) (*valuation.Response, error) {
	return m.simulateFn(ctx, req)
}

type mockSagaSimulator struct {
	simulateFn func(ctx context.Context, script string, inputData map[string]interface{}) (interface{}, error)
}

func (m *mockSagaSimulator) Simulate(ctx context.Context, script string, inputData map[string]interface{}) (interface{}, error) {
	return m.simulateFn(ctx, script, inputData)
}

func newSimulationDeps(
	cel tools.CELEvaluator,
	differ tools.ManifestDiffer,
	valSim tools.ValuationSimulator,
	sagaSim tools.SagaSimulator,
) tools.SimulationDeps {
	return tools.SimulationDeps{
		CELEvaluator:       cel,
		ManifestDiffer:     differ,
		ValuationSimulator: valSim,
		SagaSimulator:      sagaSim,
	}
}

// --- meridian_cel_evaluate tests ---

func TestCELEvaluate_ValidExpression_ReturnsResult(t *testing.T) {
	mock := &mockCELEvaluator{
		evaluateFn: func(expr, env string, _ map[string]interface{}) (interface{}, error) {
			if expr != "amount > 0" {
				t.Errorf("unexpected expression: %s", expr)
			}
			if env != "validation" {
				t.Errorf("unexpected environment: %s", env)
			}
			return true, nil
		},
	}

	deps := newSimulationDeps(mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"expression": "amount > 0",
		"environment": "validation",
		"variables": {"amount": "100.00"}
	}`)
	result, err := r.Call(context.Background(), "meridian_cel_evaluate", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if _, hasResult := m["result"]; !hasResult {
		t.Errorf("expected 'result' key in response, got keys: %v", m)
	}
}

func TestCELEvaluate_EvaluatorError_ReturnsFormattedError(t *testing.T) {
	mock := &mockCELEvaluator{
		evaluateFn: func(_, _ string, _ map[string]interface{}) (interface{}, error) {
			return nil, errors.New("CEL compilation failed: ERROR: :1:1: undeclared reference to 'x'")
		},
	}

	deps := newSimulationDeps(mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"expression": "x > 0",
		"environment": "validation",
		"variables": {}
	}`)
	result, err := r.Call(context.Background(), "meridian_cel_evaluate", params)
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if _, hasErr := m["error"]; !hasErr {
		if _, hasErrors := m["errors"]; !hasErrors {
			t.Errorf("expected 'error' or 'errors' key in result, got keys: %v", m)
		}
	}
}

func TestCELEvaluate_MissingExpression_ValidationError(t *testing.T) {
	mock := &mockCELEvaluator{
		evaluateFn: func(_, _ string, _ map[string]interface{}) (interface{}, error) {
			t.Fatal("handler should not be called for missing expression")
			return nil, nil
		},
	}

	deps := newSimulationDeps(mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	_, err := r.Call(context.Background(), "meridian_cel_evaluate", json.RawMessage(`{"environment": "validation"}`))
	if err == nil {
		t.Fatal("expected validation error for missing expression")
	}
}

func TestCELEvaluate_MissingEnvironment_ValidationError(t *testing.T) {
	mock := &mockCELEvaluator{
		evaluateFn: func(_, _ string, _ map[string]interface{}) (interface{}, error) {
			t.Fatal("handler should not be called for missing environment")
			return nil, nil
		},
	}

	deps := newSimulationDeps(mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	_, err := r.Call(context.Background(), "meridian_cel_evaluate", json.RawMessage(`{"expression": "amount > 0"}`))
	if err == nil {
		t.Fatal("expected validation error for missing environment")
	}
}

func TestCELEvaluate_NoVariables_HandlerCalled(t *testing.T) {
	called := false
	mock := &mockCELEvaluator{
		evaluateFn: func(_, _ string, _ map[string]interface{}) (interface{}, error) {
			called = true
			return false, nil
		},
	}

	deps := newSimulationDeps(mock, nil, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{"expression": "1 == 2", "environment": "validation"}`)
	result, err := r.Call(context.Background(), "meridian_cel_evaluate", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Error("expected evaluator to be called")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- meridian_manifest_diff tests ---

func TestManifestDiff_TwoManifests_ReturnsDiff(t *testing.T) {
	mock := &mockManifestDiffer{
		diffFn: func(_, _ json.RawMessage) (interface{}, error) {
			return map[string]interface{}{
				"added":   []interface{}{"instruments[1]"},
				"removed": []interface{}{},
				"changed": []interface{}{},
			}, nil
		},
	}

	deps := newSimulationDeps(nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"current": {"version": "1.0", "metadata": {"name": "Test"}},
		"proposed": {"version": "1.0", "metadata": {"name": "Test"}, "instruments": [{"code": "GBP", "name": "British Pound", "type": 1}]}
	}`)
	result, err := r.Call(context.Background(), "meridian_manifest_diff", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestManifestDiff_DifferError_ReturnsFormattedError(t *testing.T) {
	mock := &mockManifestDiffer{
		diffFn: func(_, _ json.RawMessage) (interface{}, error) {
			return nil, errors.New("invalid manifest structure")
		},
	}

	deps := newSimulationDeps(nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"current": {},
		"proposed": {}
	}`)
	result, err := r.Call(context.Background(), "meridian_manifest_diff", params)
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestManifestDiff_MissingCurrent_ValidationError(t *testing.T) {
	mock := &mockManifestDiffer{
		diffFn: func(_, _ json.RawMessage) (interface{}, error) {
			t.Fatal("handler should not be called for missing current")
			return nil, nil
		},
	}

	deps := newSimulationDeps(nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	_, err := r.Call(context.Background(), "meridian_manifest_diff", json.RawMessage(`{"proposed": {}}`))
	if err == nil {
		t.Fatal("expected validation error for missing current")
	}
}

func TestManifestDiff_MissingProposed_ValidationError(t *testing.T) {
	mock := &mockManifestDiffer{
		diffFn: func(_, _ json.RawMessage) (interface{}, error) {
			t.Fatal("handler should not be called for missing proposed")
			return nil, nil
		},
	}

	deps := newSimulationDeps(nil, mock, nil, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	_, err := r.Call(context.Background(), "meridian_manifest_diff", json.RawMessage(`{"current": {}}`))
	if err == nil {
		t.Fatal("expected validation error for missing proposed")
	}
}

// --- meridian_valuation_simulate tests ---

func TestValuationSimulate_ValidRequest_ReturnsResult(t *testing.T) {
	methodID := uuid.New()
	mock := &mockValuationSimulator{
		simulateFn: func(_ context.Context, req *valuation.Request) (*valuation.Response, error) {
			if req.MethodID == uuid.Nil {
				return nil, errors.New("method_id required")
			}
			return makeValuationResponse("GBP", nil), nil
		},
	}

	deps := newSimulationDeps(nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"method_id": "` + methodID.String() + `",
		"input_instrument": "KWH",
		"input_amount": "100.0",
		"account_id": "550e8400-e29b-41d4-a716-446655440000",
		"party_id": "550e8400-e29b-41d4-a716-446655440001"
	}`)
	result, err := r.Call(context.Background(), "meridian_valuation_simulate", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if _, hasValued := m["valued_amount"]; !hasValued {
		t.Errorf("expected 'valued_amount' key in result, got keys: %v", m)
	}
}

func TestValuationSimulate_SimulatorError_ReturnsFormattedError(t *testing.T) {
	mock := &mockValuationSimulator{
		simulateFn: func(_ context.Context, _ *valuation.Request) (*valuation.Response, error) {
			return nil, errors.New("method not found")
		},
	}

	deps := newSimulationDeps(nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"method_id": "550e8400-e29b-41d4-a716-446655440000",
		"input_instrument": "KWH",
		"input_amount": "100.0",
		"account_id": "550e8400-e29b-41d4-a716-446655440001",
		"party_id": "550e8400-e29b-41d4-a716-446655440002"
	}`)
	result, err := r.Call(context.Background(), "meridian_valuation_simulate", params)
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestValuationSimulate_MissingMethodID_ValidationError(t *testing.T) {
	mock := &mockValuationSimulator{
		simulateFn: func(_ context.Context, _ *valuation.Request) (*valuation.Response, error) {
			t.Fatal("handler should not be called for missing method_id")
			return nil, nil
		},
	}

	deps := newSimulationDeps(nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"input_instrument": "KWH",
		"input_amount": "100.0",
		"account_id": "550e8400-e29b-41d4-a716-446655440001",
		"party_id": "550e8400-e29b-41d4-a716-446655440002"
	}`)
	_, err := r.Call(context.Background(), "meridian_valuation_simulate", params)
	if err == nil {
		t.Fatal("expected validation error for missing method_id")
	}
}

func TestValuationSimulate_WithAnalysis_ReturnsCalculationPath(t *testing.T) {
	methodID := uuid.New()
	analysis := &valuation.Analysis{}
	analysis.AddPathEntry("input received", nil)
	analysis.AddPathEntry("policy applied: rate_conversion", map[string]interface{}{"rate": "0.755"})
	analysis.RecordPolicyExecution("rate_conversion", 1, nil, nil, 42)
	mock := &mockValuationSimulator{
		simulateFn: func(_ context.Context, _ *valuation.Request) (*valuation.Response, error) {
			return makeValuationResponse("USD", analysis), nil
		},
	}

	deps := newSimulationDeps(nil, nil, mock, nil)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{
		"method_id": "` + methodID.String() + `",
		"input_instrument": "KWH",
		"input_amount": "100.0",
		"account_id": "550e8400-e29b-41d4-a716-446655440001",
		"party_id": "550e8400-e29b-41d4-a716-446655440002"
	}`)
	result, err := r.Call(context.Background(), "meridian_valuation_simulate", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if _, hasPath := m["calculation_path"]; !hasPath {
		t.Errorf("expected 'calculation_path' key in result, got keys: %v", m)
	}
}

// --- meridian_saga_simulate tests ---

func TestSagaSimulate_ValidScript_ReturnsTrace(t *testing.T) {
	script := `
def run(ctx):
    return {"status": "completed"}

result = run(ctx)
`
	mock := &mockSagaSimulator{
		simulateFn: func(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
			return map[string]interface{}{
				"status": "completed",
				"steps":  []interface{}{map[string]interface{}{"name": "run", "status": "ok"}},
			}, nil
		},
	}

	deps := newSimulationDeps(nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params, _ := json.Marshal(map[string]interface{}{
		"script":     script,
		"input_data": map[string]interface{}{"account_id": "acc-001"},
	})
	result, err := r.Call(context.Background(), "meridian_saga_simulate", json.RawMessage(params))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if _, hasStatus := m["status"]; !hasStatus {
		t.Errorf("expected 'status' key in result, got keys: %v", m)
	}
}

func TestSagaSimulate_SimulatorError_ReturnsFormattedError(t *testing.T) {
	mock := &mockSagaSimulator{
		simulateFn: func(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
			return nil, errors.New("starlark execution error: saga.star:3:5: undefined: unknown_fn")
		},
	}

	deps := newSimulationDeps(nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{"script": "unknown_fn()", "input_data": {}}`)
	result, err := r.Call(context.Background(), "meridian_saga_simulate", params)
	if err != nil {
		t.Fatalf("expected formatted error result, not returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSagaSimulate_MissingScript_ValidationError(t *testing.T) {
	mock := &mockSagaSimulator{
		simulateFn: func(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
			t.Fatal("handler should not be called for missing script")
			return nil, nil
		},
	}

	deps := newSimulationDeps(nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	_, err := r.Call(context.Background(), "meridian_saga_simulate", json.RawMessage(`{"input_data": {}}`))
	if err == nil {
		t.Fatal("expected validation error for missing script")
	}
}

func TestSagaSimulate_NoInputData_HandlerCalled(t *testing.T) {
	called := false
	mock := &mockSagaSimulator{
		simulateFn: func(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
			called = true
			return map[string]interface{}{"status": "completed"}, nil
		},
	}

	deps := newSimulationDeps(nil, nil, nil, mock)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	params := json.RawMessage(`{"script": "result = None"}`)
	result, err := r.Call(context.Background(), "meridian_saga_simulate", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Error("expected simulator to be called")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- Registration tests ---

func TestSimulationTools_AllRegistered(t *testing.T) {
	deps := newSimulationDeps(
		&mockCELEvaluator{evaluateFn: func(_, _ string, _ map[string]interface{}) (interface{}, error) { return nil, nil }},
		&mockManifestDiffer{diffFn: func(_, _ json.RawMessage) (interface{}, error) { return nil, nil }},
		&mockValuationSimulator{simulateFn: func(_ context.Context, _ *valuation.Request) (*valuation.Response, error) { return nil, nil }},
		&mockSagaSimulator{simulateFn: func(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) { return nil, nil }},
	)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	listed := r.List()
	names := make(map[string]bool)
	for _, tool := range listed {
		names[tool.Name] = true
	}

	required := []string{
		"meridian_cel_evaluate",
		"meridian_manifest_diff",
		"meridian_valuation_simulate",
		"meridian_saga_simulate",
	}
	for _, name := range required {
		if !names[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestSimulationTools_AllCategorySimulate(t *testing.T) {
	deps := newSimulationDeps(
		&mockCELEvaluator{evaluateFn: func(_, _ string, _ map[string]interface{}) (interface{}, error) { return nil, nil }},
		&mockManifestDiffer{diffFn: func(_, _ json.RawMessage) (interface{}, error) { return nil, nil }},
		&mockValuationSimulator{simulateFn: func(_ context.Context, _ *valuation.Request) (*valuation.Response, error) { return nil, nil }},
		&mockSagaSimulator{simulateFn: func(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) { return nil, nil }},
	)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	for _, tool := range r.List() {
		if tool.Category != tools.CategorySimulate {
			t.Errorf("expected tool %q to be CategorySimulate, got %v", tool.Name, tool.Category)
		}
	}
}

func TestSimulationTools_NilDep_SkipsRegistration(t *testing.T) {
	// Only CELEvaluator is configured; the other 3 tools must not be registered.
	deps := newSimulationDeps(
		&mockCELEvaluator{evaluateFn: func(_, _ string, _ map[string]interface{}) (interface{}, error) { return nil, nil }},
		nil, nil, nil,
	)
	r := tools.NewRegistry()
	tools.RegisterSimulationTools(r, deps)

	listed := r.List()
	if len(listed) != 1 {
		t.Fatalf("expected 1 registered tool, got %d: %v", len(listed), listed)
	}
	if listed[0].Name != "meridian_cel_evaluate" {
		t.Errorf("expected meridian_cel_evaluate, got %q", listed[0].Name)
	}
}

// --- helpers ---

func makeValuationResponse(instrument string, analysis *valuation.Analysis) *valuation.Response {
	if analysis == nil {
		analysis = &valuation.Analysis{}
	}
	// Use zero-value decimal (Quantity.Amount defaults to 0).
	return &valuation.Response{
		ValuedAmount: valuation.Quantity{
			InstrumentCode: instrument,
		},
		Analysis:   analysis,
		ComputedAt: time.Now(),
	}
}
