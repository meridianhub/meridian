package executor

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test sentinel errors
var errTestConnectionTimeout = errors.New("connection timeout")

// testLogger returns a discarding logger for tests.
func testLogger() *slog.Logger {
	return slog.Default()
}

func makeTestPlan(positionCount int) *RebucketingPlan {
	positions := make([]AffectedPosition, positionCount)
	for i := 0; i < positionCount; i++ {
		positions[i] = AffectedPosition{
			PositionID:     uuid.New(),
			AccountID:      "ACC001",
			InstrumentCode: "GBP",
			OldBucketKey:   "old-bucket-1",
			NewBucketKey:   "new-bucket-1",
			Amount:         decimal.NewFromInt(100),
			Dimension:      "Monetary",
			CreatedAt:      time.Now().UTC(),
			CreatedBy:      "original-user",
		}
	}

	return &RebucketingPlan{
		InstrumentCode:       "GBP",
		OldInstrumentVersion: "v1-abc123",
		NewInstrumentVersion: "v2-def456",
		BucketMappings: map[string]string{
			"old-bucket-1": "new-bucket-1",
		},
		AffectedPositions: positions,
	}
}

func TestNewPositionUpdateExecutor(t *testing.T) {
	t.Run("returns error for nil pool", func(t *testing.T) {
		_, err := NewPositionUpdateExecutor(nil, nil, nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool)
	})

	t.Run("validates pool before config", func(t *testing.T) {
		config := &Config{BatchSize: 0}
		_, err := NewPositionUpdateExecutor(nil, config, nil)

		require.Error(t, err)
		// Pool is validated before config, so nil pool error is returned first
		assert.ErrorIs(t, err, ErrNilPool)
	})
}

func TestValidatePlan(t *testing.T) {
	// Create an executor with validation only (pool not needed for validation)
	executor := &PositionUpdateExecutor{
		config:      DefaultConfig(),
		authorizer:  NewAdminAuthorizer(),
		auditLogger: NewAuditLogger(),
		logger:      testLogger(),
	}

	t.Run("accepts valid plan", func(t *testing.T) {
		plan := makeTestPlan(10)

		err := executor.validatePlan(plan)

		assert.NoError(t, err)
	})

	t.Run("rejects nil plan", func(t *testing.T) {
		err := executor.validatePlan(nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPlan)
	})

	t.Run("rejects empty positions", func(t *testing.T) {
		plan := &RebucketingPlan{
			InstrumentCode:       "GBP",
			OldInstrumentVersion: "v1",
			NewInstrumentVersion: "v2",
			AffectedPositions:    []AffectedPosition{},
		}

		err := executor.validatePlan(plan)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyPlan)
	})

	t.Run("rejects missing old version", func(t *testing.T) {
		plan := makeTestPlan(1)
		plan.OldInstrumentVersion = ""

		err := executor.validatePlan(plan)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingInstrumentVersion)
	})

	t.Run("rejects missing new version", func(t *testing.T) {
		plan := makeTestPlan(1)
		plan.NewInstrumentVersion = ""

		err := executor.validatePlan(plan)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingInstrumentVersion)
	})

	t.Run("rejects empty old bucket key in mapping", func(t *testing.T) {
		plan := makeTestPlan(1)
		plan.BucketMappings[""] = "new-bucket"

		err := executor.validatePlan(plan)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidBucketMapping)
	})

	t.Run("rejects empty new bucket key in mapping", func(t *testing.T) {
		plan := makeTestPlan(1)
		plan.BucketMappings["old-bucket"] = ""

		err := executor.validatePlan(plan)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidBucketMapping)
	})
}

func TestExecute_Authorization(t *testing.T) {
	executor := &PositionUpdateExecutor{
		config:      DefaultConfig(),
		authorizer:  NewAdminAuthorizer(),
		auditLogger: NewAuditLogger(),
		logger:      testLogger(),
	}

	t.Run("rejects unauthorized user", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{
			UserID: "operator-user",
			Roles:  []string{"operator"},
		})
		plan := makeTestPlan(1)

		result, err := executor.Execute(ctx, plan)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrUnauthorized))
		assert.False(t, result.Success)
	})

	t.Run("rejects missing claims", func(t *testing.T) {
		ctx := context.Background()
		plan := makeTestPlan(1)

		result, err := executor.Execute(ctx, plan)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClaims)
		assert.False(t, result.Success)
	})
}

func TestExecute_DryRun(t *testing.T) {
	config := DefaultConfig()
	config.DryRun = true

	// Create a minimal executor without pool (dry-run doesn't need DB)
	posUpdater := &PositionUpdater{batchSize: config.BatchSize}
	executor := &PositionUpdateExecutor{
		config:      config,
		authorizer:  NewAdminAuthorizer(),
		auditLogger: NewAuditLogger(),
		posUpdater:  posUpdater,
		logger:      testLogger(),
	}

	t.Run("returns results without making changes", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{
			UserID: "admin-user",
			Roles:  []string{"admin"},
		})
		plan := makeTestPlan(100)

		result, err := executor.Execute(ctx, plan)

		require.NoError(t, err)
		assert.True(t, result.Success)
		assert.True(t, result.DryRun)
		assert.Equal(t, int64(100), result.PositionsUpdated)
		assert.Equal(t, 1, result.BucketsAffected)
		assert.Equal(t, int64(200), result.AuditLogEntries) // 2 per position
	})
}

func TestDryRun(t *testing.T) {
	config := DefaultConfig()
	posUpdater := &PositionUpdater{batchSize: config.BatchSize}
	executor := &PositionUpdateExecutor{
		config:      config,
		authorizer:  NewAdminAuthorizer(),
		auditLogger: NewAuditLogger(),
		posUpdater:  posUpdater,
		logger:      testLogger(),
	}

	t.Run("generates dry run plan", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{
			UserID: "admin-user",
			Roles:  []string{"admin"},
		})
		plan := makeTestPlan(1000)

		dryRunPlan, err := executor.DryRun(ctx, plan)

		require.NoError(t, err)
		assert.Equal(t, "GBP", dryRunPlan.InstrumentCode)
		assert.Equal(t, "v1-abc123", dryRunPlan.OldInstrumentVersion)
		assert.Equal(t, "v2-def456", dryRunPlan.NewInstrumentVersion)
		assert.Equal(t, int64(1000), dryRunPlan.AffectedPositionCount)
		assert.Equal(t, int64(2000), dryRunPlan.EstimatedAuditEntries)
		assert.Equal(t, 2, dryRunPlan.EstimatedBatches) // 1000 / 500 = 2
	})

	t.Run("rejects unauthorized user", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{
			UserID: "operator-user",
			Roles:  []string{"operator"},
		})
		plan := makeTestPlan(1)

		_, err := executor.DryRun(ctx, plan)

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrUnauthorized))
	})
}

func TestGenerateDryRunPlan(t *testing.T) {
	config := DefaultConfig()
	posUpdater := &PositionUpdater{batchSize: config.BatchSize}
	executor := &PositionUpdateExecutor{
		config:      config,
		authorizer:  NewAdminAuthorizer(),
		auditLogger: NewAuditLogger(),
		posUpdater:  posUpdater,
		logger:      testLogger(),
	}

	t.Run("generates bucket summary", func(t *testing.T) {
		plan := &RebucketingPlan{
			InstrumentCode:       "GBP",
			OldInstrumentVersion: "v1",
			NewInstrumentVersion: "v2",
			BucketMappings: map[string]string{
				"bucket-a": "bucket-x",
				"bucket-b": "bucket-y",
			},
			AffectedPositions: []AffectedPosition{
				{PositionID: uuid.New(), OldBucketKey: "bucket-a", NewBucketKey: "bucket-x", Amount: decimal.NewFromInt(100)},
				{PositionID: uuid.New(), OldBucketKey: "bucket-a", NewBucketKey: "bucket-x", Amount: decimal.NewFromInt(200)},
				{PositionID: uuid.New(), OldBucketKey: "bucket-b", NewBucketKey: "bucket-y", Amount: decimal.NewFromInt(50)},
			},
		}

		dryRunPlan := executor.generateDryRunPlan(plan)

		require.Len(t, dryRunPlan.BucketSummary, 2)

		// Find bucket-a summary
		var bucketASummary *BucketMappingSummary
		for i := range dryRunPlan.BucketSummary {
			if dryRunPlan.BucketSummary[i].OldBucketKey == "bucket-a" {
				bucketASummary = &dryRunPlan.BucketSummary[i]
				break
			}
		}
		require.NotNil(t, bucketASummary)
		assert.Equal(t, int64(2), bucketASummary.PositionCount)
		assert.True(t, bucketASummary.TotalAmount.Equal(decimal.NewFromInt(300)))
	})
}

func TestPrintDryRunReport(t *testing.T) {
	executor := &PositionUpdateExecutor{}

	plan := &DryRunPlan{
		InstrumentCode:        "KWH",
		OldInstrumentVersion:  "v1-old",
		NewInstrumentVersion:  "v2-new",
		AffectedPositionCount: 1500,
		EstimatedBatches:      3,
		EstimatedAuditEntries: 3000,
		BucketSummary: []BucketMappingSummary{
			{OldBucketKey: "peak", NewBucketKey: "peak-summer", PositionCount: 1000, TotalAmount: decimal.NewFromInt(50000)},
			{OldBucketKey: "offpeak", NewBucketKey: "offpeak-winter", PositionCount: 500, TotalAmount: decimal.NewFromInt(25000)},
		},
	}

	report := executor.PrintDryRunReport(plan)

	assert.Contains(t, report, "KWH")
	assert.Contains(t, report, "v1-old")
	assert.Contains(t, report, "v2-new")
	assert.Contains(t, report, "1500")
	assert.Contains(t, report, "peak -> peak-summer")
	assert.Contains(t, report, "offpeak -> offpeak-winter")
}

func TestPrintExecutionReport(t *testing.T) {
	executor := &PositionUpdateExecutor{}

	t.Run("prints successful result", func(t *testing.T) {
		result := &ExecutionResult{
			Success:          true,
			PositionsUpdated: 1500,
			BucketsAffected:  12,
			AuditLogEntries:  3000,
			Duration:         4200 * time.Millisecond,
			DryRun:           false,
		}

		report := executor.PrintExecutionReport(result)

		assert.Contains(t, report, "COMPLETED")
		assert.Contains(t, report, "Live")
		assert.Contains(t, report, "1500")
		assert.Contains(t, report, "12")
		assert.Contains(t, report, "3000")
	})

	t.Run("prints failed result with error", func(t *testing.T) {
		result := &ExecutionResult{
			Success:          false,
			PositionsUpdated: 0,
			Duration:         100 * time.Millisecond,
			Error:            errTestConnectionTimeout,
		}

		report := executor.PrintExecutionReport(result)

		assert.Contains(t, report, "FAILED")
		assert.Contains(t, report, "connection timeout")
	})

	t.Run("prints partial progress", func(t *testing.T) {
		result := &ExecutionResult{
			Success:  false,
			Duration: 2 * time.Second,
			Error:    ErrTransactionRollback,
			PartialProgress: &PartialProgress{
				PositionsProcessed: 750,
				BatchesCompleted:   1,
			},
		}

		report := executor.PrintExecutionReport(result)

		assert.Contains(t, report, "FAILED")
		assert.Contains(t, report, "750")
		assert.Contains(t, report, "Partial Progress")
	})

	t.Run("prints dry-run mode", func(t *testing.T) {
		result := &ExecutionResult{
			Success: true,
			DryRun:  true,
		}

		report := executor.PrintExecutionReport(result)

		assert.Contains(t, report, "Dry-Run")
	})
}

func TestExecutionResultFields(t *testing.T) {
	t.Run("execution result has all fields", func(t *testing.T) {
		positionID := uuid.New()
		result := &ExecutionResult{
			Success:          true,
			PositionsUpdated: 500,
			BucketsAffected:  5,
			AuditLogEntries:  1000,
			Duration:         3 * time.Second,
			DryRun:           false,
			PartialProgress: &PartialProgress{
				PositionsProcessed:      250,
				LastProcessedPositionID: positionID,
				BatchesCompleted:        1,
			},
		}

		assert.True(t, result.Success)
		assert.Equal(t, int64(500), result.PositionsUpdated)
		assert.Equal(t, 5, result.BucketsAffected)
		assert.Equal(t, int64(1000), result.AuditLogEntries)
		assert.Equal(t, 3*time.Second, result.Duration)
		assert.False(t, result.DryRun)
		require.NotNil(t, result.PartialProgress)
		assert.Equal(t, int64(250), result.PartialProgress.PositionsProcessed)
		assert.Equal(t, positionID, result.PartialProgress.LastProcessedPositionID)
		assert.Equal(t, 1, result.PartialProgress.BatchesCompleted)
	})
}
