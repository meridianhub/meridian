package service

import (
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/reconciliation/observability"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/sony/gobreaker/v2"
)

// Upstream service name constants for circuit breaker identification.
const (
	ServicePositionKeeping     = "position-keeping"
	ServiceFinancialAccounting = "financial-accounting"
	ServiceCurrentAccount      = "current-account"
)

// circuitBreakerFailureThreshold is the number of consecutive failures before
// the circuit breaker trips to open state.
const circuitBreakerFailureThreshold = 5

// CircuitBreakerRegistry holds circuit breakers for upstream service dependencies.
type CircuitBreakerRegistry struct {
	breakers map[string]*gobreaker.CircuitBreaker[any]
	logger   *slog.Logger
}

// NewCircuitBreakerRegistry creates circuit breakers for all upstream services
// with a 5-failure threshold and standard timeout/interval settings.
func NewCircuitBreakerRegistry(logger *slog.Logger) *CircuitBreakerRegistry {
	if logger == nil {
		logger = slog.Default()
	}

	services := []string{
		ServicePositionKeeping,
		ServiceFinancialAccounting,
		ServiceCurrentAccount,
	}

	breakers := make(map[string]*gobreaker.CircuitBreaker[any], len(services))
	for _, svc := range services {
		svc := svc // capture for closure
		breakers[svc] = gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
			Name:        svc,
			MaxRequests: 1,
			Interval:    defaults.DefaultCircuitBreakerInterval,
			Timeout:     defaults.DefaultCircuitBreakerOpenTimeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= circuitBreakerFailureThreshold
			},
			OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
				logger.Info("circuit breaker state changed",
					"service", name,
					"from", from.String(),
					"to", to.String(),
				)
				observability.RecordCircuitBreakerTrip(name, from.String(), to.String())
				observability.SetCircuitBreakerState(name, stateToInt(to))
			},
		})
		// Initialize gauge to closed state
		observability.SetCircuitBreakerState(svc, 0)
	}

	return &CircuitBreakerRegistry{
		breakers: breakers,
		logger:   logger,
	}
}

// Execute runs the given function through the circuit breaker for the specified service.
// Returns the result or an error if the circuit is open or the function fails.
func (r *CircuitBreakerRegistry) Execute(serviceName string, fn func() (any, error)) (any, error) {
	cb, ok := r.breakers[serviceName]
	if !ok {
		// No circuit breaker configured for this service, execute directly
		return fn()
	}
	return cb.Execute(fn)
}

// State returns the current state of the circuit breaker for the given service.
func (r *CircuitBreakerRegistry) State(serviceName string) gobreaker.State {
	cb, ok := r.breakers[serviceName]
	if !ok {
		return gobreaker.StateClosed
	}
	return cb.State()
}

// stateToInt converts a gobreaker.State to an integer for the Prometheus gauge.
func stateToInt(state gobreaker.State) int {
	switch state {
	case gobreaker.StateClosed:
		return 0
	case gobreaker.StateHalfOpen:
		return 1
	case gobreaker.StateOpen:
		return 2
	default:
		return 0
	}
}

// WaitForHalfOpen is a helper for testing that returns the circuit breaker
// timeout duration, useful for verifying state transitions.
func (r *CircuitBreakerRegistry) WaitForHalfOpen() time.Duration {
	return defaults.DefaultCircuitBreakerOpenTimeout
}
