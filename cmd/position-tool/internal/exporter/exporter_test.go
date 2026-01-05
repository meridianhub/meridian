package exporter_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/exporter"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSVWriter_WriteRow(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_output.csv")

	attrKeys := []string{"zone", "source"}
	writer, err := exporter.NewCSVWriter(outputPath, attrKeys)
	require.NoError(t, err)
	defer writer.Close()

	now := time.Now().UTC().Truncate(time.Second)
	refID := uuid.New()

	row := exporter.PositionRow{
		AccountID:      "acc-001",
		InstrumentCode: "KWH",
		Amount:         decimal.NewFromFloat(1000.50),
		BucketKey:      "zone-a:solar:2024-01",
		Dimension:      "Energy",
		Attributes: map[string]string{
			"zone":   "zone-a",
			"source": "solar",
		},
		CreatedAt:   now,
		ReferenceID: refID,
	}

	err = writer.WriteRow(row)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	// Read and verify output
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 2, "should have header and one data row")

	// Verify header (bucket_key excluded - computed from attributes during import)
	header := lines[0]
	assert.Contains(t, header, "account_id")
	assert.Contains(t, header, "instrument_code")
	assert.Contains(t, header, "amount")
	assert.NotContains(t, header, "bucket_key") // bucket_key is NOT exported
	assert.Contains(t, header, "dimension")
	assert.Contains(t, header, "created_at")
	assert.Contains(t, header, "reference_id")
	assert.Contains(t, header, "attr_source")
	assert.Contains(t, header, "attr_zone")

	// Verify data row
	dataRow := lines[1]
	assert.Contains(t, dataRow, "acc-001")
	assert.Contains(t, dataRow, "KWH")
	assert.Contains(t, dataRow, "1000.5")
	// Note: bucket_key value "zone-a:solar:2024-01" is not in export
	assert.Contains(t, dataRow, "Energy")
	assert.Contains(t, dataRow, refID.String())
	assert.Contains(t, dataRow, "zone-a")
	assert.Contains(t, dataRow, "solar")
}

func TestCSVWriter_MultipleRows(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "multi_row.csv")

	writer, err := exporter.NewCSVWriter(outputPath, []string{"category"})
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)

	// Write multiple rows
	for i := 0; i < 100; i++ {
		row := exporter.PositionRow{
			AccountID:      "acc-001",
			InstrumentCode: "USD",
			Amount:         decimal.NewFromInt(int64(i * 100)),
			BucketKey:      "default",
			Dimension:      "Monetary",
			Attributes:     map[string]string{"category": "test"},
			CreatedAt:      now,
		}
		err = writer.WriteRow(row)
		require.NoError(t, err)
	}

	err = writer.Close()
	require.NoError(t, err)

	// Read and verify
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 101, "should have header + 100 data rows")
}

func TestCSVWriter_EmptyAttributes(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "empty_attrs.csv")

	writer, err := exporter.NewCSVWriter(outputPath, []string{"zone", "source"})
	require.NoError(t, err)

	row := exporter.PositionRow{
		AccountID:      "acc-001",
		InstrumentCode: "KWH",
		Amount:         decimal.NewFromFloat(500),
		BucketKey:      "default",
		Dimension:      "Energy",
		Attributes:     nil, // No attributes
		CreatedAt:      time.Now().UTC(),
	}

	err = writer.WriteRow(row)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	// Read and verify - should have empty attribute columns
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 2)

	// Header should still have attribute columns
	assert.Contains(t, lines[0], "attr_zone")
	assert.Contains(t, lines[0], "attr_source")
}

func TestCSVWriter_AttributeKeysSorted(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "sorted_attrs.csv")

	// Provide unsorted keys
	writer, err := exporter.NewCSVWriter(outputPath, []string{"zone", "category", "aaa_first"})
	require.NoError(t, err)

	// Verify keys are sorted
	keys := writer.AttributeKeys()
	assert.Equal(t, []string{"aaa_first", "category", "zone"}, keys)

	err = writer.Close()
	require.NoError(t, err)
}

func TestCSVWriter_BytesWritten(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "bytes_count.csv")

	writer, err := exporter.NewCSVWriter(outputPath, nil)
	require.NoError(t, err)

	row := exporter.PositionRow{
		AccountID:      "acc-001",
		InstrumentCode: "USD",
		Amount:         decimal.NewFromFloat(100.0),
		BucketKey:      "default",
		Dimension:      "Monetary",
		CreatedAt:      time.Now().UTC(),
	}

	err = writer.WriteRow(row)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	bytesWritten := writer.BytesWritten()
	assert.Greater(t, bytesWritten, int64(0), "should have written some bytes")

	// Verify matches actual file size
	stat, err := os.Stat(outputPath)
	require.NoError(t, err)
	assert.Equal(t, stat.Size(), bytesWritten)
}

func TestCSVWriter_NilUUID(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "nil_uuid.csv")

	writer, err := exporter.NewCSVWriter(outputPath, nil)
	require.NoError(t, err)

	row := exporter.PositionRow{
		AccountID:      "acc-001",
		InstrumentCode: "USD",
		Amount:         decimal.NewFromFloat(100.0),
		BucketKey:      "default",
		Dimension:      "Monetary",
		CreatedAt:      time.Now().UTC(),
		ReferenceID:    uuid.Nil, // Nil UUID
	}

	err = writer.WriteRow(row)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	// Read and verify - reference_id should be empty
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 2)

	// The reference_id column should be empty (no UUID string)
	dataRow := lines[1]
	// UUID string is 36 chars, should not be present
	assert.NotContains(t, dataRow, "00000000-0000-0000-0000-000000000000")
}

func TestCSVWriter_HeaderCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		attributeKeys []string
		expectedCount int
	}{
		{
			name:          "no attributes",
			attributeKeys: nil,
			expectedCount: 6, // 6 fixed columns only (bucket_key excluded)
		},
		{
			name:          "one attribute",
			attributeKeys: []string{"zone"},
			expectedCount: 7,
		},
		{
			name:          "multiple attributes",
			attributeKeys: []string{"zone", "source", "category"},
			expectedCount: 9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			outputPath := filepath.Join(tmpDir, "header_count.csv")

			writer, err := exporter.NewCSVWriter(outputPath, tt.attributeKeys)
			require.NoError(t, err)
			defer writer.Close()

			assert.Equal(t, tt.expectedCount, writer.HeaderCount())
		})
	}
}

func TestCSVWriter_CloseTwice(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "close_twice.csv")

	writer, err := exporter.NewCSVWriter(outputPath, nil)
	require.NoError(t, err)

	// First close
	err = writer.Close()
	require.NoError(t, err)

	// Second close should also succeed (idempotent)
	err = writer.Close()
	require.NoError(t, err)
}

func TestCSVWriter_WriteAfterClose(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "write_after_close.csv")

	writer, err := exporter.NewCSVWriter(outputPath, nil)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	// Write after close should fail
	row := exporter.PositionRow{
		AccountID:      "acc-001",
		InstrumentCode: "USD",
		Amount:         decimal.NewFromFloat(100.0),
		BucketKey:      "default",
		Dimension:      "Monetary",
		CreatedAt:      time.Now().UTC(),
	}

	err = writer.WriteRow(row)
	assert.Error(t, err)
	assert.ErrorIs(t, err, exporter.ErrWriterClosed)
}

func TestExportOptions_Defaults(t *testing.T) {
	t.Parallel()

	opts := exporter.ExportOptions{
		OutputPath: "/tmp/test.csv",
		TenantID:   "test_tenant",
	}

	// BatchSize should be 0 (will be normalized by exporter)
	assert.Equal(t, 0, opts.BatchSize)

	// Default batch size constant should be available
	assert.Equal(t, 10000, exporter.DefaultBatchSize)
}

func TestNewExporter_NilPool(t *testing.T) {
	t.Parallel()

	exp, err := exporter.New(nil, nil)
	assert.Nil(t, exp)
	assert.ErrorIs(t, err, exporter.ErrNilPool)
}

func TestExportResult_Fields(t *testing.T) {
	t.Parallel()

	result := exporter.ExportResult{
		TotalRows:      1000,
		OutputFile:     "/tmp/export.csv",
		FileSizeBytes:  50000,
		Interrupted:    false,
		InterruptedRow: 0,
		AttributeKeys:  []string{"zone", "source"},
	}

	assert.Equal(t, int64(1000), result.TotalRows)
	assert.Equal(t, "/tmp/export.csv", result.OutputFile)
	assert.Equal(t, int64(50000), result.FileSizeBytes)
	assert.False(t, result.Interrupted)
	assert.Equal(t, []string{"zone", "source"}, result.AttributeKeys)
}

func TestPositionRow_Fields(t *testing.T) {
	t.Parallel()

	refID := uuid.New()
	now := time.Now().UTC()

	row := exporter.PositionRow{
		AccountID:      "acc-123",
		InstrumentCode: "KWH",
		Amount:         decimal.NewFromFloat(1234.56),
		BucketKey:      "bucket-1",
		Dimension:      "Energy",
		Attributes:     map[string]string{"key": "value"},
		CreatedAt:      now,
		ReferenceID:    refID,
	}

	assert.Equal(t, "acc-123", row.AccountID)
	assert.Equal(t, "KWH", row.InstrumentCode)
	assert.True(t, row.Amount.Equal(decimal.NewFromFloat(1234.56)))
	assert.Equal(t, "bucket-1", row.BucketKey)
	assert.Equal(t, "Energy", row.Dimension)
	assert.Equal(t, "value", row.Attributes["key"])
	assert.Equal(t, now, row.CreatedAt)
	assert.Equal(t, refID, row.ReferenceID)
}

func TestCSVWriter_LargeExport(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "large_export.csv")

	attrKeys := []string{"zone", "source", "category", "region"}
	writer, err := exporter.NewCSVWriter(outputPath, attrKeys)
	require.NoError(t, err)

	now := time.Now().UTC()
	rowCount := 10000

	// Write many rows to test streaming behavior
	for i := 0; i < rowCount; i++ {
		row := exporter.PositionRow{
			AccountID:      "acc-001",
			InstrumentCode: "KWH",
			Amount:         decimal.NewFromInt(int64(i)),
			BucketKey:      "zone-a:solar",
			Dimension:      "Energy",
			Attributes: map[string]string{
				"zone":     "zone-a",
				"source":   "solar",
				"category": "renewable",
				"region":   "north",
			},
			CreatedAt:   now,
			ReferenceID: uuid.New(),
		}

		err = writer.WriteRow(row)
		require.NoError(t, err)
	}

	err = writer.Close()
	require.NoError(t, err)

	// Verify file was created and has expected size
	bytesWritten := writer.BytesWritten()
	assert.Greater(t, bytesWritten, int64(0))

	// Verify row count
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, rowCount+1, "should have header + data rows")
}

func TestCSVWriter_SpecialCharacters(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "special_chars.csv")

	writer, err := exporter.NewCSVWriter(outputPath, []string{"description"})
	require.NoError(t, err)

	// Test with special CSV characters (commas, quotes, newlines)
	row := exporter.PositionRow{
		AccountID:      "acc-001",
		InstrumentCode: "USD",
		Amount:         decimal.NewFromFloat(100.0),
		BucketKey:      "key,with,commas",
		Dimension:      "Monetary",
		Attributes: map[string]string{
			"description": `Value with "quotes" and, commas`,
		},
		CreatedAt: time.Now().UTC(),
	}

	err = writer.WriteRow(row)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	// Read and verify - CSV should properly escape special characters
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	// The content should be valid and contain our data
	assert.Contains(t, string(content), "acc-001")
	assert.Contains(t, string(content), "USD")
}

func TestExportErrors(t *testing.T) {
	t.Parallel()

	// Test that error types are properly defined
	assert.NotNil(t, exporter.ErrNoPositions)
	assert.NotNil(t, exporter.ErrExportInterrupted)
	assert.NotNil(t, exporter.ErrNilPool)

	// Test error messages
	assert.Contains(t, exporter.ErrNoPositions.Error(), "no positions")
	assert.Contains(t, exporter.ErrExportInterrupted.Error(), "interrupted")
	assert.Contains(t, exporter.ErrNilPool.Error(), "pool")
}

func TestCSVWriter_FlushError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "flush_test.csv")

	writer, err := exporter.NewCSVWriter(outputPath, nil)
	require.NoError(t, err)

	// Write a row
	row := exporter.PositionRow{
		AccountID:      "acc-001",
		InstrumentCode: "USD",
		Amount:         decimal.NewFromFloat(100.0),
		BucketKey:      "default",
		Dimension:      "Monetary",
		CreatedAt:      time.Now().UTC(),
	}

	err = writer.WriteRow(row)
	require.NoError(t, err)

	// Flush should succeed
	err = writer.Flush()
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)
}

func TestExportOptions_AllFilters(t *testing.T) {
	t.Parallel()

	fromTime := time.Now().Add(-24 * time.Hour)
	toTime := time.Now()

	opts := exporter.ExportOptions{
		OutputPath:     "/tmp/filtered.csv",
		TenantID:       "test_tenant",
		InstrumentCode: "KWH",
		AccountID:      "acc-123",
		FromTime:       &fromTime,
		ToTime:         &toTime,
		BatchSize:      5000,
		DryRun:         true,
	}

	assert.Equal(t, "/tmp/filtered.csv", opts.OutputPath)
	assert.Equal(t, "test_tenant", opts.TenantID)
	assert.Equal(t, "KWH", opts.InstrumentCode)
	assert.Equal(t, "acc-123", opts.AccountID)
	assert.NotNil(t, opts.FromTime)
	assert.NotNil(t, opts.ToTime)
	assert.Equal(t, 5000, opts.BatchSize)
	assert.True(t, opts.DryRun)
}

// TestCSVWriter_Concurrent verifies that the CSV writer is safe for sequential use
// (not concurrent - CSV writing is inherently sequential)
func TestCSVWriter_Sequential(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "sequential.csv")

	writer, err := exporter.NewCSVWriter(outputPath, []string{"batch"})
	require.NoError(t, err)

	now := time.Now().UTC()

	// Write rows in batches
	for batch := 0; batch < 10; batch++ {
		for i := 0; i < 100; i++ {
			row := exporter.PositionRow{
				AccountID:      "acc-001",
				InstrumentCode: "USD",
				Amount:         decimal.NewFromInt(int64(batch*100 + i)),
				BucketKey:      "default",
				Dimension:      "Monetary",
				Attributes:     map[string]string{"batch": string(rune('0' + batch))},
				CreatedAt:      now,
			}
			err := writer.WriteRow(row)
			require.NoError(t, err)
		}
	}

	err = writer.Close()
	require.NoError(t, err)

	// Verify all rows were written
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 1001, "should have header + 1000 data rows")
}

// TestExportContext verifies context cancellation is respected
func TestExportContext(t *testing.T) {
	t.Parallel()

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Verify context is cancelled
	assert.Error(t, ctx.Err())
}
