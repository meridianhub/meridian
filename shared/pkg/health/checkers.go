package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/redis/go-redis/v9"
)

// ErrUnexpectedStatusCode is returned when an HTTP health check receives a non-2xx status code.
var ErrUnexpectedStatusCode = errors.New("unexpected HTTP status code")

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
	return "database"
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
	return "kafka"
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

// HTTPChecker checks the health of an HTTP endpoint.
type HTTPChecker struct {
	name       string
	endpoint   string
	httpClient *http.Client
	timeout    time.Duration
}

// HTTPCheckerConfig configures an HTTP health checker.
type HTTPCheckerConfig struct {
	// Name is the component name (e.g., "ecb-api", "external-service")
	Name string
	// Endpoint is the URL to check
	Endpoint string
	// Timeout is the maximum duration for the health check request
	Timeout time.Duration
	// HTTPClient is an optional custom HTTP client (uses default if nil)
	HTTPClient *http.Client
}

// NewHTTPChecker creates a new HTTP health checker.
// Performs a HEAD request to the endpoint to verify connectivity.
func NewHTTPChecker(cfg HTTPCheckerConfig) *HTTPChecker {
	if cfg.Name == "" {
		panic("NewHTTPChecker: name cannot be empty")
	}
	if cfg.Endpoint == "" {
		panic("NewHTTPChecker: endpoint cannot be empty")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second // Default health check timeout
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}

	return &HTTPChecker{
		name:       cfg.Name,
		endpoint:   cfg.Endpoint,
		httpClient: client,
		timeout:    cfg.Timeout,
	}
}

// Name returns the component name.
func (h *HTTPChecker) Name() string {
	return h.name
}

// Check performs an HTTP health check by sending a HEAD request to the endpoint.
// Returns healthy if the endpoint responds with any 2xx status code.
func (h *HTTPChecker) Check(ctx context.Context) ComponentResult {
	start := time.Now()

	// Create context with timeout for the health check
	checkCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, http.MethodHead, h.endpoint, nil)
	if err != nil {
		return ComponentResult{
			Name:         h.name,
			Status:       StatusUnhealthy,
			Message:      fmt.Sprintf("failed to create HTTP request: %v", err),
			ResponseTime: time.Since(start),
			CheckedAt:    start,
			Error:        err,
		}
	}

	resp, err := h.httpClient.Do(req)
	responseTime := time.Since(start)

	if err != nil {
		return ComponentResult{
			Name:         h.name,
			Status:       StatusUnhealthy,
			Message:      fmt.Sprintf("HTTP request failed: %v", err),
			ResponseTime: responseTime,
			CheckedAt:    start,
			Error:        err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	status := StatusHealthy
	message := fmt.Sprintf("HTTP endpoint accessible (status %d)", resp.StatusCode)

	// Accept any 2xx status code as healthy
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status = StatusUnhealthy
		message = fmt.Sprintf("HTTP endpoint returned non-success status: %d", resp.StatusCode)
		err = fmt.Errorf("%w: %d", ErrUnexpectedStatusCode, resp.StatusCode)
	}

	return ComponentResult{
		Name:         h.name,
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}
