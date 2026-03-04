// Package ports defines the interfaces (ports) for the operational-gateway service.
package ports

import (
	"context"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/shared/pkg/dispatch"
)

// DispatchResult captures the outcome of a single outbound HTTP dispatch attempt.
// It is an alias for dispatch.Result from the shared dispatch package.
type DispatchResult = dispatch.Result

// InstructionOutcome captures the result of processing an inbound provider response.
// It is an alias for dispatch.Outcome from the shared dispatch package.
type InstructionOutcome = dispatch.Outcome

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
