// Package client provides Starlark service bindings for Account Reconciliation.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga step execution with real Reconciliation service integration.
package client

import (
	"context"
	"fmt"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// RegisterStarlarkHandlers registers all Starlark service bindings for Account Reconciliation.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// Example usage:
//
//	registry := saga.NewHandlerRegistry()
//	client, cleanup, _ := client.New(client.Config{...})
//	defer cleanup()
//	err := RegisterStarlarkHandlers(registry, client)
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
	handlers := map[string]struct {
		handler  saga.Handler
		metadata saga.HandlerMetadata
	}{
		"reconciliation.initiate_run": {
			handler: initiateRunHandler(client),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				Description:         "Initiate a new settlement reconciliation run",
				Compensate:          "reconciliation.cancel_run",
				HasAutoCompensation: true,
				ProtoRequestType:    (*reconciliationv1.InitiateAccountReconciliationRequest)(nil),
				ProtoResponseType:   (*reconciliationv1.InitiateAccountReconciliationResponse)(nil),
				ParamOverrides: map[string]saga.ParamOverride{
					"scope":           {Type: "enum"},
					"settlement_type": {Type: "enum"},
				},
				Version: 1,
			},
		},
		"reconciliation.execute_run": {
			handler: executeRunHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Trigger execution of a pending settlement run",
				CompensationStrategy: "none",
				ProtoRequestType:     (*reconciliationv1.ExecuteAccountReconciliationRequest)(nil),
				ProtoResponseType:    (*reconciliationv1.ExecuteAccountReconciliationResponse)(nil),
				Version:              1,
			},
		},
		"reconciliation.retrieve_run": {
			handler: retrieveRunHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Retrieve a settlement run summary",
				CompensationStrategy: "none",
				ProtoRequestType:     (*reconciliationv1.RetrieveAccountReconciliationRequest)(nil),
				ProtoResponseType:    (*reconciliationv1.RetrieveAccountReconciliationResponse)(nil),
				Version:              1,
			},
		},
		"reconciliation.cancel_run": {
			handler: cancelRunHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Cancel a settlement run (compensation handler)",
				CompensationStrategy: "none",
				ProtoRequestType:     (*reconciliationv1.ControlAccountReconciliationRequest)(nil),
				ProtoResponseType:    (*reconciliationv1.ControlAccountReconciliationResponse)(nil),
				Version:              1,
			},
		},
		"reconciliation.assert_balance": {
			handler: assertBalanceHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Evaluate a balance assertion against current positions",
				CompensationStrategy: "none",
				ProtoRequestType:     (*reconciliationv1.AssertBalanceRequest)(nil),
				ProtoResponseType:    (*reconciliationv1.AssertBalanceResponse)(nil),
				Version:              1,
			},
		},
		"reconciliation.initiate_dispute": {
			handler: initiateDisputeHandler(client),
			metadata: saga.HandlerMetadata{
				Category:             saga.HandlerCategorySettlement,
				Description:          "Raise a formal dispute against a detected variance",
				CompensationStrategy: "none",
				ProtoRequestType:     (*reconciliationv1.InitiateDisputeRequest)(nil),
				ProtoResponseType:    (*reconciliationv1.InitiateDisputeResponse)(nil),
				Version:              1,
			},
		},
	}

	for name, h := range handlers {
		if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
			return fmt.Errorf("failed to register %s: %w", name, err)
		}
	}
	return nil
}

// initiateRunHandler starts a new settlement run via gRPC.
//
// Parameters:
//   - account_id (string): The account identifier to reconcile
//   - scope (string): Reconciliation scope (ACCOUNT, INSTRUMENT, PORTFOLIO, FULL)
//   - settlement_type (string): Settlement cycle (DAILY, WEEKLY, etc.)
//   - initiated_by (string): User or system starting the run
//
// Returns a map containing:
//   - run_id: The unique settlement run identifier
//   - status: The run status (PENDING)
func initiateRunHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}
		initiatedBy, err := saga.RequireStringParam(params, "initiated_by")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		req := &reconciliationv1.InitiateAccountReconciliationRequest{
			AccountId:   accountID,
			Scope:       parseScope(params),
			InitiatedBy: initiatedBy,
		}

		if st := parseSettlementType(params); st != reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED {
			req.SettlementType = st
		}

		resp, err := client.InitiateAccountReconciliation(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("reconciliation.initiate_run: %w", err)
		}

		run := resp.GetRun()
		return map[string]any{
			"run_id": run.GetRunId(),
			"status": "PENDING",
		}, nil
	}
}

// executeRunHandler triggers execution of a pending settlement run.
//
// Parameters:
//   - run_id (string): The settlement run identifier to execute
//
// Returns a map containing:
//   - run_id: The settlement run identifier
//   - status: The run status (RUNNING)
func executeRunHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		runID, err := saga.RequireStringParam(params, "run_id")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		resp, err := client.ExecuteAccountReconciliation(clientCtx, &reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: runID,
		})
		if err != nil {
			return nil, fmt.Errorf("reconciliation.execute_run: %w", err)
		}

		run := resp.GetRun()
		return map[string]any{
			"run_id": run.GetRunId(),
			"status": "RUNNING",
		}, nil
	}
}

// retrieveRunHandler retrieves a settlement run summary.
//
// Parameters:
//   - run_id (string): The settlement run identifier to retrieve
//
// Returns a map containing:
//   - run_id: The settlement run identifier
//   - status: The current run status
//   - variance_count: Number of variances detected
func retrieveRunHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		runID, err := saga.RequireStringParam(params, "run_id")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		resp, err := client.RetrieveAccountReconciliation(clientCtx, &reconciliationv1.RetrieveAccountReconciliationRequest{
			RunId: runID,
		})
		if err != nil {
			return nil, fmt.Errorf("reconciliation.retrieve_run: %w", err)
		}

		run := resp.GetRun()
		return map[string]any{
			"run_id":         run.GetRunId(),
			"status":         run.GetStatus().String(),
			"variance_count": int64(run.GetVarianceCount()),
		}, nil
	}
}

// cancelRunHandler cancels a settlement run.
//
// Parameters:
//   - run_id (string): The settlement run identifier to cancel
//   - reason (string): Optional reason for cancellation
//
// Returns a map containing:
//   - run_id: The settlement run identifier
//   - status: The run status (CANCELLED)
func cancelRunHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		runID, err := saga.RequireStringParam(params, "run_id")
		if err != nil {
			return nil, err
		}

		reason := optionalString(params, "reason")

		clientCtx := prepareClientContext(ctx)

		resp, err := client.ControlAccountReconciliation(clientCtx, &reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  runID,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
			Reason: reason,
		})
		if err != nil {
			return nil, fmt.Errorf("reconciliation.cancel_run: %w", err)
		}

		run := resp.GetRun()
		return map[string]any{
			"run_id": run.GetRunId(),
			"status": "CANCELLED",
		}, nil
	}
}

// assertBalanceHandler evaluates a balance assertion.
//
// Parameters:
//   - account_id (string): The account to assert
//   - instrument_code (string): The asset type to assert
//   - expression (string): The assertion rule (CEL expression)
//   - expected_balance (string): Expected balance as decimal string
//
// Returns a map containing:
//   - assertion_id: The assertion identifier
//   - status: The assertion result (PASSED, FAILED)
func assertBalanceHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}
		instrumentCode, err := saga.RequireStringParam(params, "instrument_code")
		if err != nil {
			return nil, err
		}
		expression, err := saga.RequireStringParam(params, "expression")
		if err != nil {
			return nil, err
		}
		expectedBalance, err := saga.RequireStringParam(params, "expected_balance")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		resp, err := client.AssertBalance(clientCtx, &reconciliationv1.AssertBalanceRequest{
			AccountId:       accountID,
			InstrumentCode:  instrumentCode,
			Expression:      expression,
			ExpectedBalance: expectedBalance,
		})
		if err != nil {
			return nil, fmt.Errorf("reconciliation.assert_balance: %w", err)
		}

		assertion := resp.GetAssertion()
		return map[string]any{
			"assertion_id": assertion.GetAssertionId(),
			"status":       assertion.GetStatus().String(),
		}, nil
	}
}

// initiateDisputeHandler raises a formal dispute against a variance.
//
// Parameters:
//   - variance_id (string): The variance being disputed
//   - run_id (string): The settlement run
//   - account_id (string): The account
//   - reason (string): Why the variance is being disputed
//   - raised_by (string): Who is raising the dispute
//
// Returns a map containing:
//   - dispute_id: The dispute identifier
//   - status: The dispute status (OPEN)
func initiateDisputeHandler(client *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		varianceID, err := saga.RequireStringParam(params, "variance_id")
		if err != nil {
			return nil, err
		}
		runID, err := saga.RequireStringParam(params, "run_id")
		if err != nil {
			return nil, err
		}
		accountID, err := saga.RequireStringParam(params, "account_id")
		if err != nil {
			return nil, err
		}
		reason, err := saga.RequireStringParam(params, "reason")
		if err != nil {
			return nil, err
		}
		raisedBy, err := saga.RequireStringParam(params, "raised_by")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)

		resp, err := client.InitiateDispute(clientCtx, &reconciliationv1.InitiateDisputeRequest{
			VarianceId: varianceID,
			RunId:      runID,
			AccountId:  accountID,
			Reason:     reason,
			RaisedBy:   raisedBy,
		})
		if err != nil {
			return nil, fmt.Errorf("reconciliation.initiate_dispute: %w", err)
		}

		dispute := resp.GetDispute()
		return map[string]any{
			"dispute_id": dispute.GetDisputeId(),
			"status":     "OPEN",
		}, nil
	}
}

// contextKey is a type for context keys to avoid collisions.
type contextKey string

// correlationIDContextKey is the typed context key for correlation ID.
const correlationIDContextKey contextKey = "x-correlation-id"

func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context
	clientCtx = context.WithValue(clientCtx, correlationIDContextKey, ctx.CorrelationID.String())
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)
	return clientCtx
}

func parseScope(params map[string]any) reconciliationv1.ReconciliationScope {
	scopeStr := optionalString(params, "scope")
	switch scopeStr {
	case "ACCOUNT":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT
	case "INSTRUMENT":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT
	case "PORTFOLIO":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO
	case "FULL":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL
	default:
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT
	}
}

func parseSettlementType(params map[string]any) reconciliationv1.SettlementType {
	stStr := optionalString(params, "settlement_type")
	switch stStr {
	case "DAILY":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY
	case "WEEKLY":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY
	case "MONTHLY":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY
	case "ON_DEMAND":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND
	case "END_OF_DAY":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY
	case "REAL_TIME":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME
	default:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED
	}
}

// optionalString extracts a string parameter from the params map or returns empty string.
func optionalString(params map[string]any, key string) string {
	v, ok := params[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
