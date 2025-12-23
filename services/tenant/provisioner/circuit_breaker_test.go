package provisioner

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ServiceCircuitBreakers Constructor Tests
// =============================================================================

func TestNewServiceCircuitBreakers(t *testing.T) {
	scb := NewServiceCircuitBreakers()

	require.NotNil(t, scb)
	require.NotNil(t, scb.breakers)
	assert.Empty(t, scb.breakers, "breakers map should be empty on creation")
}

// =============================================================================
// GetBreaker Tests
// =============================================================================

func TestGetBreaker_CreatesNewBreakerForUnknownService(t *testing.T) {
	scb := NewServiceCircuitBreakers()

	breaker := scb.GetBreaker("party-service")

	require.NotNil(t, breaker)
	assert.Equal(t, "party-service", breaker.Name())
}

func TestGetBreaker_ReturnsSameInstanceForSameService(t *testing.T) {
	scb := NewServiceCircuitBreakers()

	breaker1 := scb.GetBreaker("current-account")
	breaker2 := scb.GetBreaker("current-account")

	// Both should be the exact same instance
	assert.Same(t, breaker1, breaker2, "should return same breaker instance for same service")
}

func TestGetBreaker_CreatesDifferentBreakersForDifferentServices(t *testing.T) {
	scb := NewServiceCircuitBreakers()

	partyBreaker := scb.GetBreaker("party-service")
	accountBreaker := scb.GetBreaker("current-account")

	assert.NotSame(t, partyBreaker, accountBreaker, "different services should have different breakers")
	assert.Equal(t, "party-service", partyBreaker.Name())
	assert.Equal(t, "current-account", accountBreaker.Name())
}

func TestGetBreaker_ConcurrentAccessMaintainsThreadSafety(t *testing.T) {
	scb := NewServiceCircuitBreakers()
	const numGoroutines = 100
	const serviceName = "concurrent-test-service"

	var wg sync.WaitGroup
	breakers := make([]*gobreaker.CircuitBreaker[any], numGoroutines)

	// Launch multiple goroutines to get the same breaker concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			breakers[index] = scb.GetBreaker(serviceName)
		}(i)
	}

	wg.Wait()

	// All goroutines should have received the exact same breaker instance
	firstBreaker := breakers[0]
	require.NotNil(t, firstBreaker)

	for i := 1; i < numGoroutines; i++ {
		assert.Same(t, firstBreaker, breakers[i],
			"goroutine %d should have received the same breaker instance", i)
	}
}

func TestGetBreaker_ConcurrentAccessToDifferentServices(t *testing.T) {
	scb := NewServiceCircuitBreakers()
	services := []string{"party", "current-account", "transaction", "ledger", "payment"}

	var wg sync.WaitGroup
	breakerMap := make(map[string]*gobreaker.CircuitBreaker[any])
	var mu sync.Mutex

	// Launch multiple goroutines accessing different services
	for _, service := range services {
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(svc string) {
				defer wg.Done()
				breaker := scb.GetBreaker(svc)

				mu.Lock()
				if existing, ok := breakerMap[svc]; ok {
					assert.Same(t, existing, breaker)
				} else {
					breakerMap[svc] = breaker
				}
				mu.Unlock()
			}(service)
		}
	}

	wg.Wait()

	// Each service should have exactly one breaker
	assert.Len(t, breakerMap, len(services))
	for _, service := range services {
		assert.NotNil(t, breakerMap[service])
	}
}

// =============================================================================
// Breaker Settings Tests
// =============================================================================

func TestBreakerSettings_MaxRequests(t *testing.T) {
	// Verify constant value matches specification
	assert.Equal(t, uint32(3), BreakerMaxRequests,
		"MaxRequests should be 3 as per specification")
}

func TestBreakerSettings_Interval(t *testing.T) {
	// Verify constant value matches specification (60 seconds)
	assert.Equal(t, 60*time.Second, BreakerInterval,
		"Interval should be 60 seconds as per specification")
}

func TestBreakerSettings_Timeout(t *testing.T) {
	// Verify constant value matches specification (5 minutes = 300 seconds)
	assert.Equal(t, 300*time.Second, BreakerTimeout,
		"Timeout should be 300 seconds (5 minutes) as per specification")
}

func TestBreakerSettings_MinRequestsAndFailureRatio(t *testing.T) {
	// Verify constant values match specification
	assert.Equal(t, uint32(5), BreakerMinRequests,
		"MinRequests should be 5 as per specification")
	assert.Equal(t, 0.6, BreakerFailureRatio,
		"FailureRatio should be 0.6 (60%%) as per specification")
}

// =============================================================================
// ReadyToTrip Tests
// =============================================================================

func TestReadyToTrip_NotEnoughRequests(t *testing.T) {
	tests := []struct {
		name     string
		requests uint32
		failures uint32
	}{
		{"0 requests", 0, 0},
		{"1 request, 1 failure", 1, 1},
		{"2 requests, 2 failures", 2, 2},
		{"3 requests, 3 failures", 3, 3},
		{"4 requests, 4 failures", 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counts := gobreaker.Counts{
				Requests:      tt.requests,
				TotalFailures: tt.failures,
			}
			// Even with 100% failure rate, should not trip until MinRequests is reached
			assert.False(t, readyToTrip(counts),
				"should not trip with %d requests (need %d)", tt.requests, BreakerMinRequests)
		})
	}
}

func TestReadyToTrip_ExactlyMinRequestsWithHighFailureRate(t *testing.T) {
	// 5 requests with 3 failures = 60% failure rate (exactly at threshold)
	counts := gobreaker.Counts{
		Requests:      5,
		TotalFailures: 3,
	}
	assert.True(t, readyToTrip(counts),
		"should trip with 5 requests and 60%% failure rate")
}

func TestReadyToTrip_AboveThreshold(t *testing.T) {
	tests := []struct {
		name     string
		requests uint32
		failures uint32
	}{
		{"5 requests, 4 failures (80%)", 5, 4},
		{"5 requests, 5 failures (100%)", 5, 5},
		{"10 requests, 6 failures (60%)", 10, 6},
		{"10 requests, 8 failures (80%)", 10, 8},
		{"100 requests, 70 failures (70%)", 100, 70},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counts := gobreaker.Counts{
				Requests:      tt.requests,
				TotalFailures: tt.failures,
			}
			failureRate := float64(tt.failures) / float64(tt.requests) * 100
			assert.True(t, readyToTrip(counts),
				"should trip with %.0f%% failure rate", failureRate)
		})
	}
}

func TestReadyToTrip_BelowThreshold(t *testing.T) {
	tests := []struct {
		name     string
		requests uint32
		failures uint32
	}{
		{"5 requests, 2 failures (40%)", 5, 2},
		{"10 requests, 5 failures (50%)", 10, 5},
		{"10 requests, 3 failures (30%)", 10, 3},
		{"100 requests, 50 failures (50%)", 100, 50},
		{"100 requests, 0 failures (0%)", 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counts := gobreaker.Counts{
				Requests:      tt.requests,
				TotalFailures: tt.failures,
			}
			failureRate := float64(tt.failures) / float64(tt.requests) * 100
			assert.False(t, readyToTrip(counts),
				"should not trip with %.0f%% failure rate (need >=60%%)", failureRate)
		})
	}
}

// =============================================================================
// Integration Tests - Circuit Breaker Behavior
// =============================================================================

// errTestFailure is a sentinel error for testing circuit breaker behavior.
var errTestFailure = errors.New("test failure")

func TestBreakerBehavior_TripsAfterFailureThreshold(t *testing.T) {
	scb := NewServiceCircuitBreakers()
	breaker := scb.GetBreaker("failing-service")

	// Simulate 5 failures (should trip at 60% = 3 failures out of 5)
	for i := 0; i < 5; i++ {
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})
	}

	// Next call should be blocked by the circuit breaker
	_, err := breaker.Execute(func() (any, error) {
		return "success", nil
	})

	assert.ErrorIs(t, err, gobreaker.ErrOpenState,
		"circuit breaker should be open after 5 consecutive failures")
}

func TestBreakerBehavior_StaysClosedBelowThreshold(t *testing.T) {
	scb := NewServiceCircuitBreakers()
	breaker := scb.GetBreaker("mostly-working-service")

	// Simulate mixed success/failure (40% failure rate - below 60% threshold)
	// Pattern: 4 failures and 6 successes = 40% failure rate
	// We need to be careful to never hit 60% at any point with 5+ requests
	// Start with 2 failures, 3 successes (40%), then 2 failures, 3 successes
	failurePattern := []bool{
		true, true, false, false, false, // 2F, 3S = 40% after 5 requests
		true, true, false, false, false, // Total: 4F, 6S = 40% after 10 requests
	}

	for _, shouldFail := range failurePattern {
		_, _ = breaker.Execute(func() (any, error) {
			if shouldFail {
				return nil, errTestFailure
			}
			return "success", nil
		})
	}

	// Breaker should still be closed (40% < 60% threshold)
	result, err := breaker.Execute(func() (any, error) {
		return "success", nil
	})

	assert.NoError(t, err, "circuit breaker should remain closed at 40%% failure rate")
	assert.Equal(t, "success", result)
}

func TestBreakerBehavior_RecordsSuccessesCorrectly(t *testing.T) {
	scb := NewServiceCircuitBreakers()
	breaker := scb.GetBreaker("successful-service")

	// Execute 10 successful operations
	for i := 0; i < 10; i++ {
		result, err := breaker.Execute(func() (any, error) {
			return "success", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "success", result)
	}

	// Breaker should still be closed
	result, err := breaker.Execute(func() (any, error) {
		return "still working", nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "still working", result)
}
