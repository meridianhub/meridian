package exporter

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCSVWriter(t *testing.T) {
	t.Run("creates writer for valid path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.csv")

		w, err := NewCSVWriter(path, nil)
		require.NoError(t, err)
		require.NotNil(t, w)
		defer w.Close()
	})

	t.Run("returns error for invalid path", func(t *testing.T) {
		_, err := NewCSVWriter("/nonexistent-dir/out.csv", nil)
		assert.Error(t, err)
	})

	t.Run("sorts attribute keys alphabetically", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.csv")
		w, err := NewCSVWriter(path, []string{"zzz", "aaa", "mmm"})
		require.NoError(t, err)
		defer w.Close()

		keys := w.AttributeKeys()
		assert.Equal(t, []string{"aaa", "mmm", "zzz"}, keys)
	})
}

func TestCSVWriter_HeaderCount(t *testing.T) {
	t.Run("six fixed columns with no attributes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.csv")
		w, err := NewCSVWriter(path, nil)
		require.NoError(t, err)
		defer w.Close()

		assert.Equal(t, 6, w.HeaderCount())
	})

	t.Run("six fixed plus attribute columns", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.csv")
		w, err := NewCSVWriter(path, []string{"tenor", "currency"})
		require.NoError(t, err)
		defer w.Close()

		assert.Equal(t, 8, w.HeaderCount())
	})
}

func TestCSVWriter_WriteRow(t *testing.T) {
	t.Run("writes header then data row", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.csv")
		w, err := NewCSVWriter(path, []string{"tenor"})
		require.NoError(t, err)

		refID := uuid.New()
		now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

		pos := PositionRow{
			AccountID:      "ACC001",
			InstrumentCode: "GBPUSD",
			Amount:         decimal.NewFromFloat(1000.50),
			Dimension:      "NOTIONAL",
			CreatedAt:      now,
			ReferenceID:    refID,
			Attributes:     map[string]string{"tenor": "3M"},
		}

		require.NoError(t, w.WriteRow(pos))
		require.NoError(t, w.Close())

		// Read back and verify
		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()

		records, err := csv.NewReader(f).ReadAll()
		require.NoError(t, err)
		require.Len(t, records, 2)

		header := records[0]
		assert.Equal(t, "account_id", header[0])
		assert.Equal(t, "instrument_code", header[1])
		assert.Equal(t, "amount", header[2])
		assert.Equal(t, "dimension", header[3])
		assert.Equal(t, "created_at", header[4])
		assert.Equal(t, "reference_id", header[5])
		assert.Equal(t, "attr_tenor", header[6])

		row := records[1]
		assert.Equal(t, "ACC001", row[0])
		assert.Equal(t, "GBPUSD", row[1])
		assert.Equal(t, "1000.5", row[2])
		assert.Equal(t, "NOTIONAL", row[3])
		assert.Equal(t, now.Format(time.RFC3339), row[4])
		assert.Equal(t, refID.String(), row[5])
		assert.Equal(t, "3M", row[6])
	})

	t.Run("nil reference ID writes empty string", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.csv")
		w, err := NewCSVWriter(path, nil)
		require.NoError(t, err)

		pos := PositionRow{
			AccountID:      "ACC001",
			InstrumentCode: "GBPUSD",
			Amount:         decimal.NewFromFloat(100),
			Dimension:      "NOTIONAL",
			CreatedAt:      time.Now(),
			ReferenceID:    uuid.Nil,
		}

		require.NoError(t, w.WriteRow(pos))
		require.NoError(t, w.Close())

		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()

		records, err := csv.NewReader(f).ReadAll()
		require.NoError(t, err)
		require.Len(t, records, 2)
		assert.Equal(t, "", records[1][5], "nil UUID should be empty string")
	})

	t.Run("missing attribute value writes empty string", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.csv")
		w, err := NewCSVWriter(path, []string{"tenor", "currency"})
		require.NoError(t, err)

		pos := PositionRow{
			AccountID:      "ACC001",
			InstrumentCode: "GBPUSD",
			Amount:         decimal.NewFromFloat(100),
			Dimension:      "NOTIONAL",
			CreatedAt:      time.Now(),
			Attributes:     map[string]string{"tenor": "1M"}, // no "currency"
		}

		require.NoError(t, w.WriteRow(pos))
		require.NoError(t, w.Close())

		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()

		records, err := csv.NewReader(f).ReadAll()
		require.NoError(t, err)
		require.Len(t, records, 2)
		// Sorted attributes: currency (index 6), tenor (index 7)
		// attr_currency should be empty (not in attributes map)
		assert.Equal(t, "attr_currency", records[0][6])
		assert.Equal(t, "", records[1][6])
		// attr_tenor should have "1M"
		assert.Equal(t, "attr_tenor", records[0][7])
		assert.Equal(t, "1M", records[1][7])
	})
}

func TestCSVWriter_WriteAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewCSVWriter(path, nil)
	require.NoError(t, err)

	require.NoError(t, w.Close())

	err = w.WriteRow(PositionRow{
		InstrumentCode: "GBPUSD",
		Amount:         decimal.NewFromFloat(1),
		CreatedAt:      time.Now(),
	})
	assert.True(t, errors.Is(err, ErrWriterClosed))
}

func TestCSVWriter_CloseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewCSVWriter(path, nil)
	require.NoError(t, err)

	assert.NoError(t, w.Close())
	assert.NoError(t, w.Close()) // second close should not error
}

func TestCSVWriter_BytesWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewCSVWriter(path, nil)
	require.NoError(t, err)

	pos := PositionRow{
		AccountID:      "ACC001",
		InstrumentCode: "GBPUSD",
		Amount:         decimal.NewFromFloat(100),
		Dimension:      "NOTIONAL",
		CreatedAt:      time.Now(),
	}

	require.NoError(t, w.WriteRow(pos))
	require.NoError(t, w.Flush())

	assert.Greater(t, w.BytesWritten(), int64(0))
	require.NoError(t, w.Close())
}

func TestCSVWriter_Flush_AfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewCSVWriter(path, nil)
	require.NoError(t, err)

	require.NoError(t, w.Close())
	// Flush after close is a no-op
	assert.NoError(t, w.Flush())
}

func TestCSVWriter_MultipleRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewCSVWriter(path, nil)
	require.NoError(t, err)

	now := time.Now()
	for i := range 5 {
		pos := PositionRow{
			AccountID:      "ACC001",
			InstrumentCode: "GBPUSD",
			Amount:         decimal.NewFromInt(int64(i + 1)),
			Dimension:      "NOTIONAL",
			CreatedAt:      now,
		}
		require.NoError(t, w.WriteRow(pos))
	}
	require.NoError(t, w.Close())

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 6) // 1 header + 5 data rows
}
