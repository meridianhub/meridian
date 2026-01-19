package validation

import (
	"context"
	"fmt"
	"sync"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
)

// DatasetChecker validates that a dataset exists and is active via gRPC.
// Results are cached to minimize gRPC calls.
type DatasetChecker struct {
	client      *infra.GRPCClient
	datasetCode string

	// Cache for dataset existence check
	mu       sync.RWMutex
	checked  bool
	exists   bool
	isActive bool
	err      error
}

// NewDatasetChecker creates a new dataset checker.
func NewDatasetChecker(client *infra.GRPCClient, datasetCode string) *DatasetChecker {
	return &DatasetChecker{
		client:      client,
		datasetCode: datasetCode,
	}
}

// Check validates that the dataset exists and is active.
// The result is cached after the first call.
func (c *DatasetChecker) Check(ctx context.Context, datasetCode string) error {
	// Fast path: already checked
	c.mu.RLock()
	if c.checked {
		defer c.mu.RUnlock()
		return c.err
	}
	c.mu.RUnlock()

	// Slow path: need to check via gRPC
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.checked {
		return c.err
	}

	// Perform the check
	dataset, err := c.client.GetDataSet(ctx, datasetCode, nil)
	if err != nil {
		c.checked = true
		c.exists = false
		c.err = fmt.Errorf("%w: %s: %w", ErrDatasetNotFound, datasetCode, err)
		return c.err
	}

	c.exists = true
	c.isActive = dataset.Status == "DATA_SET_STATUS_ACTIVE"
	c.checked = true

	if !c.isActive {
		c.err = fmt.Errorf("%w: %s (status: %s)", ErrDatasetNotActive, datasetCode, dataset.Status)
		return c.err
	}

	return nil
}

// Reset clears the cached result, allowing a fresh check.
func (c *DatasetChecker) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = false
	c.exists = false
	c.isActive = false
	c.err = nil
}

// IsChecked returns true if the dataset has been checked.
func (c *DatasetChecker) IsChecked() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.checked
}

// Exists returns true if the dataset exists (must call Check first).
func (c *DatasetChecker) Exists() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.exists
}

// IsActive returns true if the dataset is active (must call Check first).
func (c *DatasetChecker) IsActive() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isActive
}
