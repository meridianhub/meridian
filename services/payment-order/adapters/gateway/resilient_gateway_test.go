package gateway_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// Test sentinel errors for resilient gateway tests.
var (
	errTransientNetwork   = errors.New("temporary network error")
	errPersistent         = errors.New("persistent error")
	errGatewayUnavailable = errors.New("gateway unavailable")
)

// stubGateway is a test double that allows controlling gateway behavior.
type stubGateway struct {
	callCount    atomic.Int32
	shouldFail   bool
	failCount    int // Number of times to fail before succeeding
	delay        time.Duration
	response     gateway.PaymentResponse
	err          error
	mu           sync.Mutex
	currentFails int
}

func (s *stubGateway) SendPayment(ctx context.Context, _ gateway.PaymentRequest) (gateway.PaymentResponse, error) {
	s.callCount.Add(1)

	// Check context cancellation
	select {
	case <-ctx.Done():
		return gateway.PaymentResponse{}, ctx.Err()
	default:
	}

	// Simulate delay
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return gateway.PaymentResponse{}, ctx.Err()
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Fail a limited number of times, then succeed
	if s.failCount > 0 && s.currentFails < s.failCount {
		s.currentFails++
		return gateway.PaymentResponse{}, s.err
	}

	if s.shouldFail {
		return gateway.PaymentResponse{}, s.err
	}

	return s.response, nil
}

func (s *stubGateway) CallCount() int {
	return int(s.callCount.Load())
}

func newSuccessStub() *stubGateway {
	return &stubGateway{
		response: gateway.PaymentResponse{
			GatewayReferenceID: "GW-" + uuid.New().String(),
			Status:             gateway.StatusAccepted,
			Message:            "payment accepted",
		},
	}
}

func newFailingStub(err error) *stubGateway {
	return &stubGateway{
		shouldFail: true,
		err:        err,
	}
}

func newTransientFailureStub(failTimes int, err error) *stubGateway {
	return &stubGateway{
		failCount: failTimes,
		err:       err,
		response: gateway.PaymentResponse{
			GatewayReferenceID: "GW-" + uuid.New().String(),
			Status:             gateway.StatusAccepted,
			Message:            "payment accepted",
		},
	}
}

func createResilientTestRequest(t *testing.T) gateway.PaymentRequest {
	t.Helper()
	amount, err := domain.NewMoney("GBP", 10000)
	require.NoError(t, err)
	return gateway.PaymentRequest{
		PaymentOrderID:    uuid.New(),
		DebtorAccountID:   "debtor-123",
		CreditorReference: "GB82WEST12345698765432",
		Amount:            amount,
		IdempotencyKey:    "idem-key-" + uuid.New().String(),
	}
}

func TestResilientPaymentGateway_SendPayment_Success(t *testing.T) {
	stub := newSuccessStub()
	config := gateway.DefaultResilientGatewayConfig()
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)
	resp, err := resilient.SendPayment(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.NotEmpty(t, resp.GatewayReferenceID)
	assert.Equal(t, 1, stub.CallCount())
}

func TestResilientPaymentGateway_ImplementsInterface(_ *testing.T) {
	var _ gateway.PaymentGateway = (*gateway.ResilientPaymentGateway)(nil)
}

func TestResilientPaymentGateway_RetryOnTransientFailure(t *testing.T) {
	// Fail twice, then succeed
	stub := newTransientFailureStub(2, errTransientNetwork)
	config := gateway.DefaultResilientGatewayConfig()
	config.MaxRetries = 3
	config.InitialInterval = 1 * time.Millisecond // Fast retries for test
	config.MaxInterval = 10 * time.Millisecond
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)
	resp, err := resilient.SendPayment(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.Equal(t, 3, stub.CallCount()) // 2 failures + 1 success
}

func TestResilientPaymentGateway_ExhaustRetries(t *testing.T) {
	stub := newFailingStub(errPersistent)
	config := gateway.DefaultResilientGatewayConfig()
	config.MaxRetries = 2
	config.InitialInterval = 1 * time.Millisecond
	config.MaxInterval = 10 * time.Millisecond
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)
	_, err := resilient.SendPayment(context.Background(), req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persistent error")
	assert.Equal(t, 3, stub.CallCount()) // 1 initial + 2 retries
}

func TestResilientPaymentGateway_CircuitBreaker_TripsAfterFailures(t *testing.T) {
	stub := newFailingStub(errGatewayUnavailable)
	config := gateway.DefaultResilientGatewayConfig()
	config.FailureThreshold = 3 // Trip after 3 consecutive failures
	config.MaxRetries = 0       // No retries to test circuit breaker directly
	config.CircuitBreakerTimeout = 1 * time.Second
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)

	// Make 3 calls to trip the circuit
	for i := 0; i < 3; i++ {
		_, err := resilient.SendPayment(context.Background(), req)
		require.Error(t, err)
	}

	// Circuit should now be open
	assert.Equal(t, gobreaker.StateOpen, resilient.CircuitBreakerState())

	// Next call should fail immediately with circuit open error
	_, err := resilient.SendPayment(context.Background(), req)
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrCircuitOpen)

	// Verify that the delegate was not called for the fourth request
	assert.Equal(t, 3, stub.CallCount())
}

func TestResilientPaymentGateway_CircuitBreaker_RecoverAfterTimeout(t *testing.T) {
	stub := newFailingStub(errGatewayUnavailable)
	config := gateway.DefaultResilientGatewayConfig()
	config.FailureThreshold = 2
	config.MaxRetries = 0
	config.CircuitBreakerTimeout = 50 * time.Millisecond // Short timeout for test
	config.MaxRequests = 1
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_, _ = resilient.SendPayment(context.Background(), req)
	}
	assert.Equal(t, gobreaker.StateOpen, resilient.CircuitBreakerState())

	// Wait for circuit to transition to half-open
	err := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return resilient.CircuitBreakerState() == gobreaker.StateHalfOpen
	})
	require.NoError(t, err, "circuit should transition to half-open")

	// Make the stub succeed now
	stub.shouldFail = false
	stub.response = gateway.PaymentResponse{
		GatewayReferenceID: "GW-recovered",
		Status:             gateway.StatusAccepted,
		Message:            "payment accepted",
	}

	// Should allow one request through (half-open state)
	resp, err := resilient.SendPayment(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)

	// Circuit should now be closed
	assert.Equal(t, gobreaker.StateClosed, resilient.CircuitBreakerState())
}

func TestResilientPaymentGateway_RateLimiting(t *testing.T) {
	stub := newSuccessStub()
	config := gateway.DefaultResilientGatewayConfig()
	config.RateLimit = 5      // 5 requests per second
	config.RateLimitBurst = 2 // Burst of 2
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)

	// First 2 requests should succeed (burst)
	for i := 0; i < 2; i++ {
		_, err := resilient.SendPayment(context.Background(), req)
		require.NoError(t, err)
	}

	// Third request should be rate limited (exceeded burst)
	_, err := resilient.SendPayment(context.Background(), req)
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrRateLimited)

	// Wait for rate limiter to refill and request to succeed
	err = await.AtMost(1 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		_, sendErr := resilient.SendPayment(context.Background(), req)
		return sendErr == nil
	})
	require.NoError(t, err, "rate limiter should eventually allow requests")
}

func TestResilientPaymentGateway_ContextCancellation(t *testing.T) {
	stub := &stubGateway{
		delay: 100 * time.Millisecond,
		response: gateway.PaymentResponse{
			GatewayReferenceID: "GW-test",
			Status:             gateway.StatusAccepted,
		},
	}
	config := gateway.DefaultResilientGatewayConfig()
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := resilient.SendPayment(ctx, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestResilientPaymentGateway_ContextDeadline(t *testing.T) {
	stub := &stubGateway{
		delay: 100 * time.Millisecond,
		response: gateway.PaymentResponse{
			GatewayReferenceID: "GW-test",
			Status:             gateway.StatusAccepted,
		},
	}
	config := gateway.DefaultResilientGatewayConfig()
	config.MaxRetries = 0 // Disable retries to test deadline directly
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := resilient.SendPayment(ctx, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestResilientPaymentGateway_NoRetryOnContextError(t *testing.T) {
	stub := &stubGateway{
		delay: 50 * time.Millisecond,
		response: gateway.PaymentResponse{
			GatewayReferenceID: "GW-test",
			Status:             gateway.StatusAccepted,
		},
	}
	config := gateway.DefaultResilientGatewayConfig()
	config.MaxRetries = 5
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	req := createResilientTestRequest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := resilient.SendPayment(ctx, req)
	require.Error(t, err)
	// Should not have retried - only 1 call attempt
	assert.LessOrEqual(t, stub.CallCount(), 1)
}

func TestResilientPaymentGateway_ConcurrentRequests(t *testing.T) {
	stub := newSuccessStub()
	config := gateway.DefaultResilientGatewayConfig()
	config.RateLimit = 1000 // High rate limit for concurrent test
	config.RateLimitBurst = 100
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			req := createResilientTestRequest(t)
			_, err := resilient.SendPayment(context.Background(), req)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// All requests should succeed
	for err := range errors {
		t.Errorf("unexpected error: %v", err)
	}
	assert.Equal(t, goroutines, stub.CallCount())
}

func TestDefaultResilientGatewayConfig(t *testing.T) {
	config := gateway.DefaultResilientGatewayConfig()

	// Verify sensible defaults
	assert.Equal(t, "payment-gateway", config.CircuitBreakerName)
	assert.Equal(t, 30*time.Second, config.CircuitBreakerTimeout)
	assert.Equal(t, 60*time.Second, config.CircuitBreakerInterval)
	assert.Equal(t, uint32(1), config.MaxRequests)
	assert.Equal(t, uint32(5), config.FailureThreshold)
	assert.Equal(t, 100.0, config.RateLimit)
	assert.Equal(t, 10, config.RateLimitBurst)
	assert.Equal(t, 3, config.MaxRetries)
	assert.Equal(t, 100*time.Millisecond, config.InitialInterval)
	assert.Equal(t, 5*time.Second, config.MaxInterval)
	assert.Equal(t, 2.0, config.Multiplier)
	assert.Equal(t, 0.5, config.RandomizationFactor)
}

func TestResilientPaymentGateway_CircuitBreakerState(t *testing.T) {
	stub := newSuccessStub()
	config := gateway.DefaultResilientGatewayConfig()
	resilient := gateway.NewResilientPaymentGateway(stub, config)

	// Initially closed
	assert.Equal(t, gobreaker.StateClosed, resilient.CircuitBreakerState())
}
