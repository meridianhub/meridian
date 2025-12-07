// Package gateway provides the PaymentGateway port interface and implementations
// for external payment gateway interactions in the payment order saga.
package gateway

import (
	"context"

	"github.com/google/uuid"

	"github.com/meridianhub/meridian/services/payment-order/domain"
)

// Status represents the response status from the external payment gateway.
type Status string

const (
	// StatusAccepted indicates the payment was accepted by the gateway.
	StatusAccepted Status = "ACCEPTED"
	// StatusRejected indicates the payment was rejected by the gateway.
	StatusRejected Status = "REJECTED"
	// StatusPending indicates the payment is pending confirmation.
	StatusPending Status = "PENDING"
)

// PaymentRequest represents a request to send a payment through the gateway.
type PaymentRequest struct {
	PaymentOrderID    uuid.UUID
	DebtorAccountID   string
	CreditorReference string
	Amount            domain.Money
	IdempotencyKey    string
}

// PaymentResponse represents the response from the payment gateway.
type PaymentResponse struct {
	GatewayReferenceID string
	Status             Status
	Message            string
}

// PaymentGateway defines the port interface for external payment gateway interactions.
// Implementations may connect to real payment providers or provide mock behavior for testing.
type PaymentGateway interface {
	// SendPayment sends a payment request to the external gateway.
	// Returns a response with the gateway reference ID and status.
	// The IdempotencyKey in the request ensures at-most-once processing.
	SendPayment(ctx context.Context, req PaymentRequest) (PaymentResponse, error)
}
