// Package validator provides validation services for rebucketing operations,
// including settlement lock checking, instrument deprecation, and prerequisites validation.
package validator

import (
	"errors"
	"fmt"
)

// Sentinel errors for validation operations.
var (
	// ErrMissingTenantContext indicates that the tenant context is missing from the request.
	ErrMissingTenantContext = errors.New("tenant context missing")

	// ErrTargetOrServiceRequired indicates that neither Target nor ServiceName was provided.
	ErrTargetOrServiceRequired = errors.New("either target or serviceName must be provided")

	// ErrClosingConnections indicates that errors occurred while closing connections.
	ErrClosingConnections = errors.New("errors closing connections")

	// errServiceUnavailable is used for testing service unavailability scenarios.
	errServiceUnavailable = errors.New("service unavailable")

	// errCloseFailed is used for testing close failure scenarios.
	errCloseFailed = errors.New("close failed")
)

// SettlementLockError indicates that positions exist in finalized settlements
// and cannot be rebucketed without breaking settlement integrity.
type SettlementLockError struct {
	InstrumentCode    string
	InstrumentVersion int
	PositionCount     int
	SettlementIDs     []string
}

// Error implements the error interface.
func (e *SettlementLockError) Error() string {
	return fmt.Sprintf(
		"settlement lock: instrument %s v%d has %d positions in finalized settlements %v",
		e.InstrumentCode, e.InstrumentVersion, e.PositionCount, e.SettlementIDs,
	)
}

// Is allows errors.Is to match SettlementLockError instances.
func (e *SettlementLockError) Is(target error) bool {
	_, ok := target.(*SettlementLockError)
	return ok
}

// InstrumentNotFoundError indicates that the specified instrument version does not exist.
type InstrumentNotFoundError struct {
	InstrumentCode    string
	InstrumentVersion int
}

// Error implements the error interface.
func (e *InstrumentNotFoundError) Error() string {
	return fmt.Sprintf(
		"instrument not found: %s v%d does not exist",
		e.InstrumentCode, e.InstrumentVersion,
	)
}

// Is allows errors.Is to match InstrumentNotFoundError instances.
func (e *InstrumentNotFoundError) Is(target error) bool {
	_, ok := target.(*InstrumentNotFoundError)
	return ok
}

// InstrumentAlreadyDeprecatedError indicates the instrument is already deprecated.
type InstrumentAlreadyDeprecatedError struct {
	InstrumentCode    string
	InstrumentVersion int
}

// Error implements the error interface.
func (e *InstrumentAlreadyDeprecatedError) Error() string {
	return fmt.Sprintf(
		"instrument already deprecated: %s v%d is already in DEPRECATED status",
		e.InstrumentCode, e.InstrumentVersion,
	)
}

// Is allows errors.Is to match InstrumentAlreadyDeprecatedError instances.
func (e *InstrumentAlreadyDeprecatedError) Is(target error) bool {
	_, ok := target.(*InstrumentAlreadyDeprecatedError)
	return ok
}

// InstrumentNotActiveError indicates the instrument is not in ACTIVE status
// and therefore cannot be deprecated.
type InstrumentNotActiveError struct {
	InstrumentCode    string
	InstrumentVersion int
	CurrentStatus     string
}

// Error implements the error interface.
func (e *InstrumentNotActiveError) Error() string {
	return fmt.Sprintf(
		"instrument not active: %s v%d is in %s status, only ACTIVE instruments can be deprecated",
		e.InstrumentCode, e.InstrumentVersion, e.CurrentStatus,
	)
}

// Is allows errors.Is to match InstrumentNotActiveError instances.
func (e *InstrumentNotActiveError) Is(target error) bool {
	_, ok := target.(*InstrumentNotActiveError)
	return ok
}

// RawMeasurementsUnavailableError indicates that raw measurements
// required for rebucketing are not available.
type RawMeasurementsUnavailableError struct {
	InstrumentCode    string
	InstrumentVersion int
	Reason            string
}

// Error implements the error interface.
func (e *RawMeasurementsUnavailableError) Error() string {
	return fmt.Sprintf(
		"raw measurements unavailable: instrument %s v%d - %s",
		e.InstrumentCode, e.InstrumentVersion, e.Reason,
	)
}

// Is allows errors.Is to match RawMeasurementsUnavailableError instances.
func (e *RawMeasurementsUnavailableError) Is(target error) bool {
	_, ok := target.(*RawMeasurementsUnavailableError)
	return ok
}

// InstrumentInUseError indicates the instrument is currently being used
// for new transactions and cannot be deprecated.
type InstrumentInUseError struct {
	InstrumentCode    string
	InstrumentVersion int
	ActiveTradeCount  int
}

// Error implements the error interface.
func (e *InstrumentInUseError) Error() string {
	return fmt.Sprintf(
		"instrument in use: %s v%d has %d active trades/transactions",
		e.InstrumentCode, e.InstrumentVersion, e.ActiveTradeCount,
	)
}

// Is allows errors.Is to match InstrumentInUseError instances.
func (e *InstrumentInUseError) Is(target error) bool {
	_, ok := target.(*InstrumentInUseError)
	return ok
}

// ValidationError is a generic validation error with a wrapped cause.
type ValidationError struct {
	Operation string
	Message   string
	Cause     error
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("validation failed during %s: %s: %v", e.Operation, e.Message, e.Cause)
	}
	return fmt.Sprintf("validation failed during %s: %s", e.Operation, e.Message)
}

// Unwrap returns the underlying cause for errors.Is/As support.
func (e *ValidationError) Unwrap() error {
	return e.Cause
}

// Is allows errors.Is to match ValidationError instances.
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)
	return ok
}
