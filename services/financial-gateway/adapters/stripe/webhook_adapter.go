package stripe

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	stripego "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

// Webhook adapter errors.
var (
	ErrEmptyEndpointSecret     = errors.New("webhook endpoint secret cannot be empty")
	ErrWebhookInvalidSignature = errors.New("invalid stripe webhook signature")
	ErrWebhookMissingSignature = errors.New("missing Stripe-Signature header")
	ErrWebhookUnsupportedEvent = errors.New("unsupported stripe event type")
)

// Stripe event type constants handled by this adapter.
const (
	EventPaymentIntentSucceeded = "payment_intent.succeeded"
	EventPaymentIntentFailed    = "payment_intent.payment_failed"
	EventChargeRefunded         = "charge.refunded"
	EventChargeDisputeCreated   = "charge.dispute.created"
)

// ParsedWebhookEvent represents a Stripe webhook event translated into
// a gateway-neutral domain representation for upstream processing.
type ParsedWebhookEvent struct {
	// EventID is the Stripe event ID (e.g., "evt_1234"), used for idempotency.
	EventID            string
	GatewayReferenceID string
	PaymentOrderID     string
	Status             string
	Message            string
	Timestamp          time.Time

	// AmountMinorUnits is the amount in the smallest currency unit (e.g., cents for USD).
	// Zero if the event does not carry an amount (e.g., dispute events).
	AmountMinorUnits int64

	// Currency is the ISO 4217 currency code (e.g., "USD", "GBP").
	// Empty string if the event does not carry a currency.
	Currency string
}

// WebhookAdapter validates Stripe webhook signatures and translates
// Stripe events into ParsedWebhookEvent for further processing.
type WebhookAdapter struct {
	endpointSecret string
}

// NewWebhookAdapter creates a new WebhookAdapter with the given
// per-tenant webhook endpoint secret.
func NewWebhookAdapter(endpointSecret string) (*WebhookAdapter, error) {
	if endpointSecret == "" {
		return nil, ErrEmptyEndpointSecret
	}
	return &WebhookAdapter{
		endpointSecret: endpointSecret,
	}, nil
}

// ParseWebhook validates the Stripe-Signature header using HMAC-SHA256 and
// translates the Stripe event into a ParsedWebhookEvent. The body parameter
// should be the raw request body bytes (read before calling this method).
func (a *WebhookAdapter) ParseWebhook(r *http.Request, body []byte) (ParsedWebhookEvent, error) {
	sigHeader := r.Header.Get("Stripe-Signature")
	if sigHeader == "" {
		return ParsedWebhookEvent{}, ErrWebhookMissingSignature
	}

	event, err := webhook.ConstructEventWithOptions(body, sigHeader, a.endpointSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		return ParsedWebhookEvent{}, ErrWebhookInvalidSignature
	}

	switch string(event.Type) {
	case EventPaymentIntentSucceeded:
		return a.parsePaymentIntentSucceeded(&event)
	case EventPaymentIntentFailed:
		return a.parsePaymentIntentFailed(&event)
	case EventChargeRefunded:
		return a.parseChargeRefunded(&event)
	case EventChargeDisputeCreated:
		return a.parseChargeDisputed(&event)
	default:
		return ParsedWebhookEvent{}, ErrWebhookUnsupportedEvent
	}
}

func (a *WebhookAdapter) parsePaymentIntentSucceeded(event *stripego.Event) (ParsedWebhookEvent, error) {
	var pi stripego.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		return ParsedWebhookEvent{}, err
	}

	return ParsedWebhookEvent{
		EventID:            event.ID,
		GatewayReferenceID: pi.ID,
		PaymentOrderID:     pi.Metadata["payment_order_id"],
		Status:             "SETTLED",
		Timestamp:          time.Unix(event.Created, 0),
		AmountMinorUnits:   pi.Amount,
		Currency:           string(pi.Currency),
	}, nil
}

func (a *WebhookAdapter) parsePaymentIntentFailed(event *stripego.Event) (ParsedWebhookEvent, error) {
	var pi stripego.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		return ParsedWebhookEvent{}, err
	}

	var message string
	if pi.LastPaymentError != nil {
		message = pi.LastPaymentError.Msg
	}

	return ParsedWebhookEvent{
		EventID:            event.ID,
		GatewayReferenceID: pi.ID,
		PaymentOrderID:     pi.Metadata["payment_order_id"],
		Status:             "REJECTED",
		Message:            message,
		Timestamp:          time.Unix(event.Created, 0),
		AmountMinorUnits:   pi.Amount,
		Currency:           string(pi.Currency),
	}, nil
}

func (a *WebhookAdapter) parseChargeRefunded(event *stripego.Event) (ParsedWebhookEvent, error) {
	var charge stripego.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		return ParsedWebhookEvent{}, err
	}

	paymentOrderID := extractPaymentOrderID(&charge)

	return ParsedWebhookEvent{
		EventID:            event.ID,
		GatewayReferenceID: charge.ID,
		PaymentOrderID:     paymentOrderID,
		Status:             "REFUNDED",
		Timestamp:          time.Unix(event.Created, 0),
		AmountMinorUnits:   charge.Amount,
		Currency:           string(charge.Currency),
	}, nil
}

// disputeData holds the fields we need from a Stripe Dispute event.
// We use a minimal struct instead of stripe.Dispute because the SDK's
// Dispute type expects charge as a string ID, but webhook payloads
// deliver an expanded charge object.
type disputeData struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Status string `json:"status"`
	Charge struct {
		ID       string            `json:"id"`
		Metadata map[string]string `json:"metadata"`
	} `json:"charge"`
}

func (a *WebhookAdapter) parseChargeDisputed(event *stripego.Event) (ParsedWebhookEvent, error) {
	var dispute disputeData
	if err := json.Unmarshal(event.Data.Raw, &dispute); err != nil {
		return ParsedWebhookEvent{}, err
	}

	var paymentOrderID string
	if dispute.Charge.Metadata != nil {
		paymentOrderID = dispute.Charge.Metadata["payment_order_id"]
	}

	return ParsedWebhookEvent{
		EventID:            event.ID,
		GatewayReferenceID: dispute.Charge.ID,
		PaymentOrderID:     paymentOrderID,
		Status:             "DISPUTED",
		Message:            "dispute reason: " + dispute.Reason,
		Timestamp:          time.Unix(event.Created, 0),
	}, nil
}

// extractPaymentOrderID extracts the payment_order_id from a Charge,
// preferring the PaymentIntent metadata, falling back to charge metadata.
func extractPaymentOrderID(charge *stripego.Charge) string {
	if charge.PaymentIntent != nil && charge.PaymentIntent.Metadata != nil {
		if poID, ok := charge.PaymentIntent.Metadata["payment_order_id"]; ok {
			return poID
		}
	}
	if charge.Metadata != nil {
		return charge.Metadata["payment_order_id"]
	}
	return ""
}
