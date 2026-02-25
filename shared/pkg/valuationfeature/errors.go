// Package valuationfeature provides shared domain, persistence, and CRUD operations
// for valuation features across account services (Current Account, Internal Account).
package valuationfeature

import "errors"

// Domain errors
var (
	ErrInvalidLifecycleTransition = errors.New("invalid valuation feature lifecycle transition")
	ErrNotActive                  = errors.New("valuation feature is not in active status")
	ErrInvalidParameters          = errors.New("invalid valuation feature parameters")
	ErrInvalidTemporalRange       = errors.New("valid_from must be before valid_to")
	ErrInstrumentCodeEmpty        = errors.New("instrument_code cannot be empty")
)

// Repository errors
var (
	ErrNotFound        = errors.New("valuation feature not found")
	ErrVersionConflict = errors.New("version conflict: valuation feature was modified by another transaction")
	ErrAlreadyExists   = errors.New("valuation feature already exists for this account and instrument")
)

// Service errors
var (
	ErrMethodOutputMismatch  = errors.New("valuation method output_instrument does not match account native instrument")
	ErrInvalidAction         = errors.New("invalid valuation feature action")
	ErrNoConversionAvailable = errors.New("no conversion method available for the given instrument pair")
)
