package valuation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMethodResolver struct {
	methods map[string]*Method
	err     error
}

func (r *mockMethodResolver) ResolveMethod(_ context.Context, methodID string, _ *int) (*Method, error) {
	if r.err != nil {
		return nil, r.err
	}
	m, ok := r.methods[methodID]
	if !ok {
		return nil, errors.New("method not found")
	}
	return m, nil
}

func newTestRequest(methodID string) *Request {
	return &Request{
		RequestID: uuid.New(),
		MethodID:  uuid.MustParse(methodID),
		Quantity: Quantity{
			Amount:         decimal.NewFromFloat(100.00),
			InstrumentCode: "GBP",
		},
		AccountID:   uuid.New(),
		PartyID:     uuid.New(),
		KnowledgeAt: time.Now(),
	}
}

func TestDefaultEngine_Valuate_IdentityScript(t *testing.T) {
	methodID := uuid.New().String()
	resolver := &mockMethodResolver{
		methods: map[string]*Method{
			methodID: {
				ID:      methodID,
				Version: 1,
				Name:    "identity",
				Script: `
input = ctx["input_quantity"]
result = {
    "valued_amount": input["amount"],
    "instrument": input["instrument"],
}
`,
			},
		},
	}

	policyRT, err := NewPolicyRuntime()
	require.NoError(t, err)

	starlarkRT := NewStarlarkRuntime(StarlarkRuntimeConfig{
		PolicyRuntime: policyRT,
	})

	engine := NewEngine(Config{
		StarlarkRuntime: starlarkRT,
		PolicyRuntime:   policyRT,
		Cache:           NewInMemoryCache(InMemoryCacheConfig{}),
	}, resolver)

	req := newTestRequest(methodID)
	resp, err := engine.Valuate(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
	assert.True(t, resp.ValuedAmount.Amount.Equal(decimal.NewFromFloat(100.00)),
		"expected 100.00, got %s", resp.ValuedAmount.Amount.String())
	assert.False(t, resp.CacheHit)
}

func TestDefaultEngine_Valuate_OutputMismatch(t *testing.T) {
	methodID := uuid.New().String()
	resolver := &mockMethodResolver{
		methods: map[string]*Method{
			methodID: {
				ID:               methodID,
				Version:          1,
				Name:             "gbp-only",
				OutputInstrument: "USD", // Expect USD output
				Script: `
result = {
    "valued_amount": 100.0,
    "instrument": "GBP",
}
`,
			},
		},
	}

	policyRT, err := NewPolicyRuntime()
	require.NoError(t, err)

	engine := NewEngine(Config{
		StarlarkRuntime: NewStarlarkRuntime(StarlarkRuntimeConfig{PolicyRuntime: policyRT}),
		PolicyRuntime:   policyRT,
	}, resolver)

	req := newTestRequest(methodID)
	resp, err := engine.Valuate(context.Background(), req)
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutputMismatch)
}

func TestDefaultEngine_Valuate_MethodNotFound(t *testing.T) {
	resolver := &mockMethodResolver{
		methods: map[string]*Method{},
	}

	policyRT, err := NewPolicyRuntime()
	require.NoError(t, err)

	engine := NewEngine(Config{
		StarlarkRuntime: NewStarlarkRuntime(StarlarkRuntimeConfig{PolicyRuntime: policyRT}),
		PolicyRuntime:   policyRT,
	}, resolver)

	req := newTestRequest(uuid.New().String())
	resp, err := engine.Valuate(context.Background(), req)
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMethodNotFound)
}

func TestDefaultEngine_Valuate_InvalidRequest(t *testing.T) {
	resolver := &mockMethodResolver{}

	policyRT, err := NewPolicyRuntime()
	require.NoError(t, err)

	engine := NewEngine(Config{
		StarlarkRuntime: NewStarlarkRuntime(StarlarkRuntimeConfig{PolicyRuntime: policyRT}),
		PolicyRuntime:   policyRT,
	}, resolver)

	req := &Request{} // Invalid: missing required fields
	resp, err := engine.Valuate(context.Background(), req)
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestDefaultEngine_Valuate_CacheHit(t *testing.T) {
	methodID := uuid.New().String()
	method := &Method{
		ID:      methodID,
		Version: 1,
		Name:    "cached-method",
		Script: `
input = ctx["input_quantity"]
result = {
    "valued_amount": input["amount"],
    "instrument": input["instrument"],
}
`,
	}

	resolver := &mockMethodResolver{
		methods: map[string]*Method{methodID: method},
	}

	policyRT, err := NewPolicyRuntime()
	require.NoError(t, err)

	cache := NewInMemoryCache(InMemoryCacheConfig{})
	engine := NewEngine(Config{
		StarlarkRuntime: NewStarlarkRuntime(StarlarkRuntimeConfig{PolicyRuntime: policyRT}),
		PolicyRuntime:   policyRT,
		Cache:           cache,
	}, resolver)

	req := newTestRequest(methodID)

	// First call: cache miss
	resp1, err := engine.Valuate(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, resp1.CacheHit)

	// Second call: cache hit (method was cached)
	req2 := newTestRequest(methodID)
	req2.MethodVersion = intPtr(1)
	resp2, err := engine.Valuate(context.Background(), req2)
	require.NoError(t, err)
	assert.True(t, resp2.CacheHit)
}

func intPtr(i int) *int {
	return &i
}
