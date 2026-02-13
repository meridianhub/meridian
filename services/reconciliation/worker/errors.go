// Package worker provides background workers for the reconciliation service.
package worker

import "errors"

var (
	// ErrRunAlreadyExists is returned when a reconciliation run already exists for the period.
	ErrRunAlreadyExists = errors.New("reconciliation run already exists for this period")
	// ErrNilRunResponse is returned when the gRPC response contains a nil run.
	ErrNilRunResponse = errors.New("initiate reconciliation returned nil run")
	// ErrUnknownScope is returned when the reconciliation scope string is not recognized.
	ErrUnknownScope = errors.New("unknown reconciliation scope")
	// ErrUnknownSettlementType is returned when the settlement type string is not recognized.
	ErrUnknownSettlementType = errors.New("unknown settlement type")
	// ErrUnexpectedMetadata is returned when schedule metadata is not the expected type.
	ErrUnexpectedMetadata = errors.New("unexpected schedule metadata type")
)
