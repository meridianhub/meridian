package validation

import (
	"context"
	"sync"

	bloom "github.com/bits-and-blooms/bloom/v3"
)

// BloomFilterConfig contains configuration for the bloom filter.
type BloomFilterConfig struct {
	// ExpectedItems is the expected number of items to be added to the filter.
	// This determines the filter size.
	ExpectedItems uint

	// FalsePositiveRate is the desired false positive rate (0.0-1.0).
	// Lower values require more memory. Recommended: 0.01 (1%).
	FalsePositiveRate float64
}

// DefaultBloomFilterConfig returns configuration optimized for bulk imports.
func DefaultBloomFilterConfig() BloomFilterConfig {
	return BloomFilterConfig{
		ExpectedItems:     1_000_000, // 1M expected measurements
		FalsePositiveRate: 0.01,      // 1% false positive rate
	}
}

// DatabaseLookup is a function that checks if measurement IDs exist in the database.
// It receives a batch of IDs and returns the set of IDs that exist.
type DatabaseLookup func(ctx context.Context, measurementIDs []string) (map[string]bool, error)

// DuplicateChecker provides fast duplicate detection using a bloom filter
// backed by database verification for potential matches.
//
// The bloom filter provides O(1) lookup with ~90% reduction in database queries
// by eliminating definite non-duplicates. Potential duplicates (bloom filter hits)
// are verified against the database to handle false positives.
//
// Thread-safety: All methods are safe for concurrent use.
type DuplicateChecker struct {
	filter   *bloom.BloomFilter
	dbLookup DatabaseLookup

	// mu protects the bloom filter and seen map
	mu sync.RWMutex

	// seen tracks measurement IDs added in this session (for within-file duplicates).
	// Maps measurement_id -> line_number of first occurrence.
	seen map[string]int

	// Stats
	bloomHits        int64
	bloomMisses      int64
	dbLookups        int64
	dbFalsePositives int64
	truePositives    int64
	withinFileDupes  int64
}

// NewDuplicateChecker creates a new duplicate checker with the given configuration.
// The dbLookup function is called to verify potential duplicates against the database.
func NewDuplicateChecker(cfg BloomFilterConfig, dbLookup DatabaseLookup) *DuplicateChecker {
	if cfg.ExpectedItems == 0 {
		cfg = DefaultBloomFilterConfig()
	}
	if cfg.FalsePositiveRate <= 0 || cfg.FalsePositiveRate >= 1 {
		cfg.FalsePositiveRate = 0.01
	}

	return &DuplicateChecker{
		filter:   bloom.NewWithEstimates(cfg.ExpectedItems, cfg.FalsePositiveRate),
		dbLookup: dbLookup,
		seen:     make(map[string]int),
	}
}

// CheckResult contains the result of a duplicate check.
type CheckResult struct {
	// IsDuplicate indicates if the measurement is a duplicate.
	IsDuplicate bool

	// InDatabase indicates if the duplicate was found in the database.
	InDatabase bool

	// ExistingLineNumber is the line number of the first occurrence within the file.
	// Only set if the duplicate is within the current file.
	ExistingLineNumber int
}

// Check tests if a measurement ID is a duplicate.
// It first checks the in-memory bloom filter, then verifies against the database
// if there's a potential match.
//
// After checking, the measurement ID is added to the filter for future checks.
func (dc *DuplicateChecker) Check(ctx context.Context, measurementID string, lineNumber int) (*CheckResult, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	result := &CheckResult{}

	// Check for within-file duplicates first
	if existingLine, exists := dc.seen[measurementID]; exists {
		dc.withinFileDupes++
		dc.truePositives++
		result.IsDuplicate = true
		result.ExistingLineNumber = existingLine
		return result, nil
	}

	// Check bloom filter for potential database duplicates
	if dc.filter.Test([]byte(measurementID)) {
		dc.bloomHits++

		// Bloom filter hit - verify against database
		if dc.dbLookup != nil {
			dc.dbLookups++
			exists, err := dc.dbLookup(ctx, []string{measurementID})
			if err != nil {
				return nil, err
			}

			if exists[measurementID] {
				dc.truePositives++
				result.IsDuplicate = true
				result.InDatabase = true
				return result, nil
			}

			// False positive from bloom filter
			dc.dbFalsePositives++
		}
	} else {
		dc.bloomMisses++
	}

	// Not a duplicate - add to tracking
	dc.filter.Add([]byte(measurementID))
	dc.seen[measurementID] = lineNumber

	return result, nil
}

// CheckBatch tests multiple measurement IDs for duplicates in a batch.
// This is more efficient than individual checks as it batches database lookups.
func (dc *DuplicateChecker) CheckBatch(ctx context.Context, rows []ImportRow) (map[int]*CheckResult, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	results := make(map[int]*CheckResult)
	var potentialDBDuplicates []string
	potentialDBRowMap := make(map[string]int) // measurementID -> lineNumber

	for _, row := range rows {
		result := &CheckResult{}

		// Check for within-file duplicates first
		if existingLine, exists := dc.seen[row.MeasurementID]; exists {
			dc.withinFileDupes++
			dc.truePositives++
			result.IsDuplicate = true
			result.ExistingLineNumber = existingLine
			results[row.LineNumber] = result
			continue
		}

		// Check bloom filter
		if dc.filter.Test([]byte(row.MeasurementID)) {
			dc.bloomHits++
			// Queue for database verification
			potentialDBDuplicates = append(potentialDBDuplicates, row.MeasurementID)
			potentialDBRowMap[row.MeasurementID] = row.LineNumber
		} else {
			dc.bloomMisses++
			// Definitely not a duplicate
			results[row.LineNumber] = result
			// Add to tracking
			dc.filter.Add([]byte(row.MeasurementID))
			dc.seen[row.MeasurementID] = row.LineNumber
		}
	}

	// Batch database lookup for potential duplicates
	if len(potentialDBDuplicates) > 0 && dc.dbLookup != nil {
		dc.dbLookups++
		exists, err := dc.dbLookup(ctx, potentialDBDuplicates)
		if err != nil {
			return nil, err
		}

		for _, measurementID := range potentialDBDuplicates {
			lineNumber := potentialDBRowMap[measurementID]
			result := &CheckResult{}

			if exists[measurementID] {
				dc.truePositives++
				result.IsDuplicate = true
				result.InDatabase = true
			} else {
				// False positive from bloom filter
				dc.dbFalsePositives++
				// Not a duplicate - add to tracking
				dc.filter.Add([]byte(measurementID))
				dc.seen[measurementID] = lineNumber
			}

			results[lineNumber] = result
		}
	}

	return results, nil
}

// PreloadFromDatabase loads existing measurement IDs into the bloom filter.
// This should be called before processing begins to seed the filter with
// known duplicates, reducing database lookups during validation.
func (dc *DuplicateChecker) PreloadFromDatabase(ctx context.Context, measurementIDs []string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	for _, id := range measurementIDs {
		dc.filter.Add([]byte(id))
	}
}

// DuplicateStats contains statistics about duplicate detection.
type DuplicateStats struct {
	// BloomHits is the number of bloom filter positive matches.
	BloomHits int64

	// BloomMisses is the number of bloom filter negative matches.
	BloomMisses int64

	// DatabaseLookups is the number of database verification queries.
	DatabaseLookups int64

	// FalsePositives is the number of bloom filter false positives.
	FalsePositives int64

	// TruePositives is the number of confirmed duplicates.
	TruePositives int64

	// WithinFileDuplicates is the number of duplicates within the current file.
	WithinFileDuplicates int64

	// UniqueIDs is the number of unique measurement IDs seen.
	UniqueIDs int
}

// Stats returns statistics about duplicate detection performance.
func (dc *DuplicateChecker) Stats() DuplicateStats {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	return DuplicateStats{
		BloomHits:            dc.bloomHits,
		BloomMisses:          dc.bloomMisses,
		DatabaseLookups:      dc.dbLookups,
		FalsePositives:       dc.dbFalsePositives,
		TruePositives:        dc.truePositives,
		WithinFileDuplicates: dc.withinFileDupes,
		UniqueIDs:            len(dc.seen),
	}
}

// FalsePositiveRate returns the observed false positive rate.
func (dc *DuplicateChecker) FalsePositiveRate() float64 {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	if dc.bloomHits == 0 {
		return 0
	}
	return float64(dc.dbFalsePositives) / float64(dc.bloomHits)
}

// Reset clears the duplicate checker state.
// This should be called when starting a new import.
func (dc *DuplicateChecker) Reset(cfg BloomFilterConfig) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if cfg.ExpectedItems == 0 {
		cfg = DefaultBloomFilterConfig()
	}
	if cfg.FalsePositiveRate <= 0 || cfg.FalsePositiveRate >= 1 {
		cfg.FalsePositiveRate = 0.01
	}

	dc.filter = bloom.NewWithEstimates(cfg.ExpectedItems, cfg.FalsePositiveRate)
	dc.seen = make(map[string]int)
	dc.bloomHits = 0
	dc.bloomMisses = 0
	dc.dbLookups = 0
	dc.dbFalsePositives = 0
	dc.truePositives = 0
	dc.withinFileDupes = 0
}
