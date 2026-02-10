package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock saga client ---

type mockSagaClient struct {
	startErr    error
	statusErr   error
	status      string
	executions  []sagaExecution
	startCalled int
}

type sagaExecution struct {
	sagaName string
	version  string
	input    map[string]any
}

func (m *mockSagaClient) StartSaga(_ context.Context, sagaName, version string, input map[string]any) (uuid.UUID, error) {
	m.startCalled++
	if m.startErr != nil {
		return uuid.Nil, m.startErr
	}
	exec := sagaExecution{sagaName: sagaName, version: version, input: input}
	m.executions = append(m.executions, exec)
	return uuid.New(), nil
}

func (m *mockSagaClient) GetSagaStatus(_ context.Context, _ uuid.UUID) (string, error) {
	if m.statusErr != nil {
		return "", m.statusErr
	}
	return m.status, nil
}

// Verify interface compliance.
var _ SagaClient = (*mockSagaClient)(nil)

func testPaymentMetrics(t *testing.T) *BillingMetrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewBillingMetricsWithRegistry(reg)
}

func testPaymentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestInitiatePayments(t *testing.T) {
	ctx := context.Background()
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	t.Run("shadow mode keeps invoices as DRAFT", func(t *testing.T) {
		repo := newMockBillingRepo()
		sagaClient := &mockSagaClient{}

		billingRun := createTestBillingRun(t, "tenant-shadow", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		invoices := createTestInvoices(t, billingRun, 3)
		for _, inv := range invoices {
			require.NoError(t, repo.CreateInvoice(ctx, inv))
		}

		initiator := NewPaymentInitiator(sagaClient, repo, testPaymentMetrics(t), testPaymentLogger())
		err := initiator.InitiatePayments(ctx, billingRun, invoices, true)
		require.NoError(t, err)

		// No sagas should be started in shadow mode
		assert.Equal(t, 0, sagaClient.startCalled)

		// Invoices should remain as DRAFT
		for _, inv := range invoices {
			assert.Equal(t, domain.InvoiceStatusDraft, inv.Status)
		}
	})

	t.Run("live mode issues invoices and starts sagas", func(t *testing.T) {
		repo := newMockBillingRepo()
		sagaClient := &mockSagaClient{}

		billingRun := createTestBillingRun(t, "tenant-live", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		invoices := createTestInvoices(t, billingRun, 2)
		for _, inv := range invoices {
			require.NoError(t, repo.CreateInvoice(ctx, inv))
		}

		initiator := NewPaymentInitiator(sagaClient, repo, testPaymentMetrics(t), testPaymentLogger())
		err := initiator.InitiatePayments(ctx, billingRun, invoices, false)
		require.NoError(t, err)

		assert.Equal(t, 2, sagaClient.startCalled)
		assert.Len(t, sagaClient.executions, 2)

		// Invoices should be ISSUED
		for _, inv := range invoices {
			assert.Equal(t, domain.InvoiceStatusIssued, inv.Status)
		}

		// Verify saga input
		exec := sagaClient.executions[0]
		assert.Equal(t, "stripe_payment", exec.sagaName)
		assert.Equal(t, "v1.0.0", exec.version)
		assert.Contains(t, exec.input, "invoice_id")
		assert.Contains(t, exec.input, "party_id")
		assert.Contains(t, exec.input, "account_id")
		assert.Contains(t, exec.input, "amount_cents")
		assert.Contains(t, exec.input, "currency")
		assert.Contains(t, exec.input, "idempotency_key")
	})

	t.Run("saga start failure marks invoice as overdue", func(t *testing.T) {
		repo := newMockBillingRepo()
		sagaClient := &mockSagaClient{
			startErr: errors.New("saga orchestrator unavailable"),
		}

		billingRun := createTestBillingRun(t, "tenant-fail", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		invoices := createTestInvoices(t, billingRun, 1)
		for _, inv := range invoices {
			require.NoError(t, repo.CreateInvoice(ctx, inv))
		}

		initiator := NewPaymentInitiator(sagaClient, repo, testPaymentMetrics(t), testPaymentLogger())
		err := initiator.InitiatePayments(ctx, billingRun, invoices, false)

		// Should return error since all invoices failed
		assert.ErrorIs(t, err, ErrAllPaymentsFailed)

		// Invoice should be OVERDUE due to saga failure
		assert.Equal(t, domain.InvoiceStatusOverdue, invoices[0].Status)
	})

	t.Run("partial saga failures do not return error", func(t *testing.T) {
		repo := newMockBillingRepo()

		// Alternate between success and failure
		callCount := 0
		sagaClient := &mockSagaClient{}

		billingRun := createTestBillingRun(t, "tenant-partial", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		invoices := createTestInvoices(t, billingRun, 2)
		for _, inv := range invoices {
			require.NoError(t, repo.CreateInvoice(ctx, inv))
		}

		// Override StartSaga to fail on second call
		originalStart := sagaClient.StartSaga
		_ = originalStart
		sagaClient2 := &failOnNthSagaClient{failOnCall: 2}

		initiator := NewPaymentInitiator(sagaClient2, repo, testPaymentMetrics(t), testPaymentLogger())
		err := initiator.InitiatePayments(ctx, billingRun, invoices, false)
		_ = callCount

		// Partial failure should not return error (only 1 of 2 failed)
		require.NoError(t, err)
	})

	t.Run("empty invoice list succeeds", func(t *testing.T) {
		repo := newMockBillingRepo()
		sagaClient := &mockSagaClient{}

		billingRun := createTestBillingRun(t, "tenant-empty", periodStart, periodEnd)
		require.NoError(t, repo.CreateBillingRun(ctx, billingRun))

		initiator := NewPaymentInitiator(sagaClient, repo, testPaymentMetrics(t), testPaymentLogger())
		err := initiator.InitiatePayments(ctx, billingRun, nil, false)
		require.NoError(t, err)
		assert.Equal(t, 0, sagaClient.startCalled)
	})
}

// failOnNthSagaClient fails StartSaga on the Nth call.
type failOnNthSagaClient struct {
	failOnCall int
	callCount  int
	executions []sagaExecution
}

func (m *failOnNthSagaClient) StartSaga(_ context.Context, sagaName, version string, input map[string]any) (uuid.UUID, error) {
	m.callCount++
	if m.callCount == m.failOnCall {
		return uuid.Nil, errors.New("saga orchestrator timeout")
	}
	exec := sagaExecution{sagaName: sagaName, version: version, input: input}
	m.executions = append(m.executions, exec)
	return uuid.New(), nil
}

func (m *failOnNthSagaClient) GetSagaStatus(_ context.Context, _ uuid.UUID) (string, error) {
	return "COMPLETED", nil
}

var _ SagaClient = (*failOnNthSagaClient)(nil)

func createTestInvoices(t *testing.T, billingRun *domain.BillingRun, count int) []*domain.Invoice {
	t.Helper()
	var invoices []*domain.Invoice
	for i := 1; i <= count; i++ {
		inv, err := domain.NewInvoice(
			billingRun.ID,
			"party-"+uuid.New().String()[:8],
			"acct-"+uuid.New().String()[:8],
			formatInvoiceNumber(billingRun.CycleEnd, i),
			billingRun.CycleStart,
			billingRun.CycleEnd,
			[]domain.InvoiceLineItem{
				{
					Description:    "Test service fee",
					Quantity:       mustDecimal("1"),
					UnitPriceCents: 5000,
					TotalCents:     5000,
				},
			},
			"USD",
		)
		require.NoError(t, err)
		invoices = append(invoices, inv)
	}
	return invoices
}

func mustDecimal(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}
