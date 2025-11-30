package gateway

import (
	"context"
	"errors"
	"math/rand/v2"
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
type MockGatewayConfig struct {
	// Latency is the simulated network latency for each request.
	Latency time.Duration
	// FailureRate is the probability (0.0-1.0) of random failures.
	FailureRate float64
	// DeterministicFailures enables failure for amounts ending in 99 cents.
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
type MockGateway struct {
	config MockGatewayConfig
	rng    *rand.Rand
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
	// Simulate latency
	if g.config.Latency > 0 {
		select {
		case <-time.After(g.config.Latency):
		case <-ctx.Done():
			return PaymentResponse{}, ctx.Err()
		}
	}

	// Check for timeout
	if g.config.TimeoutRate > 0 && g.rng.Float64() < g.config.TimeoutRate {
		return PaymentResponse{}, ErrGatewayTimeout
	}

	// Check for deterministic failures (amounts ending in 99 cents)
	if g.config.DeterministicFailures && req.Amount.AmountCents()%100 == 99 {
		return PaymentResponse{
			GatewayReferenceID: generateGatewayReferenceID(),
			Status:             StatusRejected,
			Message:            "deterministic rejection: amount ends in .99",
		}, nil
	}

	// Check for random failures
	if g.config.FailureRate > 0 && g.rng.Float64() < g.config.FailureRate {
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
