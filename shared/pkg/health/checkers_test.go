package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	componentNameDatabase = "database"
	componentNameRedis    = "redis"
	componentNameKafka    = "kafka"
)

// TestDatabaseChecker_Name verifies the checker name
func TestDatabaseChecker_Name(t *testing.T) {
	// Use a minimal pool for testing Name() - Name() doesn't access the pool
	pool := &db.PostgresPool{}
	checker := NewDatabaseChecker(pool)
	if checker.Name() != componentNameDatabase {
		t.Errorf("Name() = %v, want database", checker.Name())
	}
}

// TestDatabaseChecker_Check_Healthy tests successful database check
func TestDatabaseChecker_Check_Healthy(t *testing.T) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("Failed to start postgres container: %v", err)
	}
	defer func() {
		_ = testcontainers.TerminateContainer(pgContainer)
	}()

	connStr, err := pgContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	pool, err := db.NewPostgresPool(ctx, &db.Config{
		ConnectionString: connStr,
	})
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	checker := NewDatabaseChecker(pool)
	result := checker.Check(ctx)

	if result.Name != componentNameDatabase {
		t.Errorf("Name = %v, want database", result.Name)
	}
	if result.Status != StatusHealthy {
		t.Errorf("Status = %v, want %v (error: %v)", result.Status, StatusHealthy, result.Error)
	}
	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}
	if result.ResponseTime <= 0 {
		t.Error("ResponseTime should be > 0")
	}
}

// TestDatabaseChecker_Check_Unhealthy tests failed database check
func TestDatabaseChecker_Check_Unhealthy(t *testing.T) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("Failed to start postgres container: %v", err)
	}
	defer func() {
		_ = testcontainers.TerminateContainer(pgContainer)
	}()

	connStr, err := pgContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	pool, err := db.NewPostgresPool(ctx, &db.Config{
		ConnectionString: connStr,
	})
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	checker := NewDatabaseChecker(pool)

	// Now stop the container to make the check unhealthy
	_ = testcontainers.TerminateContainer(pgContainer)

	// Use short timeout to fail fast
	checkCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	result := checker.Check(checkCtx)

	if result.Status != StatusUnhealthy {
		t.Errorf("Status = %v, want %v", result.Status, StatusUnhealthy)
	}
	if result.Error == nil {
		t.Error("Error should not be nil for unhealthy database")
	}
}

// TestRedisChecker_Name verifies the checker name
func TestRedisChecker_Name(t *testing.T) {
	// Use miniredis for a lightweight valid instance
	mr := miniredis.RunT(t)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()
	checker := NewRedisChecker(client)
	if checker.Name() != componentNameRedis {
		t.Errorf("Name() = %v, want redis", checker.Name())
	}
}

// TestRedisChecker_Check_Healthy tests successful Redis check
func TestRedisChecker_Check_Healthy(t *testing.T) {
	// Start miniredis server
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = client.Close() }()

	checker := NewRedisChecker(client)
	result := checker.Check(context.Background())

	if result.Name != componentNameRedis {
		t.Errorf("Name = %v, want redis", result.Name)
	}
	if result.Status != StatusHealthy {
		t.Errorf("Status = %v, want %v (error: %v)", result.Status, StatusHealthy, result.Error)
	}
	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}
	if result.ResponseTime <= 0 {
		t.Error("ResponseTime should be > 0")
	}
}

// TestRedisChecker_Check_Unhealthy tests failed Redis check
func TestRedisChecker_Check_Unhealthy(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:9999", // Invalid address
	})
	defer func() { _ = client.Close() }()

	checker := NewRedisChecker(client)

	// Use short timeout to fail fast
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := checker.Check(ctx)

	if result.Status != StatusUnhealthy {
		t.Errorf("Status = %v, want %v", result.Status, StatusUnhealthy)
	}
	if result.Error == nil {
		t.Error("Error should not be nil for unhealthy Redis")
	}
}

// TestKafkaChecker_Name verifies the checker name
func TestKafkaChecker_Name(t *testing.T) {
	// Use a no-op check function for the Name test
	checkFunc := func(_ context.Context) error { return nil }
	checker := NewKafkaChecker(checkFunc)
	if checker.Name() != componentNameKafka {
		t.Errorf("Name() = %v, want kafka", checker.Name())
	}
}

// TestKafkaChecker_Check_Healthy tests successful Kafka check
func TestKafkaChecker_Check_Healthy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Kafka integration test in short mode")
	}

	ctx := context.Background()

	kafkaContainer, err := kafka.Run(ctx,
		"confluentinc/confluent-local:7.5.0",
		kafka.WithClusterID("test-cluster"),
	)
	if err != nil {
		t.Fatalf("Failed to start Kafka container: %v", err)
	}
	defer func() {
		_ = testcontainers.TerminateContainer(kafkaContainer)
	}()

	brokers, err := kafkaContainer.Brokers(ctx)
	if err != nil {
		t.Fatalf("Failed to get brokers: %v", err)
	}

	// Create a simple check function that returns nil (healthy)
	errNoBrokers := errors.New("no brokers available") //nolint:err113 // test error
	checkFunc := func(_ context.Context) error {
		// In real implementation, this would check Kafka connectivity
		// For now, just verify we can reach it
		if len(brokers) == 0 {
			return errNoBrokers
		}
		return nil
	}

	checker := NewKafkaChecker(checkFunc)
	result := checker.Check(ctx)

	if result.Name != componentNameKafka {
		t.Errorf("Name = %v, want kafka", result.Name)
	}
	if result.Status != StatusHealthy {
		t.Errorf("Status = %v, want %v (error: %v)", result.Status, StatusHealthy, result.Error)
	}
	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}
}

// TestKafkaChecker_Check_Unhealthy tests failed Kafka check
func TestKafkaChecker_Check_Unhealthy(t *testing.T) {
	errKafkaFailed := errors.New("kafka connection failed") //nolint:err113 // test error
	checkFunc := func(_ context.Context) error {
		return errKafkaFailed
	}

	checker := NewKafkaChecker(checkFunc)
	result := checker.Check(context.Background())

	if result.Status != StatusUnhealthy {
		t.Errorf("Status = %v, want %v", result.Status, StatusUnhealthy)
	}
	if result.Error == nil {
		t.Error("Error should not be nil for unhealthy Kafka")
	}
}

const componentNameHTTP = "http-test"

// TestNewHTTPChecker_PanicsEmptyName verifies panic on empty name
func TestNewHTTPChecker_PanicsEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for empty name")
		}
	}()
	NewHTTPChecker(HTTPCheckerConfig{
		Name:     "",
		Endpoint: "http://example.com",
	})
}

// TestNewHTTPChecker_PanicsEmptyEndpoint verifies panic on empty endpoint
func TestNewHTTPChecker_PanicsEmptyEndpoint(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for empty endpoint")
		}
	}()
	NewHTTPChecker(HTTPCheckerConfig{
		Name:     "test",
		Endpoint: "",
	})
}

// TestHTTPChecker_Name verifies the checker name
func TestHTTPChecker_Name(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewHTTPChecker(HTTPCheckerConfig{
		Name:     componentNameHTTP,
		Endpoint: server.URL,
	})
	if checker.Name() != componentNameHTTP {
		t.Errorf("Name() = %v, want %s", checker.Name(), componentNameHTTP)
	}
}

// TestHTTPChecker_Check_Healthy tests successful HTTP check with 200 OK
func TestHTTPChecker_Check_Healthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("Expected HEAD request, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewHTTPChecker(HTTPCheckerConfig{
		Name:     componentNameHTTP,
		Endpoint: server.URL,
		Timeout:  5 * time.Second,
	})

	result := checker.Check(context.Background())

	if result.Name != componentNameHTTP {
		t.Errorf("Name = %v, want %s", result.Name, componentNameHTTP)
	}
	if result.Status != StatusHealthy {
		t.Errorf("Status = %v, want %v (error: %v)", result.Status, StatusHealthy, result.Error)
	}
	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}
	if result.ResponseTime <= 0 {
		t.Error("ResponseTime should be > 0")
	}
}

// TestHTTPChecker_Check_Healthy2xx tests various 2xx status codes
func TestHTTPChecker_Check_Healthy2xx(t *testing.T) {
	statusCodes := []int{200, 201, 202, 204, 299}

	for _, code := range statusCodes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer server.Close()

			checker := NewHTTPChecker(HTTPCheckerConfig{
				Name:     componentNameHTTP,
				Endpoint: server.URL,
			})

			result := checker.Check(context.Background())

			if result.Status != StatusHealthy {
				t.Errorf("Status = %v, want %v for status code %d", result.Status, StatusHealthy, code)
			}
		})
	}
}

// TestHTTPChecker_Check_UnhealthyStatusCode tests non-2xx status codes
func TestHTTPChecker_Check_UnhealthyStatusCode(t *testing.T) {
	statusCodes := []int{400, 404, 500, 503}

	for _, code := range statusCodes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer server.Close()

			checker := NewHTTPChecker(HTTPCheckerConfig{
				Name:     componentNameHTTP,
				Endpoint: server.URL,
			})

			result := checker.Check(context.Background())

			if result.Status != StatusUnhealthy {
				t.Errorf("Status = %v, want %v for status code %d", result.Status, StatusUnhealthy, code)
			}
			if result.Error == nil {
				t.Error("Error should not be nil for non-2xx status")
			}
		})
	}
}

// TestHTTPChecker_Check_Timeout tests timeout behavior
func TestHTTPChecker_Check_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		//nolint:forbidigo // simulates slow HTTP handler to trigger client timeout
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewHTTPChecker(HTTPCheckerConfig{
		Name:     componentNameHTTP,
		Endpoint: server.URL,
		Timeout:  50 * time.Millisecond,
	})

	result := checker.Check(context.Background())

	if result.Status != StatusUnhealthy {
		t.Errorf("Status = %v, want %v", result.Status, StatusUnhealthy)
	}
	if result.Error == nil {
		t.Error("Error should not be nil for timeout")
	}
}

// TestHTTPChecker_Check_NetworkError tests network error handling
func TestHTTPChecker_Check_NetworkError(t *testing.T) {
	// Use non-routable IP to simulate network failure
	checker := NewHTTPChecker(HTTPCheckerConfig{
		Name:     componentNameHTTP,
		Endpoint: "http://192.0.2.1:9999", // TEST-NET-1, guaranteed to fail
		Timeout:  1 * time.Second,
	})

	result := checker.Check(context.Background())

	if result.Status != StatusUnhealthy {
		t.Errorf("Status = %v, want %v", result.Status, StatusUnhealthy)
	}
	if result.Error == nil {
		t.Error("Error should not be nil for network error")
	}
}

// TestHTTPChecker_Check_CustomHTTPClient tests using a custom HTTP client
func TestHTTPChecker_Check_CustomHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	customClient := &http.Client{Timeout: 10 * time.Second}

	checker := NewHTTPChecker(HTTPCheckerConfig{
		Name:       componentNameHTTP,
		Endpoint:   server.URL,
		HTTPClient: customClient,
	})

	result := checker.Check(context.Background())

	if result.Status != StatusHealthy {
		t.Errorf("Status = %v, want %v", result.Status, StatusHealthy)
	}
}

// TestCheckers_Integration tests all checkers together
func TestCheckers_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Setup PostgreSQL
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("Failed to start postgres: %v", err)
	}
	defer func() {
		_ = testcontainers.TerminateContainer(pgContainer)
	}()

	connStr, err := pgContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	pool, err := db.NewPostgresPool(ctx, &db.Config{
		ConnectionString: connStr,
	})
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Setup Redis
	mr := miniredis.RunT(t)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = redisClient.Close() }()

	// Setup Kafka
	kafkaCheckFunc := func(_ context.Context) error {
		return nil // Healthy
	}

	// Create aggregator with all checkers
	checkers := []Checker{
		NewDatabaseChecker(pool),
		NewRedisChecker(redisClient),
		NewKafkaChecker(kafkaCheckFunc),
	}

	agg := NewAggregator(checkers)
	report := agg.CheckAll(ctx)

	if len(report.Components) != 3 {
		t.Errorf("Expected 3 components, got %d", len(report.Components))
	}

	if report.OverallStatus() != StatusHealthy {
		t.Errorf("OverallStatus() = %v, want %v", report.OverallStatus(), StatusHealthy)
		for _, comp := range report.Components {
			t.Logf("  %s: %v (error: %v)", comp.Name, comp.Status, comp.Error)
		}
	}
}
