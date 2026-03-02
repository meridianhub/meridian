// Package ports defines the interfaces (ports) for the operational-gateway service.
package ports

import (
	"context"
	"time"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
)

// DispatchResult captures the outcome of a single outbound HTTP dispatch attempt.
type DispatchResult struct {
	// StatusCode is the HTTP response status code from the provider.
	StatusCode int

	// ResponseBody is the raw response body received from the provider.
	ResponseBody []byte

	// Outcome is the extracted InstructionOutcome after applying the inbound transform.
	// May be nil if the dispatch did not reach the response parsing stage.
	Outcome *InstructionOutcome

	// Duration is the total time elapsed for the dispatch attempt, including
	// connect, send, and receive time.
	Duration time.Duration

	// Error is non-nil when the dispatch failed before a response could be parsed
	// (e.g., network error, auth failure, transform failure).
	Error error
}

// Dispatcher sends an instruction to an external provider via a configured connection.
// Implementations handle the transport-level concerns (HTTP, gRPC, etc.) and authentication.
// Retry logic, circuit breaking, and persistence are handled by the dispatch worker layer.
type Dispatcher interface {
	// Dispatch sends the instruction to the provider using the given connection and route.
	// It returns a DispatchResult containing the HTTP status, raw body, parsed outcome,
	// elapsed duration, and any transport-level error.
	// Dispatch does not implement retry logic; callers are responsible for retrying.
	Dispatch(ctx context.Context, instruction *domain.Instruction, conn *domain.ProviderConnection, route *InstructionRoute) DispatchResult
}
