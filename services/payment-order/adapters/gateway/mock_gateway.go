package gateway

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MockGateway errors
var (
	ErrGatewayTimeout     = errors.New("gateway timeout")
	ErrGatewayUnavailable = errors.New("gateway unavailable")
	ErrPaymentRejected    = errors.New("payment rejected by gateway")
)

// MockGatewayConfig configures the behavior of the mock gateway for testing.
//
// Deterministic Failure Convention:
// When DeterministicFailures is enabled, amounts ending in 99 cents (e.g., £10.99, £1.99)
// will always be rejected. This provides a predictable way to test failure handling
// without relying on random behavior.
type MockGatewayConfig struct {
	// Latency is the simulated network latency for each request.
	Latency time.Duration
	// FailureRate is the probability (0.0-1.0) of random failures.
	FailureRate float64
	// DeterministicFailures enables failure for amounts ending in 99 cents.
	// Use this for predictable test scenarios (e.g., £10.99 will always fail).
	DeterministicFailures bool
	// TimeoutRate is the probability (0.0-1.0) of timeout errors.
	TimeoutRate float64
}

// DefaultMockGatewayConfig returns a default configuration with no failures.
func DefaultMockGatewayConfig() MockGatewayConfig {
	return MockGatewayConfig{
		Latency:               0,
		FailureRate:           0,
		DeterministicFailures: false,
		TimeoutRate:           0,
	}
}

// MockGateway is a test implementation of PaymentGateway with configurable behaviors.
// It is safe for concurrent use.
type MockGateway struct {
	config MockGatewayConfig
	rng    *rand.Rand
	mu     sync.Mutex
}

// NewMockGateway creates a new MockGateway with the given configuration.
// Uses math/rand for reproducible test scenarios (not crypto operations).
func NewMockGateway(config MockGatewayConfig) *MockGateway {
	now := time.Now().UnixNano()
	return &MockGateway{
		config: config,
		// #nosec G404 -- This is a mock for testing, not for cryptographic purposes
		rng: rand.New(rand.NewPCG(uint64(now), uint64(now>>32))), // #nosec G115
	}
}

// NewMockGatewayWithSeed creates a new MockGateway with a seeded random for deterministic testing.
// Uses math/rand for reproducible test scenarios (not crypto operations).
func NewMockGatewayWithSeed(config MockGatewayConfig, seed uint64) *MockGateway {
	return &MockGateway{
		config: config,
		// #nosec G404 -- This is a mock for testing, not for cryptographic purposes
		rng: rand.New(rand.NewPCG(seed, seed>>32)),
	}
}

// SendPayment simulates sending a payment to an external gateway.
func (g *MockGateway) SendPayment(ctx context.Context, req PaymentRequest) (PaymentResponse, error) {
	// Fast path: check context cancellation immediately
	select {
	case <-ctx.Done():
		return PaymentResponse{}, ctx.Err()
	default:
	}

	// Simulate latency
	if g.config.Latency > 0 {
		select {
		case <-time.After(g.config.Latency):
		case <-ctx.Done():
			return PaymentResponse{}, ctx.Err()
		}
	}

	// Check for deterministic failures first (no RNG needed)
	// ToMinorUnitsUnchecked is acceptable here: this is mock/test code with small test values
	if g.config.DeterministicFailures {
		amountCents := req.Amount.ToMinorUnitsUnchecked()
		if amountCents%100 == 99 {
			return PaymentResponse{
				GatewayReferenceID: generateGatewayReferenceID(),
				Status:             StatusRejected,
				Message:            "deterministic rejection: amount ends in .99",
			}, nil
		}
	}

	// Batch RNG checks under a single lock for efficiency
	var shouldTimeout, shouldFail bool
	if g.config.TimeoutRate > 0 || g.config.FailureRate > 0 {
		g.mu.Lock()
		if g.config.TimeoutRate > 0 {
			shouldTimeout = g.rng.Float64() < g.config.TimeoutRate
		}
		if g.config.FailureRate > 0 && !shouldTimeout {
			shouldFail = g.rng.Float64() < g.config.FailureRate
		}
		g.mu.Unlock()
	}

	if shouldTimeout {
		return PaymentResponse{}, ErrGatewayTimeout
	}

	if shouldFail {
		return PaymentResponse{
			GatewayReferenceID: generateGatewayReferenceID(),
			Status:             StatusRejected,
			Message:            "random rejection",
		}, nil
	}

	// Success case
	return PaymentResponse{
		GatewayReferenceID: generateGatewayReferenceID(),
		Status:             StatusAccepted,
		Message:            "payment accepted",
	}, nil
}

// generateGatewayReferenceID generates a realistic gateway reference ID.
func generateGatewayReferenceID() string {
	return "GW-" + uuid.New().String()
}

// Compile-time interface check
var _ PaymentGateway = (*MockGateway)(nil)
