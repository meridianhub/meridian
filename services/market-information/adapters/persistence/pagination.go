// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Constants for pagination.
const (
	// DefaultPageSize is the default number of results returned per page.
	DefaultPageSize = 50
	// MaxPageSize is the maximum number of results allowed per page.
	MaxPageSize = 100
)

// ErrInvalidPageToken is returned when the pagination token has an invalid format.
var ErrInvalidPageToken = errors.New("invalid page token format")

// Timestamp bounds for security validation.
// Records before Unix epoch (1970) or far in the future are unexpected
// and could indicate token manipulation.
var (
	minValidTimestamp = int64(0)                                           // Unix epoch (1970-01-01)
	maxValidTimestamp = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix() // Year 2100
)

// parseCursorToken parses a pagination token in format "timestamp_uuid".
// Returns the timestamp and UUID, or an error if the format is invalid.
// An empty token returns zero values with no error (indicating first page).
func parseCursorToken(token string) (time.Time, uuid.UUID, error) {
	if token == "" {
		return time.Time{}, uuid.Nil, nil
	}

	// Use SplitN to handle edge cases where UUID might theoretically contain underscore
	// (though standard UUIDs use hyphens). This ensures we only split on the first underscore.
	parts := strings.SplitN(token, "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return time.Time{}, uuid.Nil, ErrInvalidPageToken
	}

	// Parse timestamp
	timestampUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: invalid timestamp", ErrInvalidPageToken)
	}

	// Validate timestamp bounds for security - records should be within reasonable range
	if timestampUnix < minValidTimestamp || timestampUnix > maxValidTimestamp {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: timestamp out of valid range", ErrInvalidPageToken)
	}

	timestamp := time.Unix(timestampUnix, 0).UTC()

	// Parse UUID
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: invalid uuid", ErrInvalidPageToken)
	}

	return timestamp, id, nil
}

// formatCursorToken creates a "timestamp_uuid" format pagination token.
func formatCursorToken(timestamp time.Time, id uuid.UUID) string {
	return fmt.Sprintf("%d_%s", timestamp.Unix(), id.String())
}
