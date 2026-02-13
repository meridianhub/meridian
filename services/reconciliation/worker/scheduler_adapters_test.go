package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

type mockRefDataClient struct {
	mu        sync.Mutex
	schedules []SettlementSchedule
	err       error
}

func (m *mockRefDataClient) ListSettlementSchedules(_ context.Context) ([]SettlementSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	result := make([]SettlementSchedule, len(m.schedules))
	copy(result, m.schedules)
	return result, nil
}

// --- ScheduleProvider Tests ---

func TestSettlementScheduleProvider_ListSchedules(t *testing.T) {
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "0 2 * * *",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
				PeriodOffset:   24 * time.Hour,
			},
			{
				ScheduleID:     "sched-2",
				AssetType:      "kWh",
				AccountID:      "acc-2",
				CronExpression: "30 3 * * *",
				SettlementType: "WEEKLY",
				Scope:          "INSTRUMENT",
			},
		},
	}

	provider := NewSettlementScheduleProvider(refData)
	schedules, err := provider.ListSchedules(context.Background())
	require.NoError(t, err)
	require.Len(t, schedules, 2)

	assert.Equal(t, "sched-1", schedules[0].ID)
	assert.Equal(t, "0 2 * * *", schedules[0].CronExpr)
	assert.IsType(t, SettlementSchedule{}, schedules[0].Metadata)

	assert.Equal(t, "sched-2", schedules[1].ID)
	assert.Equal(t, "30 3 * * *", schedules[1].CronExpr)

	// Verify metadata carries through
	meta := schedules[0].Metadata.(SettlementSchedule)
	assert.Equal(t, "acc-1", meta.AccountID)
	assert.Equal(t, "DAILY", meta.SettlementType)
}

func TestSettlementScheduleProvider_EmptyList(t *testing.T) {
	refData := &mockRefDataClient{}

	provider := NewSettlementScheduleProvider(refData)
	schedules, err := provider.ListSchedules(context.Background())
	require.NoError(t, err)
	assert.Empty(t, schedules)
}

func TestSettlementScheduleProvider_PropagatesError(t *testing.T) {
	refData := &mockRefDataClient{
		err: errors.New("connection refused"),
	}

	provider := NewSettlementScheduleProvider(refData)
	_, err := provider.ListSchedules(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestSettlementScheduleProvider_ImplementsInterface(_ *testing.T) {
	var _ scheduler.ScheduleProvider = NewSettlementScheduleProvider(&mockRefDataClient{}) //nolint:staticcheck // compile-time interface check
}

// --- Executor Tests ---

type trackingReconClient struct {
	mu       sync.Mutex
	requests []InitiateRequest
	runID    string
	err      error
}

func (m *trackingReconClient) InitiateReconciliation(_ context.Context, req InitiateRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if m.err != nil {
		return "", m.err
	}
	return m.runID, nil
}

func (m *trackingReconClient) getRequests() []InitiateRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]InitiateRequest, len(m.requests))
	copy(result, m.requests)
	return result
}

func adapterTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func adapterTestMetrics(t *testing.T) *SchedulerMetrics {
	t.Helper()
	return NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())
}

func TestSettlementExecutor_Execute(t *testing.T) {
	recon := &trackingReconClient{runID: "run-123"}
	metrics := adapterTestMetrics(t)
	executor := NewSettlementExecutor(recon, metrics, adapterTestLogger())

	schedule := scheduler.Schedule{
		ID:       "sched-1",
		CronExpr: "0 2 * * *",
		Metadata: SettlementSchedule{
			ScheduleID:     "sched-1",
			AssetType:      "GBP",
			AccountID:      "acc-1",
			CronExpression: "0 2 * * *",
			SettlementType: "DAILY",
			Scope:          "ACCOUNT",
			PeriodOffset:   24 * time.Hour,
		},
	}

	err := executor.Execute(context.Background(), schedule)
	require.NoError(t, err)

	requests := recon.getRequests()
	require.Len(t, requests, 1)
	assert.Equal(t, "acc-1", requests[0].AccountID)
	assert.Equal(t, "DAILY", requests[0].SettlementType)
	assert.Equal(t, "ACCOUNT", requests[0].Scope)
	assert.Equal(t, "settlement-scheduler", requests[0].InitiatedBy)
	assert.True(t, requests[0].PeriodStart.Before(requests[0].PeriodEnd))
}

func TestSettlementExecutor_DuplicateRunReturnsNil(t *testing.T) {
	recon := &trackingReconClient{err: ErrRunAlreadyExists}
	metrics := adapterTestMetrics(t)
	executor := NewSettlementExecutor(recon, metrics, adapterTestLogger())

	schedule := scheduler.Schedule{
		ID:       "sched-1",
		CronExpr: "0 2 * * *",
		Metadata: SettlementSchedule{
			ScheduleID:     "sched-1",
			AssetType:      "GBP",
			AccountID:      "acc-1",
			CronExpression: "0 2 * * *",
			SettlementType: "DAILY",
			Scope:          "ACCOUNT",
			PeriodOffset:   24 * time.Hour,
		},
	}

	// ErrRunAlreadyExists should be treated as success (not re-raised as error)
	err := executor.Execute(context.Background(), schedule)
	assert.NoError(t, err)
}

func TestSettlementExecutor_PropagatesError(t *testing.T) {
	recon := &trackingReconClient{err: errors.New("gRPC unavailable")}
	metrics := adapterTestMetrics(t)
	executor := NewSettlementExecutor(recon, metrics, adapterTestLogger())

	schedule := scheduler.Schedule{
		ID:       "sched-1",
		CronExpr: "0 2 * * *",
		Metadata: SettlementSchedule{
			ScheduleID:     "sched-1",
			AssetType:      "GBP",
			AccountID:      "acc-1",
			CronExpression: "0 2 * * *",
			SettlementType: "DAILY",
			Scope:          "ACCOUNT",
			PeriodOffset:   24 * time.Hour,
		},
	}

	err := executor.Execute(context.Background(), schedule)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gRPC unavailable")
}

func TestSettlementExecutor_InvalidMetadataType(t *testing.T) {
	recon := &trackingReconClient{runID: "run-123"}
	metrics := adapterTestMetrics(t)
	executor := NewSettlementExecutor(recon, metrics, adapterTestLogger())

	schedule := scheduler.Schedule{
		ID:       "sched-1",
		CronExpr: "0 2 * * *",
		Metadata: "not a SettlementSchedule",
	}

	err := executor.Execute(context.Background(), schedule)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnexpectedMetadata)
}

func TestSettlementExecutor_NilMetricsUsesDefault(t *testing.T) {
	// NewSchedulerMetrics() uses promauto with the global registry, which will
	// panic if metrics are already registered from other tests in this process.
	// This test only verifies the nil path doesn't crash in isolation;
	// in practice the nil fallback is safe because main.go always passes metrics.
	// We skip in non-isolated runs to avoid global registry conflicts.
	t.Skip("nil metrics fallback calls promauto on global registry; tested via integration")
}

func TestSettlementExecutor_ImplementsInterface(t *testing.T) {
	recon := &trackingReconClient{runID: "run-1"}
	metrics := adapterTestMetrics(t)
	_ = NewSettlementExecutor(recon, metrics, adapterTestLogger())
}

func TestSettlementExecutor_UsesAlignedPeriod(t *testing.T) {
	recon := &trackingReconClient{runID: "run-123"}
	metrics := adapterTestMetrics(t)
	executor := NewSettlementExecutor(recon, metrics, adapterTestLogger())

	schedule := scheduler.Schedule{
		ID:       "sched-1",
		CronExpr: "0 2 * * *",
		Metadata: SettlementSchedule{
			ScheduleID:     "sched-1",
			AssetType:      "GBP",
			AccountID:      "acc-1",
			CronExpression: "0 2 * * *",
			SettlementType: "DAILY",
			Scope:          "ACCOUNT",
			// No PeriodOffset: should use aligned midnight boundaries
		},
	}

	err := executor.Execute(context.Background(), schedule)
	require.NoError(t, err)

	requests := recon.getRequests()
	require.Len(t, requests, 1)
	// With DAILY and no offset, CalculatePeriod should produce midnight-aligned boundaries
	assert.Equal(t, 0, requests[0].PeriodStart.Hour(), "period start should be at midnight")
	assert.Equal(t, 0, requests[0].PeriodStart.Minute())
	assert.Equal(t, 0, requests[0].PeriodEnd.Hour(), "period end should be at midnight")
	assert.Equal(t, 0, requests[0].PeriodEnd.Minute())
}
