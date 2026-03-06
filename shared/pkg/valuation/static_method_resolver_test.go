package valuation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticMethodResolver_Register_And_Resolve(t *testing.T) {
	resolver := NewStaticMethodResolver()
	method := &Method{
		ID:      "test-method-id",
		Version: 1,
		Name:    "test-method",
		Script:  "result = {\"valued_amount\": 42.0, \"instrument\": \"GBP\"}",
	}

	resolver.Register(method)

	resolved, err := resolver.ResolveMethod(context.Background(), "test-method-id", nil)
	require.NoError(t, err)
	assert.Equal(t, method, resolved)
}

func TestStaticMethodResolver_NotFound(t *testing.T) {
	resolver := NewStaticMethodResolver()

	_, err := resolver.ResolveMethod(context.Background(), "nonexistent", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMethodNotRegistered)
}

func TestNewIdentityMethodResolver(t *testing.T) {
	resolver := NewIdentityMethodResolver()

	method, err := resolver.ResolveMethod(context.Background(), IdentityMethodID, nil)
	require.NoError(t, err)
	assert.Equal(t, "identity-conversion", method.Name)
	assert.NotEmpty(t, method.Script)
}

func TestIdentityConversionScript_ExecutesCorrectly(t *testing.T) {
	resolver := NewIdentityMethodResolver()

	policyRT, err := NewPolicyRuntime()
	require.NoError(t, err)

	engine := NewEngine(Config{
		StarlarkRuntime: NewStarlarkRuntime(StarlarkRuntimeConfig{PolicyRuntime: policyRT}),
		PolicyRuntime:   policyRT,
		Cache:           NewInMemoryCache(InMemoryCacheConfig{}),
	}, resolver)

	req := newTestRequest(IdentityMethodID)
	resp, err := engine.Valuate(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
	assert.True(t, req.Quantity.Amount.Equal(resp.ValuedAmount.Amount),
		"identity valuation should return same amount")
}
