// Package service implements gRPC services for the internal account domain.
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

// Health checker errors.
var (
	// ErrHealthCheckerRepositoryNil is returned when attempting to create a health checker with a nil repository.
	ErrHealthCheckerRepositoryNil = errors.New("health checker repository cannot be nil")
)
