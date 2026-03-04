package dispatch

import (
	"context"
	"time"
)

// Result captures the outcome of a single outbound dispatch attempt.
type Result struct {
	// StatusCode is the HTTP response status code from the provider.
	StatusCode int

	// ResponseBody is the raw response body received from the provider.
	ResponseBody []byte

	// Outcome is the extracted dispatch outcome after applying any inbound transform.
	// May be nil if the dispatch did not reach the response parsing stage.
	Outcome *Outcome

	// Duration is the total time elapsed for the dispatch attempt.
	Duration time.Duration

	// Error is non-nil when the dispatch failed before a response could be parsed
	// (e.g., network error, auth failure, transform failure).
	Error error
}

// Outcome captures the parsed result from a provider response.
type Outcome struct {
	// ExternalID is the provider's identifier for the instruction, if returned.
	ExternalID string

	// ProviderStatus is the provider's status string for the instruction
	// (e.g., "ACCEPTED", "PENDING", "REJECTED").
	ProviderStatus string

	// ShouldRetry indicates whether the dispatch should be retried.
	ShouldRetry bool

	// FailureReason contains a description of why the instruction failed.
	// Non-empty only when the provider indicates a permanent failure.
	FailureReason string
}

// Dispatcher sends a dispatchable instruction to an external endpoint.
// Implementations handle transport-level concerns (HTTP, gRPC, etc.) and authentication.
// Retry logic, circuit breaking, and persistence are handled by the worker layer.
type Dispatcher[I DispatchableInstruction, C any, R any] interface {
	// Dispatch sends the instruction to the provider using the given connection and route.
	Dispatch(ctx context.Context, instruction I, connection C, route R) Result
}
