package clients

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewResilientClient(t *testing.T) {
	t.Run("creates client with default config", func(t *testing.T) {
		config := DefaultResilientClientConfig("test-service")
		client := NewResilientClient(config)

		if client == nil {
			t.Fatal("expected non-nil client")
		}
		if client.CircuitBreaker() == nil {
			t.Error("expected non-nil circuit breaker")
		}
		if client.Logger() == nil {
			t.Error("expected non-nil logger")
		}
	})

	t.Run("creates client with nil logger uses default", func(t *testing.T) {
		config := ResilientClientConfig{
			CircuitBreakerName: "test",
			Logger:             nil,
		}
		client := NewResilientClient(config)

		if client.Logger() == nil {
			t.Error("expected default logger when nil provided")
		}
	})

	t.Run("creates client with custom config", func(t *testing.T) {
		config := ResilientClientConfig{
			CircuitBreakerName:     "custom-service",
			CircuitBreakerTimeout:  10 * time.Second,
			CircuitBreakerInterval: 30 * time.Second,
			MaxRequests:            5,
			FailureThreshold:       3,
			MaxRetries:             5,
			InitialInterval:        200 * time.Millisecond,
			MaxInterval:            10 * time.Second,
			Multiplier:             1.5,
			RandomizationFactor:    0.3,
		}
		client := NewResilientClient(config)

		retryConfig := client.RetryConfig()
		if retryConfig.MaxRetries != 5 {
			t.Errorf("expected MaxRetries=5, got %d", retryConfig.MaxRetries)
		}
		if retryConfig.InitialInterval != 200*time.Millisecond {
			t.Errorf("expected InitialInterval=200ms, got %v", retryConfig.InitialInterval)
		}
	})
}

func TestExecuteWithResilience(t *testing.T) {
	t.Run("successful execution returns result", func(t *testing.T) {
		config := DefaultResilientClientConfig("test")
		client := NewResilientClient(config)
		ctx := context.Background()

		result, err := ExecuteWithResilience(ctx, client, "test-op", func() (string, error) {
			return "success", nil
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "success" {
			t.Errorf("expected 'success', got '%s'", result)
		}
	})

	t.Run("retries on transient gRPC failure", func(t *testing.T) {
		config := ResilientClientConfig{
			CircuitBreakerName:  "retry-test",
			MaxRetries:          3,
			InitialInterval:     1 * time.Millisecond,
			MaxInterval:         10 * time.Millisecond,
			Multiplier:          2.0,
			RandomizationFactor: 0,
		}
		client := NewResilientClient(config)
		ctx := context.Background()

		var attempts int32
		// Use gRPC Unavailable status - this is retryable
		result, err := ExecuteWithResilience(ctx, client, "retry-op", func() (int, error) {
			count := atomic.AddInt32(&attempts, 1)
			if count < 3 {
				return 0, status.Error(codes.Unavailable, "service unavailable")
			}
			return 42, nil
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 42 {
			t.Errorf("expected 42, got %d", result)
		}
		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("fails after max retries exhausted with gRPC error", func(t *testing.T) {
		config := ResilientClientConfig{
			CircuitBreakerName:  "fail-test",
			MaxRetries:          2,
			InitialInterval:     1 * time.Millisecond,
			MaxInterval:         10 * time.Millisecond,
			Multiplier:          2.0,
			RandomizationFactor: 0,
		}
		client := NewResilientClient(config)
		ctx := context.Background()

		var attempts int32
		// Use gRPC Unavailable status - this is retryable
		_, err := ExecuteWithResilience(ctx, client, "fail-op", func() (string, error) {
			atomic.AddInt32(&attempts, 1)
			return "", status.Error(codes.Unavailable, "service unavailable")
		})

		if err == nil {
			t.Fatal("expected error after retries exhausted")
		}
		// MaxRetries=2 means initial attempt + 2 retries = 3 total attempts
		if attempts != 3 {
			t.Errorf("expected 3 attempts (1 initial + 2 retries), got %d", attempts)
		}
	})

	t.Run("does not retry non-gRPC errors", func(t *testing.T) {
		config := ResilientClientConfig{
			CircuitBreakerName:  "no-retry-generic",
			MaxRetries:          3,
			InitialInterval:     1 * time.Millisecond,
			MaxInterval:         10 * time.Millisecond,
			Multiplier:          2.0,
			RandomizationFactor: 0,
		}
		client := NewResilientClient(config)
		ctx := context.Background()

		var attempts int32
		// Generic Go errors are NOT retryable
		_, err := ExecuteWithResilience(ctx, client, "no-retry-op", func() (string, error) {
			atomic.AddInt32(&attempts, 1)
			return "", errors.New("generic error")
		})

		if err == nil {
			t.Fatal("expected error")
		}
		// Generic errors should not be retried
		if attempts != 1 {
			t.Errorf("expected 1 attempt (no retries for generic errors), got %d", attempts)
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		config := DefaultResilientClientConfig("ctx-test")
		client := NewResilientClient(config)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := ExecuteWithResilience(ctx, client, "ctx-op", func() (string, error) {
			return "should not reach", nil
		})

		if err == nil {
			t.Fatal("expected error from cancelled context")
		}
	})

	t.Run("works with different types", func(t *testing.T) {
		config := DefaultResilientClientConfig("type-test")
		client := NewResilientClient(config)
		ctx := context.Background()

		// Test with struct type
		type Response struct {
			ID   int
			Name string
		}
		result, err := ExecuteWithResilience(ctx, client, "struct-op", func() (Response, error) {
			return Response{ID: 1, Name: "test"}, nil
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ID != 1 || result.Name != "test" {
			t.Errorf("unexpected result: %+v", result)
		}
	})
}

func TestExecuteWithResilienceNoRetry(t *testing.T) {
	t.Run("does not retry on failure", func(t *testing.T) {
		config := ResilientClientConfig{
			CircuitBreakerName: "no-retry-test",
			MaxRetries:         5, // This should be ignored
			InitialInterval:    1 * time.Millisecond,
		}
		client := NewResilientClient(config)
		ctx := context.Background()

		var attempts int32
		_, err := ExecuteWithResilienceNoRetry(ctx, client, "no-retry-op", func() (string, error) {
			atomic.AddInt32(&attempts, 1)
			return "", errors.New("error")
		})

		if err == nil {
			t.Fatal("expected error")
		}
		if attempts != 1 {
			t.Errorf("expected exactly 1 attempt (no retries), got %d", attempts)
		}
	})

	t.Run("successful execution returns result", func(t *testing.T) {
		config := DefaultResilientClientConfig("no-retry-success")
		client := NewResilientClient(config)
		ctx := context.Background()

		result, err := ExecuteWithResilienceNoRetry(ctx, client, "success-op", func() (int, error) {
			return 100, nil
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 100 {
			t.Errorf("expected 100, got %d", result)
		}
	})
}

func TestDefaultResilientClientConfig(t *testing.T) {
	config := DefaultResilientClientConfig("my-service")

	if config.CircuitBreakerName != "my-service" {
		t.Errorf("expected name 'my-service', got '%s'", config.CircuitBreakerName)
	}
	if config.CircuitBreakerTimeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", config.CircuitBreakerTimeout)
	}
	if config.CircuitBreakerInterval != 60*time.Second {
		t.Errorf("expected interval 60s, got %v", config.CircuitBreakerInterval)
	}
	if config.MaxRequests != 1 {
		t.Errorf("expected MaxRequests=1, got %d", config.MaxRequests)
	}
	if config.FailureThreshold != 5 {
		t.Errorf("expected FailureThreshold=5, got %d", config.FailureThreshold)
	}
	if config.MaxRetries != 3 {
		t.Errorf("expected MaxRetries=3, got %d", config.MaxRetries)
	}
	if config.InitialInterval != 100*time.Millisecond {
		t.Errorf("expected InitialInterval=100ms, got %v", config.InitialInterval)
	}
	if config.MaxInterval != 5*time.Second {
		t.Errorf("expected MaxInterval=5s, got %v", config.MaxInterval)
	}
	if config.Multiplier != 2.0 {
		t.Errorf("expected Multiplier=2.0, got %f", config.Multiplier)
	}
	if config.RandomizationFactor != 0.5 {
		t.Errorf("expected RandomizationFactor=0.5, got %f", config.RandomizationFactor)
	}
}

func TestResilientClientAccessors(t *testing.T) {
	config := DefaultResilientClientConfig("accessor-test")
	client := NewResilientClient(config)

	t.Run("CircuitBreaker returns non-nil", func(t *testing.T) {
		if client.CircuitBreaker() == nil {
			t.Error("expected non-nil circuit breaker")
		}
	})

	t.Run("RetryConfig returns configured values", func(t *testing.T) {
		rc := client.RetryConfig()
		if rc.MaxRetries != 3 {
			t.Errorf("expected MaxRetries=3, got %d", rc.MaxRetries)
		}
	})

	t.Run("Logger returns non-nil", func(t *testing.T) {
		if client.Logger() == nil {
			t.Error("expected non-nil logger")
		}
	})
}
