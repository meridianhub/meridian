// Package domain provides domain-level types for audit event processing and utilization metering.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Measurement errors
var (
	// ErrInvalidPeriod indicates the period end is before start.
	ErrInvalidPeriod = errors.New("invalid period: end before start")

	// ErrNonUTCTimestamp indicates a timestamp is not in UTC.
	ErrNonUTCTimestamp = errors.New("timestamp must be in UTC")
)

// Period represents a time range. For point-in-time events, Start equals End.
// All timestamps MUST be in UTC to ensure consistent comparisons and storage.
type Period struct {
	Start time.Time
	End   time.Time
}

// NewPeriod creates a validated Period. Returns error if timestamps are not UTC
// or if End is before Start.
func NewPeriod(start, end time.Time) (Period, error) {
	if start.Location() != time.UTC {
		return Period{}, ErrNonUTCTimestamp
	}
	if end.Location() != time.UTC {
		return Period{}, ErrNonUTCTimestamp
	}
	if end.Before(start) {
		return Period{}, ErrInvalidPeriod
	}
	return Period{Start: start, End: end}, nil
}

// MustPeriod creates a Period, panicking on validation failure.
// Use only in tests or initialization where failure is fatal.
func MustPeriod(start, end time.Time) Period {
	p, err := NewPeriod(start, end)
	if err != nil {
		panic(err)
	}
	return p
}

// Instant creates a Period representing a single point in time (Start=End=t).
func Instant(t time.Time) (Period, error) {
	if t.Location() != time.UTC {
		return Period{}, ErrNonUTCTimestamp
	}
	return Period{Start: t, End: t}, nil
}

// IsInstant returns true if this period represents a single point in time.
func (p Period) IsInstant() bool {
	return p.Start.Equal(p.End)
}

// Duration returns the time span of this period.
func (p Period) Duration() time.Duration {
	return p.End.Sub(p.Start)
}

// Validate checks period invariants. Prefer NewPeriod() for construction.
func (p Period) Validate() error {
	if p.Start.Location() != time.UTC {
		return ErrNonUTCTimestamp
	}
	if p.End.Location() != time.UTC {
		return ErrNonUTCTimestamp
	}
	if p.End.Before(p.Start) {
		return ErrInvalidPeriod
	}
	return nil
}

// Measurement represents a single utilization data point for position keeping.
// This follows ADR-0017 Temporal Quality Ladder pattern for metered asset tracking.
// Immutable after creation except for SupersededBy pointer.
type Measurement struct {
	// ID uniquely identifies this measurement
	ID uuid.UUID

	// AccountID references the position-keeping account (in tenant-zero)
	// This is mapped from the tenant_id in the audit event
	AccountID uuid.UUID

	// AssetCode identifies the metered asset (e.g., "MERIDIAN-CURRENT-ACCOUNT-OPS")
	// Derived from service name in audit events
	AssetCode string

	// Quantity represents the measured amount (e.g., 1 for per-event counting)
	Quantity decimal.Decimal

	// Period is the time window for this measurement.
	// For audit events, this is an instant (Start=End=timestamp)
	Period Period

	// Attributes provide fungibility dimensions (service, operation, table)
	// Used for position aggregation and reporting
	Attributes map[string]string

	// Source identifies where this measurement came from (e.g., "AUDIT_STREAM")
	// Lookup key into Source Authority Registry for quality scoring
	Source string

	// QualityScore is a denormalized snapshot of the source's quality ranking
	// Higher scores indicate more authoritative data (0-100 scale)
	// Note: May diverge from registry if rankings change post-ingestion
	QualityScore int

	// ReceivedAt tracks when this measurement was ingested
	ReceivedAt time.Time

	// SupersededBy points to a replacement measurement with higher quality
	// Nil if this is the current measurement for its position
	SupersededBy *uuid.UUID

	// SettlementRun identifies the settlement cycle (e.g., "D+1", "M+14", "FINAL")
	// Empty until processed by settlement engine
	SettlementRun string

	// LockedAt marks when this measurement was locked for final settlement
	// Nil if unlocked; non-nil prevents supersession
	LockedAt *time.Time
}

// IsCurrent returns true if this measurement has not been superseded.
func (m Measurement) IsCurrent() bool {
	return m.SupersededBy == nil
}

// IsLocked returns true if this measurement cannot be superseded.
func (m Measurement) IsLocked() bool {
	return m.LockedAt != nil
}
