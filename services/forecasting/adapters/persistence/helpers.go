// Package persistence provides CockroachDB persistence implementations for the Forecasting service.
package persistence

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var errInvalidCursorFormat = errors.New("invalid cursor token format")

// Pagination defaults and limits.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// nullStringPtr converts a sql.NullString to a value for use in queries.
func nullStringPtr(ns sql.NullString) interface{} {
	if !ns.Valid {
		return nil
	}
	return ns.String
}

// formatCursorToken creates a cursor token from a timestamp and UUID.
func formatCursorToken(t time.Time, id uuid.UUID) string {
	raw := fmt.Sprintf("%d|%s", t.UnixMicro(), id.String())
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// parseCursorToken parses a cursor token into a timestamp and UUID.
// Returns zero values if the token is empty (first page).
func parseCursorToken(token string) (time.Time, uuid.UUID, error) {
	if token == "" {
		return time.Time{}, uuid.Nil, nil
	}

	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor token encoding: %w", err)
	}

	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, errInvalidCursorFormat
	}

	var micros int64
	if _, err := fmt.Sscanf(parts[0], "%d", &micros); err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor timestamp: %w", err)
	}

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor UUID: %w", err)
	}

	return time.UnixMicro(micros), id, nil
}
