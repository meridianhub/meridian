package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	t.Run("returns error for nil pool", func(t *testing.T) {
		manager, err := NewManager(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool)
		assert.Nil(t, manager)
	})
}

func TestStatus(t *testing.T) {
	tests := []struct {
		status   Status
		expected string
	}{
		{StatusRunning, "RUNNING"},
		{StatusCompleted, "COMPLETED"},
		{StatusFailed, "FAILED"},
		{StatusCancelled, "CANCELLED"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestCheckpoint_AddRollbackStatement(t *testing.T) {
	cp := &Checkpoint{}

	t.Run("adds single statement", func(t *testing.T) {
		cp.AddRollbackStatement("DELETE FROM positions WHERE id = '123'")
		assert.Len(t, cp.RollbackSQL, 1)
		assert.Equal(t, "DELETE FROM positions WHERE id = '123'", cp.RollbackSQL[0])
	})

	t.Run("adds multiple statements", func(t *testing.T) {
		cp.AddRollbackStatement("DELETE FROM positions WHERE id = '456'")
		assert.Len(t, cp.RollbackSQL, 2)
	})
}

func TestCheckpoint_IncrementSuccess(t *testing.T) {
	cp := &Checkpoint{}

	cp.IncrementSuccess(5)
	assert.Equal(t, 5, cp.SuccessCount)
	assert.Equal(t, 5, cp.ProcessedRows)

	cp.IncrementSuccess(3)
	assert.Equal(t, 8, cp.SuccessCount)
	assert.Equal(t, 8, cp.ProcessedRows)
}

func TestCheckpoint_IncrementFailure(t *testing.T) {
	cp := &Checkpoint{}

	cp.IncrementFailure(2)
	assert.Equal(t, 2, cp.FailureCount)
	assert.Equal(t, 2, cp.ProcessedRows)

	cp.IncrementFailure(1)
	assert.Equal(t, 3, cp.FailureCount)
	assert.Equal(t, 3, cp.ProcessedRows)
}

// TestCheckpoint_ShouldSkipResumedLine pins the resume off-by-one boundary.
//
// ProcessedRows counts data rows; the source CSV has a header on line 1, so M
// processed data rows occupy lines 2..M+1. On resume the guard must skip
// exactly those lines and no more. The boundary case (M processed, line M+1)
// is the last committed data row: a guard of `<= ProcessedRows` would NOT skip
// it, reprocessing it and duplicating its ledger position.
func TestCheckpoint_ShouldSkipResumedLine(t *testing.T) {
	tests := []struct {
		name          string
		processedRows int
		lineNumber    int
		wantSkip      bool
	}{
		// Fresh import (nothing processed): never skip, even the header-adjacent line.
		{name: "no rows processed, first data row", processedRows: 0, lineNumber: 2, wantSkip: false},
		{name: "no rows processed, header line", processedRows: 0, lineNumber: 1, wantSkip: false},

		// 5 data rows processed -> they occupy lines 2..6.
		{name: "5 processed, header line skipped", processedRows: 5, lineNumber: 1, wantSkip: true},
		{name: "5 processed, first committed data row", processedRows: 5, lineNumber: 2, wantSkip: true},
		{name: "5 processed, boundary last committed data row", processedRows: 5, lineNumber: 6, wantSkip: true},
		{name: "5 processed, first new data row", processedRows: 5, lineNumber: 7, wantSkip: false},
		{name: "5 processed, later new data row", processedRows: 5, lineNumber: 11, wantSkip: false},

		// 1 data row processed -> it occupies line 2.
		{name: "1 processed, committed data row", processedRows: 1, lineNumber: 2, wantSkip: true},
		{name: "1 processed, next data row", processedRows: 1, lineNumber: 3, wantSkip: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &Checkpoint{ProcessedRows: tt.processedRows}
			assert.Equal(t, tt.wantSkip, cp.ShouldSkipResumedLine(tt.lineNumber))
		})
	}
}

func TestCheckpoint_SetTotalRows(t *testing.T) {
	cp := &Checkpoint{}

	cp.SetTotalRows(1000)
	assert.Equal(t, 1000, cp.TotalRows)

	cp.SetTotalRows(500)
	assert.Equal(t, 500, cp.TotalRows)
}

func TestCheckpoint_Progress(t *testing.T) {
	tests := []struct {
		name          string
		totalRows     int
		processedRows int
		expected      float64
	}{
		{
			name:          "zero total returns zero",
			totalRows:     0,
			processedRows: 10,
			expected:      0,
		},
		{
			name:          "50 percent complete",
			totalRows:     100,
			processedRows: 50,
			expected:      50.0,
		},
		{
			name:          "100 percent complete",
			totalRows:     100,
			processedRows: 100,
			expected:      100.0,
		},
		{
			name:          "25 percent complete",
			totalRows:     1000,
			processedRows: 250,
			expected:      25.0,
		},
		{
			name:          "handles partial percentage",
			totalRows:     3,
			processedRows: 1,
			expected:      33.333333333333336,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &Checkpoint{
				TotalRows:     tt.totalRows,
				ProcessedRows: tt.processedRows,
			}
			assert.InDelta(t, tt.expected, cp.Progress(), 0.0001)
		})
	}
}

func TestCheckpoint_IsResumable(t *testing.T) {
	tests := []struct {
		status   Status
		expected bool
	}{
		{StatusRunning, true},
		{StatusCancelled, true},
		{StatusFailed, true},
		{StatusCompleted, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			cp := &Checkpoint{Status: tt.status}
			assert.Equal(t, tt.expected, cp.IsResumable())
		})
	}
}

func TestCheckpoint_MixedIncrements(t *testing.T) {
	cp := &Checkpoint{}

	cp.IncrementSuccess(10)
	cp.IncrementFailure(2)
	cp.IncrementSuccess(5)
	cp.IncrementFailure(1)

	assert.Equal(t, 15, cp.SuccessCount)
	assert.Equal(t, 3, cp.FailureCount)
	assert.Equal(t, 18, cp.ProcessedRows)
}

func TestCalculateFileChecksum(t *testing.T) {
	t.Run("calculates correct checksum", func(t *testing.T) {
		// Create a temporary file with known content
		content := "test content for checksum"
		tmpFile := createTempFile(t, content)
		defer os.Remove(tmpFile)

		checksum, err := calculateFileChecksum(tmpFile)
		require.NoError(t, err)

		// Verify it's a valid SHA256 hex string (64 chars)
		assert.Len(t, checksum, 64)

		// Verify the checksum is correct
		expectedHash := sha256.Sum256([]byte(content))
		expectedChecksum := hex.EncodeToString(expectedHash[:])
		assert.Equal(t, expectedChecksum, checksum)
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		checksum, err := calculateFileChecksum("/nonexistent/path/file.csv")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrFileNotFound)
		assert.Empty(t, checksum)
	})

	t.Run("same content produces same checksum", func(t *testing.T) {
		content := "identical content"
		file1 := createTempFile(t, content)
		defer os.Remove(file1)

		file2 := createTempFile(t, content)
		defer os.Remove(file2)

		checksum1, err := calculateFileChecksum(file1)
		require.NoError(t, err)

		checksum2, err := calculateFileChecksum(file2)
		require.NoError(t, err)

		assert.Equal(t, checksum1, checksum2)
	})

	t.Run("different content produces different checksum", func(t *testing.T) {
		file1 := createTempFile(t, "content one")
		defer os.Remove(file1)

		file2 := createTempFile(t, "content two")
		defer os.Remove(file2)

		checksum1, err := calculateFileChecksum(file1)
		require.NoError(t, err)

		checksum2, err := calculateFileChecksum(file2)
		require.NoError(t, err)

		assert.NotEqual(t, checksum1, checksum2)
	})

	t.Run("empty file has valid checksum", func(t *testing.T) {
		file := createTempFile(t, "")
		defer os.Remove(file)

		checksum, err := calculateFileChecksum(file)
		require.NoError(t, err)
		assert.Len(t, checksum, 64)

		// SHA256 of empty string
		expectedHash := sha256.Sum256([]byte(""))
		expectedChecksum := hex.EncodeToString(expectedHash[:])
		assert.Equal(t, expectedChecksum, checksum)
	})
}

func TestEncodeDecodeRollbackSQL(t *testing.T) {
	t.Run("encodes nil to nil", func(t *testing.T) {
		result := encodeRollbackSQL(nil)
		assert.Nil(t, result)
	})

	t.Run("encodes empty slice to nil", func(t *testing.T) {
		result := encodeRollbackSQL([]string{})
		assert.Nil(t, result)
	})

	t.Run("encodes single statement", func(t *testing.T) {
		result := encodeRollbackSQL([]string{"DELETE FROM t1 WHERE id = 1"})
		require.NotNil(t, result)
		assert.Equal(t, "DELETE FROM t1 WHERE id = 1", *result)
	})

	t.Run("encodes multiple statements", func(t *testing.T) {
		statements := []string{
			"DELETE FROM t1 WHERE id = 1",
			"DELETE FROM t2 WHERE id = 2",
			"DELETE FROM t3 WHERE id = 3",
		}
		result := encodeRollbackSQL(statements)
		require.NotNil(t, result)
		assert.Contains(t, *result, "DELETE FROM t1 WHERE id = 1")
		assert.Contains(t, *result, "DELETE FROM t2 WHERE id = 2")
		assert.Contains(t, *result, "DELETE FROM t3 WHERE id = 3")
	})

	t.Run("decodes nil to nil", func(t *testing.T) {
		result := decodeRollbackSQL(nil)
		assert.Nil(t, result)
	})

	t.Run("decodes empty string to nil", func(t *testing.T) {
		empty := ""
		result := decodeRollbackSQL(&empty)
		assert.Nil(t, result)
	})

	t.Run("roundtrip single statement", func(t *testing.T) {
		original := []string{"DELETE FROM t1 WHERE id = 1"}
		encoded := encodeRollbackSQL(original)
		decoded := decodeRollbackSQL(encoded)
		assert.Equal(t, original, decoded)
	})

	t.Run("roundtrip multiple statements", func(t *testing.T) {
		original := []string{
			"DELETE FROM t1 WHERE id = 1",
			"DELETE FROM t2 WHERE id = 2",
			"DELETE FROM t3 WHERE id = 3",
		}
		encoded := encodeRollbackSQL(original)
		decoded := decodeRollbackSQL(encoded)
		assert.Equal(t, original, decoded)
	})
}

func TestNullableInt(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected *int
	}{
		{
			name:     "zero returns nil",
			input:    0,
			expected: nil,
		},
		{
			name:  "positive returns pointer",
			input: 42,
		},
		{
			name:  "negative returns pointer",
			input: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nullableInt(tt.input)
			if tt.expected == nil && tt.input == 0 {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.input, *result)
			}
		})
	}
}

func TestCheckpoint_FullWorkflow(t *testing.T) {
	// Simulates a complete import workflow
	cp := &Checkpoint{
		ManifestID: uuid.New(),
		TenantID:   "test_tenant",
		SourceFile: "/path/to/import.csv",
		Status:     StatusRunning,
	}

	// Set total after parsing file
	cp.SetTotalRows(1000)
	assert.Equal(t, 1000, cp.TotalRows)
	assert.Equal(t, 0.0, cp.Progress())

	// Process batches
	cp.IncrementSuccess(100)
	cp.AddRollbackStatement("DELETE FROM positions WHERE batch = 1")
	assert.Equal(t, 10.0, cp.Progress())

	cp.IncrementSuccess(90)
	cp.IncrementFailure(10) // Some failures
	cp.AddRollbackStatement("DELETE FROM positions WHERE batch = 2")
	assert.Equal(t, 20.0, cp.Progress())

	// Continue processing
	for i := 0; i < 8; i++ {
		cp.IncrementSuccess(100)
	}

	assert.Equal(t, 1000, cp.ProcessedRows)
	assert.Equal(t, 990, cp.SuccessCount)
	assert.Equal(t, 10, cp.FailureCount)
	assert.Equal(t, 100.0, cp.Progress())
	assert.Len(t, cp.RollbackSQL, 2)
}

func TestCheckpoint_InitialState(t *testing.T) {
	cp := &Checkpoint{}

	assert.Equal(t, uuid.Nil, cp.ManifestID)
	assert.Empty(t, cp.TenantID)
	assert.Empty(t, cp.SourceFile)
	assert.Empty(t, cp.FileChecksum)
	assert.Equal(t, 0, cp.TotalRows)
	assert.Equal(t, 0, cp.ProcessedRows)
	assert.Equal(t, 0, cp.SuccessCount)
	assert.Equal(t, 0, cp.FailureCount)
	assert.Equal(t, Status(""), cp.Status)
	assert.Nil(t, cp.RollbackSQL)
	assert.Empty(t, cp.ErrorMessage)
	assert.True(t, cp.CreatedAt.IsZero())
	assert.True(t, cp.UpdatedAt.IsZero())
}

func TestCheckpoint_RollbackSQLTypes(t *testing.T) {
	cp := &Checkpoint{}

	// Test various SQL patterns that might be used
	statements := []string{
		"DELETE FROM positions WHERE id = 'abc-123'",
		"DELETE FROM positions WHERE id IN ('a', 'b', 'c')",
		"DELETE FROM positions WHERE batch_id = '550e8400-e29b-41d4-a716-446655440000'",
		"DELETE FROM measurements WHERE position_id IN (SELECT id FROM positions WHERE import_manifest_id = '123')",
	}

	for _, sql := range statements {
		cp.AddRollbackStatement(sql)
	}

	assert.Len(t, cp.RollbackSQL, 4)

	// Verify roundtrip encoding
	encoded := encodeRollbackSQL(cp.RollbackSQL)
	decoded := decodeRollbackSQL(encoded)
	assert.Equal(t, statements, decoded)
}

func TestCalculateFileChecksum_LargeFile(t *testing.T) {
	// Create a larger file to ensure streaming works correctly
	// Using 1MB of data
	size := 1024 * 1024
	content := strings.Repeat("x", size)

	tmpFile := createTempFile(t, content)
	defer os.Remove(tmpFile)

	start := time.Now()
	checksum, err := calculateFileChecksum(tmpFile)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Len(t, checksum, 64)

	// Should complete quickly (under 1 second for 1MB)
	assert.Less(t, elapsed, time.Second)
}

// Helper function to create temporary files for testing.
func createTempFile(t *testing.T, content string) string {
	t.Helper()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test_file.csv")

	err := os.WriteFile(tmpFile, []byte(content), 0o644)
	require.NoError(t, err)

	return tmpFile
}

func BenchmarkCalculateFileChecksum(b *testing.B) {
	// Create a 1MB file for benchmarking
	content := strings.Repeat("benchmark content ", 50000)
	tmpDir := b.TempDir()
	tmpFile := filepath.Join(tmpDir, "benchmark.csv")

	err := os.WriteFile(tmpFile, []byte(content), 0o644)
	require.NoError(b, err)

	b.ResetTimer()
	for b.Loop() {
		_, _ = calculateFileChecksum(tmpFile)
	}
}

func BenchmarkCheckpoint_IncrementSuccess(b *testing.B) {
	cp := &Checkpoint{}

	b.ResetTimer()
	for b.Loop() {
		cp.IncrementSuccess(1)
	}
}

func BenchmarkCheckpoint_Progress(b *testing.B) {
	cp := &Checkpoint{
		TotalRows:     1000000,
		ProcessedRows: 500000,
	}

	b.ResetTimer()
	for b.Loop() {
		_ = cp.Progress()
	}
}

func BenchmarkEncodeRollbackSQL(b *testing.B) {
	statements := []string{
		"DELETE FROM positions WHERE id = 'abc-123'",
		"DELETE FROM positions WHERE id = 'def-456'",
		"DELETE FROM positions WHERE id = 'ghi-789'",
	}

	b.ResetTimer()
	for b.Loop() {
		_ = encodeRollbackSQL(statements)
	}
}

func BenchmarkDecodeRollbackSQL(b *testing.B) {
	encoded := "DELETE FROM positions WHERE id = 'abc-123'\nDELETE FROM positions WHERE id = 'def-456'\nDELETE FROM positions WHERE id = 'ghi-789'"

	b.ResetTimer()
	for b.Loop() {
		_ = decodeRollbackSQL(&encoded)
	}
}
