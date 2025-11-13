package app

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/pkg/platform/health"
	"github.com/redis/go-redis/v9"
)

const testRedisName = "redis"

func TestRedisChecker_Name(t *testing.T) {
	// Create minimal Redis client for test
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer func() { _ = client.Close() }()

	checker := NewRedisChecker(client)

	if got := checker.Name(); got != testRedisName {
		t.Errorf("Name() = %q, want %q", got, testRedisName)
	}
}

func TestRedisChecker_NilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewRedisChecker(nil) did not panic")
		}
	}()

	_ = NewRedisChecker(nil)
}

func TestRedisChecker_Check_ReturnsResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create minimal Redis client for test
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer func() { _ = client.Close() }()

	checker := NewRedisChecker(client)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := checker.Check(ctx)

	// Verify result structure (Redis likely won't connect in test environment)
	if result.Name != testRedisName {
		t.Errorf("result.Name = %q, want %q", result.Name, testRedisName)
	}

	if result.CheckedAt.IsZero() {
		t.Error("result.CheckedAt is zero, want non-zero timestamp")
	}

	if result.ResponseTime == 0 {
		t.Error("result.ResponseTime is zero, want non-zero duration")
	}

	// Status should be either healthy or unhealthy (not unknown or degraded)
	if result.Status != health.StatusHealthy && result.Status != health.StatusUnhealthy {
		t.Errorf("result.Status = %v, want either StatusHealthy or StatusUnhealthy", result.Status)
	}

	// If unhealthy, should have error and message
	if result.Status == health.StatusUnhealthy {
		if result.Error == nil {
			t.Error("result.Error is nil for unhealthy status")
		}
		if result.Message == "" {
			t.Error("result.Message is empty for unhealthy status")
		}
	}
}
