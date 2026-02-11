package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/meridianhub/meridian/services/reconciliation/domain"
)

// Prometheus metric for fee variance detection.
var stripePlatformFeeVarianceTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "stripe_platform_fee_variance_total",
		Help: "Total number of Stripe platform fee variances detected",
	},
	[]string{"tenant_id", "variance_type"},
)

func init() {
	prometheus.MustRegister(stripePlatformFeeVarianceTotal)
}

// FeeReconciliationRecord represents a single payment's fee data for reconciliation.
// This is the input to the fee reconciler, typically populated by joining
// payment_orders with Stripe balance transaction data.
type FeeReconciliationRecord struct {
	PaymentOrderID     string
	GatewayReferenceID string
	ExpectedFeeCents   int64
	ActualFeeCents     int64
	Currency           string
	TenantID           string
}

// FeeReconciliationDataProvider retrieves fee reconciliation records for a tenant and period.
type FeeReconciliationDataProvider interface {
	// GetFeeReconciliationRecords returns records where the expected and actual fees
	// may differ, for the given tenant within the specified time range.
	GetFeeReconciliationRecords(ctx context.Context, tenantID string, periodStart, periodEnd time.Time) ([]FeeReconciliationRecord, error)
}

// FeeReconciler compares expected platform fees against actual Stripe application fees
// and generates variance reports with Prometheus metrics.
type FeeReconciler struct {
	provider FeeReconciliationDataProvider
	logger   *slog.Logger
}

// NewFeeReconciler creates a new FeeReconciler.
func NewFeeReconciler(provider FeeReconciliationDataProvider, logger *slog.Logger) *FeeReconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &FeeReconciler{
		provider: provider,
		logger:   logger,
	}
}

// GenerateReport produces a FeeReconciliationReport for the given tenant and period.
// It queries for all payments, identifies fee discrepancies, emits Prometheus metrics,
// and returns the report.
func (r *FeeReconciler) GenerateReport(ctx context.Context, tenantID string, periodStart, periodEnd time.Time) (*domain.FeeReconciliationReport, error) {
	records, err := r.provider.GetFeeReconciliationRecords(ctx, tenantID, periodStart, periodEnd)
	if err != nil {
		return nil, err
	}

	variances := make([]domain.FeeVariance, 0, len(records))
	for _, rec := range records {
		if rec.ExpectedFeeCents == rec.ActualFeeCents {
			continue
		}

		v := domain.NewFeeVariance(
			parseUUID(rec.PaymentOrderID),
			rec.GatewayReferenceID,
			rec.ExpectedFeeCents,
			rec.ActualFeeCents,
			rec.Currency,
			rec.TenantID,
		)

		variances = append(variances, v)

		// Emit Prometheus metric
		stripePlatformFeeVarianceTotal.WithLabelValues(tenantID, v.VarianceType()).Inc()

		r.logger.Warn("platform fee variance detected",
			"payment_order_id", rec.PaymentOrderID,
			"gateway_reference_id", rec.GatewayReferenceID,
			"expected_fee_cents", rec.ExpectedFeeCents,
			"actual_fee_cents", rec.ActualFeeCents,
			"variance_cents", v.VarianceCents,
			"variance_type", v.VarianceType(),
			"tenant_id", tenantID,
		)
	}

	report := &domain.FeeReconciliationReport{
		TenantID:       tenantID,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
		TotalPayments:  len(records),
		TotalVariances: len(variances),
		Variances:      variances,
		GeneratedAt:    time.Now().UTC(),
	}

	r.logger.Info("fee reconciliation report generated",
		"tenant_id", tenantID,
		"total_payments", report.TotalPayments,
		"total_variances", report.TotalVariances,
		"period_start", periodStart.Format(time.RFC3339),
		"period_end", periodEnd.Format(time.RFC3339),
	)

	return report, nil
}

// parseUUID parses a UUID string, returning uuid.Nil on failure.
func parseUUID(s string) uuid.UUID {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}
