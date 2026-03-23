package service

import (
	"errors"

	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
)

// Service-level sentinel errors for validation and state checks
var (
	ErrRepositoryNil                       = errors.New("repository cannot be nil")
	ErrPositionKeepingClientNil            = errors.New("position keeping client not available")
	ErrPositionKeepingServiceNameEmpty     = errors.New("position keeping service name cannot be empty")
	ErrFinancialAccountingServiceNameEmpty = errors.New("financial accounting service name cannot be empty")
	// ErrHealthCheckerRepositoryNil is returned when attempting to create a health checker with a nil repository
	ErrHealthCheckerRepositoryNil = errors.New("health checker requires non-nil repository")
)

// Orchestrator configuration errors for nil dependency validation.
// Re-exported from shared/pkg/clients for backward compatibility.
// These errors are returned by orchestrator constructors instead of panicking,
// allowing callers to handle initialization failures gracefully.
//
// When service startup fails due to these errors, the application will:
// 1. Exit with a non-zero status code
// 2. Log the specific error with context about which dependency is missing
// 3. Enter crash loop backoff in Kubernetes until the configuration is fixed
var (
	ErrOrchestratorLoggerNil             = sharedclients.ErrConfigLoggerNil
	ErrOrchestratorRepositoryNil         = sharedclients.ErrConfigRepositoryNil
	ErrOrchestratorPosKeepingClientNil   = sharedclients.ErrConfigPositionKeepingClientNil
	ErrOrchestratorFinAcctClientNil      = sharedclients.ErrConfigFinancialAccountingClientNil
	ErrOrchestratorSagaRunnerNil         = errors.New("saga runner cannot be nil")
	ErrOrchestratorDepositScriptEmpty    = errors.New("deposit script cannot be empty")
	ErrOrchestratorWithdrawalScriptEmpty = errors.New("withdrawal script cannot be empty")
	ErrSagaScriptLoadFailed              = errors.New("failed to get current file path for saga script loading")
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

// Note: ErrPartyNotFound and ErrPartyNotActive are defined in client_interfaces.go
