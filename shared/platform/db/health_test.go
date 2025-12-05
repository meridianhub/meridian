package db

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestNewHealthChecker(t *testing.T) {
	// Create a minimal PostgresPool for testing
	// We don't need a real database connection for config tests
	pool := &PostgresPool{
		db: &sql.DB{},
	}

	t.Run("with default config", func(t *testing.T) {
		hc := NewHealthChecker(pool, nil)

		if hc.pool != pool {
			t.Error("Expected pool to be set")
		}
		if hc.checkInterval != 30*time.Second {
			t.Errorf("Expected default interval 30s, got %v", hc.checkInterval)
		}
		if hc.checkTimeout != 5*time.Second {
			t.Errorf("Expected default timeout 5s, got %v", hc.checkTimeout)
		}
		if hc.ctx == nil {
			t.Error("Expected context to be initialized")
		}
		if hc.cancel == nil {
			t.Error("Expected cancel function to be initialized")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &HealthCheckConfig{
			CheckInterval: 10 * time.Second,
			CheckTimeout:  2 * time.Second,
		}
		hc := NewHealthChecker(pool, config)

		if hc.checkInterval != 10*time.Second {
			t.Errorf("Expected interval 10s, got %v", hc.checkInterval)
		}
		if hc.checkTimeout != 2*time.Second {
			t.Errorf("Expected timeout 2s, got %v", hc.checkTimeout)
		}
	})

	t.Run("with empty config uses defaults", func(t *testing.T) {
		config := &HealthCheckConfig{}
		hc := NewHealthChecker(pool, config)

		if hc.checkInterval != 30*time.Second {
			t.Errorf("Expected default interval 30s, got %v", hc.checkInterval)
		}
		if hc.checkTimeout != 5*time.Second {
			t.Errorf("Expected default timeout 5s, got %v", hc.checkTimeout)
		}
	})
}

func TestHealthChecker_IsHealthy(t *testing.T) {
	pool := &PostgresPool{
		db: &sql.DB{},
	}
	hc := NewHealthChecker(pool, nil)

	t.Run("initially not healthy", func(t *testing.T) {
		// No checks performed yet
		if hc.IsHealthy() {
			t.Error("Expected not healthy before first check")
		}
	})

	t.Run("healthy after manual update", func(t *testing.T) {
		// Simulate a successful health check
		hc.mu.Lock()
		hc.lastCheckTime = time.Now()
		hc.lastCheckErr = nil
		hc.mu.Unlock()

		if !hc.IsHealthy() {
			t.Error("Expected healthy after successful check")
		}
	})

	t.Run("not healthy when check failed", func(t *testing.T) {
		// Simulate a failed health check
		hc.mu.Lock()
		hc.lastCheckTime = time.Now()
		hc.lastCheckErr = sql.ErrConnDone
		hc.mu.Unlock()

		if hc.IsHealthy() {
			t.Error("Expected not healthy when check failed")
		}
	})

	t.Run("not healthy when check is stale", func(t *testing.T) {
		// Simulate an old successful check (beyond 2x interval)
		hc.mu.Lock()
		hc.lastCheckTime = time.Now().Add(-65 * time.Second) // More than 2x default interval (30s)
		hc.lastCheckErr = nil
		hc.mu.Unlock()

		if hc.IsHealthy() {
			t.Error("Expected not healthy when check is stale")
		}
	})
}

func TestHealthChecker_GetLastCheckTime(t *testing.T) {
	pool := &PostgresPool{
		db: &sql.DB{},
	}
	hc := NewHealthChecker(pool, nil)

	t.Run("zero time initially", func(t *testing.T) {
		checkTime := hc.GetLastCheckTime()
		if !checkTime.IsZero() {
			t.Error("Expected zero time before first check")
		}
	})

	t.Run("returns set time", func(t *testing.T) {
		now := time.Now()
		hc.mu.Lock()
		hc.lastCheckTime = now
		hc.mu.Unlock()

		checkTime := hc.GetLastCheckTime()
		if !checkTime.Equal(now) {
			t.Errorf("Expected time %v, got %v", now, checkTime)
		}
	})
}

func TestHealthChecker_GetLastCheckError(t *testing.T) {
	pool := &PostgresPool{
		db: &sql.DB{},
	}
	hc := NewHealthChecker(pool, nil)

	t.Run("nil error initially", func(t *testing.T) {
		err := hc.GetLastCheckError()
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	t.Run("returns set error", func(t *testing.T) {
		expectedErr := sql.ErrConnDone
		hc.mu.Lock()
		hc.lastCheckErr = expectedErr
		hc.mu.Unlock()

		err := hc.GetLastCheckError()
		if !errors.Is(err, expectedErr) {
			t.Errorf("Expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestHealthChecker_Stop(t *testing.T) {
	pool := &PostgresPool{
		db: &sql.DB{},
	}
	hc := NewHealthChecker(pool, nil)

	t.Run("stop without starting is safe", func(_ *testing.T) {
		// Should not panic or hang
		hc.Stop()
	})

	t.Run("multiple stops are safe", func(_ *testing.T) {
		hc.Stop()
		hc.Stop() // Should not panic
	})
}

func TestPoolStats(t *testing.T) {
	t.Run("PoolStats structure", func(t *testing.T) {
		stats := PoolStats{
			MaxOpenConnections: 50,
			OpenConnections:    10,
			InUse:              5,
			Idle:               5,
			WaitCount:          100,
			WaitDuration:       1 * time.Second,
			MaxIdleClosed:      10,
			MaxIdleTimeClosed:  5,
			MaxLifetimeClosed:  3,
		}

		if stats.MaxOpenConnections != 50 {
			t.Errorf("Expected MaxOpenConnections=50, got %d", stats.MaxOpenConnections)
		}
		if stats.OpenConnections != 10 {
			t.Errorf("Expected OpenConnections=10, got %d", stats.OpenConnections)
		}
		if stats.InUse != 5 {
			t.Errorf("Expected InUse=5, got %d", stats.InUse)
		}
		if stats.Idle != 5 {
			t.Errorf("Expected Idle=5, got %d", stats.Idle)
		}
	})
}

func TestHealthCheckConfig_Defaults(t *testing.T) {
	t.Run("default values applied", func(t *testing.T) {
		pool := &PostgresPool{
			db: &sql.DB{},
		}
		hc := NewHealthChecker(pool, nil)

		// Verify defaults match documentation
		if hc.checkInterval != 30*time.Second {
			t.Errorf("Default CheckInterval should be 30s, got %v", hc.checkInterval)
		}
		if hc.checkTimeout != 5*time.Second {
			t.Errorf("Default CheckTimeout should be 5s, got %v", hc.checkTimeout)
		}
	})

	t.Run("custom values override defaults", func(t *testing.T) {
		pool := &PostgresPool{
			db: &sql.DB{},
		}
		config := &HealthCheckConfig{
			CheckInterval: 1 * time.Minute,
			CheckTimeout:  10 * time.Second,
		}
		hc := NewHealthChecker(pool, config)

		if hc.checkInterval != 1*time.Minute {
			t.Errorf("Custom CheckInterval not applied, got %v", hc.checkInterval)
		}
		if hc.checkTimeout != 10*time.Second {
			t.Errorf("Custom CheckTimeout not applied, got %v", hc.checkTimeout)
		}
	})
}
