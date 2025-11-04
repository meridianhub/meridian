package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestStatus_String verifies status string representations
func TestStatus_String(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusHealthy, "healthy"},
		{StatusDegraded, "degraded"},
		{StatusUnhealthy, "unhealthy"},
		{StatusUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("Status.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestComponentResult_Structure verifies component result can be constructed
func TestComponentResult_Structure(t *testing.T) {
	now := time.Now()
	result := ComponentResult{
		Name:         "database",
		Status:       StatusHealthy,
		Message:      "connection successful",
		ResponseTime: 15 * time.Millisecond,
		CheckedAt:    now,
		Error:        nil,
	}

	if result.Name != "database" {
		t.Errorf("Name = %v, want database", result.Name)
	}
	if result.Status != StatusHealthy {
		t.Errorf("Status = %v, want %v", result.Status, StatusHealthy)
	}
	if result.ResponseTime != 15*time.Millisecond {
		t.Errorf("ResponseTime = %v, want %v", result.ResponseTime, 15*time.Millisecond)
	}
}

// TestReport_OverallStatus tests aggregated health status logic
func TestReport_OverallStatus(t *testing.T) {
	tests := []struct {
		name       string
		components []ComponentResult
		want       Status
	}{
		{
			name: "all healthy",
			components: []ComponentResult{
				{Name: "db", Status: StatusHealthy},
				{Name: "kafka", Status: StatusHealthy},
				{Name: "redis", Status: StatusHealthy},
			},
			want: StatusHealthy,
		},
		{
			name: "one degraded makes overall degraded",
			components: []ComponentResult{
				{Name: "db", Status: StatusHealthy},
				{Name: "kafka", Status: StatusDegraded},
				{Name: "redis", Status: StatusHealthy},
			},
			want: StatusDegraded,
		},
		{
			name: "any unhealthy makes overall unhealthy",
			components: []ComponentResult{
				{Name: "db", Status: StatusHealthy},
				{Name: "kafka", Status: StatusDegraded},
				{Name: "redis", Status: StatusUnhealthy},
			},
			want: StatusUnhealthy,
		},
		{
			name: "unknown status is treated as unhealthy",
			components: []ComponentResult{
				{Name: "db", Status: StatusUnknown},
			},
			want: StatusUnhealthy,
		},
		{
			name:       "no components is healthy",
			components: []ComponentResult{},
			want:       StatusHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := &Report{
				Components: tt.components,
			}
			got := report.OverallStatus()
			if got != tt.want {
				t.Errorf("OverallStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Mock checker for testing
type mockChecker struct {
	name         string
	returnStatus Status
	returnError  error
	checkDelay   time.Duration
}

func (m *mockChecker) Name() string {
	return m.name
}

func (m *mockChecker) Check(ctx context.Context) ComponentResult {
	if m.checkDelay > 0 {
		select {
		case <-time.After(m.checkDelay):
		case <-ctx.Done():
			return ComponentResult{
				Name:      m.name,
				Status:    StatusUnhealthy,
				Message:   "check cancelled",
				Error:     ctx.Err(),
				CheckedAt: time.Now(),
			}
		}
	}

	return ComponentResult{
		Name:         m.name,
		Status:       m.returnStatus,
		Message:      "mock check",
		ResponseTime: m.checkDelay,
		CheckedAt:    time.Now(),
		Error:        m.returnError,
	}
}

// TestAggregator_CheckAll tests checking all components
func TestAggregator_CheckAll(t *testing.T) {
	checkers := []Checker{
		&mockChecker{name: "database", returnStatus: StatusHealthy},
		&mockChecker{name: "kafka", returnStatus: StatusHealthy},
		&mockChecker{name: "redis", returnStatus: StatusDegraded},
	}

	agg := NewAggregator(checkers)
	report := agg.CheckAll(context.Background())

	if len(report.Components) != 3 {
		t.Errorf("Expected 3 components, got %d", len(report.Components))
	}

	if report.OverallStatus() != StatusDegraded {
		t.Errorf("OverallStatus() = %v, want %v", report.OverallStatus(), StatusDegraded)
	}
}

// TestAggregator_CheckAll_ContextCancellation tests cancellation handling
func TestAggregator_CheckAll_ContextCancellation(t *testing.T) {
	checkers := []Checker{
		&mockChecker{name: "slow", returnStatus: StatusHealthy, checkDelay: 5 * time.Second},
	}

	agg := NewAggregator(checkers)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	report := agg.CheckAll(ctx)

	if len(report.Components) != 1 {
		t.Fatalf("Expected 1 component, got %d", len(report.Components))
	}

	// Check should have been cancelled
	if report.Components[0].Status != StatusUnhealthy {
		t.Errorf("Expected unhealthy status due to cancellation, got %v", report.Components[0].Status)
	}
	if !errors.Is(report.Components[0].Error, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", report.Components[0].Error)
	}
}

// TestAggregator_CheckAll_Empty tests empty aggregator
func TestAggregator_CheckAll_Empty(t *testing.T) {
	agg := NewAggregator(nil)
	report := agg.CheckAll(context.Background())

	if len(report.Components) != 0 {
		t.Errorf("Expected 0 components, got %d", len(report.Components))
	}
	if report.OverallStatus() != StatusHealthy {
		t.Errorf("Empty aggregator should be healthy, got %v", report.OverallStatus())
	}
}

// TestAggregator_CheckByName tests checking specific component
func TestAggregator_CheckByName(t *testing.T) {
	checkers := []Checker{
		&mockChecker{name: "database", returnStatus: StatusHealthy},
		&mockChecker{name: "kafka", returnStatus: StatusDegraded},
	}

	agg := NewAggregator(checkers)

	// Check existing component
	result, found := agg.CheckByName(context.Background(), "kafka")
	if !found {
		t.Fatal("Expected to find kafka component")
	}
	if result.Name != "kafka" {
		t.Errorf("Name = %v, want kafka", result.Name)
	}
	if result.Status != StatusDegraded {
		t.Errorf("Status = %v, want %v", result.Status, StatusDegraded)
	}

	// Check non-existent component
	_, found = agg.CheckByName(context.Background(), "nonexistent")
	if found {
		t.Error("Should not find nonexistent component")
	}
}

// TestAggregator_Concurrent tests concurrent health checks
func TestAggregator_Concurrent(t *testing.T) {
	checkers := []Checker{
		&mockChecker{name: "comp1", returnStatus: StatusHealthy, checkDelay: 50 * time.Millisecond},
		&mockChecker{name: "comp2", returnStatus: StatusHealthy, checkDelay: 50 * time.Millisecond},
		&mockChecker{name: "comp3", returnStatus: StatusHealthy, checkDelay: 50 * time.Millisecond},
	}

	agg := NewAggregator(checkers)

	start := time.Now()
	report := agg.CheckAll(context.Background())
	elapsed := time.Since(start)

	// If run sequentially, would take 150ms+
	// If run concurrently, should take ~50ms
	if elapsed > 100*time.Millisecond {
		t.Errorf("CheckAll took %v, expected concurrent execution (~50ms)", elapsed)
	}

	if len(report.Components) != 3 {
		t.Errorf("Expected 3 components, got %d", len(report.Components))
	}
}
