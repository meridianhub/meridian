//go:build integration

// Package main contains integration tests for the market-data-tool CLI.
//
// These tests use Testcontainers to spin up a PostgreSQL database and the
// Market Information Service for end-to-end testing of the import workflow.
//
// Run with: go test -tags=integration -v ./cmd/market-data-tool/...
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/adapters/csv"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/validation"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// testContext holds the test infrastructure.
type testContext struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	dbURL    string
	tenantID string
}

func setupTestContext(t *testing.T) *testContext {
	t.Helper()

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("meridian_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate postgres container: %v", err)
		}
	})

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create connection pool
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	// Run migrations to create checkpoint table
	err = runMigrations(ctx, pool)
	require.NoError(t, err)

	return &testContext{
		ctx:      ctx,
		pool:     pool,
		dbURL:    connStr,
		tenantID: "test-tenant",
	}
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// Create the checkpoint table used by position-tool that we're reusing
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS import_manifest (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			source_file TEXT NOT NULL,
			file_checksum TEXT NOT NULL,
			total_rows INTEGER,
			processed_rows INTEGER NOT NULL DEFAULT 0,
			success_count INTEGER,
			failure_count INTEGER,
			status TEXT NOT NULL DEFAULT 'RUNNING',
			rollback_sql TEXT,
			error_message TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT import_manifest_tenant_file_checksum_unique
				UNIQUE (tenant_id, source_file, file_checksum)
		);

		CREATE INDEX IF NOT EXISTS idx_import_manifest_tenant_status
			ON import_manifest(tenant_id, status);
	`)
	return err
}

func TestCSVParser_Integration(t *testing.T) {
	t.Run("parses FX rate CSV file", func(t *testing.T) {
		csvData := `observed_at,quality_level,value,currency_pair
2024-01-15T10:30:00Z,ACTUAL,1.0856,USD_EUR
2024-01-15T11:30:00Z,ACTUAL,1.0860,USD_EUR
2024-01-15T12:30:00Z,ESTIMATE,1.0865,USD_EUR`

		dataset := &infra.DataSetDefinition{
			Code: "USD_EUR_FX",
		}

		parser := csv.NewParser(dataset)
		var rows []csv.ObservationRow

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), csv.DefaultParseConfig(), func(batch csv.RowBatch) error {
			rows = append(rows, batch.Rows...)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 3, result.RowCount)
		assert.Equal(t, 0, result.ErrorCount)
		require.Len(t, rows, 3)

		// Verify first row
		assert.Equal(t, 2, rows[0].LineNumber)
		assert.Equal(t, "1.0856", rows[0].Value)
		assert.Equal(t, "ACTUAL", rows[0].QualityLevel)
		assert.Equal(t, "USD_EUR", rows[0].Attributes["currency_pair"])
	})

	t.Run("parses large CSV with batching", func(t *testing.T) {
		// Generate 1000 rows
		var lines []string
		lines = append(lines, "observed_at,quality_level,value")
		for i := 0; i < 1000; i++ {
			lines = append(lines, fmt.Sprintf("2024-01-15T%02d:30:00Z,ACTUAL,1.%04d", i%24, i))
		}
		csvData := strings.Join(lines, "\n")

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := csv.NewParser(dataset)

		var batchCount int
		var totalRows int

		config := csv.ParseConfig{
			BatchSize:        100,
			SkipEmptyRows:    true,
			TimestampFormats: []string{time.RFC3339},
		}

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), config, func(batch csv.RowBatch) error {
			batchCount++
			totalRows += len(batch.Rows)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 1000, result.RowCount)
		assert.Equal(t, 10, batchCount) // 1000 / 100 = 10 batches
		assert.Equal(t, 1000, totalRows)
	})
}

func TestValidationPipeline_Integration(t *testing.T) {
	t.Run("validates observation rows with schema", func(t *testing.T) {
		schema := `{
			"type": "object",
			"properties": {
				"tenor": {"type": "string"},
				"settlement_type": {"type": "string"}
			},
			"required": ["tenor"]
		}`

		schemaValidator := validation.NewSchemaValidatorFromJSON(schema)
		celPreview := infra.NewCELPreview("value != ''")

		pipeline := validation.NewPipeline(validation.PipelineConfig{
			SchemaValidator: schemaValidator,
			CELPreview:      celPreview,
		})

		// Valid row
		validRow := &validation.ObservationRow{
			LineNumber:   2,
			Value:        "5.25",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
			Attributes: map[string]string{
				"tenor":           "1M",
				"settlement_type": "T+2",
			},
		}
		result := pipeline.ValidateRow(context.Background(), validRow)
		assert.False(t, result.HasErrors())

		// Invalid row - missing required attribute
		invalidRow := &validation.ObservationRow{
			LineNumber:   3,
			Value:        "5.30",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
			Attributes: map[string]string{
				"settlement_type": "T+2",
			},
		}
		result = pipeline.ValidateRow(context.Background(), invalidRow)
		assert.True(t, result.HasErrors())

		summary := pipeline.Summary()
		assert.Equal(t, 2, summary.TotalRows)
		assert.Equal(t, 1, summary.ValidRows)
		assert.Equal(t, 1, summary.InvalidRows)
	})
}

func TestCheckpointManager_Integration(t *testing.T) {
	tc := setupTestContext(t)

	t.Run("creates and resumes checkpoint", func(t *testing.T) {
		// Create test CSV file
		tmpDir := t.TempDir()
		testCSVPath := filepath.Join(tmpDir, "test.csv")
		err := os.WriteFile(testCSVPath, []byte("observed_at,quality_level,value\n2024-01-15T10:30:00Z,ACTUAL,1.0856\n"), 0o644)
		require.NoError(t, err)

		// Create checkpoint manager
		checkpointMgr, err := infra.NewCheckpointManager(tc.ctx, tc.dbURL)
		require.NoError(t, err)
		defer checkpointMgr.Close()

		// Start import
		cp, err := checkpointMgr.StartImport(tc.ctx, tc.tenantID, testCSVPath)
		require.NoError(t, err)
		assert.NotEmpty(t, cp.ManifestID)

		// Update progress
		cp.IncrementSuccess(100)
		err = checkpointMgr.UpdateProgress(tc.ctx, cp)
		require.NoError(t, err)

		// Resume by ID
		resumed, err := checkpointMgr.ResumeByID(tc.ctx, cp.ManifestID)
		require.NoError(t, err)
		assert.Equal(t, cp.ManifestID, resumed.ManifestID)
		assert.Equal(t, 100, resumed.SuccessCount)
	})

	t.Run("handles checkpoint completion", func(t *testing.T) {
		// Create test CSV file
		tmpDir := t.TempDir()
		completeCSVPath := filepath.Join(tmpDir, "complete.csv")
		err := os.WriteFile(completeCSVPath, []byte("observed_at,quality_level,value\n2024-01-15T10:30:00Z,ACTUAL,1.0856\n"), 0o644)
		require.NoError(t, err)

		checkpointMgr, err := infra.NewCheckpointManager(tc.ctx, tc.dbURL)
		require.NoError(t, err)
		defer checkpointMgr.Close()

		cp, err := checkpointMgr.StartImport(tc.ctx, tc.tenantID, completeCSVPath)
		require.NoError(t, err)

		cp.SetTotalRows(500)
		cp.IncrementSuccess(500)

		err = checkpointMgr.Complete(tc.ctx, cp)
		require.NoError(t, err)

		// Verify status in database
		var status string
		err = tc.pool.QueryRow(tc.ctx,
			"SELECT status FROM import_manifest WHERE id = $1",
			cp.ManifestID).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "COMPLETED", status)
	})
}

func TestCELPreview_Integration(t *testing.T) {
	t.Run("evaluates simple expression", func(t *testing.T) {
		preview := infra.NewCELPreview("value != '' && quality_level == 'ACTUAL'")
		require.True(t, preview.IsEnabled())

		result := preview.Evaluate("1.0856", "USD_EUR_FX", "ACTUAL", nil)
		assert.True(t, result.Valid)
	})

	t.Run("fails for empty value", func(t *testing.T) {
		preview := infra.NewCELPreview("value != ''")

		result := preview.Evaluate("", "USD_EUR_FX", "ACTUAL", nil)
		assert.False(t, result.Valid)
	})

	t.Run("handles invalid expression gracefully", func(t *testing.T) {
		preview := infra.NewCELPreview("invalid {{ expression")
		assert.False(t, preview.IsEnabled())

		result := preview.Evaluate("1.0", "TEST", "ACTUAL", nil)
		assert.True(t, result.Valid) // Disabled preview defaults to valid
	})

	t.Run("accesses attributes", func(t *testing.T) {
		preview := infra.NewCELPreview("attributes['tenor'] == '1M'")

		result := preview.Evaluate("5.25", "INTEREST_RATE", "ACTUAL", map[string]string{
			"tenor": "1M",
		})
		assert.True(t, result.Valid)

		result = preview.Evaluate("5.25", "INTEREST_RATE", "ACTUAL", map[string]string{
			"tenor": "3M",
		})
		assert.False(t, result.Valid)
	})
}

func TestEndToEnd_ImportWorkflow(t *testing.T) {
	tc := setupTestContext(t)

	t.Run("complete import workflow simulation", func(t *testing.T) {
		// Create test CSV file
		tmpDir := t.TempDir()
		csvPath := filepath.Join(tmpDir, "fx_rates.csv")
		csvContent := `observed_at,quality_level,value,valid_from
2024-01-15T10:30:00Z,ACTUAL,1.0856,2024-01-15T00:00:00Z
2024-01-15T11:30:00Z,ACTUAL,1.0860,2024-01-15T00:00:00Z
2024-01-15T12:30:00Z,ESTIMATE,1.0865,2024-01-15T00:00:00Z
2024-01-15T13:30:00Z,ACTUAL,1.0870,2024-01-15T00:00:00Z
2024-01-15T14:30:00Z,PROVISIONAL,1.0875,2024-01-15T00:00:00Z`

		err := os.WriteFile(csvPath, []byte(csvContent), 0o644)
		require.NoError(t, err)

		// Create checkpoint manager
		checkpointMgr, err := infra.NewCheckpointManager(tc.ctx, tc.dbURL)
		require.NoError(t, err)
		defer checkpointMgr.Close()

		// Start import
		cp, err := checkpointMgr.StartImport(tc.ctx, tc.tenantID, csvPath)
		require.NoError(t, err)

		// Parse CSV
		file, err := os.Open(csvPath)
		require.NoError(t, err)
		defer file.Close()

		dataset := &infra.DataSetDefinition{Code: "USD_EUR_FX"}
		parser := csv.NewParser(dataset)

		// Create validation pipeline
		pipeline := validation.NewPipeline(validation.PipelineConfig{})

		// Process rows
		var validCount, errorCount int
		parseResult, err := parser.Parse(tc.ctx, file, csv.DefaultParseConfig(), func(batch csv.RowBatch) error {
			for _, csvRow := range batch.Rows {
				row := &validation.ObservationRow{
					LineNumber:   csvRow.LineNumber,
					Value:        csvRow.Value,
					QualityLevel: csvRow.QualityLevel,
					ObservedAt:   csvRow.ObservedAt,
					ValidFrom:    csvRow.ValidFrom,
				}

				result := pipeline.ValidateRow(tc.ctx, row)
				if result.HasErrors() {
					errorCount++
					cp.IncrementFailure(1)
				} else {
					validCount++
					cp.IncrementSuccess(1)
				}
			}
			return nil
		})
		require.NoError(t, err)

		// Complete import
		cp.SetTotalRows(parseResult.RowCount)
		err = checkpointMgr.Complete(tc.ctx, cp)
		require.NoError(t, err)

		// Verify results
		assert.Equal(t, 5, parseResult.RowCount)
		assert.Equal(t, 5, validCount)
		assert.Equal(t, 0, errorCount)

		// Verify checkpoint in database
		var status string
		var successRows int
		err = tc.pool.QueryRow(tc.ctx,
			"SELECT status, success_count FROM import_manifest WHERE id = $1",
			cp.ManifestID).Scan(&status, &successRows)
		require.NoError(t, err)
		assert.Equal(t, "COMPLETED", status)
		assert.Equal(t, 5, successRows)
	})
}

func TestCheckpointResume_Integration(t *testing.T) {
	tc := setupTestContext(t)

	t.Run("resumes interrupted import", func(t *testing.T) {
		// Create test CSV file
		tmpDir := t.TempDir()
		largeCSVPath := filepath.Join(tmpDir, "large_import.csv")
		err := os.WriteFile(largeCSVPath, []byte("observed_at,quality_level,value\n2024-01-15T10:30:00Z,ACTUAL,1.0856\n"), 0o644)
		require.NoError(t, err)

		// Create checkpoint manager
		checkpointMgr, err := infra.NewCheckpointManager(tc.ctx, tc.dbURL)
		require.NoError(t, err)
		defer checkpointMgr.Close()

		// Start import and simulate interruption at row 500
		cp, err := checkpointMgr.StartImport(tc.ctx, tc.tenantID, largeCSVPath)
		require.NoError(t, err)

		// Simulate processing 500 rows
		for i := 0; i < 500; i++ {
			cp.IncrementSuccess(1)
		}
		cp.SetTotalRows(1000)

		// Cancel the import (simulate interruption)
		err = checkpointMgr.Cancel(tc.ctx, cp)
		require.NoError(t, err)

		// Wait for database to persist
		err = await.Until(func() bool {
			var status string
			queryErr := tc.pool.QueryRow(tc.ctx,
				"SELECT status FROM import_manifest WHERE id = $1",
				cp.ManifestID).Scan(&status)
			return queryErr == nil && status == "CANCELLED"
		})
		require.NoError(t, err)

		// Resume the import
		resumed, err := checkpointMgr.ResumeByID(tc.ctx, cp.ManifestID)
		require.NoError(t, err)
		assert.Equal(t, 500, resumed.SuccessCount)
		assert.Equal(t, 500, resumed.ProcessedRows)

		// Complete the remaining 500 rows
		for i := 0; i < 500; i++ {
			resumed.IncrementSuccess(1)
		}
		err = checkpointMgr.Complete(tc.ctx, resumed)
		require.NoError(t, err)

		// Verify final state
		var status string
		var successRows int
		err = tc.pool.QueryRow(tc.ctx,
			"SELECT status, success_count FROM import_manifest WHERE id = $1",
			resumed.ManifestID).Scan(&status, &successRows)
		require.NoError(t, err)
		assert.Equal(t, "COMPLETED", status)
		assert.Equal(t, 1000, successRows)
	})
}
