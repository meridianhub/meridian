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

		// Extract required parameters
		paymentOrderIDStr, err := requireStringParam(params, "payment_order_id")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		paymentOrderID, err := uuid.Parse(paymentOrderIDStr)
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("invalid payment_order_id: %w", err))
		}

		debtorAccountID, err := requireStringParam(params, "debtor_account_id")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		creditorReference, err := requireStringParam(params, "creditor_reference")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		amountCents, err := requireInt64Param(params, "amount_cents")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		currency, err := requireStringParam(params, "currency")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		idempotencyKey, err := requireStringParam(params, "idempotency_key")
		if err != nil {
			return nil, wrapHandlerError(handlerName, err)
		}

		logger.Info("sending payment to gateway",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", paymentOrderIDStr,
		)

		// Check required dependency
		if deps.PaymentGateway == nil {
			return nil, wrapHandlerError(handlerName, ErrPaymentGatewayNotConfigured)
		}

		// Build gateway request
		resp, err := deps.PaymentGateway.SendPayment(ctx.Context, gateway.PaymentRequest{
			PaymentOrderID:    paymentOrderID,
			DebtorAccountID:   debtorAccountID,
			CreditorReference: creditorReference,
			Amount:            mustNewMoney(currency, amountCents),
			IdempotencyKey:    idempotencyKey,
		})
		if err != nil {
			return nil, wrapHandlerError(handlerName, fmt.Errorf("failed to send payment: %w", err))
		}

		// Process gateway response
		switch resp.Status {
		case gateway.StatusRejected:
			return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", ErrPaymentRejected, resp.Message))
		case gateway.StatusAccepted, gateway.StatusPending:
			// Success
		default:
			return nil, wrapHandlerError(handlerName, fmt.Errorf("%w: %s", ErrUnexpectedGatewayStatus, resp.Status))
		}

		logger.Info("payment sent to gateway successfully",
			"saga_execution_id", ctx.SagaExecutionID,
			"payment_order_id", paymentOrderIDStr,
			"gateway_reference_id", resp.GatewayReferenceID,
			"gateway_status", resp.Status,
		)

		return map[string]any{
			"gateway_reference_id": resp.GatewayReferenceID,
			"gateway_status":       string(resp.Status),
		}, nil
	}
}
