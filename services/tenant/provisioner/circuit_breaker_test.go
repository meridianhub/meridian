package provisioner

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
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

// =============================================================================
// GetCircuitBreakerState Tests
// =============================================================================

func TestGetCircuitBreakerState_NonexistentService(t *testing.T) {
	scb := NewServiceCircuitBreakers()

	// Request state for service that hasn't been accessed
	state := scb.GetCircuitBreakerState("nonexistent-service")

	assert.Nil(t, state, "should return nil for service that has never been accessed")
}

func TestGetCircuitBreakerState_ClosedState(t *testing.T) {
	scb := NewServiceCircuitBreakers()
	breaker := scb.GetBreaker("closed-service")

	// Execute a successful operation to initialize counts
	_, _ = breaker.Execute(func() (any, error) {
		return "success", nil
	})

	state := scb.GetCircuitBreakerState("closed-service")

	require.NotNil(t, state)
	assert.Equal(t, "closed-service", state.ServiceName)
	assert.Equal(t, "closed", state.State)
	assert.Equal(t, uint32(1), state.Counts.Requests)
	assert.Equal(t, uint32(1), state.Counts.TotalSuccesses)
	assert.Equal(t, uint32(0), state.Counts.TotalFailures)
}

func TestGetCircuitBreakerState_OpenState(t *testing.T) {
	scb := NewServiceCircuitBreakers()
	breaker := scb.GetBreaker("open-service")

	// Trip the breaker with failures
	for i := 0; i < 5; i++ {
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})
	}

	state := scb.GetCircuitBreakerState("open-service")

	require.NotNil(t, state)
	assert.Equal(t, "open-service", state.ServiceName)
	assert.Equal(t, "open", state.State)
	// Note: Counts are reset when breaker transitions to open state
	// We verify the breaker is open, which is the key assertion
}

func TestGetAllCircuitBreakerStates(t *testing.T) {
	scb := NewServiceCircuitBreakers()

	// Access multiple services
	_ = scb.GetBreaker("service-a")
	_ = scb.GetBreaker("service-b")
	_ = scb.GetBreaker("service-c")

	states := scb.GetAllCircuitBreakerStates()

	assert.Len(t, states, 3)

	// Verify all services are present
	serviceNames := make(map[string]bool)
	for _, state := range states {
		serviceNames[state.ServiceName] = true
	}
	assert.True(t, serviceNames["service-a"])
	assert.True(t, serviceNames["service-b"])
	assert.True(t, serviceNames["service-c"])
}

func TestGetAllCircuitBreakerStates_Empty(t *testing.T) {
	scb := NewServiceCircuitBreakers()

	states := scb.GetAllCircuitBreakerStates()

	assert.Empty(t, states, "should return empty slice when no breakers exist")
}

// =============================================================================
// Half-Open State and Recovery Tests
// =============================================================================

// TestBreakerBehavior_HalfOpenStateTransition tests that the circuit breaker
// transitions to half-open state after the timeout period.
// Note: This test uses a custom breaker with short timeout for testing.
func TestBreakerBehavior_HalfOpenStateTransition(t *testing.T) {
	// Create a custom breaker with very short timeout for testing
	// MaxRequests=1 means only 1 successful request is needed to close
	breaker := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        "half-open-test",
		MaxRequests: 1, // Only need 1 success to close
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond, // Very short for testing
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	})

	// Trip the breaker
	for i := 0; i < 3; i++ {
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})
	}

	// Verify breaker is open
	assert.Equal(t, gobreaker.StateOpen, breaker.State())

	// Wait for timeout to transition to half-open
	err := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return breaker.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, err, "circuit should transition to half-open")

	// Next request should be allowed (half-open state allows test requests)
	// The gobreaker library transitions to half-open lazily on the next request
	result, err := breaker.Execute(func() (any, error) {
		return "recovered", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "recovered", result)
	// After successful execution in half-open (with MaxRequests=1), breaker should close
	assert.Equal(t, gobreaker.StateClosed, breaker.State())
}

// TestBreakerBehavior_HalfOpenFailure tests that failures in half-open state
// cause the breaker to reopen.
func TestBreakerBehavior_HalfOpenFailure(t *testing.T) {
	breaker := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        "half-open-failure-test",
		MaxRequests: 3,
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	})

	// Trip the breaker
	for i := 0; i < 3; i++ {
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})
	}
	assert.Equal(t, gobreaker.StateOpen, breaker.State())

	// Wait for timeout to transition to half-open
	err := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return breaker.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, err, "circuit should transition to half-open")

	// Try a request that fails in half-open state
	_, _ = breaker.Execute(func() (any, error) {
		return nil, errTestFailure
	})

	// Breaker should reopen
	assert.Equal(t, gobreaker.StateOpen, breaker.State())
}

// TestBreakerBehavior_HalfOpenMaxRequests tests that the breaker only allows
// MaxRequests during half-open state.
func TestBreakerBehavior_HalfOpenMaxRequests(t *testing.T) {
	breaker := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        "half-open-max-test",
		MaxRequests: 2, // Only allow 2 test requests
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	})

	// Trip the breaker
	for i := 0; i < 3; i++ {
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})
	}

	// Wait for timeout to transition to half-open
	err := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return breaker.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, err, "circuit should transition to half-open")

	// Start concurrent requests to test MaxRequests limiting
	// Use a WaitGroup to synchronize
	var wg sync.WaitGroup
	results := make(chan error, 5)

	// Simulate slow operations that would block during half-open
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, execErr := breaker.Execute(func() (any, error) {
				time.Sleep(50 * time.Millisecond) //nolint:forbidigo // holds slot to test MaxRequests limiting in half-open state
				return "success", nil
			})
			results <- execErr
		}()
	}

	wg.Wait()
	close(results)

	// Count errors - some should be ErrTooManyRequests
	var tooManyErrors int
	for err := range results {
		if errors.Is(err, gobreaker.ErrTooManyRequests) {
			tooManyErrors++
		}
	}

	// With MaxRequests=2 and 5 concurrent requests, at least some should be rejected
	// (exact number depends on timing)
	assert.Greater(t, tooManyErrors, 0, "some requests should be rejected with ErrTooManyRequests")
}

// TestBreakerBehavior_RecoverySequence tests the full recovery sequence:
// closed -> open -> half-open -> closed
func TestBreakerBehavior_RecoverySequence(t *testing.T) {
	breaker := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        "recovery-sequence-test",
		MaxRequests: 1, // Only need 1 success to close from half-open
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	})

	// Phase 1: Closed state - normal operation
	assert.Equal(t, gobreaker.StateClosed, breaker.State())

	result, err := breaker.Execute(func() (any, error) {
		return "initial success", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "initial success", result)
	assert.Equal(t, gobreaker.StateClosed, breaker.State())

	// Phase 2: Trip to Open state
	for i := 0; i < 3; i++ {
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})
	}
	assert.Equal(t, gobreaker.StateOpen, breaker.State())

	// Phase 3: Requests blocked in Open state
	_, err = breaker.Execute(func() (any, error) {
		return "should not execute", nil
	})
	assert.ErrorIs(t, err, gobreaker.ErrOpenState)

	// Phase 4: Wait for timeout and transition to half-open
	awaitErr := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return breaker.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, awaitErr, "circuit should transition to half-open")

	// Phase 5: Successful execution in half-open closes the breaker
	result, err = breaker.Execute(func() (any, error) {
		return "recovered", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "recovered", result)

	// Phase 6: Verify breaker is closed again
	assert.Equal(t, gobreaker.StateClosed, breaker.State())

	// Phase 7: Normal operation resumes
	for i := 0; i < 5; i++ {
		result, err = breaker.Execute(func() (any, error) {
			return "normal operation", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "normal operation", result)
	}
}

// TestBreakerBehavior_ReopenAfterHalfOpenFailures tests that continued failures
// in half-open state keep the breaker open.
func TestBreakerBehavior_ReopenAfterHalfOpenFailures(t *testing.T) {
	breaker := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        "reopen-test",
		MaxRequests: 1, // Only need 1 success to close
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	})

	// Initial trip
	for i := 0; i < 3; i++ {
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})
	}
	assert.Equal(t, gobreaker.StateOpen, breaker.State())

	// Multiple cycles of: wait -> half-open -> fail -> reopen
	for cycle := 0; cycle < 3; cycle++ {
		// Wait for timeout to transition to half-open
		err := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
			return breaker.State() == gobreaker.StateHalfOpen
		})
		require.NoError(t, err, "cycle %d: circuit should transition to half-open", cycle)

		// Fail in half-open state
		_, _ = breaker.Execute(func() (any, error) {
			return nil, errTestFailure
		})

		// Should be back to open
		assert.Equal(t, gobreaker.StateOpen, breaker.State(),
			"cycle %d: breaker should reopen after half-open failure", cycle)
	}

	// Finally recover - wait for transition to half-open
	err := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return breaker.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, err, "circuit should transition to half-open for recovery")
	result, err := breaker.Execute(func() (any, error) {
		return "finally recovered", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "finally recovered", result)
	assert.Equal(t, gobreaker.StateClosed, breaker.State())
}
