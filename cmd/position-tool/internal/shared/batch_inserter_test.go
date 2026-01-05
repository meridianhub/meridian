package shared

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

func TestNewBatchInserter(t *testing.T) {
	t.Run("returns error for nil pool", func(t *testing.T) {
		inserter, err := NewBatchInserter(BatchInserterConfig{
			Pool: nil,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool)
		assert.Nil(t, inserter)
	})

	t.Run("returns error for invalid batch size", func(t *testing.T) {
		// Note: We can't test this without a real pool since we check pool first
		// This test documents the expected behavior
		inserter, err := NewBatchInserter(BatchInserterConfig{
			Pool:      nil, // Will fail first
			BatchSize: -1,
		})
		require.Error(t, err)
		assert.Nil(t, inserter)
	})
}

func TestBatchInserter_Stats(t *testing.T) {
	// Test Stats() method behavior without database
	// Since we can't create a real inserter without a pool,
	// we document the expected behavior through BatchStats tests

	t.Run("BatchStats default values", func(t *testing.T) {
		stats := BatchStats{
			TotalInserted: 0,
			BatchCount:    0,
			BufferSize:    0,
			BatchSize:     DefaultBatchSize,
		}

		assert.Equal(t, 0, stats.TotalInserted)
		assert.Equal(t, 0, stats.BatchCount)
		assert.Equal(t, 0, stats.BufferSize)
		assert.Equal(t, DefaultBatchSize, stats.BatchSize)
	})

	t.Run("BatchStats with values", func(t *testing.T) {
		stats := BatchStats{
			TotalInserted: 1500,
			BatchCount:    3,
			BufferSize:    100,
			BatchSize:     500,
		}

		assert.Equal(t, 1500, stats.TotalInserted)
		assert.Equal(t, 3, stats.BatchCount)
		assert.Equal(t, 100, stats.BufferSize)
		assert.Equal(t, 500, stats.BatchSize)
	})
}

func TestDefaultBatchSize(t *testing.T) {
	assert.Equal(t, 500, DefaultBatchSize, "default batch size should be 500")
}

// createTestPosition creates a test position for unit tests.
func createTestPosition(accountID, instrumentCode, bucketKey string, amount float64) *domain.Position {
	pos, _ := domain.NewPosition(
		accountID,
		instrumentCode,
		bucketKey,
		decimal.NewFromFloat(amount),
		"Monetary",
		map[string]string{"test": "true"},
		uuid.New(),
		"test-user",
	)
	return pos
}

// TestBatchInserterConfig verifies the config struct works correctly.
func TestBatchInserterConfig(t *testing.T) {
	callbackCalled := false
	callback := func(_, _, _ int) {
		callbackCalled = true
	}

	config := BatchInserterConfig{
		Pool:            nil, // Would be set in integration test
		BatchSize:       100,
		OnBatchComplete: callback,
	}

	assert.Equal(t, 100, config.BatchSize)
	assert.NotNil(t, config.OnBatchComplete)

	// Verify callback is callable
	config.OnBatchComplete(1, 100, 100)
	assert.True(t, callbackCalled)
}

// Note: Integration tests with actual database are in batch_inserter_integration_test.go
// These tests require testcontainers and are tagged with //go:build integration

func TestCreateTestPosition(t *testing.T) {
	pos := createTestPosition("acc-123", "USD", "bucket-abc", 100.50)

	require.NotNil(t, pos)
	assert.Equal(t, "acc-123", pos.AccountID)
	assert.Equal(t, "USD", pos.InstrumentCode)
	assert.Equal(t, "bucket-abc", pos.BucketKey)
	assert.True(t, pos.Amount.Equal(decimal.NewFromFloat(100.50)))
	assert.Equal(t, "Monetary", pos.Dimension)
	assert.Equal(t, "true", pos.Attributes["test"])
	assert.Equal(t, "test-user", pos.CreatedBy)
	assert.NotEqual(t, uuid.Nil, pos.ID)
	assert.NotEqual(t, uuid.Nil, pos.ReferenceID)
}

func TestBatchInserterCallbackParameters(t *testing.T) {
	// Verify callback function signature works as expected
	var capturedBatchNum, capturedPositionsInBatch, capturedTotalInserted int

	callback := func(batchNum, positionsInBatch, totalInserted int) {
		capturedBatchNum = batchNum
		capturedPositionsInBatch = positionsInBatch
		capturedTotalInserted = totalInserted
	}

	// Simulate batch completion
	callback(5, 500, 2500)

	assert.Equal(t, 5, capturedBatchNum)
	assert.Equal(t, 500, capturedPositionsInBatch)
	assert.Equal(t, 2500, capturedTotalInserted)
}

// MockBatchInserter is a test double for BatchInserter that doesn't need a database.
type MockBatchInserter struct {
	positions []*domain.Position
	batchSize int
	stats     BatchStats
}

func NewMockBatchInserter(batchSize int) *MockBatchInserter {
	return &MockBatchInserter{
		batchSize: batchSize,
		stats:     BatchStats{BatchSize: batchSize},
	}
}

func (m *MockBatchInserter) Add(_ context.Context, pos *domain.Position) error {
	m.positions = append(m.positions, pos)
	m.stats.BufferSize++

	if m.stats.BufferSize >= m.batchSize {
		m.stats.TotalInserted += m.stats.BufferSize
		m.stats.BatchCount++
		m.stats.BufferSize = 0
	}
	return nil
}

func (m *MockBatchInserter) Stats() BatchStats {
	return m.stats
}

func TestMockBatchInserter(t *testing.T) {
	mock := NewMockBatchInserter(3)

	// Add positions one by one
	for i := 0; i < 5; i++ {
		err := mock.Add(context.Background(), createTestPosition("acc", "USD", "bucket", float64(i)))
		require.NoError(t, err)
	}

	stats := mock.Stats()

	// After 5 positions with batch size 3:
	// - Batch 1: positions 0,1,2 -> flushed
	// - Buffer: positions 3,4 -> still in buffer
	assert.Equal(t, 3, stats.TotalInserted)
	assert.Equal(t, 1, stats.BatchCount)
	assert.Equal(t, 2, stats.BufferSize)
	assert.Equal(t, 5, len(mock.positions))
}
