// Package client provides Starlark service bindings for FinancialGateway.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga scripts to dispatch payments to external payment rails.
package client

import (
	"context"
	"fmt"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// RegisterStarlarkHandlers registers all Starlark service bindings for FinancialGateway.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register FinancialGateway
// handlers with the saga execution engine. Registered handlers:
//   - financial_gateway.dispatch_payment
//   - financial_gateway.dispatch_refund
//
// Example usage:
//
//	registry := saga.NewHandlerRegistry()
//	client, cleanup, _ := client.New(client.Config{...})
//	defer cleanup()
//	err := RegisterStarlarkHandlers(registry, client)
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, c *Client) error {
	handlers := map[string]struct {
		handler  saga.Handler
		metadata saga.HandlerMetadata
	}{
		"financial_gateway.dispatch_payment": {
			handler: dispatchPaymentHandler(c),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		"financial_gateway.dispatch_refund": {
			handler: dispatchRefundHandler(c),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
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

// dispatchPaymentParams holds validated parameters for a dispatch_payment call.
type dispatchPaymentParams struct {
	paymentOrderID    string
	amountMinorUnits  int64
	currency          string
	debtorAccountID   string
	creditorAccountID string
	reference         string
	rail              financialgatewayv1.PaymentRail
}

// parseDispatchPaymentParams extracts and validates required params for dispatch_payment.
func parseDispatchPaymentParams(params map[string]any) (dispatchPaymentParams, error) {
	var p dispatchPaymentParams
	var err error

	if p.paymentOrderID, err = saga.RequireStringParam(params, "payment_order_id"); err != nil {
		return p, err
	}
	if p.amountMinorUnits, err = requireInt64Param(params, "amount_minor_units"); err != nil {
		return p, err
	}
	if p.currency, err = saga.RequireStringParam(params, "currency"); err != nil {
		return p, err
	}
	if p.debtorAccountID, err = saga.RequireStringParam(params, "debtor_account_id"); err != nil {
		return p, err
	}
	if p.creditorAccountID, err = saga.RequireStringParam(params, "creditor_account_id"); err != nil {
		return p, err
	}
	if p.reference, err = saga.RequireStringParam(params, "reference"); err != nil {
		return p, err
	}

	railStr, err := saga.RequireStringParam(params, "rail")
	if err != nil {
		return p, err
	}
	if p.rail, err = stringToPaymentRail(railStr); err != nil {
		return p, err
	}

	return p, nil
}

// buildDispatchPaymentRequest constructs the gRPC request from validated params and context.
func buildDispatchPaymentRequest(p dispatchPaymentParams, ctx *saga.StarlarkContext, params map[string]any) *financialgatewayv1.DispatchPaymentRequest {
	req := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    p.paymentOrderID,
		Rail:              p.rail,
		AmountUnits:       p.amountMinorUnits,
		InstrumentCode:    p.currency,
		DebtorAccountId:   p.debtorAccountID,
		CreditorAccountId: p.creditorAccountID,
		Reference:         p.reference,
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: ctx.IdempotencyKey},
	}
	applyOptionalFields(req, ctx, params)
	return req
}

// applyOptionalFields sets optional correlation_id, causation_id, and metadata on the request.
// Accepts *DispatchPaymentRequest or *DispatchRefundRequest via type switch.
func applyOptionalFields(req any, ctx *saga.StarlarkContext, params map[string]any) {
	switch r := req.(type) {
	case *financialgatewayv1.DispatchPaymentRequest:
		if corrID, ok := params["correlation_id"].(string); ok && corrID != "" {
			r.CorrelationId = corrID
		} else {
			r.CorrelationId = ctx.CorrelationID.String()
		}
		if causationID, ok := params["causation_id"].(string); ok && causationID != "" {
			r.CausationId = causationID
		}
		r.Metadata = extractStringMetadata(params)
	case *financialgatewayv1.DispatchRefundRequest:
		if corrID, ok := params["correlation_id"].(string); ok && corrID != "" {
			r.CorrelationId = corrID
		} else {
			r.CorrelationId = ctx.CorrelationID.String()
		}
		if causationID, ok := params["causation_id"].(string); ok && causationID != "" {
			r.CausationId = causationID
		}
		r.Metadata = extractStringMetadata(params)
	}
}

// extractStringMetadata converts optional "metadata" param to map[string]string.
func extractStringMetadata(params map[string]any) map[string]string {
	metadataRaw, ok := params["metadata"]
	if !ok || metadataRaw == nil {
		return nil
	}
	metaMap, ok := metadataRaw.(map[string]any)
	if !ok {
		return nil
	}
	meta := make(map[string]string, len(metaMap))
	for k, v := range metaMap {
		if s, ok := v.(string); ok {
			meta[k] = s
		}
	}
	return meta
}

// dispatchPaymentHandler dispatches a financial payment via the FinancialGateway service.
//
// Parameters:
//   - payment_order_id (string, required): UUID of the payment order being dispatched
//   - amount_minor_units (int64, required): Payment amount in smallest currency unit (e.g., cents)
//   - currency (string, required): Instrument/currency code (e.g., "GBP", "USD")
//   - debtor_account_id (string, required): UUID of the account being debited
//   - creditor_account_id (string, required): UUID of the account being credited
//   - reference (string, required): Human-readable payment reference for the beneficiary
//   - rail (string, required): Payment rail enum string: STRIPE, SWIFT, ACH, or FEDNOW
//   - correlation_id (string, optional): Links to originating saga/event
//   - causation_id (string, optional): Identifies the event that caused this dispatch
//   - metadata (map, optional): Additional key-value pairs for routing or audit
//
// Returns a map containing:
//   - dispatch_id: UUID of the created dispatch record
//   - payment_order_id: Echo of the input payment_order_id
//   - status: Lifecycle status string (e.g., "PENDING")
//   - provider_reference: Payment rail's own identifier (if immediately available)
func dispatchPaymentHandler(c *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "financial_gateway.dispatch_payment"

		p, err := parseDispatchPaymentParams(params)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", handlerName, err)
		}

		req := buildDispatchPaymentRequest(p, ctx, params)

		clientCtx := prepareClientContext(ctx)
		resp, err := c.DispatchPayment(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", handlerName, err)
		}

		return map[string]any{
			"dispatch_id":        resp.GetDispatchId(),
			"payment_order_id":   resp.GetPaymentOrderId(),
			"status":             dispatchStatusToString(resp.GetStatus()),
			"provider_reference": resp.GetProviderReference(),
		}, nil
	}
}

// dispatchRefundParams holds validated parameters for a dispatch_refund call.
type dispatchRefundParams struct {
	originalDispatchID string
	refundAmountUnits  int64
	reason             string
}

// parseDispatchRefundParams extracts and validates required params for dispatch_refund.
func parseDispatchRefundParams(params map[string]any) (dispatchRefundParams, error) {
	var p dispatchRefundParams
	var err error

	if p.originalDispatchID, err = saga.RequireStringParam(params, "original_dispatch_id"); err != nil {
		return p, err
	}
	if p.refundAmountUnits, err = requireInt64Param(params, "refund_amount_units"); err != nil {
		return p, err
	}
	if p.reason, err = saga.RequireStringParam(params, "reason"); err != nil {
		return p, err
	}
	return p, nil
}

// dispatchRefundHandler dispatches a financial refund via the FinancialGateway service.
//
// Parameters:
//   - original_dispatch_id (string, required): UUID of the payment dispatch being refunded
//   - refund_amount_units (int64, required): Refund amount in smallest currency unit
//   - reason (string, required): Human-readable reason for the refund
//   - correlation_id (string, optional): Links to originating saga/event
//   - causation_id (string, optional): Identifies the event that caused this refund
//   - metadata (map, optional): Additional key-value pairs for routing or audit
//
// Returns a map containing:
//   - dispatch_id: UUID of the created refund dispatch record
//   - original_dispatch_id: Echo of the input original_dispatch_id
//   - status: Lifecycle status string (e.g., "PENDING")
//   - provider_reference: Payment rail's own identifier (if immediately available)
func dispatchRefundHandler(c *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "financial_gateway.dispatch_refund"

		p, err := parseDispatchRefundParams(params)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", handlerName, err)
		}

		req := &financialgatewayv1.DispatchRefundRequest{
			OriginalDispatchId: p.originalDispatchID,
			RefundAmountUnits:  p.refundAmountUnits,
			Reason:             p.reason,
			IdempotencyKey:     &commonv1.IdempotencyKey{Key: ctx.IdempotencyKey},
		}
		applyOptionalFields(req, ctx, params)

		clientCtx := prepareClientContext(ctx)
		resp, err := c.DispatchRefund(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", handlerName, err)
		}

		return map[string]any{
			"dispatch_id":          resp.GetDispatchId(),
			"original_dispatch_id": resp.GetOriginalDispatchId(),
			"status":               dispatchStatusToString(resp.GetStatus()),
			"provider_reference":   resp.GetProviderReference(),
		}, nil
	}
}

// prepareClientContext enriches the gRPC client context with saga metadata.
func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := clients.PropagateCorrelationID(ctx.Context)
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)
	clientCtx = clients.PropagateOrganization(clientCtx)
	return clientCtx
}

// stringToPaymentRail converts a Starlark rail string to the proto PaymentRail enum.
// Returns an error for unrecognized values rather than silently defaulting.
func stringToPaymentRail(s string) (financialgatewayv1.PaymentRail, error) {
	switch s {
	case "STRIPE":
		return financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, nil
	case "SWIFT":
		return financialgatewayv1.PaymentRail_PAYMENT_RAIL_SWIFT, nil
	case "ACH":
		return financialgatewayv1.PaymentRail_PAYMENT_RAIL_ACH, nil
	case "FEDNOW":
		return financialgatewayv1.PaymentRail_PAYMENT_RAIL_FEDNOW, nil
	default:
		return financialgatewayv1.PaymentRail_PAYMENT_RAIL_UNSPECIFIED,
			fmt.Errorf("%w: rail %q is not valid (expected STRIPE|SWIFT|ACH|FEDNOW)", saga.ErrInvalidParamType, s)
	}
}

// dispatchStatusToString converts the proto DispatchStatus to a human-readable string.
func dispatchStatusToString(s financialgatewayv1.DispatchStatus) string {
	switch s {
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_UNSPECIFIED:
		return "UNSPECIFIED"
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_PENDING:
		return "PENDING"
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING:
		return "DISPATCHING"
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED:
		return "DELIVERED"
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_ACKNOWLEDGED:
		return "ACKNOWLEDGED"
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_RETRYING:
		return "RETRYING"
	case financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// requireInt64Param extracts a required int64 parameter from Starlark params.
// Accepts int64, int, or float64 values (Starlark integers are typically int64).
func requireInt64Param(params map[string]any, key string) (int64, error) {
	val, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", saga.ErrMissingParam, key)
	}
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%w: %s must be numeric, got %T", saga.ErrInvalidParamType, key, val)
	}
}
