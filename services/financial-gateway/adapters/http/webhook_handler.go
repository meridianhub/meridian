// Package http provides the HTTP adapter for receiving Stripe webhooks in the financial-gateway service.
//
// When a webhook is received, it is validated, translated into a domain event
// (PaymentCapturedEvent or PaymentFailedEvent), and published to the transactional outbox
// for reliable delivery to Kafka. This moves webhook processing from payment-order service
// to financial-gateway, the correct architectural home for provider-specific concerns.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"

	financialgatewayeventsv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway_events/v1"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// StripeWebhookMaxBodySize is the maximum allowed Stripe webhook payload size (512KB).
const StripeWebhookMaxBodySize = 512 * 1024

// Webhook handler errors.
var (
	ErrNilClientFactory     = errors.New("stripe client factory cannot be nil")
	ErrNilOutboxPublisher   = errors.New("outbox publisher cannot be nil")
	ErrMissingTenantContext = errors.New("missing tenant context for stripe webhook")
)

// OutboxEventPublisher is the interface for publishing domain events to the transactional outbox.
// This abstraction allows for test doubles without requiring a real *gorm.DB.
type OutboxEventPublisher interface {
	Publish(ctx context.Context, tx *gorm.DB, event proto.Message, cfg events.PublishConfig) error
}

// WebhookHandler handles incoming Stripe webhook events by validating the Stripe-Signature,
// translating events into domain events, and publishing them to the transactional outbox.
type WebhookHandler struct {
	clientFactory *stripeadapter.ClientFactory
	publisher     OutboxEventPublisher
	db            *gorm.DB
	logger        *slog.Logger
}

// WebhookHandlerConfig contains configuration for creating a WebhookHandler.
type WebhookHandlerConfig struct {
	// ClientFactory creates tenant-scoped Stripe clients for signature validation.
	ClientFactory *stripeadapter.ClientFactory

	// OutboxPublisher publishes domain events to the transactional outbox.
	// If nil, a real OutboxPublisher must be provided via OutboxPublisherInner.
	OutboxPublisher OutboxEventPublisher

	// DB is the database connection for transactional outbox writes.
	// May be nil in tests using a stub publisher.
	DB *gorm.DB

	// Logger is the structured logger. Defaults to slog.Default() if nil.
	Logger *slog.Logger
}

// NewWebhookHandler creates a new WebhookHandler.
// Panics if ClientFactory or OutboxPublisher is nil to fail fast during initialization.
func NewWebhookHandler(cfg WebhookHandlerConfig) *WebhookHandler {
	if cfg.ClientFactory == nil {
		panic(ErrNilClientFactory.Error())
	}
	if cfg.OutboxPublisher == nil {
		panic(ErrNilOutboxPublisher.Error())
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{
		clientFactory: cfg.ClientFactory,
		publisher:     cfg.OutboxPublisher,
		db:            cfg.DB,
		logger:        logger,
	}
}

// webhookResponse is the JSON response body for Stripe webhook acknowledgements.
type webhookResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	Message      string `json:"message,omitempty"`
	Error        string `json:"error,omitempty"`
}

// HandleStripeWebhook processes an incoming Stripe webhook.
//
// The route must be registered as POST /webhooks/stripe/{tenantID} so that
// r.PathValue("tenantID") returns the tenant identifier. Stripe is configured
// to call the tenant-specific URL (e.g. /webhooks/stripe/acme-corp).
//
// Flow:
//  1. Validate HTTP method and read body
//  2. Extract tenant ID from URL path and inject into context
//  3. Resolve per-tenant Stripe client (contains webhook secret)
//  4. Validate Stripe-Signature using the tenant-specific secret
//  5. Map the Stripe event to a domain event (PaymentCapturedEvent or PaymentFailedEvent)
//  6. Publish the domain event to the transactional outbox
//  7. Return 200 on success (or for unsupported events, to prevent Stripe retries)
func (h *WebhookHandler) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	defer func() { _ = r.Body.Close() }()

	body, err := readWebhookBody(r)
	if err != nil {
		h.logger.Error("failed to read stripe webhook body", "error", err)
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if int64(len(body)) > StripeWebhookMaxBodySize {
		h.writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	tenantID, ctx, err := h.extractTenantFromPath(r, ctx)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, ErrMissingTenantContext.Error())
		return
	}

	parsed, err := h.validateAndParseWebhook(ctx, r, body, tenantID, w)
	if err != nil {
		return // response already written by validateAndParseWebhook
	}

	domainEvent, topic, err := h.mapToDomainEvent(parsed)
	if err != nil {
		var noMapping errNoMapping
		if errors.As(err, &noMapping) {
			h.writeSuccess(w, "event acknowledged")
			return
		}
		h.logger.Error("failed to map stripe event to domain event",
			"event_id", parsed.EventID,
			"error", err,
		)
		h.writeError(w, http.StatusInternalServerError, "failed to process event")
		return
	}

	// Require payment_order_id to be present in Stripe metadata.
	if parsed.PaymentOrderID == "" {
		h.logger.Warn("stripe webhook missing payment_order_id in metadata - event acknowledged without processing",
			"event_id", parsed.EventID,
			"gateway_reference_id", parsed.GatewayReferenceID,
			"tenant_id", tenantID.String(),
		)
		h.writeSuccess(w, "event acknowledged - no payment_order_id in metadata")
		return
	}

	h.publishDomainEvent(ctx, w, parsed, domainEvent, topic, tenantID)
}

// readWebhookBody reads the request body up to the maximum allowed size.
func readWebhookBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, StripeWebhookMaxBodySize+1))
}

// extractTenantFromPath extracts the tenant ID from the URL path and injects it into context.
func (h *WebhookHandler) extractTenantFromPath(r *http.Request, ctx context.Context) (tenant.TenantID, context.Context, error) {
	rawTenantID := r.PathValue("tenantID")
	if rawTenantID == "" {
		h.logger.Warn("missing tenant ID in stripe webhook URL path")
		return "", ctx, ErrMissingTenantContext
	}
	tenantID := tenant.TenantID(rawTenantID)
	return tenantID, tenant.WithTenant(ctx, tenantID), nil
}

// validateAndParseWebhook resolves the tenant's Stripe client, validates the signature,
// and parses the webhook. On error, it writes the HTTP response and returns a non-nil error.
func (h *WebhookHandler) validateAndParseWebhook(
	ctx context.Context,
	r *http.Request,
	body []byte,
	tenantID tenant.TenantID,
	w http.ResponseWriter,
) (stripeadapter.ParsedWebhookEvent, error) {
	client, err := h.clientFactory.NewClient(ctx)
	if err != nil {
		h.logger.Error("failed to get stripe client for tenant", "tenant_id", tenantID.String(), "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to resolve tenant configuration")
		return stripeadapter.ParsedWebhookEvent{}, err
	}

	if client.WebhookEndpointSecret == "" {
		h.logger.Error("no webhook secret for tenant", "tenant_id", tenantID.String())
		h.writeError(w, http.StatusInternalServerError, "no webhook secret configured for tenant")
		return stripeadapter.ParsedWebhookEvent{}, ErrMissingTenantContext
	}

	adapter, err := stripeadapter.NewWebhookAdapter(client.WebhookEndpointSecret)
	if err != nil {
		h.logger.Error("failed to create stripe webhook adapter", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return stripeadapter.ParsedWebhookEvent{}, err
	}

	parsed, err := adapter.ParseWebhook(r, body)
	if err != nil {
		h.handleParseError(w, err, tenantID)
		return stripeadapter.ParsedWebhookEvent{}, err
	}

	return parsed, nil
}

// handleParseError writes the appropriate HTTP response for a webhook parsing error.
func (h *WebhookHandler) handleParseError(w http.ResponseWriter, err error, tenantID tenant.TenantID) {
	switch {
	case errors.Is(err, stripeadapter.ErrWebhookInvalidSignature):
		h.logger.Warn("invalid stripe webhook signature", "tenant_id", tenantID.String())
		h.writeError(w, http.StatusUnauthorized, "invalid webhook signature")
	case errors.Is(err, stripeadapter.ErrWebhookMissingSignature):
		h.logger.Warn("missing stripe webhook signature")
		h.writeError(w, http.StatusUnauthorized, "missing Stripe-Signature header")
	case errors.Is(err, stripeadapter.ErrWebhookUnsupportedEvent):
		h.logger.Debug("unsupported stripe event type", "tenant_id", tenantID.String())
		h.writeSuccess(w, "event type not handled")
	default:
		h.logger.Error("failed to parse stripe webhook", "error", err, "tenant_id", tenantID.String())
		h.writeError(w, http.StatusBadRequest, "failed to parse webhook")
	}
}

// publishDomainEvent publishes the mapped domain event to the transactional outbox.
func (h *WebhookHandler) publishDomainEvent(
	ctx context.Context,
	w http.ResponseWriter,
	parsed stripeadapter.ParsedWebhookEvent,
	domainEvent proto.Message,
	topic string,
	tenantID tenant.TenantID,
) {
	h.logger.Info("publishing stripe webhook domain event",
		"event_id", parsed.EventID,
		"gateway_reference_id", parsed.GatewayReferenceID,
		"payment_order_id", parsed.PaymentOrderID,
		"topic", topic,
		"tenant_id", tenantID.String(),
	)

	if err := h.publisher.Publish(ctx, h.db, domainEvent, events.PublishConfig{
		EventType:     topicToEventType(topic),
		Topic:         topic,
		AggregateID:   parsed.PaymentOrderID,
		AggregateType: "PaymentOrder",
		PartitionKey:  parsed.PaymentOrderID,
	}); err != nil {
		h.logger.Error("failed to publish domain event to outbox",
			"event_id", parsed.EventID,
			"topic", topic,
			"error", err,
		)
		h.writeError(w, http.StatusInternalServerError, "failed to publish event")
		return
	}

	h.writeSuccess(w, "webhook processed successfully")
}

// mapToDomainEvent translates a parsed Stripe webhook event into the appropriate
// domain event protobuf message and returns the Kafka topic to publish to.
func (h *WebhookHandler) mapToDomainEvent(parsed stripeadapter.ParsedWebhookEvent) (proto.Message, string, error) {
	switch parsed.Status {
	case "SETTLED":
		return buildCapturedEvent(parsed), topics.FinancialGatewayPaymentCapturedV1, nil
	case "REJECTED":
		return buildFailedEvent(parsed), topics.FinancialGatewayPaymentFailedV1, nil
	case "REFUNDED":
		return buildRefundedEvent(parsed), topics.FinancialGatewayPaymentRefundedV1, nil
	case "DISPUTED":
		return buildDisputedEvent(parsed), topics.FinancialGatewayPaymentDisputedV1, nil
	default:
		h.logger.Debug("stripe event acknowledged without domain event mapping",
			"status", parsed.Status,
			"event_id", parsed.EventID,
		)
		return nil, "", errNoMapping{status: parsed.Status}
	}
}

// buildCapturedEvent creates a PaymentCapturedEvent from a parsed webhook event.
func buildCapturedEvent(parsed stripeadapter.ParsedWebhookEvent) *financialgatewayeventsv1.PaymentCapturedEvent {
	evt := &financialgatewayeventsv1.PaymentCapturedEvent{
		EventId:             uuid.New().String(),
		Version:             1,
		PaymentOrderId:      parsed.PaymentOrderID,
		ProviderReferenceId: parsed.GatewayReferenceID,
		ProviderEventId:     parsed.EventID,
		CausationId:         parsed.EventID,
		AmountMinorUnits:    parsed.AmountMinorUnits,
		Currency:            parsed.Currency,
	}
	if !parsed.Timestamp.IsZero() {
		evt.CapturedAt = timestamppb.New(parsed.Timestamp)
	}
	return evt
}

// buildFailedEvent creates a PaymentFailedEvent from a parsed webhook event.
func buildFailedEvent(parsed stripeadapter.ParsedWebhookEvent) *financialgatewayeventsv1.PaymentFailedEvent {
	evt := &financialgatewayeventsv1.PaymentFailedEvent{
		EventId:             uuid.New().String(),
		Version:             1,
		PaymentOrderId:      parsed.PaymentOrderID,
		ProviderReferenceId: parsed.GatewayReferenceID,
		FailureReason:       parsed.Message,
		ProviderEventId:     parsed.EventID,
		CausationId:         parsed.EventID,
	}
	if !parsed.Timestamp.IsZero() {
		evt.FailedAt = timestamppb.New(parsed.Timestamp)
	}
	return evt
}

// buildRefundedEvent creates a PaymentRefundedEvent from a parsed webhook event.
func buildRefundedEvent(parsed stripeadapter.ParsedWebhookEvent) *financialgatewayeventsv1.PaymentRefundedEvent {
	evt := &financialgatewayeventsv1.PaymentRefundedEvent{
		EventId:                  uuid.New().String(),
		Version:                  1,
		PaymentOrderId:           parsed.PaymentOrderID,
		ProviderReferenceId:      parsed.GatewayReferenceID,
		ProviderEventId:          parsed.EventID,
		CausationId:              parsed.EventID,
		AmountRefundedMinorUnits: parsed.AmountMinorUnits,
		Currency:                 parsed.Currency,
	}
	if !parsed.Timestamp.IsZero() {
		evt.RefundedAt = timestamppb.New(parsed.Timestamp)
	}
	return evt
}

// buildDisputedEvent creates a PaymentDisputedEvent from a parsed webhook event.
func buildDisputedEvent(parsed stripeadapter.ParsedWebhookEvent) *financialgatewayeventsv1.PaymentDisputedEvent {
	evt := &financialgatewayeventsv1.PaymentDisputedEvent{
		EventId:             uuid.New().String(),
		Version:             1,
		PaymentOrderId:      parsed.PaymentOrderID,
		ProviderReferenceId: parsed.GatewayReferenceID,
		ProviderEventId:     parsed.EventID,
		CausationId:         parsed.EventID,
		DisputeReason:       parsed.Message,
	}
	if !parsed.Timestamp.IsZero() {
		evt.DisputedAt = timestamppb.New(parsed.Timestamp)
	}
	return evt
}

// errNoMapping is returned when a Stripe event status has no domain event mapping.
type errNoMapping struct {
	status string
}

func (e errNoMapping) Error() string {
	return "no domain event mapping for status: " + e.status
}

// topicToEventType converts a Kafka topic constant to an outbox event type string.
func topicToEventType(topic string) string {
	switch topic {
	case topics.FinancialGatewayPaymentCapturedV1:
		return "financial_gateway.payment_captured.v1"
	case topics.FinancialGatewayPaymentFailedV1:
		return "financial_gateway.payment_failed.v1"
	case topics.FinancialGatewayPaymentRefundedV1:
		return "financial_gateway.payment_refunded.v1"
	case topics.FinancialGatewayPaymentDisputedV1:
		return "financial_gateway.payment_disputed.v1"
	default:
		return topic
	}
}

func (h *WebhookHandler) writeSuccess(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(webhookResponse{Acknowledged: true, Message: message}); err != nil {
		h.logger.Error("failed to encode success response", "error", err)
	}
}

func (h *WebhookHandler) writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(webhookResponse{Acknowledged: false, Error: message}); err != nil {
		h.logger.Error("failed to encode error response", "error", err)
	}
}
