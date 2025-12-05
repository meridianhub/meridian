// Package clients provides gRPC client wrappers with resilience patterns.
//
// This package re-exports types from shared/pkg/clients for backward compatibility.
// New code should import directly from github.com/meridianhub/meridian/shared/pkg/clients.
package clients

import (
	"log/slog"

	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
)

// SagaStep is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type SagaStep = sharedclients.SagaStep

// SagaOrchestrator is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type SagaOrchestrator = sharedclients.SagaOrchestrator

// SagaResult is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type SagaResult = sharedclients.SagaResult

// NewSagaOrchestrator creates a new saga orchestrator.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func NewSagaOrchestrator(logger *slog.Logger) *SagaOrchestrator {
	return sharedclients.NewSagaOrchestrator(logger)
}
