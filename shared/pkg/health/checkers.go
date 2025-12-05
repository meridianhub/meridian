package health

import (
	"context"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/redis/go-redis/v9"
)

// DatabaseChecker checks the health of a PostgreSQL database connection pool.
type DatabaseChecker struct {
	pool *db.PostgresPool
}

// NewDatabaseChecker creates a new database health checker.
func NewDatabaseChecker(pool *db.PostgresPool) *DatabaseChecker {
	if pool == nil {
		panic("NewDatabaseChecker: pool cannot be nil")
	}
	return &DatabaseChecker{
		pool: pool,
	}
}

// Name returns the component name.
func (d *DatabaseChecker) Name() string {
	return "database" //nolint:goconst // component name
}

// Check performs a database health check by pinging the connection pool.
func (d *DatabaseChecker) Check(ctx context.Context) ComponentResult {
	start := time.Now()

	err := d.pool.Ping(ctx)
	responseTime := time.Since(start)

	status := StatusHealthy
	message := "database connection successful"

	if err != nil {
		status = StatusUnhealthy
		message = fmt.Sprintf("database ping failed: %v", err)
	}

	return ComponentResult{
		Name:         d.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}

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
func (r *RedisChecker) Check(ctx context.Context) ComponentResult {
	start := time.Now()

	err := r.client.Ping(ctx).Err()
	responseTime := time.Since(start)

	status := StatusHealthy
	message := "redis connection successful"

	if err != nil {
		status = StatusUnhealthy
		message = fmt.Sprintf("redis ping failed: %v", err)
	}

	return ComponentResult{
		Name:         r.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}

// KafkaCheckFunc is a function that checks Kafka connectivity.
// This allows flexibility in implementation (admin client, producer test, etc.)
type KafkaCheckFunc func(ctx context.Context) error

// KafkaChecker checks the health of Kafka connectivity.
type KafkaChecker struct {
	checkFunc KafkaCheckFunc
}

// NewKafkaChecker creates a new Kafka health checker with a custom check function.
// The check function should verify Kafka connectivity (e.g., admin client metadata check).
func NewKafkaChecker(checkFunc KafkaCheckFunc) *KafkaChecker {
	if checkFunc == nil {
		panic("NewKafkaChecker: checkFunc cannot be nil")
	}
	return &KafkaChecker{
		checkFunc: checkFunc,
	}
}

// Name returns the component name.
func (k *KafkaChecker) Name() string {
	return "kafka" //nolint:goconst // component name
}

// Check performs a Kafka health check using the configured check function.
func (k *KafkaChecker) Check(ctx context.Context) ComponentResult {
	start := time.Now()

	err := k.checkFunc(ctx)
	responseTime := time.Since(start)

	status := StatusHealthy
	message := "kafka connection successful"

	if err != nil {
		status = StatusUnhealthy
		message = fmt.Sprintf("kafka check failed: %v", err)
	}

	return ComponentResult{
		Name:         k.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}
