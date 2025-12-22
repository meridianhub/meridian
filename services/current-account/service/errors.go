package service

import (
	"errors"

	"github.com/meridianhub/meridian/services/current-account/clients"
)

// Service-level sentinel errors for validation and state checks
var (
	ErrRepositoryNil                       = errors.New("repository cannot be nil")
	ErrPositionKeepingServiceNameEmpty     = errors.New("position keeping service name cannot be empty")
	ErrFinancialAccountingServiceNameEmpty = errors.New("financial accounting service name cannot be empty")
)

// Saga orchestration errors for compensation and state tracking
var (
	ErrOriginalAccountStateNotFound = errors.New("original account state not available for compensation")
	ErrPositionLogIDNotFound        = errors.New("position log ID not available for compensation")
	ErrLedgerPostingIDNotFound      = errors.New("ledger posting ID not available for compensation")
	ErrCompensationFailed           = errors.New("saga compensation failed")
)

// Nil response errors for defensive checks on gRPC responses.
// These are used by both grpc_service.go and deposit_orchestrator.go.
var (
	ErrNilPositionLog   = errors.New("position keeping returned nil log")
	ErrNilBookingLog    = errors.New("financial accounting returned nil booking log")
	ErrNilDebitPosting  = errors.New("financial accounting returned nil debit posting")
	ErrNilCreditPosting = errors.New("financial accounting returned nil credit posting")
)

// Party validation errors (re-exported from clients package for convenience)
var (
	ErrPartyNotFound  = clients.ErrPartyNotFound
	ErrPartyNotActive = clients.ErrPartyNotActive
)
