// Package stripe provides Stripe webhook handling for the Control Plane service.
// It receives Stripe webhook events, verifies signatures, and publishes payment
// events to Kafka for downstream saga processing.
package stripe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

// Webhook handler errors.
var (
	ErrInvalidSignature   = errors.New("invalid webhook signature")
	ErrMissingSignature   = errors.New("missing Stripe-Signature header")
	ErrInvalidRequestBody = errors.New("invalid request body")
	ErrMissingMetadata    = errors.New("required metadata field missing")
	ErrNilEventPublisher  = errors.New("event publisher cannot be nil")
	ErrEmptyWebhookSecret = errors.New("webhook secret cannot be empty")
	ErrUnsupportedEvent   = errors.New("unsupported event type")
	ErrPayloadTooLarge    = errors.New("request body exceeds maximum size")
	ErrPublishFailed      = errors.New("failed to publish payment event")
	ErrMissingChargeID    = errors.New("missing charge ID on succeeded payment intent")
)

// StripeSignatureHeader is the HTTP header containing the Stripe webhook signature.
const StripeSignatureHeader = "Stripe-Signature"

// MaxBodySize is the maximum allowed webhook payload size (512KB).
const MaxBodySize = 512 * 1024

// DefaultSignatureTolerance is the maximum age for webhook timestamps.
const DefaultSignatureTolerance = 5 * time.Minute

// Event type constants for the Stripe events we handle.
const (
	EventTypePaymentIntentSucceeded = "payment_intent.succeeded"
	EventTypePaymentIntentFailed    = "payment_intent.payment_failed"
	EventTypeChargeRefunded         = "charge.refunded"
)

// PaymentEvent represents a processed Stripe payment event ready for Kafka publishing.
type PaymentEvent struct {
	// EventID is a unique identifier for this event.
	EventID string `json:"event_id"`
	// StripeEventID is the original Stripe event ID (for idempotency).
	StripeEventID string `json:"stripe_event_id"`
	// EventType is the Stripe event type (e.g., "payment_intent.succeeded").
	EventType string `json:"event_type"`
	// TenantID is extracted from PaymentIntent metadata.
	TenantID string `json:"tenant_id"`
	// PartyID is extracted from PaymentIntent metadata.
	PartyID string `json:"party_id"`
	// AmountCents is the payment amount in the smallest currency unit.
	AmountCents int64 `json:"amount_cents"`
	// Currency is the three-letter ISO currency code (uppercase).
	Currency string `json:"currency"`
	// ChargeID is the Stripe Charge ID for reconciliation (external_reference_id).
	ChargeID string `json:"charge_id"`
	// PaymentIntentID is the Stripe PaymentIntent ID.
	PaymentIntentID string `json:"payment_intent_id"`
	// Timestamp is when the Stripe event was created.
	Timestamp time.Time `json:"timestamp"`
	// IdempotencyKey is a deterministic key derived from the Stripe event.
	IdempotencyKey string `json:"idempotency_key"`
}

// EventPublisher publishes payment events to the message broker.
type EventPublisher interface {
	// PublishPaymentEvent publishes a payment event to Kafka.
	// The key should be tenant_id for partition affinity.
	PublishPaymentEvent(ctx context.Context, event *PaymentEvent) error
}

// WebhookHandler handles incoming Stripe webhook events.
type WebhookHandler struct {
	webhookSecret string
	publisher     EventPublisher
	logger        *slog.Logger
}

// WebhookHandlerConfig contains configuration for creating a WebhookHandler.
type WebhookHandlerConfig struct {
	// WebhookSecret is the Stripe webhook signing secret (whsec_...).
	WebhookSecret string
	// Publisher publishes payment events to Kafka.
	Publisher EventPublisher
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// NewWebhookHandler creates a new Stripe WebhookHandler.
func NewWebhookHandler(cfg WebhookHandlerConfig) (*WebhookHandler, error) {
	if cfg.Publisher == nil {
		return nil, ErrNilEventPublisher
	}
	if cfg.WebhookSecret == "" {
		return nil, ErrEmptyWebhookSecret
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{
		webhookSecret: cfg.WebhookSecret,
		publisher:     cfg.Publisher,
		logger:        logger,
	}, nil
}

// HandleWebhook processes incoming Stripe webhook events.
// It verifies the Stripe signature, routes events by type, and publishes
// payment events to Kafka for saga processing.
func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	defer func() { _ = r.Body.Close() }()

	// Read request body with size limit
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodySize+1))
	if err != nil {
		h.logger.Error("failed to read request body", "error", err)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return
	}
	if int64(len(body)) > MaxBodySize {
		h.writeErrorResponse(w, http.StatusRequestEntityTooLarge, ErrPayloadTooLarge.Error())
		return
	}

	// Verify Stripe signature
	sigHeader := r.Header.Get(StripeSignatureHeader)
	if sigHeader == "" {
		h.logger.Warn("missing Stripe-Signature header")
		h.writeErrorResponse(w, http.StatusUnauthorized, ErrMissingSignature.Error())
		return
	}

	event, err := webhook.ConstructEventWithOptions(body, sigHeader, h.webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		h.logger.Warn("invalid webhook signature", "error", err)
		h.writeErrorResponse(w, http.StatusUnauthorized, ErrInvalidSignature.Error())
		return
	}

	h.logger.Info("received stripe webhook",
		"event_id", event.ID,
		"event_type", string(event.Type),
	)

	// Route by event type (using string() to avoid exhaustive switch on stripe.EventType)
	switch string(event.Type) {
	case EventTypePaymentIntentSucceeded:
		h.handlePaymentIntentSucceeded(r.Context(), w, &event)
	case EventTypePaymentIntentFailed:
		h.handlePaymentIntentFailed(w, &event)
	case EventTypeChargeRefunded:
		h.handleChargeRefunded(r.Context(), w, &event)
	default:
		// Acknowledge unknown events to prevent Stripe from retrying
		h.logger.Debug("ignoring unhandled event type", "event_type", string(event.Type))
		h.writeSuccessResponse(w, "event type not handled")
	}
}

// handlePaymentIntentSucceeded processes a successful payment and publishes it for saga execution.
func (h *WebhookHandler) handlePaymentIntentSucceeded(ctx context.Context, w http.ResponseWriter, event *stripe.Event) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		h.logger.Error("failed to unmarshal payment intent", "error", err, "event_id", event.ID)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return
	}

	tenantID, partyID, chargeID, err := validatePaymentIntentMetadata(&pi, event)
	if err != nil {
		h.logger.Error(err.Error(), "payment_intent_id", pi.ID, "event_id", event.ID)
		statusCode := http.StatusBadRequest
		if errors.Is(err, ErrMissingChargeID) {
			statusCode = http.StatusInternalServerError
		}
		h.writeErrorResponse(w, statusCode, err.Error())
		return
	}

	paymentEvent := &PaymentEvent{
		EventID:         uuid.New().String(),
		StripeEventID:   event.ID,
		EventType:       string(event.Type),
		TenantID:        tenantID,
		PartyID:         partyID,
		AmountCents:     pi.Amount,
		Currency:        strings.ToUpper(string(pi.Currency)),
		ChargeID:        chargeID,
		PaymentIntentID: pi.ID,
		Timestamp:       time.Unix(event.Created, 0),
		IdempotencyKey:  generateIdempotencyKey(event.ID, string(event.Type)),
	}

	if err := h.publisher.PublishPaymentEvent(ctx, paymentEvent); err != nil {
		h.logger.Error("failed to publish payment event",
			"error", err,
			"event_id", event.ID,
			"payment_intent_id", pi.ID,
		)
		h.writeErrorResponse(w, http.StatusInternalServerError, ErrPublishFailed.Error())
		return
	}

	h.logger.Info("payment event published",
		"event_id", event.ID,
		"payment_intent_id", pi.ID,
		"tenant_id", tenantID,
		"amount_cents", pi.Amount,
		"currency", string(pi.Currency),
	)

	h.writeSuccessResponse(w, "payment event published")
}

// validatePaymentIntentMetadata extracts and validates required metadata fields
// from a succeeded payment intent.
func validatePaymentIntentMetadata(pi *stripe.PaymentIntent, event *stripe.Event) (tenantID, partyID, chargeID string, err error) {
	tenantID = pi.Metadata["tenant_id"]
	if tenantID == "" {
		return "", "", "", fmt.Errorf("missing tenant_id in metadata")
	}

	partyID = pi.Metadata["party_id"]
	if partyID == "" {
		return "", "", "", fmt.Errorf("missing party_id in metadata")
	}

	chargeID = extractChargeID(pi)
	if chargeID == "" {
		return "", "", "", ErrMissingChargeID
	}

	return tenantID, partyID, chargeID, nil
}

// handlePaymentIntentFailed logs the failure. No saga is triggered for failures.
func (h *WebhookHandler) handlePaymentIntentFailed(w http.ResponseWriter, event *stripe.Event) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		h.logger.Error("failed to unmarshal failed payment intent", "error", err, "event_id", event.ID)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return
	}

	var failureMessage string
	if pi.LastPaymentError != nil {
		failureMessage = pi.LastPaymentError.Msg
	}

	h.logger.Warn("payment intent failed",
		"event_id", event.ID,
		"payment_intent_id", pi.ID,
		"failure_message", failureMessage,
	)

	h.writeSuccessResponse(w, "failure logged")
}

// handleChargeRefunded processes a refund and publishes it for refund saga execution.
func (h *WebhookHandler) handleChargeRefunded(ctx context.Context, w http.ResponseWriter, event *stripe.Event) {
	var charge stripe.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		h.logger.Error("failed to unmarshal charge", "error", err, "event_id", event.ID)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return
	}

	tenantID, partyID := extractRefundMetadata(&charge)

	if tenantID == "" {
		h.logger.Warn("missing tenant_id for refund, acknowledging without processing",
			"charge_id", charge.ID,
			"event_id", event.ID,
		)
		h.writeSuccessResponse(w, "refund acknowledged without tenant context")
		return
	}

	if partyID == "" {
		h.logger.Warn("missing party_id for refund, acknowledging without processing",
			"charge_id", charge.ID,
			"event_id", event.ID,
			"tenant_id", tenantID,
		)
		h.writeSuccessResponse(w, "refund acknowledged without party context")
		return
	}

	paymentEvent := buildRefundEvent(event, &charge, tenantID, partyID)

	if err := h.publisher.PublishPaymentEvent(ctx, paymentEvent); err != nil {
		h.logger.Error("failed to publish refund event",
			"error", err,
			"event_id", event.ID,
			"charge_id", charge.ID,
		)
		h.writeErrorResponse(w, http.StatusInternalServerError, ErrPublishFailed.Error())
		return
	}

	h.logger.Info("refund event published",
		"event_id", event.ID,
		"charge_id", charge.ID,
		"amount_refunded_cents", charge.AmountRefunded,
	)

	h.writeSuccessResponse(w, "refund event published")
}

// extractRefundMetadata extracts tenant_id and party_id from a refund charge,
// checking the PaymentIntent metadata first, then falling back to charge metadata.
func extractRefundMetadata(charge *stripe.Charge) (tenantID, partyID string) {
	if charge.PaymentIntent != nil && charge.PaymentIntent.Metadata != nil {
		tenantID = charge.PaymentIntent.Metadata["tenant_id"]
		partyID = charge.PaymentIntent.Metadata["party_id"]
	}
	if tenantID == "" && charge.Metadata != nil {
		tenantID = charge.Metadata["tenant_id"]
	}
	if partyID == "" && charge.Metadata != nil {
		partyID = charge.Metadata["party_id"]
	}
	return tenantID, partyID
}

// buildRefundEvent constructs a PaymentEvent from a charge refund event.
func buildRefundEvent(event *stripe.Event, charge *stripe.Charge, tenantID, partyID string) *PaymentEvent {
	paymentEvent := &PaymentEvent{
		EventID:        uuid.New().String(),
		StripeEventID:  event.ID,
		EventType:      string(event.Type),
		TenantID:       tenantID,
		PartyID:        partyID,
		AmountCents:    charge.AmountRefunded,
		Currency:       strings.ToUpper(string(charge.Currency)),
		ChargeID:       charge.ID,
		Timestamp:      time.Unix(event.Created, 0),
		IdempotencyKey: generateIdempotencyKey(event.ID, string(event.Type)),
	}
	if charge.PaymentIntent != nil {
		paymentEvent.PaymentIntentID = charge.PaymentIntent.ID
	}
	return paymentEvent
}

// extractChargeID extracts the latest charge ID from a PaymentIntent.
func extractChargeID(pi *stripe.PaymentIntent) string {
	if pi.LatestCharge != nil {
		return pi.LatestCharge.ID
	}
	return ""
}

// generateIdempotencyKey creates a deterministic idempotency key from the Stripe event.
// Uses SHA-256 of event_id + event_type to ensure duplicate webhooks produce the same key.
func generateIdempotencyKey(eventID, eventType string) string {
	data := eventID + ":" + eventType
	hash := sha256.Sum256([]byte(data))
	return "stripe:" + hex.EncodeToString(hash[:16])
}

// WebhookResponse represents the JSON response to Stripe.
type WebhookResponse struct {
	Received bool   `json:"received"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (h *WebhookHandler) writeSuccessResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := WebhookResponse{Received: true, Message: message}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode success response", "error", err)
	}
}

func (h *WebhookHandler) writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := WebhookResponse{Received: false, Error: message}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode error response", "error", err)
	}
}

// GenerateTestSignature creates a valid Stripe webhook signature for testing.
// This wraps the Stripe SDK's test helper for convenience.
func GenerateTestSignature(payload []byte, secret string) string {
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  secret,
	})
	return signed.Header
}
