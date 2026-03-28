package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/redis/go-redis/v9"
)

// BillingScheduleProvider implements scheduler.ScheduleProvider by returning
// a single static billing schedule per tenant.
type BillingScheduleProvider struct {
	tenantID       string
	cronExpression string
}

// NewBillingScheduleProvider creates a provider that returns a single billing schedule.
func NewBillingScheduleProvider(tenantID, cronExpression string) *BillingScheduleProvider {
	return &BillingScheduleProvider{
		tenantID:       tenantID,
		cronExpression: cronExpression,
	}
}

// ListSchedules returns the static billing schedule for the configured tenant.
func (p *BillingScheduleProvider) ListSchedules(_ context.Context) ([]scheduler.Schedule, error) {
	return []scheduler.Schedule{
		{
			ID:       fmt.Sprintf("billing:%s", p.tenantID),
			CronExpr: p.cronExpression,
			TenantID: p.tenantID,
		},
	}, nil
}

// BillingExecutorConfig holds configuration for the billing executor adapter.
type BillingExecutorConfig struct {
	ShadowMode bool
}

// BillingExecutor implements scheduler.Executor by running the full billing cycle:
// idempotency check -> create billing run -> generate invoices -> initiate payments.
type BillingExecutor struct {
	repo             persistence.BillingRepository
	redis            *redis.Client
	invoiceGenerator *InvoiceGenerator
	paymentInitiator *PaymentInitiator
	metrics          *BillingMetrics
	config           BillingExecutorConfig
	logger           *slog.Logger
}

// NewBillingExecutor creates a new billing executor adapter.
func NewBillingExecutor(
	repo persistence.BillingRepository,
	redisClient *redis.Client,
	metrics *BillingMetrics,
	config BillingExecutorConfig,
	logger *slog.Logger,
) *BillingExecutor {
	return &BillingExecutor{
		repo:    repo,
		redis:   redisClient,
		metrics: metrics,
		config:  config,
		logger:  logger.With("component", "billing_executor"),
	}
}

// WithInvoiceGenerator sets the invoice generator on the executor.
func (e *BillingExecutor) WithInvoiceGenerator(gen *InvoiceGenerator) *BillingExecutor {
	e.invoiceGenerator = gen
	return e
}

// WithPaymentInitiator sets the payment initiator on the executor.
func (e *BillingExecutor) WithPaymentInitiator(init *PaymentInitiator) *BillingExecutor {
	e.paymentInitiator = init
	return e
}

// Execute performs a single billing cycle for the schedule's tenant.
func (e *BillingExecutor) Execute(ctx context.Context, schedule scheduler.Schedule) error {
	start := NowFunc()

	// Calculate billing period (previous complete month)
	periodStart, periodEnd := calculateBillingPeriod(start)
	idempotencyKey := domain.BillingRunIdempotencyKey(schedule.TenantID, periodStart, periodEnd)

	e.logger.Info("executing billing run",
		"tenant_id", schedule.TenantID,
		"period_start", periodStart,
		"period_end", periodEnd,
		"idempotency_key", idempotencyKey)

	run, err := e.createBillingRunIfNew(ctx, schedule.TenantID, periodStart, periodEnd, idempotencyKey)
	if err != nil {
		return err
	}
	if run == nil {
		return nil // duplicate, already handled
	}

	if err := e.transitionToProcessing(ctx, run); err != nil {
		return err
	}

	if err := e.processInvoices(ctx, run); err != nil {
		return err
	}

	return e.completeBillingRun(ctx, run, start)
}

// createBillingRunIfNew checks idempotency and creates a new billing run.
// Returns nil run if the billing run already exists (idempotent skip).
func (e *BillingExecutor) createBillingRunIfNew(ctx context.Context, tenantID string, periodStart, periodEnd time.Time, idempotencyKey string) (*domain.BillingRun, error) {
	duplicate, err := e.checkIdempotency(ctx, idempotencyKey)
	if err != nil {
		e.logger.Error("failed to check idempotency", "error", err)
		e.metrics.RecordError("idempotency_check")
		return nil, fmt.Errorf("idempotency check: %w", err)
	}
	if duplicate {
		e.logger.Info("billing run already exists for this period, skipping",
			"idempotency_key", idempotencyKey)
		return nil, nil //nolint:nilnil // nil run signals idempotent skip
	}

	run, err := domain.NewBillingRun(tenantID, periodStart, periodEnd)
	if err != nil {
		e.logger.Error("failed to create billing run", "error", err)
		e.metrics.RecordError("create_billing_run")
		return nil, fmt.Errorf("create billing run: %w", err)
	}

	if err := e.repo.CreateBillingRun(ctx, run); err != nil {
		if errors.Is(err, persistence.ErrBillingRunDuplicate) {
			e.logger.Info("billing run already exists in database, skipping",
				"idempotency_key", idempotencyKey)
			e.markIdempotency(ctx, idempotencyKey)
			return nil, nil //nolint:nilnil // nil run signals duplicate skip
		}
		e.logger.Error("failed to persist billing run", "error", err)
		e.metrics.RecordError("persist_billing_run")
		return nil, fmt.Errorf("persist billing run: %w", err)
	}

	e.markIdempotency(ctx, idempotencyKey)
	return run, nil
}

// transitionToProcessing transitions the billing run to processing state.
func (e *BillingExecutor) transitionToProcessing(ctx context.Context, run *domain.BillingRun) error {
	if err := run.StartProcessing(); err != nil {
		e.logger.Error("failed to start processing", "error", err)
		return fmt.Errorf("start processing: %w", err)
	}
	if err := e.repo.UpdateBillingRun(ctx, run); err != nil {
		e.logger.Error("failed to update billing run to processing", "error", err)
		return fmt.Errorf("update billing run: %w", err)
	}
	e.metrics.RecordBillingRun(string(domain.BillingRunStatusProcessing))
	return nil
}

// completeBillingRun marks the billing run as complete and records metrics.
func (e *BillingExecutor) completeBillingRun(ctx context.Context, run *domain.BillingRun, start time.Time) error {
	if err := run.Complete(); err != nil {
		e.logger.Error("failed to complete billing run", "error", err)
		return fmt.Errorf("complete billing run: %w", err)
	}
	if err := e.repo.UpdateBillingRun(ctx, run); err != nil {
		e.logger.Error("failed to update billing run to completed", "error", err)
		return fmt.Errorf("update billing run completed: %w", err)
	}

	elapsed := NowFunc().Sub(start)
	e.metrics.RecordBillingRun(string(domain.BillingRunStatusCompleted))
	e.metrics.ObserveRunDuration(elapsed.Seconds())

	e.logger.Info("billing run completed",
		"billing_run_id", run.ID,
		"duration", elapsed,
		"shadow_mode", e.config.ShadowMode)

	return nil
}

// processInvoices generates invoices and initiates payments for a billing run.
// When invoiceGenerator is nil, the billing run completes without invoice generation.
// This is the expected state until the position-keeping client is wired.
func (e *BillingExecutor) processInvoices(ctx context.Context, run *domain.BillingRun) error {
	if e.invoiceGenerator == nil {
		e.logger.Debug("invoice generator not configured, skipping invoice generation",
			"billing_run_id", run.ID)
		return nil
	}

	invoices, err := e.invoiceGenerator.GenerateInvoices(ctx, run)
	if err != nil {
		e.logger.Error("invoice generation failed", "error", err)
		if failErr := run.Fail("invoice generation failed: " + err.Error()); failErr == nil {
			_ = e.repo.UpdateBillingRun(ctx, run)
		}
		e.metrics.RecordError("invoice_generation")
		e.metrics.RecordBillingRun(string(domain.BillingRunStatusFailed))
		return fmt.Errorf("invoice generation: %w", err)
	}

	if e.paymentInitiator == nil || len(invoices) == 0 {
		return nil
	}

	if err := e.paymentInitiator.InitiatePayments(ctx, run, invoices, e.config.ShadowMode); err != nil {
		e.logger.Error("payment initiation failed", "error", err)
		if failErr := run.Fail("payment initiation failed: " + err.Error()); failErr == nil {
			_ = e.repo.UpdateBillingRun(ctx, run)
		}
		e.metrics.RecordError("payment_initiation")
		e.metrics.RecordBillingRun(string(domain.BillingRunStatusFailed))
		return fmt.Errorf("payment initiation: %w", err)
	}

	return nil
}

// checkIdempotency checks Redis for an existing billing run key.
func (e *BillingExecutor) checkIdempotency(ctx context.Context, key string) (bool, error) {
	redisKey := "billing:idempotency:" + key
	exists, err := e.redis.Exists(ctx, redisKey).Result()
	if err != nil {
		return false, fmt.Errorf("redis exists check failed: %w", err)
	}
	return exists > 0, nil
}

// markIdempotency sets the idempotency key in Redis with TTL.
func (e *BillingExecutor) markIdempotency(ctx context.Context, key string) {
	redisKey := "billing:idempotency:" + key
	if err := e.redis.Set(ctx, redisKey, "1", IdempotencyKeyTTL).Err(); err != nil {
		e.logger.Error("failed to set idempotency key in Redis",
			"key", redisKey,
			"error", err)
	}
}
