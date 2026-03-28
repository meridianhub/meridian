package stripe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	stripego "github.com/stripe/stripe-go/v82"
)

// SettlementIngestorConfig holds configuration for the settlement ingestor.
type SettlementIngestorConfig struct {
	// AccountID is the Stripe Connected Account ID.
	AccountID string
	// InternalAccountID is the Meridian account ID for the settlement run.
	InternalAccountID string
	// IngestionTimeout is the maximum time for a single ingestion run.
	IngestionTimeout time.Duration
}

// SettlementIngestor fetches Stripe balance transactions for a date range,
// transforms them to SettlementSnapshot domain objects, and persists them
// via the snapshot repository. It creates a settlement run for each ingestion.
type SettlementIngestor struct {
	client      *BalanceTransactionClient
	transformer *SettlementTransformer
	runRepo     domain.SettlementRunRepository
	snapRepo    domain.SettlementSnapshotRepository
	config      SettlementIngestorConfig
	logger      *slog.Logger
}

// NewSettlementIngestor creates a new ingestor with the given dependencies.
func NewSettlementIngestor(
	client *BalanceTransactionClient,
	transformer *SettlementTransformer,
	runRepo domain.SettlementRunRepository,
	snapRepo domain.SettlementSnapshotRepository,
	cfg SettlementIngestorConfig,
	logger *slog.Logger,
) (*SettlementIngestor, error) {
	if client == nil {
		return nil, ErrNilTransactionClient
	}
	if transformer == nil {
		return nil, ErrNilTransformer
	}
	if runRepo == nil {
		return nil, ErrNilRunRepo
	}
	if snapRepo == nil {
		return nil, ErrNilSnapshotRepo
	}
	if cfg.AccountID == "" {
		return nil, ErrEmptyAccountID
	}
	if cfg.InternalAccountID == "" {
		return nil, ErrEmptyInternalAccountID
	}
	if logger == nil {
		logger = slog.Default()
	}
	timeout := cfg.IngestionTimeout
	if timeout <= 0 {
		timeout = defaultIngestionTimeout
	}
	cfg.IngestionTimeout = timeout

	return &SettlementIngestor{
		client:      client,
		transformer: transformer,
		runRepo:     runRepo,
		snapRepo:    snapRepo,
		config:      cfg,
		logger:      logger.With("component", "stripe_settlement_ingestor"),
	}, nil
}

// IngestSettlement fetches Stripe balance transactions for the given period,
// creates a settlement run, transforms and persists the snapshots.
// This is designed to be called by a daily cron job for the previous day.
func (si *SettlementIngestor) IngestSettlement(ctx context.Context, periodStart, periodEnd time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, si.config.IngestionTimeout)
	defer cancel()

	si.logger.Info("starting stripe settlement ingestion",
		"account_id", si.config.AccountID,
		"internal_account_id", si.config.InternalAccountID,
		"period_start", periodStart.Format(time.RFC3339),
		"period_end", periodEnd.Format(time.RFC3339),
	)

	run, err := si.createAndStartRun(ctx, periodStart, periodEnd)
	if err != nil {
		return err
	}

	// Fetch transactions from Stripe
	transactions, fetchErr := si.client.FetchTransactions(ctx, periodStart, periodEnd)
	if fetchErr != nil {
		si.failRun(ctx, run, fetchErr.Error())
		return fmt.Errorf("failed to fetch stripe transactions: %w", fetchErr)
	}

	si.logger.Info("fetched stripe transactions",
		"run_id", run.RunID,
		"transaction_count", len(transactions),
	)

	if len(transactions) == 0 {
		return si.completeEmptyRun(ctx, run)
	}

	if err := si.transformAndPersistSnapshots(ctx, run, transactions); err != nil {
		return err
	}

	// Complete the run
	if err := run.Complete(0); err != nil {
		return fmt.Errorf("failed to complete settlement run: %w", err)
	}
	if err := si.runRepo.Update(ctx, run); err != nil {
		return fmt.Errorf("failed to persist COMPLETED state: %w", err)
	}

	si.logger.Info("stripe settlement ingestion completed",
		"run_id", run.RunID,
		"snapshot_count", len(transactions),
	)

	return nil
}

// createAndStartRun creates a new settlement run and transitions it to RUNNING.
func (si *SettlementIngestor) createAndStartRun(ctx context.Context, periodStart, periodEnd time.Time) (*domain.SettlementRun, error) {
	run, err := domain.NewSettlementRun(
		si.config.InternalAccountID,
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		periodStart,
		periodEnd,
		"stripe-settlement-ingestor",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create settlement run: %w", err)
	}

	if err := si.runRepo.Create(ctx, run); err != nil {
		return nil, fmt.Errorf("failed to persist settlement run: %w", err)
	}

	if err := run.Start(); err != nil {
		return nil, fmt.Errorf("failed to start settlement run: %w", err)
	}
	if err := si.runRepo.Update(ctx, run); err != nil {
		return nil, fmt.Errorf("failed to update settlement run to RUNNING: %w", err)
	}

	return run, nil
}

// completeEmptyRun marks a settlement run as completed when no transactions were found.
func (si *SettlementIngestor) completeEmptyRun(ctx context.Context, run *domain.SettlementRun) error {
	si.logger.Info("no stripe transactions for period, completing run",
		"run_id", run.RunID,
	)
	if err := run.Complete(0); err != nil {
		return fmt.Errorf("failed to complete empty settlement run: %w", err)
	}
	if err := si.runRepo.Update(ctx, run); err != nil {
		return fmt.Errorf("failed to persist COMPLETED state: %w", err)
	}
	return nil
}

// transformAndPersistSnapshots cleans up previous snapshots, transforms transactions,
// and batch-inserts the resulting snapshots.
func (si *SettlementIngestor) transformAndPersistSnapshots(ctx context.Context, run *domain.SettlementRun, transactions []*stripego.BalanceTransaction) error {
	// Idempotent cleanup: remove any snapshots from a previous failed attempt
	if err := si.snapRepo.DeleteByRunID(ctx, run.RunID); err != nil {
		si.failRun(ctx, run, err.Error())
		return fmt.Errorf("failed to clean up existing snapshots: %w", err)
	}

	snapshots, transformErr := si.transformer.TransformToSnapshots(
		run.RunID,
		si.config.InternalAccountID,
		transactions,
	)
	if transformErr != nil {
		si.failRun(ctx, run, transformErr.Error())
		return fmt.Errorf("failed to transform transactions: %w", transformErr)
	}

	if err := si.snapRepo.CreateBatch(ctx, snapshots); err != nil {
		si.failRun(ctx, run, err.Error())
		return fmt.Errorf("failed to persist snapshots: %w", err)
	}

	return nil
}

// IngestPreviousDay is a convenience method that ingests the previous UTC day's
// settlement data. Designed to be called from a daily cron job.
func (si *SettlementIngestor) IngestPreviousDay(ctx context.Context) error {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return si.IngestSettlement(ctx, periodStart, periodEnd)
}

// failRun transitions the run to FAILED state and persists it.
func (si *SettlementIngestor) failRun(ctx context.Context, run *domain.SettlementRun, reason string) {
	if failErr := run.Fail(reason); failErr != nil {
		si.logger.Error("failed to transition run to FAILED state",
			"run_id", run.RunID,
			"error", failErr,
		)
		return
	}
	if updateErr := si.runRepo.Update(ctx, run); updateErr != nil {
		si.logger.Error("failed to persist FAILED state",
			"run_id", run.RunID,
			"error", updateErr,
		)
	}
}

// RunID returns a new unique run identifier. Exported for test use.
func RunID() uuid.UUID {
	return uuid.New()
}

const (
	// defaultIngestionTimeout is the maximum time for a single ingestion run.
	defaultIngestionTimeout = 10 * time.Minute
)
