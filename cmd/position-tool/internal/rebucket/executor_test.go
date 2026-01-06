package rebucket

import (
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid default config",
			config: &Config{
				BatchSize: 500,
				DryRun:    false,
			},
			wantErr: false,
		},
		{
			name: "valid small batch size",
			config: &Config{
				BatchSize: 1,
				DryRun:    false,
			},
			wantErr: false,
		},
		{
			name: "valid max batch size",
			config: &Config{
				BatchSize: 10000,
				DryRun:    false,
			},
			wantErr: false,
		},
		{
			name: "invalid zero batch size",
			config: &Config{
				BatchSize: 0,
				DryRun:    false,
			},
			wantErr: true,
			errMsg:  ErrInvalidBatchSize.Error(),
		},
		{
			name: "invalid negative batch size",
			config: &Config{
				BatchSize: -1,
				DryRun:    false,
			},
			wantErr: true,
			errMsg:  ErrInvalidBatchSize.Error(),
		},
		{
			name: "invalid too large batch size",
			config: &Config{
				BatchSize: 10001,
				DryRun:    false,
			},
			wantErr: true,
			errMsg:  ErrBatchSizeTooLarge.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, DefaultBatchSize, cfg.BatchSize)
	assert.False(t, cfg.DryRun)

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestNewExecutor_NilPool(t *testing.T) {
	// Test that nil pool returns error
	exec, err := NewExecutor(nil, DefaultConfig(), nil)
	assert.Error(t, err)
	assert.Nil(t, exec)
	assert.ErrorIs(t, err, ErrNilPool)
}

func TestAffectedPosition_Fields(t *testing.T) {
	now := time.Now().UTC()
	posID := uuid.New()
	refID := uuid.New()
	amount := decimal.NewFromFloat(100.50)

	pos := AffectedPosition{
		PositionID:     posID,
		AccountID:      "ACC-001",
		InstrumentCode: "CARBON_CREDIT",
		OldBucketKey:   "2024|VERRA",
		NewBucketKey:   "2024|GS",
		Amount:         amount,
		Dimension:      "COUNT",
		Attributes: map[string]string{
			"vintage_year": "2024",
			"registry":     "GS",
		},
		ReferenceID: refID,
		CreatedAt:   now,
		CreatedBy:   "admin@meridian.io",
	}

	assert.Equal(t, posID, pos.PositionID)
	assert.Equal(t, "ACC-001", pos.AccountID)
	assert.Equal(t, "CARBON_CREDIT", pos.InstrumentCode)
	assert.Equal(t, "2024|VERRA", pos.OldBucketKey)
	assert.Equal(t, "2024|GS", pos.NewBucketKey)
	assert.True(t, amount.Equal(pos.Amount))
	assert.Equal(t, "COUNT", pos.Dimension)
	assert.Equal(t, "2024", pos.Attributes["vintage_year"])
	assert.Equal(t, "GS", pos.Attributes["registry"])
	assert.Equal(t, refID, pos.ReferenceID)
	assert.Equal(t, now, pos.CreatedAt)
	assert.Equal(t, "admin@meridian.io", pos.CreatedBy)
}

func TestRebucketingPlan_Fields(t *testing.T) {
	plan := RebucketingPlan{
		InstrumentCode:       "CARBON_CREDIT",
		OldInstrumentVersion: "v1",
		NewInstrumentVersion: "v2",
		BucketMappings: map[string]string{
			"2024|VERRA": "2024|GS",
			"2023|VERRA": "2023|GS",
		},
		AffectedPositions: []AffectedPosition{
			{
				PositionID:     uuid.New(),
				AccountID:      "ACC-001",
				InstrumentCode: "CARBON_CREDIT",
				OldBucketKey:   "2024|VERRA",
				NewBucketKey:   "2024|GS",
				Amount:         decimal.NewFromInt(100),
				Dimension:      "COUNT",
			},
		},
	}

	assert.Equal(t, "CARBON_CREDIT", plan.InstrumentCode)
	assert.Equal(t, "v1", plan.OldInstrumentVersion)
	assert.Equal(t, "v2", plan.NewInstrumentVersion)
	assert.Len(t, plan.BucketMappings, 2)
	assert.Equal(t, "2024|GS", plan.BucketMappings["2024|VERRA"])
	assert.Len(t, plan.AffectedPositions, 1)
}

func TestExecutionResult_Fields(t *testing.T) {
	result := ExecutionResult{
		Success:          true,
		PositionsUpdated: 150,
		BucketsAffected:  3,
		AuditLogEntries:  300,
		Duration:         2 * time.Second,
		DryRun:           false,
		Error:            nil,
	}

	assert.True(t, result.Success)
	assert.Equal(t, int64(150), result.PositionsUpdated)
	assert.Equal(t, 3, result.BucketsAffected)
	assert.Equal(t, int64(300), result.AuditLogEntries)
	assert.Equal(t, 2*time.Second, result.Duration)
	assert.False(t, result.DryRun)
	assert.NoError(t, result.Error)
}

func TestExecutor_SplitIntoBatches(t *testing.T) {
	tests := []struct {
		name          string
		batchSize     int
		positionCount int
		wantBatches   int
		wantLastBatch int
	}{
		{
			name:          "exact multiple",
			batchSize:     100,
			positionCount: 300,
			wantBatches:   3,
			wantLastBatch: 100,
		},
		{
			name:          "partial last batch",
			batchSize:     100,
			positionCount: 250,
			wantBatches:   3,
			wantLastBatch: 50,
		},
		{
			name:          "single batch",
			batchSize:     100,
			positionCount: 50,
			wantBatches:   1,
			wantLastBatch: 50,
		},
		{
			name:          "empty positions",
			batchSize:     100,
			positionCount: 0,
			wantBatches:   0,
			wantLastBatch: 0,
		},
		{
			name:          "single position",
			batchSize:     100,
			positionCount: 1,
			wantBatches:   1,
			wantLastBatch: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal executor (pool is nil but we only test splitIntoBatches)
			exec := &Executor{
				batchSize: tt.batchSize,
			}

			// Generate test positions
			positions := make([]AffectedPosition, tt.positionCount)
			for i := range positions {
				positions[i] = AffectedPosition{
					PositionID: uuid.New(),
				}
			}

			batches := exec.splitIntoBatches(positions)

			assert.Len(t, batches, tt.wantBatches)

			if tt.wantBatches > 0 {
				assert.Len(t, batches[len(batches)-1], tt.wantLastBatch)

				// Verify total count matches
				var totalCount int
				for _, batch := range batches {
					totalCount += len(batch)
				}
				assert.Equal(t, tt.positionCount, totalCount)
			}
		})
	}
}

func TestExecutor_ExecuteDryRun(t *testing.T) {
	// Create executor with dry-run config
	cfg := &Config{
		BatchSize: 100,
		DryRun:    true,
	}

	exec := &Executor{
		config:    cfg,
		batchSize: cfg.BatchSize,
		logger:    slog.Default(),
	}

	// Create test plan
	plan := &RebucketingPlan{
		InstrumentCode:       "CARBON_CREDIT",
		OldInstrumentVersion: "v1",
		NewInstrumentVersion: "v2",
		BucketMappings: map[string]string{
			"2024|VERRA": "2024|GS",
		},
		AffectedPositions: make([]AffectedPosition, 250),
	}
	for i := range plan.AffectedPositions {
		plan.AffectedPositions[i] = AffectedPosition{
			PositionID:     uuid.New(),
			InstrumentCode: "CARBON_CREDIT",
			OldBucketKey:   "2024|VERRA",
			NewBucketKey:   "2024|GS",
			Amount:         decimal.NewFromInt(int64(i)),
		}
	}

	result, err := exec.executeDryRun(plan, time.Now())

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.True(t, result.DryRun)
	assert.Equal(t, int64(250), result.PositionsUpdated)
	assert.Equal(t, 1, result.BucketsAffected)
	assert.Equal(t, int64(500), result.AuditLogEntries) // 2 per position
	assert.Greater(t, result.Duration, time.Duration(0))
}
