// Package service implements gRPC services for the internal bank account domain.
package service

import "errors"

// Service-level sentinel errors for validation and state checks.
var (
	// ErrRepositoryNil is returned when attempting to create a service with a nil repository.
	ErrRepositoryNil = errors.New("repository cannot be nil")
)

// Client interface errors.
var (
	// ErrPositionKeepingClientNil is returned when Position Keeping client is required but not configured.
	ErrPositionKeepingClientNil = errors.New("position keeping client is required for balance queries")
)
