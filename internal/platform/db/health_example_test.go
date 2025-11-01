package db_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/meridianhub/meridian/internal/platform/db"
)

// ExampleHealthChecker demonstrates basic health check usage.
func ExampleHealthChecker() {
	// Create database pool
	cfg := db.DefaultConfig("postgresql://user:pass@localhost:5432/mydb")
	pool, err := db.NewPostgresPool(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	// Create health checker with custom config
	healthConfig := &db.HealthCheckConfig{
		CheckInterval: 10 * time.Second, // Check every 10 seconds
		CheckTimeout:  2 * time.Second,  // Each check times out after 2 seconds
	}
	healthChecker := db.NewHealthChecker(pool, healthConfig)

	// Start periodic background checks
	go healthChecker.PeriodicHealthCheck()
	defer healthChecker.Stop()

	// Check health status for readiness probe
	if healthChecker.IsHealthy() {
		fmt.Println("Database is healthy")
	} else {
		fmt.Println("Database is not healthy")
		if err := healthChecker.GetLastCheckError(); err != nil {
			fmt.Printf("Last check error: %v\n", err)
		}
	}

	// Get connection pool statistics
	stats := healthChecker.GetStats()
	fmt.Printf("Pool stats: %d/%d connections in use\n", stats.InUse, stats.MaxOpenConnections)
}

// ExampleHealthChecker_Check demonstrates synchronous health checks for liveness probes.
func ExampleHealthChecker_Check() {
	cfg := db.DefaultConfig("postgresql://user:pass@localhost:5432/mydb")
	pool, err := db.NewPostgresPool(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	healthChecker := db.NewHealthChecker(pool, nil)

	// Perform on-demand health check with timeout (e.g., for HTTP health endpoint)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := healthChecker.Check(ctx); err != nil {
		log.Printf("Health check failed: %v", err)
		// Return 503 Service Unavailable
	} else {
		// Return 200 OK
		fmt.Println("Health check passed")
	}
}

// ExamplePostgresPool_CloseWithContext demonstrates graceful shutdown with timeout.
func ExamplePostgresPool_CloseWithContext() {
	cfg := db.DefaultConfig("postgresql://user:pass@localhost:5432/mydb")
	pool, err := db.NewPostgresPool(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}

	// ... application logic ...

	// Graceful shutdown with 30 second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := pool.CloseWithContext(ctx); err != nil {
		log.Printf("WARN: Pool did not close cleanly: %v", err)
	} else {
		fmt.Println("Pool closed successfully")
	}
}

// ExamplePostgresPool_DrainConnections demonstrates draining active connections before shutdown.
func ExamplePostgresPool_DrainConnections() {
	cfg := db.DefaultConfig("postgresql://user:pass@localhost:5432/mydb")
	pool, err := db.NewPostgresPool(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	// ... application is running ...

	// On shutdown signal, stop accepting new requests and drain connections
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for active queries to complete
	pollInterval := 500 * time.Millisecond
	if err := pool.DrainConnections(shutdownCtx, pollInterval); err != nil {
		log.Printf("WARN: Could not drain all connections: %v", err)
		// Some queries still running - force close after timeout
	} else {
		log.Println("All connections drained successfully")
	}

	// Now safe to close the pool
	pool.Close()
}

// ExampleHealthChecker_GetStats demonstrates monitoring pool utilization.
func ExampleHealthChecker_GetStats() {
	cfg := db.DefaultConfig("postgresql://user:pass@localhost:5432/mydb")
	pool, err := db.NewPostgresPool(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	healthChecker := db.NewHealthChecker(pool, nil)

	// Periodically log pool statistics for monitoring
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := healthChecker.GetStats()
		log.Printf("Connection pool stats: open=%d in_use=%d idle=%d wait_count=%d wait_duration=%v",
			stats.OpenConnections,
			stats.InUse,
			stats.Idle,
			stats.WaitCount,
			stats.WaitDuration,
		)

		// Alert if pool is saturated
		if stats.InUse >= stats.MaxOpenConnections {
			log.Println("WARN: Connection pool is saturated, consider increasing MaxOpenConnections")
		}

		// Alert if too many waits
		if stats.WaitCount > 1000 {
			log.Printf("WARN: High wait count (%d), pool may be undersized", stats.WaitCount)
		}
	}
}

// ExampleHealthChecker_kubernetes demonstrates Kubernetes health endpoints.
func ExampleHealthChecker_kubernetes() {
	// This is a conceptual example showing how to integrate with Kubernetes probes

	cfg := db.DefaultConfig("postgresql://user:pass@localhost:5432/mydb")
	pool, err := db.NewPostgresPool(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	healthChecker := db.NewHealthChecker(pool, nil)
	go healthChecker.PeriodicHealthCheck()
	defer healthChecker.Stop()

	// Readiness probe: /health/ready
	// Returns 200 if healthy, 503 if not
	handleReadiness := func() int {
		if healthChecker.IsHealthy() {
			return 200 // OK
		}
		return 503 // Service Unavailable
	}

	// Liveness probe: /health/live
	// Returns 200 if can ping database, 503 if not
	handleLiveness := func() int {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := healthChecker.Check(ctx); err != nil {
			log.Printf("Liveness check failed: %v", err)
			return 503 // Service Unavailable
		}
		return 200 // OK
	}

	_ = handleReadiness
	_ = handleLiveness
	// In real code, these would be HTTP handlers
}
