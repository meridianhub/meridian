// Package service implements gRPC services for the current account domain
package service

import (
	"errors"
	"time"
)

// Lien-specific errors
var (
	ErrLienRepositoryNil      = errors.New("lien repository cannot be nil")
	ErrInsufficientFunds      = errors.New("insufficient available balance for lien")
	ErrLienCurrencyMismatch   = errors.New("lien currency must match account currency")
	ErrLienAmountNotPositive  = errors.New("lien amount must be positive")
	ErrAmountRequired         = errors.New("amount is required")
	ErrAmountOverflow         = errors.New("amount too large: would overflow")
	ErrInvalidPrecision       = errors.New("invalid instrument precision: must be between 0 and 9")
	ErrInstrumentCodeMismatch = errors.New("instrument code mismatch in balance response")
	// Transaction operation errors for error detection
	errTxSaveAccount       = errors.New("save_account")
	errTxUpdateLien        = errors.New("update_lien")
	errTxSaveLien          = errors.New("save_lien")
	errTxAccountNotActive  = errors.New("account_not_active")
	errTxCurrencyMismatch  = errors.New("currency_mismatch")
	errTxSumLiensFailed    = errors.New("sum_liens_failed")
	errTxInsufficientFunds = errors.New("insufficient_funds")
	errTxDomainError       = errors.New("domain_error")
	errTxExecuteFailed     = errors.New("execute_failed")
	errTxWithdrawFailed    = errors.New("withdraw_failed")
	errTxTerminateFailed   = errors.New("terminate_failed")
	errTxInvalidLienStatus = errors.New("invalid_lien_status")
)

// Default termination reason when none provided
const defaultTerminationReason = "Terminated via API"

// basisDriftThreshold is the age beyond which a lien's valuation basis is considered stale.
// If the basis knowledgeAt is older than this, a VALUATION_STALE warning is logged.
const basisDriftThreshold = 30 * 24 * time.Hour // 30 days

// Lien operation status constants for metrics
const (
	opStatusLienRepoNil           = "lien_repo_nil"
	opStatusInvalidLienID         = "invalid_lien_id"
	opStatusLienNotFound          = "lien_not_found"
	opStatusRetrieveAccountFailed = "retrieve_account_failed"
	opStatusAccountNotFound       = "account_not_found"
	opStatusRetrieveFailed        = "retrieve_failed"
	opStatusAccountNotActive      = "account_not_active"
	opStatusInvalidAmount         = "invalid_amount"
	opStatusCurrencyMismatch      = "currency_mismatch"
	opStatusSumLiensFailed        = "sum_liens_failed"
	opStatusInsufficientFunds     = "insufficient_funds"
	opStatusDomainError           = "domain_error"
	opStatusSaveFailed            = "save_failed"
	opStatusInvalidLienStatus     = "invalid_lien_status"
	opStatusExecuteFailed         = "execute_failed"
	opStatusWithdrawFailed        = "withdraw_failed"
	opStatusVersionConflict       = "version_conflict"
	opStatusUpdateLienFailed      = "update_lien_failed"
	opStatusSaveAccountFailed     = "save_account_failed"
	opStatusTerminateFailed       = "terminate_failed"
	opStatusUpdateFailed          = "update_failed"
)
