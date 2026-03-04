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
//   - financial_gateway.cancel_payment (compensation for dispatch_payment)
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
		"financial_gateway.cancel_payment": {
			handler: cancelPaymentHandler(c),
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
// Field names match the handlers.yaml schema for financial_gateway.dispatch_payment.
type dispatchPaymentParams struct {
	paymentOrderID         string
	amountMinorUnits       int64
	currency               string
	customerReference      string // provider customer ID (e.g., Stripe customer ID)
	paymentMethodReference string // provider payment method ID (e.g., Stripe pm_xxx)
	idempotencyKey         string
	rail                   financialgatewayv1.PaymentRail
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
	if p.customerReference, err = saga.RequireStringParam(params, "customer_reference"); err != nil {
		return p, err
	}
	if p.paymentMethodReference, err = saga.RequireStringParam(params, "payment_method_reference"); err != nil {
		return p, err
	}
	if p.idempotencyKey, err = saga.RequireStringParam(params, "idempotency_key"); err != nil {
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
// The proto requires internal account UUIDs for DebtorAccountId and CreditorAccountId.
// Provider-specific identifiers (customer_reference, payment_method_reference) are passed
// via metadata so the gateway can use them for Stripe API calls.
// debtor_account_id is extracted from the metadata map; creditor_reference becomes the Reference.
func buildDispatchPaymentRequest(p dispatchPaymentParams, ctx *saga.StarlarkContext, params map[string]any) *financialgatewayv1.DispatchPaymentRequest {
	// Build merged metadata: include provider references alongside any caller-supplied metadata.
	meta := extractStringMetadata(params)
	if meta == nil {
		meta = make(map[string]string)
	}
	meta["customer_reference"] = p.customerReference
	meta["payment_method_reference"] = p.paymentMethodReference

	// Extract internal account IDs from metadata (populated by the saga from input_data).
	debtorAccountID := meta["debtor_account_id"]
	creditorReference := meta["creditor_reference"]

	req := &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId:    p.paymentOrderID,
		Rail:              p.rail,
		AmountUnits:       p.amountMinorUnits,
		InstrumentCode:    p.currency,
		DebtorAccountId:   debtorAccountID,
		CreditorAccountId: debtorAccountID, // creditor is the same account for Stripe charges
		Reference:         creditorReference,
		IdempotencyKey:    &commonv1.IdempotencyKey{Key: p.idempotencyKey},
		Metadata:          meta,
	}

	// Apply optional correlation_id and causation_id (metadata already set above).
	if corrID, ok := params["correlation_id"].(string); ok && corrID != "" {
		req.CorrelationId = corrID
	} else {
		req.CorrelationId = ctx.CorrelationID.String()
	}
	if causationID, ok := params["causation_id"].(string); ok && causationID != "" {
		req.CausationId = causationID
	}

	return req
}

// applyRefundOptionalFields sets optional correlation_id, causation_id, and metadata
// on a DispatchRefundRequest.
func applyRefundOptionalFields(req *financialgatewayv1.DispatchRefundRequest, ctx *saga.StarlarkContext, params map[string]any) {
	if corrID, ok := params["correlation_id"].(string); ok && corrID != "" {
		req.CorrelationId = corrID
	} else {
		req.CorrelationId = ctx.CorrelationID.String()
	}
	if causationID, ok := params["causation_id"].(string); ok && causationID != "" {
		req.CausationId = causationID
	}
	req.Metadata = extractStringMetadata(params)
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

// cancelPaymentHandler cancels a pending payment dispatch via the FinancialGateway service.
// This is the compensation handler for dispatch_payment; it can only cancel payments that
// are still in PENDING status. Payments already dispatching or delivered must be reversed
// via dispatch_refund instead.
//
// Parameters:
//   - payment_order_id (string, required): UUID of the payment order to cancel
//   - reason (string, optional): Human-readable reason for the cancellation
//
// Returns a map containing:
//   - payment_order_id: Echo of the input payment_order_id
//   - status: Lifecycle status string (CANCELLED)
//   - reason: Echo of the input reason (if provided)
func cancelPaymentHandler(c *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "financial_gateway.cancel_payment"

		paymentOrderID, err := saga.RequireStringParam(params, "payment_order_id")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", handlerName, err)
		}

		reason, _ := params["reason"].(string)

		req := &financialgatewayv1.CancelPaymentRequest{
			PaymentOrderId: paymentOrderID,
			Reason:         reason,
			CorrelationId:  ctx.CorrelationID.String(),
		}
		if causationID, ok := params["causation_id"].(string); ok && causationID != "" {
			req.CausationId = causationID
		}

		clientCtx := prepareClientContext(ctx)
		resp, err := c.CancelPayment(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", handlerName, err)
		}

		return map[string]any{
			"payment_order_id": resp.GetPaymentOrderId(),
			"status":           resp.GetStatus(),
			"reason":           resp.GetReason(),
		}, nil
	}
}

// dispatchPaymentHandler dispatches a financial payment via the FinancialGateway service.
//
// Parameters:
//   - payment_order_id (string, required): UUID of the payment order being dispatched
//   - amount_minor_units (int64, required): Payment amount in smallest currency unit (e.g., cents)
//   - currency (string, required): Instrument/currency code (e.g., "GBP", "USD")
//   - customer_reference (string, required): Provider customer identifier (e.g., Stripe customer ID)
//   - payment_method_reference (string, required): Provider payment method identifier (e.g., Stripe pm_xxx)
//   - idempotency_key (string, required): Idempotency key to prevent duplicate charges
//   - rail (string, required): Payment rail enum string: STRIPE, SWIFT, ACH, or FEDNOW
//   - correlation_id (string, optional): Links to originating saga/event
//   - causation_id (string, optional): Identifies the event that caused this dispatch
//   - metadata (map, optional): Additional key-value pairs for routing or audit
//
// Returns a map containing:
//   - provider_reference_id: Provider-assigned reference identifier (e.g., Stripe PaymentIntent ID)
//   - status: Lifecycle status string (e.g., "PENDING")
//   - platform_fee_minor_units: Platform fee charged in minor currency units
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
			"provider_reference_id":    resp.GetProviderReference(),
			"status":                   dispatchStatusToString(resp.GetStatus()),
			"platform_fee_minor_units": int64(0),
		}, nil
	}
}

// dispatchRefundParams holds validated parameters for a dispatch_refund call.
// Field names match the handlers.yaml schema for financial_gateway.dispatch_refund.
type dispatchRefundParams struct {
	paymentOrderID         string
	refundAmountMinorUnits int64
	reason                 string
	idempotencyKey         string
}

// parseDispatchRefundParams extracts and validates required params for dispatch_refund.
func parseDispatchRefundParams(params map[string]any) (dispatchRefundParams, error) {
	var p dispatchRefundParams
	var err error

	if p.paymentOrderID, err = saga.RequireStringParam(params, "payment_order_id"); err != nil {
		return p, err
	}
	if p.refundAmountMinorUnits, err = requireInt64Param(params, "refund_amount_minor_units"); err != nil {
		return p, err
	}
	if p.idempotencyKey, err = saga.RequireStringParam(params, "idempotency_key"); err != nil {
		return p, err
	}
	// reason is optional in the schema
	if r, ok := params["reason"].(string); ok {
		p.reason = r
	}
	if p.reason == "" {
		p.reason = "Refund requested"
	}
	return p, nil
}

// dispatchRefundHandler dispatches a financial refund via the FinancialGateway service.
//
// Parameters:
//   - payment_order_id (string, required): UUID of the original payment order being refunded
//   - refund_amount_minor_units (int64, required): Refund amount in smallest currency unit (e.g., cents)
//   - idempotency_key (string, required): Idempotency key to prevent duplicate refunds
//   - reason (string, optional): Human-readable reason for the refund
//   - correlation_id (string, optional): Links to originating saga/event
//   - causation_id (string, optional): Identifies the event that caused this refund
//   - metadata (map, optional): Additional key-value pairs for routing or audit
//
// Returns a map containing:
//   - refund_reference_id: UUID of the created refund dispatch record
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
			OriginalDispatchId: p.paymentOrderID,
			RefundAmountUnits:  p.refundAmountMinorUnits,
			Reason:             p.reason,
			IdempotencyKey:     &commonv1.IdempotencyKey{Key: p.idempotencyKey},
		}
		applyRefundOptionalFields(req, ctx, params)

		clientCtx := prepareClientContext(ctx)
		resp, err := c.DispatchRefund(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", handlerName, err)
		}

		return map[string]any{
			"refund_reference_id": resp.GetDispatchId(),
			"status":              dispatchStatusToString(resp.GetStatus()),
			"provider_reference":  resp.GetProviderReference(),
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
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("%w: %s has fractional value %v, expected integer", saga.ErrInvalidParamType, key, v)
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%w: %s must be numeric, got %T", saga.ErrInvalidParamType, key, val)
	}
}
