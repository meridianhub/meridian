package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock FeeReconciliationDataProvider ---

type mockFeeDataProvider struct {
	records []FeeReconciliationRecord
	err     error
}

func (m *mockFeeDataProvider) GetFeeReconciliationRecords(_ context.Context, _ string, _, _ time.Time) ([]FeeReconciliationRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.records, nil
}

// --- Tests ---

func TestFeeReconciler_GenerateReport_WithVariances(t *testing.T) {
	poID1 := uuid.New()
	poID2 := uuid.New()
	poID3 := uuid.New()

	provider := &mockFeeDataProvider{
		records: []FeeReconciliationRecord{
			{
				PaymentOrderID:     poID1.String(),
				GatewayReferenceID: "pi_100",
				ExpectedFeeCents:   250,
				ActualFeeCents:     260,
				Currency:           "gbp",
				TenantID:           "tenant_a",
			},
			{
				PaymentOrderID:     poID2.String(),
				GatewayReferenceID: "pi_200",
				ExpectedFeeCents:   300,
				ActualFeeCents:     300, // exact match - should be skipped
				Currency:           "gbp",
				TenantID:           "tenant_a",
			},
			{
				PaymentOrderID:     poID3.String(),
				GatewayReferenceID: "pi_300",
				ExpectedFeeCents:   500,
				ActualFeeCents:     480,
				Currency:           "usd",
				TenantID:           "tenant_a",
			},
		},
	}

	reconciler := NewFeeReconciler(provider, nil)
	periodStart := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)

	report, err := reconciler.GenerateReport(context.Background(), "tenant_a", periodStart, periodEnd)
	require.NoError(t, err)

	assert.Equal(t, "tenant_a", report.TenantID)
	assert.Equal(t, periodStart, report.PeriodStart)
	assert.Equal(t, periodEnd, report.PeriodEnd)
	assert.Equal(t, 3, report.TotalPayments)
	assert.Equal(t, 2, report.TotalVariances)
	assert.Len(t, report.Variances, 2)

	// Verify first variance (over charge)
	v1 := report.Variances[0]
	assert.Equal(t, poID1, v1.PaymentOrderID)
	assert.Equal(t, "pi_100", v1.GatewayReferenceID)
	assert.Equal(t, int64(10), v1.VarianceCents)
	assert.Equal(t, "over", v1.VarianceType())

	// Verify second variance (under charge)
	v2 := report.Variances[1]
	assert.Equal(t, poID3, v2.PaymentOrderID)
	assert.Equal(t, "pi_300", v2.GatewayReferenceID)
	assert.Equal(t, int64(-20), v2.VarianceCents)
	assert.Equal(t, "under", v2.VarianceType())
}

func TestFeeReconciler_GenerateReport_NoVariances(t *testing.T) {
	provider := &mockFeeDataProvider{
		records: []FeeReconciliationRecord{
			{
				PaymentOrderID:     uuid.New().String(),
				GatewayReferenceID: "pi_100",
				ExpectedFeeCents:   250,
				ActualFeeCents:     250,
				Currency:           "gbp",
				TenantID:           "tenant_b",
			},
			{
				PaymentOrderID:     uuid.New().String(),
				GatewayReferenceID: "pi_200",
				ExpectedFeeCents:   300,
				ActualFeeCents:     300,
				Currency:           "gbp",
				TenantID:           "tenant_b",
			},
		},
	}

	reconciler := NewFeeReconciler(provider, nil)
	report, err := reconciler.GenerateReport(
		context.Background(), "tenant_b",
		time.Now().Add(-24*time.Hour), time.Now(),
	)
	require.NoError(t, err)

	assert.Equal(t, 2, report.TotalPayments)
	assert.Equal(t, 0, report.TotalVariances)
	assert.Empty(t, report.Variances)
}

func TestFeeReconciler_GenerateReport_EmptyRecords(t *testing.T) {
	provider := &mockFeeDataProvider{records: nil}

	reconciler := NewFeeReconciler(provider, nil)
	report, err := reconciler.GenerateReport(
		context.Background(), "tenant_c",
		time.Now().Add(-24*time.Hour), time.Now(),
	)
	require.NoError(t, err)

	assert.Equal(t, 0, report.TotalPayments)
	assert.Equal(t, 0, report.TotalVariances)
	assert.Empty(t, report.Variances)
}

func TestFeeReconciler_GenerateReport_ProviderError(t *testing.T) {
	provider := &mockFeeDataProvider{err: errors.New("database connection failed")}

	reconciler := NewFeeReconciler(provider, nil)
	report, err := reconciler.GenerateReport(
		context.Background(), "tenant_d",
		time.Now().Add(-24*time.Hour), time.Now(),
	)
	require.Error(t, err)
	assert.Nil(t, report)
	assert.Contains(t, err.Error(), "database connection failed")
}

func TestFeeReconciler_GenerateReport_AllVariances(t *testing.T) {
	provider := &mockFeeDataProvider{
		records: []FeeReconciliationRecord{
			{
				PaymentOrderID:     uuid.New().String(),
				GatewayReferenceID: "pi_1",
				ExpectedFeeCents:   100,
				ActualFeeCents:     110,
				Currency:           "gbp",
				TenantID:           "tenant_e",
			},
			{
				PaymentOrderID:     uuid.New().String(),
				GatewayReferenceID: "pi_2",
				ExpectedFeeCents:   200,
				ActualFeeCents:     190,
				Currency:           "gbp",
				TenantID:           "tenant_e",
			},
		},
	}

	reconciler := NewFeeReconciler(provider, nil)
	report, err := reconciler.GenerateReport(
		context.Background(), "tenant_e",
		time.Now().Add(-24*time.Hour), time.Now(),
	)
	require.NoError(t, err)

	assert.Equal(t, 2, report.TotalPayments)
	assert.Equal(t, 2, report.TotalVariances)
	assert.Len(t, report.Variances, 2)
}

func TestFeeReconciler_GenerateReport_InvalidUUID(t *testing.T) {
	provider := &mockFeeDataProvider{
		records: []FeeReconciliationRecord{
			{
				PaymentOrderID:     "not-a-uuid",
				GatewayReferenceID: "pi_bad",
				ExpectedFeeCents:   100,
				ActualFeeCents:     200,
				Currency:           "gbp",
				TenantID:           "tenant_f",
			},
		},
	}

	reconciler := NewFeeReconciler(provider, nil)
	report, err := reconciler.GenerateReport(
		context.Background(), "tenant_f",
		time.Now().Add(-24*time.Hour), time.Now(),
	)
	require.NoError(t, err)

	// Should still generate variance even with invalid UUID (falls back to uuid.Nil)
	require.Len(t, report.Variances, 1)
	assert.Equal(t, uuid.Nil, report.Variances[0].PaymentOrderID)
}

func TestParseUUID_Valid(t *testing.T) {
	id := uuid.New()
	parsed := parseUUID(id.String())
	assert.Equal(t, id, parsed)
}

func TestParseUUID_Invalid(t *testing.T) {
	parsed := parseUUID("invalid")
	assert.Equal(t, uuid.Nil, parsed)
}

func TestNewFeeReconciler_NilLogger(t *testing.T) {
	provider := &mockFeeDataProvider{}
	reconciler := NewFeeReconciler(provider, nil)
	assert.NotNil(t, reconciler)
	assert.NotNil(t, reconciler.logger)
}
