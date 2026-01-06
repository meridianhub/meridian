package infra

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// DefaultBatchSize is the default number of positions to insert per batch.
// This value is optimized for efficient COPY protocol usage without
// overwhelming memory or connection resources.
const DefaultBatchSize = 500

// Batch inserter errors.
var (
	// ErrNilPool is returned when a nil connection pool is passed.
	ErrNilPool = errors.New("database connection pool cannot be nil")
	// ErrInvalidBatchSize is returned when batch size is less than 1.
	ErrInvalidBatchSize = errors.New("batch size must be at least 1")
	// ErrBatchInsertFailed is returned when a batch insert operation fails.
	ErrBatchInsertFailed = errors.New("batch insert failed")
)

// BatchInserter manages efficient bulk insertion of position records.
// It batches positions into groups of configurable size and uses the
// PositionRepository's InsertBatch method which leverages pgx COPY protocol.
//
// The inserter maintains statistics about the operation and supports
// callbacks for progress tracking.
type BatchInserter struct {
	repo      *persistence.PositionRepository
	batchSize int

	// Callback for progress tracking (optional)
	onBatchComplete func(batchNum int, positionsInBatch int, totalInserted int)

	// Accumulated positions for the current batch
	buffer []*domain.Position

	// Statistics
	totalInserted int
	batchCount    int
}

// BatchInserterConfig contains configuration for creating a BatchInserter.
type BatchInserterConfig struct {
	// Pool is the database connection pool (required).
	Pool *pgxpool.Pool

	// BatchSize is the number of positions per batch (default: 500).
	BatchSize int

	// OnBatchComplete is called after each batch is successfully inserted.
	// Parameters: batchNumber, positionsInBatch, totalInsertedSoFar
	OnBatchComplete func(batchNum int, positionsInBatch int, totalInserted int)
}

// NewBatchInserter creates a new BatchInserter with the given configuration.
// Returns ErrNilPool if the pool is nil, or ErrInvalidBatchSize if batch size < 1.
func NewBatchInserter(config BatchInserterConfig) (*BatchInserter, error) {
	if config.Pool == nil {
		return nil, ErrNilPool
	}

	batchSize := config.BatchSize
	if batchSize == 0 {
		batchSize = DefaultBatchSize
	}
	if batchSize < 1 {
		return nil, ErrInvalidBatchSize
	}

	return &BatchInserter{
		repo:            persistence.NewPositionRepository(config.Pool),
		batchSize:       batchSize,
		onBatchComplete: config.OnBatchComplete,
		buffer:          make([]*domain.Position, 0, batchSize),
	}, nil
}

// Add queues a position for batch insertion.
// When the buffer reaches the configured batch size, it automatically
// flushes to the database.
//
// Thread-safety: This method is NOT thread-safe. Callers should ensure
// single-threaded access or use external synchronization.
func (bi *BatchInserter) Add(ctx context.Context, position *domain.Position) error {
	bi.buffer = append(bi.buffer, position)

	if len(bi.buffer) >= bi.batchSize {
		return bi.flushBuffer(ctx)
	}

	return nil
}

// AddAll queues multiple positions for batch insertion.
// Positions are added one by one, triggering automatic flushes as needed.
func (bi *BatchInserter) AddAll(ctx context.Context, positions []*domain.Position) error {
	for _, pos := range positions {
		if err := bi.Add(ctx, pos); err != nil {
			return err
		}
	}
	return nil
}

// Flush inserts any remaining buffered positions to the database.
// This should be called after all positions have been added to ensure
// the final partial batch is persisted.
func (bi *BatchInserter) Flush(ctx context.Context) error {
	if len(bi.buffer) > 0 {
		return bi.flushBuffer(ctx)
	}
	return nil
}

// flushBuffer inserts the current buffer to the database and resets it.
func (bi *BatchInserter) flushBuffer(ctx context.Context) error {
	if len(bi.buffer) == 0 {
		return nil
	}

	positionsInBatch := len(bi.buffer)

	if err := bi.repo.InsertBatch(ctx, bi.buffer); err != nil {
		return errors.Join(ErrBatchInsertFailed,
			fmt.Errorf("batch %d with %d positions: %w", bi.batchCount+1, positionsInBatch, err))
	}

	bi.totalInserted += positionsInBatch
	bi.batchCount++

	// Call progress callback if configured
	if bi.onBatchComplete != nil {
		bi.onBatchComplete(bi.batchCount, positionsInBatch, bi.totalInserted)
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
	// TotalInserted is the total number of positions inserted so far.
	TotalInserted int

	// BatchCount is the number of batches that have been flushed.
	BatchCount int

	// BufferSize is the current number of positions in the buffer awaiting flush.
	BufferSize int

	// BatchSize is the configured maximum batch size.
	BatchSize int
}

// Reset clears the inserter state for reuse.
// This does NOT roll back any already-inserted data.
func (bi *BatchInserter) Reset() {
	bi.buffer = bi.buffer[:0]
	bi.totalInserted = 0
	bi.batchCount = 0
}
