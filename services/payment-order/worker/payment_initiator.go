package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
)

// ErrAllPaymentsFailed is returned when all invoice payment initiations fail.
var ErrAllPaymentsFailed = errors.New("all invoice payments failed")

// SagaClient defines the interface for starting and querying saga executions.
type SagaClient interface {
	// StartSaga initiates a new saga execution. Returns the saga execution ID.
	StartSaga(ctx context.Context, sagaName, version string, input map[string]any) (uuid.UUID, error)

	// GetSagaStatus returns the current status of a saga execution.
	// Returns one of: PENDING, RUNNING, COMPLETED, FAILED, COMPENSATING, COMPENSATED.
	GetSagaStatus(ctx context.Context, executionID uuid.UUID) (string, error)
}

// PaymentInitiator handles invoice payment initiation, including shadow mode
// where invoices remain as DRAFT without saga execution.
type PaymentInitiator struct {
	sagaClient SagaClient
	repo       persistence.BillingRepository
	metrics    *BillingMetrics
	logger     *slog.Logger
}

// NewPaymentInitiator creates a new payment initiator.
func NewPaymentInitiator(
	sagaClient SagaClient,
	repo persistence.BillingRepository,
	metrics *BillingMetrics,
	logger *slog.Logger,
) *PaymentInitiator {
	return &PaymentInitiator{
		sagaClient: sagaClient,
		repo:       repo,
		metrics:    metrics,
		logger:     logger.With("component", "payment_initiator"),
	}
}

// InitiatePayments processes invoices for a billing run. In shadow mode, invoices
// remain as DRAFT. In live mode, invoices are issued and payment sagas are started.
func (p *PaymentInitiator) InitiatePayments(
	ctx context.Context,
	billingRun *domain.BillingRun,
	invoices []*domain.Invoice,
	shadowMode bool,
) error {
	p.logger.Info("initiating payments",
		"billing_run_id", billingRun.ID,
		"invoice_count", len(invoices),
		"shadow_mode", shadowMode)

	var failCount int

	for _, inv := range invoices {
		if shadowMode {
			p.handleShadowInvoice(inv)
			continue
		}

		if err := p.handleLiveInvoice(ctx, inv); err != nil {
			p.logger.Error("failed to initiate payment for invoice",
				"invoice_id", inv.ID,
				"party_id", inv.PartyID,
				"error", err)
			failCount++
		}
	}

	if failCount > 0 && failCount == len(invoices) {
		return fmt.Errorf("%w: %d invoices", ErrAllPaymentsFailed, failCount)
	}

	return nil
}

// handleShadowInvoice keeps the invoice in DRAFT status for shadow mode billing.
// No saga is initiated; the invoice exists for observability and reconciliation only.
func (p *PaymentInitiator) handleShadowInvoice(inv *domain.Invoice) {
	p.logger.Info("shadow mode: invoice created as DRAFT",
		"invoice_id", inv.ID,
		"party_id", inv.PartyID,
		"subtotal_cents", inv.SubtotalCents)
}

// handleLiveInvoice issues the invoice and starts a payment saga.
func (p *PaymentInitiator) handleLiveInvoice(ctx context.Context, inv *domain.Invoice) error {
	// Transition invoice to ISSUED
	if err := inv.Issue(); err != nil {
		return fmt.Errorf("issue invoice %s: %w", inv.ID, err)
	}

	if err := p.repo.UpdateInvoice(ctx, inv); err != nil {
		return fmt.Errorf("persist issued invoice %s: %w", inv.ID, err)
	}

	// Build saga input
	idempotencyKey := fmt.Sprintf("invoice_payment_%s_%d", inv.ID, inv.CreatedAt.Unix())
	sagaInput := map[string]any{
		"invoice_id":      inv.ID.String(),
		"party_id":        inv.PartyID,
		"account_id":      inv.AccountID,
		"amount_cents":    inv.SubtotalCents,
		"currency":        inv.Currency,
		"idempotency_key": idempotencyKey,
	}

	// Start payment saga
	executionID, err := p.sagaClient.StartSaga(ctx, "stripe_payment", "v1.0.0", sagaInput)
	if err != nil {
		// Mark invoice as overdue since payment initiation failed
		if markErr := inv.MarkOverdue(); markErr != nil {
			p.metrics.RecordError("mark_overdue")
			return fmt.Errorf("mark invoice overdue %s: %w", inv.ID, errors.Join(markErr, err))
		}
		if updErr := p.repo.UpdateInvoice(ctx, inv); updErr != nil {
			p.metrics.RecordError("persist_overdue")
			return fmt.Errorf("persist overdue invoice %s: %w", inv.ID, errors.Join(updErr, err))
		}
		p.metrics.RecordError("saga_start")
		return fmt.Errorf("start saga for invoice %s: %w", inv.ID, err)
	}

	p.logger.Info("payment saga started",
		"invoice_id", inv.ID,
		"saga_execution_id", executionID,
		"idempotency_key", idempotencyKey)

	p.metrics.RecordBillingRun("SAGA_INITIATED")

	return nil
}
