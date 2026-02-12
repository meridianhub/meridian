package exporter

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// CSVWriter provides efficient streaming CSV output for position exports.
// It uses buffered I/O to minimize system calls and supports tracking of bytes written.
type CSVWriter struct {
	file           *os.File
	bufferedWriter *bufio.Writer
	csvWriter      *csv.Writer
	counter        *writeCounter
	attributeKeys  []string
	headerWritten  bool
	closed         bool
}

// writeCounter wraps an io.Writer to count bytes written.
type writeCounter struct {
	writer  io.Writer
	written int64
}

func (wc *writeCounter) Write(p []byte) (int, error) {
	n, err := wc.writer.Write(p)
	atomic.AddInt64(&wc.written, int64(n))
	return n, err
}

// NewCSVWriter creates a new CSV writer that outputs to the specified file path.
// The attributeKeys parameter specifies which attribute columns to include.
// Attribute columns are sorted alphabetically for consistent output.
func NewCSVWriter(outputPath string, attributeKeys []string) (*CSVWriter, error) {
	file, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("creating output file: %w", err)
	}

	// Sort attribute keys for consistent column ordering
	sortedKeys := make([]string, len(attributeKeys))
	copy(sortedKeys, attributeKeys)
	sort.Strings(sortedKeys)

	// Use buffered writer for efficiency (64KB buffer)
	counter := &writeCounter{writer: file}
	buffered := bufio.NewWriterSize(counter, 64*1024)

	return &CSVWriter{
		file:           file,
		bufferedWriter: buffered,
		csvWriter:      csv.NewWriter(buffered),
		counter:        counter,
		attributeKeys:  sortedKeys,
	}, nil
}

// ErrWriterClosed is returned when attempting to write to a closed writer.
var ErrWriterClosed = errors.New("writer is closed")

// WriteRow writes a single position row to the CSV file.
// The header is automatically written before the first row.
func (w *CSVWriter) WriteRow(pos PositionRow) error {
	if w.closed {
		return ErrWriterClosed
	}

	// Write header if not yet written
	if !w.headerWritten {
		if err := w.writeHeader(); err != nil {
			return fmt.Errorf("writing header: %w", err)
		}
		w.headerWritten = true
	}

	// Build row values
	record := w.buildRecord(pos)

	if err := w.csvWriter.Write(record); err != nil {
		return fmt.Errorf("writing record: %w", err)
	}

	return nil
}

// writeHeader writes the CSV header row.
func (w *CSVWriter) writeHeader() error {
	headers := w.buildHeaders()
	return w.csvWriter.Write(headers)
}

// buildHeaders returns the header row for the CSV.
func (w *CSVWriter) buildHeaders() []string {
	// Fixed columns
	// Note: bucket_key is NOT exported - it is computed from attributes using
	// the instrument's fungibility key expression (CEL) during import.
	headers := make([]string, 0, 6+len(w.attributeKeys))
	headers = append(headers,
		"account_id",
		"instrument_code",
		"amount",
		"dimension",
		"created_at",
		"reference_id",
	)

	// Dynamic attribute columns with "attr_" prefix
	for _, key := range w.attributeKeys {
		headers = append(headers, "attr_"+key)
	}

	return headers
}

// buildRecord converts a PositionRow to a CSV record.
func (w *CSVWriter) buildRecord(pos PositionRow) []string {
	// Fixed columns (bucket_key excluded - computed from attributes during import)
	record := make([]string, 0, 6+len(w.attributeKeys))
	record = append(record,
		pos.AccountID,
		pos.InstrumentCode,
		pos.Amount.String(),
		pos.Dimension,
		pos.CreatedAt.Format(time.RFC3339),
		formatUUID(pos.ReferenceID),
	)

	// Dynamic attribute columns (in the same order as headers)
	for _, key := range w.attributeKeys {
		value := ""
		if pos.Attributes != nil {
			value = pos.Attributes[key]
		}
		record = append(record, value)
	}

	return record
}

// formatUUID returns an empty string for nil UUIDs, otherwise the string representation.
func formatUUID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// Flush writes any buffered data to the underlying file.
func (w *CSVWriter) Flush() error {
	if w.closed {
		return nil
	}
	w.csvWriter.Flush()
	if err := w.csvWriter.Error(); err != nil {
		return fmt.Errorf("CSV flush error: %w", err)
	}
	return w.bufferedWriter.Flush()
}

// Close flushes all buffered data and closes the file.
// It is safe to call Close multiple times.
func (w *CSVWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	// Flush CSV writer
	w.csvWriter.Flush()
	csvErr := w.csvWriter.Error()

	// Flush buffered writer
	bufErr := w.bufferedWriter.Flush()

	// Sync file to disk
	syncErr := w.file.Sync()

	// Close file
	closeErr := w.file.Close()

	// Return first error encountered
	for _, err := range []error{csvErr, bufErr, syncErr, closeErr} {
		if err != nil {
			return err
		}
	}

	return nil
}

// BytesWritten returns the total number of bytes written to the file.
func (w *CSVWriter) BytesWritten() int64 {
	return atomic.LoadInt64(&w.counter.written)
}

// HeaderCount returns the number of columns in the CSV.
func (w *CSVWriter) HeaderCount() int {
	return 6 + len(w.attributeKeys) // 6 fixed columns + attribute columns
}

// AttributeKeys returns the sorted list of attribute keys used as columns.
func (w *CSVWriter) AttributeKeys() []string {
	return w.attributeKeys
}
