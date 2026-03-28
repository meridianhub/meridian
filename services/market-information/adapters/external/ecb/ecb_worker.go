// Package ecb provides a scheduled worker for ingesting FX rates from the European Central Bank.
package ecb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"time"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
)

// Default worker configuration values.
const (
	defaultFetchInterval = 24 * time.Hour
	defaultMaxRetries    = 3
)

// MarketInformationClient is the interface for recording observations.
// This allows the worker to be tested with a mock client.
type MarketInformationClient interface {
	RecordObservation(ctx context.Context, req *marketinformationv1.RecordObservationRequest) (*marketinformationv1.RecordObservationResponse, error)
}

// WorkerConfig holds configuration for the ECB ingestion worker.
type WorkerConfig struct {
	// DatasetCode is the dataset code prefix for observations (e.g., "ECB_FX").
	DatasetCode string

	// SourceCode is the data source code (e.g., "ECB").
	SourceCode string

	// FetchInterval is how often to fetch new rates.
	// Default: 24 hours
	FetchInterval time.Duration

	// MaxRetries is the maximum number of retry attempts for failed operations.
	// Default: 3
	MaxRetries int
}

// DefaultWorkerConfig returns a WorkerConfig with sensible defaults.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		DatasetCode:   "ECB_FX",
		SourceCode:    "ECB",
		FetchInterval: defaultFetchInterval,
		MaxRetries:    defaultMaxRetries,
	}
}

// Worker is a background worker that fetches and ingests ECB FX rates on a schedule.
// It implements graceful shutdown following the pattern from shared/platform/events/worker.go.
type Worker struct {
	client           *Client
	marketInfoClient MarketInformationClient
	parser           *Parser
	config           WorkerConfig
	logger           *slog.Logger
	shutdown         chan struct{}
	shutdownOnce     sync.Once
	wg               sync.WaitGroup
}

// NewWorker creates a new ECB ingestion worker.
//
// Parameters:
//   - client: The ECB HTTP client for fetching rates
//   - marketInfoClient: gRPC client for recording observations
//   - config: Worker configuration
//   - logger: Structured logger (uses slog.Default() if nil)
//
// Returns a configured Worker ready to start processing.
func NewWorker(
	client *Client,
	marketInfoClient MarketInformationClient,
	config WorkerConfig,
	logger *slog.Logger,
) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults
	if config.FetchInterval <= 0 {
		config.FetchInterval = defaultFetchInterval
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = defaultMaxRetries
	}
	if config.SourceCode == "" {
		config.SourceCode = "ECB"
	}

	return &Worker{
		client:           client,
		marketInfoClient: marketInfoClient,
		parser:           NewParser(WithParserLogger(logger)),
		config:           config,
		logger:           logger.With("component", "ecb_worker"),
		shutdown:         make(chan struct{}),
	}
}

// Start begins background processing of ECB rate ingestion.
// This method spawns a goroutine and returns immediately.
// The worker will continue processing until Stop() is called or the context is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.run(ctx)

	w.logger.Info("ECB worker started",
		"fetch_interval", w.config.FetchInterval,
		"source_code", w.config.SourceCode,
		"max_retries", w.config.MaxRetries)
}

// Stop initiates graceful shutdown of the worker.
// It signals the worker to stop and waits for the current operation to complete.
// Safe to call multiple times - subsequent calls will block until shutdown completes.
func (w *Worker) Stop() {
	w.shutdownOnce.Do(func() {
		w.logger.Info("ECB worker stopping")
		close(w.shutdown)
	})
	w.wg.Wait()

	w.logger.Info("ECB worker stopped")
}

// run is the main processing loop that polls ECB on the configured interval.
func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.FetchInterval)
	defer ticker.Stop()

	w.logger.Info("ECB worker processing loop started")

	// Perform initial fetch immediately
	w.ingestRates(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("ECB worker context cancelled")
			return
		case <-w.shutdown:
			w.logger.Info("ECB worker shutdown signal received")
			return
		case <-ticker.C:
			w.ingestRates(ctx)
		}
	}
}

// ingestRates fetches ECB rates and records them as observations with retry logic.
// It handles transient failures with exponential backoff and logs errors but does not
// block the worker on permanent failures.
func (w *Worker) ingestRates(ctx context.Context) {
	causationID := fmt.Sprintf("ecb-feed-%s", time.Now().UTC().Format("2006-01-02"))

	var lastErr error
	for attempt := 1; attempt <= w.config.MaxRetries; attempt++ {
		err := w.attemptIngestion(ctx, causationID)
		if err == nil {
			w.logger.Info("ECB rates ingestion successful", "causation_id", causationID)
			return
		}

		lastErr = err

		// Check if error is retryable
		if !IsRetryableError(err) {
			w.logger.Error("ECB rates ingestion failed with permanent error",
				"error", err,
				"causation_id", causationID)
			return
		}

		// Don't log retry warning or wait on the last attempt
		if attempt == w.config.MaxRetries {
			break
		}

		w.logger.Warn("ECB rates ingestion failed, retrying",
			"attempt", attempt,
			"max_retries", w.config.MaxRetries,
			"error", err)

		// Exponential backoff: 1s, 2s, 4s, ...
		backoff := time.Duration(1<<(attempt-1)) * time.Second
		select {
		case <-ctx.Done():
			w.logger.Warn("ECB ingestion retry interrupted by context cancellation",
				"causation_id", causationID,
				"attempt", attempt)
			return
		case <-w.shutdown:
			w.logger.Warn("ECB ingestion retry interrupted by shutdown",
				"causation_id", causationID,
				"attempt", attempt)
			return
		case <-time.After(backoff):
			continue
		}
	}

	w.logger.Error("ECB rates ingestion failed after retries",
		"error", lastErr,
		"causation_id", causationID,
		"attempts", w.config.MaxRetries)
}

// attemptIngestion performs a single ingestion attempt: fetch -> parse -> transform -> record.
func (w *Worker) attemptIngestion(ctx context.Context, causationID string) error {
	start := time.Now()
	w.logger.Info("starting ECB rate ingestion", "causation_id", causationID)

	// Fetch and parse
	observations, totalRates, err := w.fetchAndTransformRates(ctx)
	if err != nil {
		return err
	}

	// Record observations
	successCount, failCount, err := w.recordObservations(ctx, observations)
	if err != nil {
		return err
	}

	duration := time.Since(start)
	w.logger.Info("ECB rate ingestion completed",
		"causation_id", causationID,
		"success_count", successCount,
		"fail_count", failCount,
		"total_rates", totalRates,
		"duration", duration)

	if failCount > 0 {
		return fmt.Errorf("%w: %d/%d observations failed", ErrPartialIngestion, failCount, len(observations))
	}

	return nil
}

// fetchAndTransformRates fetches ECB CSV data, parses it, and transforms rates into observation requests.
// Returns the observation requests, the total number of parsed rates, and any error.
func (w *Worker) fetchAndTransformRates(ctx context.Context) ([]*marketinformationv1.RecordObservationRequest, int, error) {
	body, err := w.client.FetchDailyRates(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch ECB rates: %w", err)
	}
	defer func() { _ = body.Close() }()

	rates, err := w.parser.ParseCSV(body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse ECB CSV: %w", err)
	}

	w.logger.Debug("parsed ECB rates", "count", len(rates))

	transformCfg := TransformConfig{
		SourceCode:          w.config.SourceCode,
		DatasetCodeTemplate: "%s_%s_FX",
	}
	return TransformToObservations(rates, transformCfg), len(rates), nil
}

// recordObservations records each observation via gRPC, checking for shutdown between calls.
// Returns success count, failure count, and an error if interrupted.
func (w *Worker) recordObservations(ctx context.Context, observations []*marketinformationv1.RecordObservationRequest) (int, int, error) {
	var successCount, failCount int
	for _, obs := range observations {
		select {
		case <-ctx.Done():
			w.logger.Warn("ingestion interrupted by context cancellation",
				"processed", successCount,
				"remaining", len(observations)-successCount-failCount)
			return successCount, failCount, ctx.Err()
		case <-w.shutdown:
			w.logger.Warn("ingestion interrupted by shutdown",
				"processed", successCount,
				"remaining", len(observations)-successCount-failCount)
			return successCount, failCount, nil
		default:
		}

		_, err := w.marketInfoClient.RecordObservation(ctx, obs)
		if err != nil {
			w.logger.Warn("failed to record observation",
				"dataset_code", obs.DatasetCode,
				"observed_at", obs.ObservedAt.AsTime(),
				"error", err)
			failCount++
			continue
		}
		successCount++
	}
	return successCount, failCount, nil
}

// IsRetryableError determines if an error should trigger a retry.
// Network errors and HTTP 5xx are retryable; 4xx and parse errors are permanent.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Rate limiting (HTTP 429) is retryable
	if errors.Is(err, ErrRateLimited) {
		return true
	}

	// Parse errors are permanent - the data won't change on retry
	if errors.Is(err, ErrInvalidCSVFormat) ||
		errors.Is(err, ErrNoData) ||
		errors.Is(err, ErrInvalidRate) ||
		errors.Is(err, ErrInvalidDate) ||
		errors.Is(err, ErrTooFewColumns) {
		return false
	}

	// Client not configured is permanent
	if errors.Is(err, ErrNotConfigured) {
		return false
	}

	// Context cancellation is not retryable
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors are retryable
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// URL errors (DNS failures, etc.) are retryable
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}

	// ErrAPIError wraps HTTP errors - these are generally 5xx which are retryable
	// HTTP 4xx errors would typically return a different error type or be checked above
	if errors.Is(err, ErrAPIError) {
		return true
	}

	// Default to retryable for unknown errors (safer for transient issues)
	return true
}
