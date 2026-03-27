package service

import (
	"fmt"
	"log/slog"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// postLedgerEntriesHandler creates a handler for the payment_order.post_ledger_entries step.
// This handler delegates to the orchestrator's PostLedgerEntriesFromParams method.
func postLedgerEntriesHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "payment_order.post_ledger_entries"

		logger.Info("posting ledger entries",
			"saga_execution_id", ctx.SagaExecutionID,
		)

		// Check required dependency
		if deps.Orchestrator == nil {
			return nil, wrapHandlerError(handlerName, ErrOrchestratorNotConfigured)
		}

		// Delegate to the orchestrator's PostLedgerEntriesFromParams method
		// which handles the complex multi-gRPC-call ledger posting flow.
		bookingLogID, err := deps.Orchestrator.PostLedgerEntriesFromParams(ctx.Context, params)
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to post ledger entries: %w", err))
		}

		logger.Info("ledger entries posted successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"booking_log_id", bookingLogID,
		)

		return map[string]any{
			"booking_log_id": bookingLogID,
			"status":         "POSTED",
		}, nil
	}
}
