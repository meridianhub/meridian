package stripe

import (
	"errors"
	"testing"

	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func TestTenantCircuitBreakerRegistry_Get_ReturnsSameInstanceForSameTenant(t *testing.T) {
	registry := NewTenantCircuitBreakerRegistry(gobreaker.Settings{Name: "stripe"})

	cb1 := registry.Get(tenant.TenantID("tenant_a"))
	cb2 := registry.Get(tenant.TenantID("tenant_a"))

	assert.Same(t, cb1, cb2, "should return the same circuit breaker for the same tenant")
}

func TestTenantCircuitBreakerRegistry_Get_ReturnsDifferentInstancesForDifferentTenants(t *testing.T) {
	registry := NewTenantCircuitBreakerRegistry(gobreaker.Settings{Name: "stripe"})

	cbA := registry.Get(tenant.TenantID("tenant_a"))
	cbB := registry.Get(tenant.TenantID("tenant_b"))

	assert.NotSame(t, cbA, cbB, "should return different circuit breakers for different tenants")
}

func TestTenantCircuitBreakerRegistry_Isolation_OneTenantFailureDoesNotAffectAnother(t *testing.T) {
	settings := gobreaker.Settings{
		Name: "stripe",
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	}
	registry := NewTenantCircuitBreakerRegistry(settings)

	tenantA := tenant.TenantID("tenant_a")
	tenantB := tenant.TenantID("tenant_b")

	cbA := registry.Get(tenantA)
	cbB := registry.Get(tenantB)

	// Trip tenant A's breaker with consecutive failures.
	simulatedErr := errors.New("stripe config provider down")
	for range 3 {
		_, _ = cbA.Execute(func() (TenantConfig, error) {
			return TenantConfig{}, simulatedErr
		})
	}

	// Tenant A should be open.
	assert.Equal(t, gobreaker.StateOpen, cbA.State(), "tenant A breaker should be open")

	// Tenant B should still be closed.
	assert.Equal(t, gobreaker.StateClosed, cbB.State(), "tenant B breaker should remain closed")

	// Tenant B can still execute successfully.
	cfg, err := cbB.Execute(func() (TenantConfig, error) {
		return TenantConfig{ConnectedAccountID: "acct_b"}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "acct_b", cfg.ConnectedAccountID)

	// Tenant A should be blocked.
	_, err = cbA.Execute(func() (TenantConfig, error) {
		return TenantConfig{ConnectedAccountID: "acct_a"}, nil
	})
	assert.ErrorIs(t, err, gobreaker.ErrOpenState)
}

func TestTenantCircuitBreakerRegistry_State_ReturnsClosedForUnknownTenant(t *testing.T) {
	registry := NewTenantCircuitBreakerRegistry(gobreaker.Settings{Name: "stripe"})

	state := registry.State(tenant.TenantID("unknown"))
	assert.Equal(t, gobreaker.StateClosed, state)
}

func TestTenantCircuitBreakerRegistry_State_ReflectsActualState(t *testing.T) {
	settings := gobreaker.Settings{
		Name: "stripe",
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 1
		},
	}
	registry := NewTenantCircuitBreakerRegistry(settings)

	tid := tenant.TenantID("tenant_a")
	cb := registry.Get(tid)

	assert.Equal(t, gobreaker.StateClosed, registry.State(tid))

	// Trip the breaker.
	_, _ = cb.Execute(func() (TenantConfig, error) {
		return TenantConfig{}, errors.New("fail")
	})

	assert.Equal(t, gobreaker.StateOpen, registry.State(tid))
}

func TestTenantCircuitBreakerRegistry_OnStateChange_CallsParentCallback(t *testing.T) {
	var parentCalled bool
	var parentName, parentFrom, parentTo string

	settings := gobreaker.Settings{
		Name: "stripe",
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 1
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			parentCalled = true
			parentName = name
			parentFrom = from.String()
			parentTo = to.String()
		},
	}
	registry := NewTenantCircuitBreakerRegistry(settings)

	cb := registry.Get(tenant.TenantID("tenant_a"))

	// Trip the breaker to trigger state change.
	_, _ = cb.Execute(func() (TenantConfig, error) {
		return TenantConfig{}, errors.New("fail")
	})

	assert.True(t, parentCalled, "parent OnStateChange should be called")
	assert.Equal(t, "stripe-tenant_a", parentName)
	assert.Equal(t, "closed", parentFrom)
	assert.Equal(t, "open", parentTo)
}

func TestTenantCircuitBreakerRegistry_Get_ConcurrentAccess(t *testing.T) {
	registry := NewTenantCircuitBreakerRegistry(gobreaker.Settings{Name: "stripe"})
	tid := tenant.TenantID("concurrent_tenant")

	const goroutines = 50
	results := make(chan *gobreaker.CircuitBreaker[TenantConfig], goroutines)

	for range goroutines {
		go func() {
			results <- registry.Get(tid)
		}()
	}

	var first *gobreaker.CircuitBreaker[TenantConfig]
	for range goroutines {
		cb := <-results
		if first == nil {
			first = cb
		}
		assert.Same(t, first, cb, "all goroutines should get the same breaker instance")
	}
}

func TestStateLabel(t *testing.T) {
	tests := []struct {
		state gobreaker.State
		want  string
	}{
		{gobreaker.StateClosed, "closed"},
		{gobreaker.StateHalfOpen, "half_open"},
		{gobreaker.StateOpen, "open"},
		{gobreaker.State(99), "unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, stateLabel(tt.state))
	}
}
