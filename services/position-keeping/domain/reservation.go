package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ReservationStatus represents the lifecycle state of a reservation.
type ReservationStatus string

// Reservation lifecycle states.
const (
	ReservationStatusActive     ReservationStatus = "ACTIVE"
	ReservationStatusExecuted   ReservationStatus = "EXECUTED"
	ReservationStatusTerminated ReservationStatus = "TERMINATED"
)

// IsValid checks if the reservation status is a recognized value.
func (s ReservationStatus) IsValid() bool {
	switch s {
	case ReservationStatusActive, ReservationStatusExecuted, ReservationStatusTerminated:
		return true
	}
	return false
}

// IsTerminal returns true if this status represents a final state.
func (s ReservationStatus) IsTerminal() bool {
	return s == ReservationStatusExecuted || s == ReservationStatusTerminated
}

// String returns the string representation.
func (s ReservationStatus) String() string {
	return string(s)
}

// Reservation domain errors
var (
	ErrReservationNotFound     = errors.New("reservation not found")
	ErrReservationAlreadyFinal = errors.New("reservation is already in a terminal state")
	ErrInvalidReservationState = errors.New("invalid reservation status transition")
	ErrEmptyLienID             = errors.New("lien_id cannot be empty")
	ErrZeroReservedAmount      = errors.New("reserved_amount must be non-zero")
)

// Reservation represents a lien-based reservation against an account's position.
// The lien_id serves as the primary key and natural idempotency key.
type Reservation struct {
	LienID         uuid.UUID
	AccountID      string
	InstrumentCode string
	BucketID       string
	ReservedAmount decimal.Decimal
	Status         ReservationStatus
	CreatedAt      time.Time
	ExecutedAt     *time.Time
	TerminatedAt   *time.Time
}

// NewReservation creates a new active reservation with validation.
func NewReservation(
	lienID uuid.UUID,
	accountID string,
	instrumentCode string,
	bucketID string,
	reservedAmount decimal.Decimal,
) (*Reservation, error) {
	if lienID == uuid.Nil {
		return nil, ErrEmptyLienID
	}
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}
	if instrumentCode == "" {
		return nil, ErrEmptyInstrumentCode
	}
	if reservedAmount.IsZero() {
		return nil, ErrZeroReservedAmount
	}

	return &Reservation{
		LienID:         lienID,
		AccountID:      accountID,
		InstrumentCode: instrumentCode,
		BucketID:       bucketID,
		ReservedAmount: reservedAmount,
		Status:         ReservationStatusActive,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

// Release transitions the reservation to a terminal state.
func (r *Reservation) Release(reason ReservationStatus) error {
	if r.Status.IsTerminal() {
		return ErrReservationAlreadyFinal
	}
	if !reason.IsTerminal() {
		return ErrInvalidReservationState
	}

	now := time.Now().UTC()
	r.Status = reason
	switch reason {
	case ReservationStatusActive:
		return ErrInvalidReservationState
	case ReservationStatusExecuted:
		r.ExecutedAt = &now
	case ReservationStatusTerminated:
		r.TerminatedAt = &now
	}
	return nil
}

// ProjectedBalance represents a calculated balance with reservation adjustments.
type ProjectedBalance struct {
	CurrentBalance          decimal.Decimal
	ActiveReservationsTotal decimal.Decimal
	ProjectedBalance        decimal.Decimal
	BucketID                string
	InstrumentCode          string
	AsOf                    time.Time
}
