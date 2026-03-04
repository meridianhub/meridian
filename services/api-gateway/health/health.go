// Package health provides health checking capabilities for the Gateway service.
package health

import (
	"context"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
)

// DefaultCheckTimeout is the default timeout for health checks.
const DefaultCheckTimeout = 5 * time.Second

// Config contains configuration for creating a GatewayHealthChecker.
type Config struct {
	// Checkers is the list of health checkers to aggregate.
	// Typically includes database, Redis, and backend service checkers.
	Checkers []health.Checker

	// CheckTimeout is the maximum time to wait for all health checks.
	// Defaults to DefaultCheckTimeout (5s) if zero.
	CheckTimeout time.Duration
}

// GatewayHealthChecker aggregates health checks from multiple components
// for the Gateway service.
//
// Health Status Semantics:
//   - Database: Critical dependency - unhealthy if down
//   - Redis: Optional dependency - degrades gracefully
//   - Backend services: Optional dependencies - degrade gracefully
type GatewayHealthChecker struct {
	aggregator   *health.Aggregator
	checkTimeout time.Duration
}

// NewGatewayHealthChecker creates a new health checker with the provided configuration.
//
// The checker aggregates health from all provided checkers. The overall status
// follows these rules:
//   - If any component is Unhealthy: Overall is Unhealthy
//   - If any component is Degraded (and none Unhealthy): Overall is Degraded
//   - If all components are Healthy: Overall is Healthy
func NewGatewayHealthChecker(config Config) *GatewayHealthChecker {
	timeout := config.CheckTimeout
	if timeout == 0 {
		timeout = DefaultCheckTimeout
	}

	return &GatewayHealthChecker{
		aggregator:   health.NewAggregator(config.Checkers),
		checkTimeout: timeout,
	}
}

// Check performs health checks on all registered components.
// Returns a report containing the status of each component.
func (g *GatewayHealthChecker) Check(ctx context.Context) *health.Report {
	checkCtx, cancel := context.WithTimeout(ctx, g.checkTimeout)
	defer cancel()

	return g.aggregator.CheckAll(checkCtx)
}

// CheckTimeout returns the configured timeout for health checks.
func (g *GatewayHealthChecker) CheckTimeout() time.Duration {
	return g.checkTimeout
}
