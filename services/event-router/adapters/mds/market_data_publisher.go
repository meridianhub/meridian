// Package mds provides the MarketDataPublisher adapter that aggregates utilization
// measurements in configurable time windows and flushes them to the Market Data
// Service (MDS) via RecordObservationBatch RPC.
package mds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/event-router/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/shopspring/decimal"
	"github.com/sony/gobreaker/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	// ErrNilMDSClient is returned when the MDS gRPC client is nil.
	ErrNilMDSClient = errors.New("MDS client cannot be nil")

	// ErrCircuitBreakerOpen is returned when the circuit breaker is open.
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")
)

// Prometheus metrics for the MarketDataPublisher.
var (
	mdsBufferSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "mds_buffer_windows",
			Help:      "Number of aggregation windows currently in the buffer",
		},
	)

	mdsFlushDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "mds_flush_duration_seconds",
			Help:      "Duration of MDS flush operations in seconds",
			Buckets:   []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
	)

	mdsPublishErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "mds_publish_errors_total",
			Help:      "Total number of errors publishing to MDS",
		},
		[]string{"error_type"},
	)

	mdsObservationsPublishedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "utilization_metering",
			Name:      "mds_observations_published_total",
			Help:      "Total number of observations successfully published to MDS",
		},
	)
)

// Config holds configuration for the MarketDataPublisher.
type Config struct {
	// WindowSize is the aggregation window duration (default: 1 hour).
	WindowSize time.Duration

	// FlushInterval is how often the buffer is flushed to MDS (default: 1 hour).
	FlushInterval time.Duration

	// SourceCode is the MDS data source code for observations (default: "MERIDIAN_UTILIZATION").
	SourceCode string

	// CircuitBreakerConsecutiveFailures is the number of consecutive failures before
	// the circuit breaker opens (default: 5).
	CircuitBreakerConsecutiveFailures uint32

	// Logger for structured logging.
	Logger *slog.Logger
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		WindowSize:                        1 * time.Hour,
		FlushInterval:                     1 * time.Hour,
		SourceCode:                        "MERIDIAN_UTILIZATION",
		CircuitBreakerConsecutiveFailures: 5,
		Logger:                            slog.Default(),
	}
}

// UtilizationWindow holds aggregated metrics for a single time window + resolution key.
type UtilizationWindow struct {
	// ResolutionKey is the hierarchical key: tenant/{resource_type}/{resource_id}
	ResolutionKey string

	// InstrumentCode is the instrument type code (e.g., "TRANSACTION").
	InstrumentCode string

	// WindowStart is the start of the aggregation window.
	WindowStart time.Time

	// WindowEnd is the end of the aggregation window.
	WindowEnd time.Time

	// TotalUnits is the sum of all measurement amounts in this window.
	TotalUnits decimal.Decimal

	// PeakUnits is the maximum single measurement amount in this window.
	PeakUnits decimal.Decimal

	// AvgUnits is the average measurement amount (TotalUnits / ObservationCount).
	AvgUnits decimal.Decimal

	// ObservationCount is the number of measurements aggregated in this window.
	ObservationCount int64
}

// recalculateAvg updates AvgUnits from TotalUnits and ObservationCount.
func (w *UtilizationWindow) recalculateAvg() {
	if w.ObservationCount == 0 {
		w.AvgUnits = decimal.Zero
		return
	}
	w.AvgUnits = w.TotalUnits.Div(decimal.NewFromInt(w.ObservationCount))
}

// AggregationBuffer provides thread-safe in-memory windowed aggregation.
type AggregationBuffer struct {
	mu         sync.Mutex
	windows    map[string]*UtilizationWindow
	windowSize time.Duration
}

// NewAggregationBuffer creates a new buffer with the given window size.
func NewAggregationBuffer(windowSize time.Duration) *AggregationBuffer {
	return &AggregationBuffer{
		windows:    make(map[string]*UtilizationWindow),
		windowSize: windowSize,
	}
}

// windowKey generates the map key: {resolution_key}_{window_start_unix}
func (b *AggregationBuffer) windowKey(resolutionKey string, windowStart time.Time) string {
	return fmt.Sprintf("%s_%d", resolutionKey, windowStart.Unix())
}

// windowStart calculates the start of the window containing the given timestamp.
func (b *AggregationBuffer) windowStart(ts time.Time) time.Time {
	return ts.Truncate(b.windowSize)
}

// Add adds a measurement to the appropriate aggregation window.
func (b *AggregationBuffer) Add(m *domain.UtilizationMeasurement) {
	resKey := fmt.Sprintf("%s/%s/%s", m.TenantID, m.ServiceName, m.OperationType)
	wStart := b.windowStart(m.Timestamp)
	key := b.windowKey(resKey, wStart)
	amount := m.Amount.GetAmount()

	b.mu.Lock()
	defer b.mu.Unlock()

	w, exists := b.windows[key]
	if !exists {
		w = &UtilizationWindow{
			ResolutionKey:  resKey,
			InstrumentCode: m.Amount.GetInstrument().Code,
			WindowStart:    wStart,
			WindowEnd:      wStart.Add(b.windowSize),
			TotalUnits:     decimal.Zero,
			PeakUnits:      decimal.Zero,
			AvgUnits:       decimal.Zero,
		}
		b.windows[key] = w
	}

	w.TotalUnits = w.TotalUnits.Add(amount)
	w.ObservationCount++
	if amount.GreaterThan(w.PeakUnits) {
		w.PeakUnits = amount
	}
	w.recalculateAvg()
}

// Snapshot returns a copy of all current windows without draining.
func (b *AggregationBuffer) Snapshot() []*UtilizationWindow {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := make([]*UtilizationWindow, 0, len(b.windows))
	for _, w := range b.windows {
		cp := *w
		result = append(result, &cp)
	}
	return result
}

// Drain returns all current windows and clears the buffer.
func (b *AggregationBuffer) Drain() []*UtilizationWindow {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := make([]*UtilizationWindow, 0, len(b.windows))
	for _, w := range b.windows {
		cp := *w
		result = append(result, &cp)
	}
	b.windows = make(map[string]*UtilizationWindow)
	return result
}

// Restore merges windows back into the buffer, combining with any existing windows.
// Used to re-queue windows on publish failure to avoid data loss.
func (b *AggregationBuffer) Restore(windows []*UtilizationWindow) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, w := range windows {
		key := b.windowKey(w.ResolutionKey, w.WindowStart)
		existing, exists := b.windows[key]
		if !exists {
			cp := *w
			b.windows[key] = &cp
			continue
		}
		// Merge: add totals, keep max peak, sum observation counts
		existing.TotalUnits = existing.TotalUnits.Add(w.TotalUnits)
		existing.ObservationCount += w.ObservationCount
		if w.PeakUnits.GreaterThan(existing.PeakUnits) {
			existing.PeakUnits = w.PeakUnits
		}
		existing.recalculateAvg()
	}
}

// Size returns the number of windows currently in the buffer.
func (b *AggregationBuffer) Size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.windows)
}

// MarketDataPublisher aggregates utilization measurements and publishes them to MDS.
type MarketDataPublisher struct {
	mdsClient marketinformationv1.MarketInformationServiceClient
	buffer    *AggregationBuffer
	cb        *sharedclients.CircuitBreaker
	config    Config
	logger    *slog.Logger

	stopCh   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// NewMarketDataPublisher creates a new publisher that periodically flushes aggregated
// measurements to MDS via RecordObservationBatch.
func NewMarketDataPublisher(
	mdsClient marketinformationv1.MarketInformationServiceClient,
	config Config,
) (*MarketDataPublisher, error) {
	if mdsClient == nil {
		return nil, ErrNilMDSClient
	}

	// Normalize config with defaults for zero/invalid values
	defaults := DefaultConfig()
	if config.WindowSize <= 0 {
		config.WindowSize = defaults.WindowSize
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = defaults.FlushInterval
	}
	if config.SourceCode == "" {
		config.SourceCode = defaults.SourceCode
	}
	if config.CircuitBreakerConsecutiveFailures == 0 {
		config.CircuitBreakerConsecutiveFailures = defaults.CircuitBreakerConsecutiveFailures
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	cbConfig := sharedclients.DefaultCircuitBreakerConfig("mds-publisher")
	cbConfig.ReadyToTrip = func(counts gobreaker.Counts) bool {
		return counts.ConsecutiveFailures >= config.CircuitBreakerConsecutiveFailures
	}
	cb := sharedclients.NewCircuitBreaker(cbConfig, config.Logger)

	pub := &MarketDataPublisher{
		mdsClient: mdsClient,
		buffer:    NewAggregationBuffer(config.WindowSize),
		cb:        cb,
		config:    config,
		logger:    config.Logger.With("component", "market_data_publisher"),
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}

	go pub.flushLoop()

	return pub, nil
}

// Publish adds a utilization measurement to the aggregation buffer.
func (p *MarketDataPublisher) Publish(m *domain.UtilizationMeasurement) {
	p.buffer.Add(m)
	mdsBufferSize.Set(float64(p.buffer.Size()))
}

// BufferSize returns the current number of aggregation windows in the buffer.
func (p *MarketDataPublisher) BufferSize() int {
	return p.buffer.Size()
}

// IsCircuitBreakerOpen returns true if the circuit breaker is currently open.
func (p *MarketDataPublisher) IsCircuitBreakerOpen() bool {
	return p.cb.State() == gobreaker.StateOpen
}

// Stop gracefully stops the publisher, flushing any remaining buffered data.
// Safe to call multiple times.
func (p *MarketDataPublisher) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	<-p.done
}

// flushLoop periodically drains and publishes buffered windows to MDS.
func (p *MarketDataPublisher) flushLoop() {
	defer close(p.done)

	ticker := time.NewTicker(p.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.flush()
		case <-p.stopCh:
			p.flush() // Final flush on stop
			return
		}
	}
}

// flush drains the buffer and publishes all windows to MDS.
// On failure, windows are restored to the buffer to avoid data loss.
func (p *MarketDataPublisher) flush() {
	windows := p.buffer.Drain()
	if len(windows) == 0 {
		return
	}

	mdsBufferSize.Set(0)
	startTime := time.Now()

	observations := make([]*marketinformationv1.BatchObservationEntry, 0, len(windows))
	for _, w := range windows {
		observations = append(observations, buildObservation(w, p.config.SourceCode))
	}

	req := &marketinformationv1.RecordObservationBatchRequest{
		Observations: observations,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	respAny, err := p.cb.Execute(ctx, func() (any, error) {
		return p.mdsClient.RecordObservationBatch(ctx, req)
	})

	duration := time.Since(startTime).Seconds()
	mdsFlushDuration.Observe(duration)

	if err != nil {
		p.logger.Error("failed to publish observations to MDS",
			"error", err,
			"window_count", len(windows),
			"duration_seconds", duration,
		)
		mdsPublishErrorsTotal.WithLabelValues(classifyError(err)).Inc()
		// Re-queue windows to avoid data loss
		p.buffer.Restore(windows)
		mdsBufferSize.Set(float64(p.buffer.Size()))
		return
	}

	// Handle partial failures: the RPC supports partial success where
	// individual observations may fail while others succeed.
	successCount := len(observations)
	resp, _ := respAny.(*marketinformationv1.RecordObservationBatchResponse)
	if resp != nil && len(resp.Results) > 0 {
		var failed []*UtilizationWindow
		successCount = 0
		for _, r := range resp.Results {
			if r.Success {
				successCount++
				continue
			}
			idx := int(r.Index)
			if idx >= 0 && idx < len(windows) {
				failed = append(failed, windows[idx])
				p.logger.Warn("observation failed in batch",
					"index", idx,
					"resolution_key", windows[idx].ResolutionKey,
					"error", r.ErrorMessage,
				)
			}
		}
		if len(failed) > 0 {
			mdsPublishErrorsTotal.WithLabelValues("partial_failure").Inc()
			p.buffer.Restore(failed)
			mdsBufferSize.Set(float64(p.buffer.Size()))
		}
	}

	mdsObservationsPublishedTotal.Add(float64(successCount))
	p.logger.Info("published observations to MDS",
		"observation_count", successCount,
		"total_in_batch", len(observations),
		"duration_seconds", duration,
	)
}

// buildObservation converts an aggregated UtilizationWindow into a batch observation entry.
// The resolution key is passed via attributes so that the MDS data set's CEL
// resolution_key_expression can evaluate it.
func buildObservation(w *UtilizationWindow, sourceCode string) *marketinformationv1.BatchObservationEntry {
	midpoint := w.WindowStart.Add(w.WindowEnd.Sub(w.WindowStart) / 2)

	return &marketinformationv1.BatchObservationEntry{
		DatasetCode: fmt.Sprintf("UTILIZATION_%s", w.InstrumentCode),
		ObservedAt:  timestamppb.New(midpoint),
		ValidFrom:   timestamppb.New(w.WindowStart),
		ValidTo:     timestamppb.New(w.WindowEnd),
		Value:       w.TotalUnits.String(),
		Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		SourceCode:  sourceCode,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "resolution_key", Value: w.ResolutionKey},
			{Key: "peak_units", Value: w.PeakUnits.String()},
			{Key: "avg_units", Value: w.AvgUnits.String()},
			{Key: "observation_count", Value: fmt.Sprintf("%d", w.ObservationCount)},
		},
		ClientReference: w.ResolutionKey,
	}
}

// classifyError returns a label for the error type for metrics.
func classifyError(err error) string {
	if errors.Is(err, ErrCircuitBreakerOpen) {
		return "circuit_breaker_open"
	}
	return "publish_failed"
}
