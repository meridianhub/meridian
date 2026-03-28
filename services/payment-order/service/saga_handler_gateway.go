package service

import (
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// sendToGatewayHandler creates a handler for the payment_order.send_to_gateway step.
func sendToGatewayHandler(deps *PaymentOrderHandlerDeps, logger *slog.Logger) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		const handlerName = "payment_order.send_to_gateway"

		gwReq, err := validateGatewayParams(params)
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		logger.Info("sending payment to gateway",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", gwReq.PaymentOrderID.String(),
		)

		if deps.PaymentGateway == nil {
			return nil, wrapHandlerError(handlerName, ErrPaymentGatewayNotConfigured)
		}

		resp, err := deps.PaymentGateway.SendPayment(ctx.Context, gwReq)
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to send payment: %w", err))
		}

		if err := validateGatewayResponse(resp); err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		logger.Info("payment sent to gateway successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", gwReq.PaymentOrderID.String(),
			"gateway_reference_id", resp.GatewayReferenceID,
			"gateway_status", resp.Status,
		)

		return map[string]any{
			"gateway_reference_id": resp.GatewayReferenceID,
			"gateway_status":       string(resp.Status),
		}, nil
	}
}

// validateGatewayParams extracts and validates required parameters for the send_to_gateway handler.
func validateGatewayParams(params map[string]any) (gateway.PaymentRequest, error) {
	paymentOrderIDStr, err := requireStringParam(params, "payment_order_id")
	if err != nil {
		return gateway.PaymentRequest{}, err
	}
	paymentOrderID, err := uuid.Parse(paymentOrderIDStr)
	if err != nil {
		return gateway.PaymentRequest{}, fmt.Errorf("invalid payment_order_id: %w", err)
	}
	debtorAccountID, err := requireStringParam(params, "debtor_account_id")
	if err != nil {
		return gateway.PaymentRequest{}, err
	}
	creditorReference, err := requireStringParam(params, "creditor_reference")
	if err != nil {
		return gateway.PaymentRequest{}, err
	}
	amountCents, err := requireInt64Param(params, "amount_cents")
	if err != nil {
		return gateway.PaymentRequest{}, err
	}
	currency, err := requireStringParam(params, "currency")
	if err != nil {
		return gateway.PaymentRequest{}, err
	}
	idempotencyKey, err := requireStringParam(params, "idempotency_key")
	if err != nil {
		return gateway.PaymentRequest{}, err
	}
	return gateway.PaymentRequest{
		PaymentOrderID:    paymentOrderID,
		DebtorAccountID:   debtorAccountID,
		CreditorReference: creditorReference,
		Amount:            mustNewMoney(currency, amountCents),
		IdempotencyKey:    idempotencyKey,
	}, nil
}

// validateGatewayResponse checks the gateway response status and returns an error for non-success statuses.
func validateGatewayResponse(resp gateway.PaymentResponse) error {
	switch resp.Status {
	case gateway.StatusRejected:
		return fmt.Errorf("%w: %s", ErrPaymentRejected, resp.Message)
	case gateway.StatusAccepted, gateway.StatusPending:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnexpectedGatewayStatus, resp.Status)
	}
}
