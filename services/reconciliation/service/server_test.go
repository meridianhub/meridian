package service

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/valuation"
	"github.com/stretchr/testify/assert"
)

// Minimal stubs for valuation interfaces.

type stubPolicyRuntime struct{}

func (stubPolicyRuntime) CompilePolicy(_ string) (valuation.CompiledPolicy, error) { return nil, nil }
func (stubPolicyRuntime) EvaluatePolicy(_ context.Context, _ valuation.CompiledPolicy, _ map[string]interface{}) (interface{}, uint64, error) {
	return nil, 0, nil
}

type stubStarlarkRuntime struct{}

func (stubStarlarkRuntime) Execute(_ context.Context, _ string, _ *valuation.Request) (*valuation.Response, error) {
	return nil, nil
}

type stubValuationCache struct{}

func (stubValuationCache) GetMethod(_ string, _ *int) (*valuation.Method, error) { return nil, nil }
func (stubValuationCache) SetMethod(_ *valuation.Method) error                   { return nil }
func (stubValuationCache) GetPolicy(_ string, _ *int) (valuation.CompiledPolicy, error) {
	return nil, nil
}
func (stubValuationCache) SetPolicy(_ string, _ int, _ valuation.CompiledPolicy) error { return nil }
func (stubValuationCache) Clear()                                                      {}

func TestWithPolicyRuntime(t *testing.T) {
	rt := stubPolicyRuntime{}
	svc := NewAccountReconciliationService(WithPolicyRuntime(rt))
	assert.Equal(t, rt, svc.policyRuntime)
}

func TestWithStarlarkRuntime(t *testing.T) {
	rt := stubStarlarkRuntime{}
	svc := NewAccountReconciliationService(WithStarlarkRuntime(rt))
	assert.Equal(t, rt, svc.starlarkRuntime)
}

func TestWithValuationCache(t *testing.T) {
	c := stubValuationCache{}
	svc := NewAccountReconciliationService(WithValuationCache(c))
	assert.Equal(t, c, svc.valuationCache)
}

func TestNewAccountReconciliationService_DefaultLogger(t *testing.T) {
	// Without WithLogger, a default logger should be set.
	svc := NewAccountReconciliationService()
	assert.NotNil(t, svc.logger, "default logger must be initialized when not provided")
}

func TestNewAccountReconciliationService_PauseSignalsInitialized(t *testing.T) {
	svc := NewAccountReconciliationService()
	assert.NotNil(t, svc.pauseSignals, "pauseSignals map must be initialized")
}

func TestNewAccountReconciliationService_AllValuationOptions(t *testing.T) {
	policyRuntime := stubPolicyRuntime{}
	starlarkRuntime := stubStarlarkRuntime{}
	cache := stubValuationCache{}

	svc := NewAccountReconciliationService(
		WithPolicyRuntime(policyRuntime),
		WithStarlarkRuntime(starlarkRuntime),
		WithValuationCache(cache),
	)

	assert.Equal(t, policyRuntime, svc.policyRuntime)
	assert.Equal(t, starlarkRuntime, svc.starlarkRuntime)
	assert.Equal(t, cache, svc.valuationCache)
}

func TestWithLogger_OverridesDefault(t *testing.T) {
	// Providing a custom logger should override the default.
	customLogger := testLogger()
	svc := NewAccountReconciliationService(WithLogger(customLogger))
	assert.Equal(t, customLogger, svc.logger)
}

func TestNewAccountReconciliationService_NilFieldsByDefault(t *testing.T) {
	svc := NewAccountReconciliationService()

	// Optional dependencies should be nil when not configured.
	assert.Nil(t, svc.runRepo)
	assert.Nil(t, svc.disputeRepo)
	assert.Nil(t, svc.assertor)
	assert.Nil(t, svc.snapshotCapturer)
	assert.Nil(t, svc.varianceDetector)
	assert.Nil(t, svc.varianceValuator)
	assert.Nil(t, svc.policyRuntime)
	assert.Nil(t, svc.starlarkRuntime)
	assert.Nil(t, svc.valuationCache)
}
