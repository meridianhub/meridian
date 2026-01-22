package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
)

// Test error sentinels
var (
	errHealthConnectionRefused = errors.New("connection refused")
	errHealthConnectionTimeout = errors.New("connection timeout")
)

// mockChecker implements health.Checker for testing.
type mockChecker struct {
	name   string
	status health.Status
	err    error
}

func (m *mockChecker) Name() string {
	return m.name
}

func (m *mockChecker) Check(_ context.Context) health.ComponentResult {
	message := m.name + " check successful"
	if m.err != nil {
		message = m.name + " check failed: " + m.err.Error()
	}
	return health.ComponentResult{
		Name:         m.name,
		Status:       m.status,
		Message:      message,
		ResponseTime: 10 * time.Millisecond,
		CheckedAt:    time.Now(),
		Error:        m.err,
	}
}

func TestGatewayHealthChecker_AllHealthy(t *testing.T) {
	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusHealthy},
		&mockChecker{name: "redis", status: health.StatusHealthy},
		&mockChecker{name: "backend-1", status: health.StatusHealthy},
	}

	checker := NewGatewayHealthChecker(Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	report := checker.Check(context.Background())

	assert.Equal(t, health.StatusHealthy, report.OverallStatus())
	assert.Len(t, report.Components, 3)
}

func TestGatewayHealthChecker_DatabaseUnhealthy(t *testing.T) {
	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusUnhealthy, err: errHealthConnectionRefused},
		&mockChecker{name: "redis", status: health.StatusHealthy},
		&mockChecker{name: "backend-1", status: health.StatusHealthy},
	}

	checker := NewGatewayHealthChecker(Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	report := checker.Check(context.Background())

	assert.Equal(t, health.StatusUnhealthy, report.OverallStatus(), "database unhealthy should make overall unhealthy")
}

func TestGatewayHealthChecker_RedisDegraded(t *testing.T) {
	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusHealthy},
		&mockChecker{name: "redis", status: health.StatusDegraded, err: errHealthConnectionTimeout},
	}

	checker := NewGatewayHealthChecker(Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	report := checker.Check(context.Background())

	assert.Equal(t, health.StatusDegraded, report.OverallStatus(), "redis degraded should make overall degraded")
}

func TestGatewayHealthChecker_BackendDegraded(t *testing.T) {
	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusHealthy},
		&mockChecker{name: "party-service", status: health.StatusDegraded},
	}

	checker := NewGatewayHealthChecker(Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	report := checker.Check(context.Background())

	assert.Equal(t, health.StatusDegraded, report.OverallStatus(), "backend degraded should make overall degraded")
}

func TestGatewayHealthChecker_NoCheckers(t *testing.T) {
	checker := NewGatewayHealthChecker(Config{
		Checkers:     []health.Checker{},
		CheckTimeout: 5 * time.Second,
	})

	report := checker.Check(context.Background())

	assert.Equal(t, health.StatusHealthy, report.OverallStatus(), "empty checkers should be healthy")
	assert.Empty(t, report.Components)
}

func TestGatewayHealthChecker_CheckTimeout(t *testing.T) {
	checker := NewGatewayHealthChecker(Config{
		Checkers:     []health.Checker{&mockChecker{name: "database", status: health.StatusHealthy}},
		CheckTimeout: 5 * time.Second,
	})

	assert.Equal(t, 5*time.Second, checker.CheckTimeout())
}

func TestGatewayHealthChecker_DefaultTimeout(t *testing.T) {
	checker := NewGatewayHealthChecker(Config{
		Checkers: []health.Checker{&mockChecker{name: "database", status: health.StatusHealthy}},
	})

	assert.Equal(t, DefaultCheckTimeout, checker.CheckTimeout())
}
