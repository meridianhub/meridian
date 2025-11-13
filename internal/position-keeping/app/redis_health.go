package app

import (
	"context"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/pkg/platform/health"
	"github.com/redis/go-redis/v9"
)

// RedisChecker checks the health of a Redis connection.
type RedisChecker struct {
	client *redis.Client
}

// NewRedisChecker creates a new Redis health checker.
func NewRedisChecker(client *redis.Client) *RedisChecker {
	if client == nil {
		panic("NewRedisChecker: client cannot be nil")
	}
	return &RedisChecker{
		client: client,
	}
}

// Name returns the component name.
func (r *RedisChecker) Name() string {
	return "redis"
}

// Check performs a Redis health check by pinging the connection.
func (r *RedisChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	// Add timeout if context doesn't have one
	checkCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		checkCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	err := r.client.Ping(checkCtx).Err()
	responseTime := time.Since(start)

	status := health.StatusHealthy
	message := "redis connection successful"

	if err != nil {
		status = health.StatusUnhealthy
		message = fmt.Sprintf("redis ping failed: %v", err)
	}

	return health.ComponentResult{
		Name:         r.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}
