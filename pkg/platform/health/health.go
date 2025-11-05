// Package health provides health checking capabilities for services and their dependencies.
package health

import (
	"context"
	"sync"
	"time"
)

// Status represents the health status of a component.
type Status int

const (
	// StatusHealthy indicates the component is fully operational
	StatusHealthy Status = iota
	// StatusDegraded indicates the component is operational but with reduced functionality
	StatusDegraded
	// StatusUnhealthy indicates the component is not operational
	StatusUnhealthy
	// StatusUnknown indicates the component's health status cannot be determined
	StatusUnknown
)

// String returns the string representation of a health status.
func (s Status) String() string {
	switch s {
	case StatusHealthy:
		return "healthy"
	case StatusDegraded:
		return "degraded"
	case StatusUnhealthy:
		return "unhealthy"
	case StatusUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// ComponentResult represents the health check result for a single component.
type ComponentResult struct {
	// Name is the component identifier (e.g., "database", "kafka", "redis")
	Name string
	// Status is the health status of this component
	Status Status
	// Message provides human-readable details about the health check
	Message string
	// ResponseTime is how long the health check took to execute
	ResponseTime time.Duration
	// CheckedAt is when this health check was performed
	CheckedAt time.Time
	// Error contains the error if the check failed (nil if successful)
	Error error
}

// Report aggregates health check results from multiple components.
type Report struct {
	// Components contains individual health check results
	Components []ComponentResult
	// CheckedAt is when this overall health check was performed
	CheckedAt time.Time
}

// OverallStatus determines the overall health status based on component statuses.
// Logic:
// - If any component is Unhealthy or Unknown, overall is Unhealthy
// - If any component is Degraded (and none Unhealthy), overall is Degraded
// - If all components are Healthy, overall is Healthy
// - If no components exist, overall is Healthy (empty system is healthy)
func (r *Report) OverallStatus() Status {
	if len(r.Components) == 0 {
		return StatusHealthy
	}

	hasUnhealthy := false
	hasDegraded := false

	for _, comp := range r.Components {
		switch comp.Status {
		case StatusUnhealthy, StatusUnknown:
			hasUnhealthy = true
		case StatusDegraded:
			hasDegraded = true
		case StatusHealthy:
			// Healthy component, continue
		}
	}

	if hasUnhealthy {
		return StatusUnhealthy
	}
	if hasDegraded {
		return StatusDegraded
	}
	return StatusHealthy
}

// Checker defines the interface for a health check component.
type Checker interface {
	// Name returns the component name
	Name() string
	// Check performs a health check and returns the result
	Check(ctx context.Context) ComponentResult
}

// Aggregator collects health checks from multiple components.
type Aggregator struct {
	checkers []Checker
}

// NewAggregator creates a new health check aggregator.
func NewAggregator(checkers []Checker) *Aggregator {
	return &Aggregator{
		checkers: checkers,
	}
}

// CheckAll performs health checks on all registered components concurrently.
// Each check runs in its own goroutine with the provided context for cancellation.
func (a *Aggregator) CheckAll(ctx context.Context) *Report {
	if len(a.checkers) == 0 {
		return &Report{
			Components: []ComponentResult{},
			CheckedAt:  time.Now(),
		}
	}

	results := make([]ComponentResult, len(a.checkers))
	var wg sync.WaitGroup

	for i, checker := range a.checkers {
		wg.Add(1)
		go func(index int, c Checker) {
			defer wg.Done()
			results[index] = c.Check(ctx)
		}(i, checker)
	}

	wg.Wait()

	return &Report{
		Components: results,
		CheckedAt:  time.Now(),
	}
}

// CheckByName performs a health check on a specific named component.
// Returns the result and true if found, or an empty result and false if not found.
func (a *Aggregator) CheckByName(ctx context.Context, name string) (ComponentResult, bool) {
	for _, checker := range a.checkers {
		if checker.Name() == name {
			return checker.Check(ctx), true
		}
	}
	return ComponentResult{}, false
}
