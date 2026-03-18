package stripe

import (
	"fmt"
	"sync"

	"github.com/sony/gobreaker/v2"

	"github.com/meridianhub/meridian/services/financial-gateway/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// TenantCircuitBreakerRegistry manages per-tenant circuit breakers so that
// one tenant's Stripe failures do not trip the breaker for other tenants.
type TenantCircuitBreakerRegistry struct {
	breakers sync.Map // map[tenant.TenantID]*gobreaker.CircuitBreaker[TenantConfig]
	settings gobreaker.Settings
}

// NewTenantCircuitBreakerRegistry creates a registry that will lazily create
// circuit breakers for each tenant using the provided settings as a template.
// The settings.Name field is used as a prefix; each tenant breaker is named
// "<prefix>-<tenantID>".
func NewTenantCircuitBreakerRegistry(settings gobreaker.Settings) *TenantCircuitBreakerRegistry {
	return &TenantCircuitBreakerRegistry{
		settings: settings,
	}
}

// Get returns the circuit breaker for the given tenant, creating one if it
// does not yet exist. Concurrent calls for the same tenant are safe and will
// return the same breaker instance.
func (r *TenantCircuitBreakerRegistry) Get(tenantID tenant.TenantID) *gobreaker.CircuitBreaker[TenantConfig] {
	if cb, ok := r.breakers.Load(tenantID); ok {
		return cb.(*gobreaker.CircuitBreaker[TenantConfig])
	}

	// Build tenant-specific settings with per-tenant metrics on state change.
	s := r.settings
	s.Name = fmt.Sprintf("%s-%s", r.settings.Name, tenantID)

	// Wrap the caller's OnStateChange (if any) to also emit per-tenant metrics.
	parentOnStateChange := r.settings.OnStateChange
	s.OnStateChange = func(name string, from gobreaker.State, to gobreaker.State) {
		if parentOnStateChange != nil {
			parentOnStateChange(name, from, to)
		}
		observability.RecordCircuitBreakerState(tenantID.String(), "stripe", stateLabel(to))
	}

	newCB := gobreaker.NewCircuitBreaker[TenantConfig](s)
	actual, _ := r.breakers.LoadOrStore(tenantID, newCB)
	return actual.(*gobreaker.CircuitBreaker[TenantConfig])
}

// State returns the circuit breaker state for a specific tenant.
// If no breaker exists yet for the tenant, it returns StateClosed (the default
// initial state for a new breaker).
func (r *TenantCircuitBreakerRegistry) State(tenantID tenant.TenantID) gobreaker.State {
	if cb, ok := r.breakers.Load(tenantID); ok {
		return cb.(*gobreaker.CircuitBreaker[TenantConfig]).State()
	}
	return gobreaker.StateClosed
}

// stateLabel converts a gobreaker.State to the label used in metrics.
func stateLabel(s gobreaker.State) string {
	switch s {
	case gobreaker.StateClosed:
		return "closed"
	case gobreaker.StateHalfOpen:
		return "half_open"
	case gobreaker.StateOpen:
		return "open"
	default:
		return "unknown"
	}
}
