// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
)

const defaultUser = "system"

// getUserFromContext extracts the user identifier from the request context.
// Returns the user ID if available, otherwise returns "system".
func getUserFromContext(ctx context.Context) string {
	if ctx == nil {
		return defaultUser
	}
	userID, ok := auth.GetUserIDFromContext(ctx)
	if !ok || userID == "" {
		return defaultUser
	}
	return userID
}

// nullStringPtr converts a sql.NullString to a *string for use in queries.
// Returns nil if not valid.
func nullStringPtr(ns sql.NullString) interface{} {
	if !ns.Valid {
		return nil
	}
	return ns.String
}

// nullTimePtr converts a sql.NullTime to a *time.Time for use in queries.
// Returns nil if not valid.
func nullTimePtr(nt sql.NullTime) interface{} {
	if !nt.Valid {
		return nil
	}
	return nt.Time
}

// nullTimeValue converts a time.Time to a sql.NullTime.
// Returns a non-valid NullTime if the time is zero.
func nullTimeValue(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{Valid: false}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// nullUUIDValue converts a *uuid.UUID to a uuid.NullUUID.
// Returns a non-valid NullUUID if the pointer is nil.
func nullUUIDValue(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{Valid: false}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// nullUUIDNonNil converts a uuid.UUID to a uuid.NullUUID.
// Returns a non-valid NullUUID if the UUID is nil (zero value).
func nullUUIDNonNil(id uuid.UUID) uuid.NullUUID {
	if id == uuid.Nil {
		return uuid.NullUUID{Valid: false}
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}
