package infra

import (
	"context"
	"errors"
	"fmt"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
)

// DefaultBatchSize is the default number of observations to insert per batch.
const DefaultBatchSize = 500

// Batch inserter errors.
var (
	// ErrNilClient is returned when a nil gRPC client is passed.
	ErrNilClient = errors.New("gRPC client cannot be nil")
	// ErrInvalidBatchSize is returned when batch size is less than 1.
	ErrInvalidBatchSize = errors.New("batch size must be at least 1")
	// ErrBatchInsertFailed is returned when a batch insert operation fails.
	ErrBatchInsertFailed = errors.New("batch insert failed")
)

// BatchInserter manages efficient bulk insertion of observation records via gRPC.
type BatchInserter struct {
	client      *GRPCClient
	batchSize   int
	datasetCode string
	sourceCode  string

	// Callback for progress tracking (optional)
	onBatchComplete func(batchNum int, observationsInBatch int, totalInserted int)

	// Accumulated observations for the current batch
	buffer []*ObservationEntry

	// Statistics
	totalInserted int
	batchCount    int
}

// BatchInserterConfig contains configuration for creating a BatchInserter.
type BatchInserterConfig struct {
	// Client is the gRPC client (required).
	Client *GRPCClient

	// BatchSize is the number of observations per batch (default: 500).
	BatchSize int

	// DatasetCode is the target dataset for all observations.
	DatasetCode string

	// SourceCode is the data source code (e.g., "BLOOMBERG") for all observations.
	SourceCode string

	// OnBatchComplete is called after each batch is successfully inserted.
	OnBatchComplete func(batchNum int, observationsInBatch int, totalInserted int)
}

// NewBatchInserter creates a new BatchInserter with the given configuration.
func NewBatchInserter(config BatchInserterConfig) *BatchInserter {
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	return &BatchInserter{
		client:          config.Client,
		batchSize:       batchSize,
		datasetCode:     config.DatasetCode,
		sourceCode:      config.SourceCode,
		onBatchComplete: config.OnBatchComplete,
		buffer:          make([]*ObservationEntry, 0, batchSize),
	}
}

// Add queues an observation for batch insertion.
// When the buffer reaches the configured batch size, it automatically
// flushes to the service.
func (bi *BatchInserter) Add(ctx context.Context, entry *ObservationEntry) error {
	if bi.client == nil {
		return ErrNilClient
	}

	// Set dataset and source if not already set
	if entry.DatasetCode == "" {
		entry.DatasetCode = bi.datasetCode
	}
	if entry.SourceCode == "" {
		entry.SourceCode = bi.sourceCode
	}

	bi.buffer = append(bi.buffer, entry)

	if len(bi.buffer) >= bi.batchSize {
		return bi.flushBuffer(ctx)
	}

	return nil
}

// Flush inserts any remaining buffered observations to the service.
func (bi *BatchInserter) Flush(ctx context.Context) error {
	if len(bi.buffer) > 0 {
		return bi.flushBuffer(ctx)
	}
	return nil
}

// flushBuffer sends the current buffer to the service and resets it.
func (bi *BatchInserter) flushBuffer(ctx context.Context) error {
	if len(bi.buffer) == 0 {
		return nil
	}

	observationsInBatch := len(bi.buffer)

	// Convert to proto entries
	protoEntries := make([]*marketinformationv1.BatchObservationEntry, 0, len(bi.buffer))
	for _, entry := range bi.buffer {
		protoEntries = append(protoEntries, entry.ToProto())
	}

	// Send batch to service
	resp, err := bi.client.RecordObservationBatch(ctx, protoEntries)
	if err != nil {
		return errors.Join(ErrBatchInsertFailed,
			fmt.Errorf("batch %d with %d observations: %w", bi.batchCount+1, observationsInBatch, err))
	}

	// Note: Partial failures (resp.FailureCount > 0) are acceptable.
	// Individual row errors are logged by the service and tracked in the response.
	// The caller can use bi.Stats() to get final success/failure counts.

	bi.totalInserted += int(resp.SuccessCount)
	bi.batchCount++

	// Call progress callback if configured
	if bi.onBatchComplete != nil {
		bi.onBatchComplete(bi.batchCount, int(resp.SuccessCount), bi.totalInserted)
	}

	// Reset buffer, preserving capacity
	bi.buffer = bi.buffer[:0]

	return nil
}

// Stats returns the current insertion statistics.
func (bi *BatchInserter) Stats() BatchStats {
	return BatchStats{
		TotalInserted: bi.totalInserted,
		BatchCount:    bi.batchCount,
		BufferSize:    len(bi.buffer),
		BatchSize:     bi.batchSize,
	}
}

// BatchStats contains statistics about the batch insertion operation.
type BatchStats struct {
	// TotalInserted is the total number of observations inserted so far.
	TotalInserted int

	// BatchCount is the number of batches that have been flushed.
	BatchCount int

	// BufferSize is the current number of observations in the buffer awaiting flush.
	BufferSize int

	// BatchSize is the configured maximum batch size.
	BatchSize int
}

// Reset clears the inserter state for reuse.
func (bi *BatchInserter) Reset() {
	bi.buffer = bi.buffer[:0]
	bi.totalInserted = 0
	bi.batchCount = 0
}
