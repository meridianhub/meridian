package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway/stripe"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Stripe webhook handler errors.
var (
	ErrNilClientFactory     = errors.New("stripe client factory cannot be nil")
	ErrMissingTenantContext = errors.New("missing tenant context for stripe webhook")
	ErrNoWebhookSecret      = errors.New("no webhook secret configured for tenant")
)

// StripeWebhookMaxBodySize is the maximum allowed Stripe webhook payload size (512KB).
const StripeWebhookMaxBodySize = 512 * 1024

// StripeWebhookHandler handles incoming Stripe webhook events by validating
// the Stripe-Signature, translating events into WebhookRequest, and delegating
// to the existing webhook processing pipeline.
type StripeWebhookHandler struct {
	clientFactory  *stripe.ClientFactory
	webhookHandler *WebhookHandler
	logger         *slog.Logger
}

// StripeWebhookHandlerConfig contains configuration for creating a StripeWebhookHandler.
type StripeWebhookHandlerConfig struct {
	ClientFactory  *stripe.ClientFactory
	WebhookHandler *WebhookHandler
	Logger         *slog.Logger
}

// NewStripeWebhookHandler creates a new StripeWebhookHandler.
func NewStripeWebhookHandler(cfg StripeWebhookHandlerConfig) (*StripeWebhookHandler, error) {
	if cfg.ClientFactory == nil {
		return nil, ErrNilClientFactory
	}
	if cfg.WebhookHandler == nil {
		return nil, ErrNilWebhookHandler
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &StripeWebhookHandler{
		clientFactory:  cfg.ClientFactory,
		webhookHandler: cfg.WebhookHandler,
		logger:         logger,
	}, nil
}

// HandleStripeWebhook processes incoming Stripe webhook events.
// It extracts the tenant context, retrieves the tenant-specific webhook secret,
// validates the Stripe signature, translates the event to a WebhookRequest,
// and calls UpdatePaymentOrder through the existing webhook handler pipeline.
func (h *StripeWebhookHandler) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	defer func() { _ = r.Body.Close() }()

	// Read body with size limit
	body, err := io.ReadAll(io.LimitReader(r.Body, StripeWebhookMaxBodySize+1))
	if err != nil {
		h.logger.Error("failed to read stripe webhook body", "error", err)
		h.writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if int64(len(body)) > StripeWebhookMaxBodySize {
		h.writeErrorResponse(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	// Get tenant-specific Stripe client (contains the webhook secret)
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		h.logger.Warn("missing tenant context for stripe webhook")
		h.writeErrorResponse(w, http.StatusBadRequest, ErrMissingTenantContext.Error())
		return
	}

	client, err := h.clientFactory.NewClient(ctx)
	if err != nil {
		h.logger.Error("failed to get stripe client for tenant",
			"tenant_id", tenantID.String(),
			"error", err,
		)
		h.writeErrorResponse(w, http.StatusInternalServerError, "failed to resolve tenant configuration")
		return
	}

	if client.WebhookEndpointSecret == "" {
		h.logger.Error("no webhook secret for tenant", "tenant_id", tenantID.String())
		h.writeErrorResponse(w, http.StatusInternalServerError, ErrNoWebhookSecret.Error())
		return
	}

	// Create adapter with tenant-specific secret and parse the webhook
	adapter, err := stripe.NewWebhookAdapter(client.WebhookEndpointSecret)
	if err != nil {
		h.logger.Error("failed to create stripe webhook adapter", "error", err)
		h.writeErrorResponse(w, http.StatusInternalServerError, "internal error")
		return
	}

	parsed, err := adapter.ParseWebhook(r, body)
	if err != nil {
		switch {
		case errors.Is(err, stripe.ErrWebhookInvalidSignature):
			h.logger.Warn("invalid stripe webhook signature", "tenant_id", tenantID.String())
			h.writeErrorResponse(w, http.StatusUnauthorized, "invalid webhook signature")
		case errors.Is(err, stripe.ErrWebhookMissingSignature):
			h.logger.Warn("missing stripe webhook signature")
			h.writeErrorResponse(w, http.StatusUnauthorized, "missing Stripe-Signature header")
		case errors.Is(err, stripe.ErrWebhookUnsupportedEvent):
			// Acknowledge unsupported events to prevent Stripe retries
			h.logger.Debug("unsupported stripe event type", "tenant_id", tenantID.String())
			h.writeSuccessResponse(w, "event type not handled")
		default:
			h.logger.Error("failed to parse stripe webhook", "error", err, "tenant_id", tenantID.String())
			h.writeErrorResponse(w, http.StatusBadRequest, "failed to parse webhook")
		}
		return
	}

	// Convert ParsedWebhookEvent to WebhookRequest
	webhookReq := WebhookRequest{
		GatewayReferenceID: parsed.GatewayReferenceID,
		PaymentOrderID:     parsed.PaymentOrderID,
		Status:             parsed.Status,
		Message:            parsed.Message,
		Timestamp:          parsed.Timestamp,
	}

	// Map gateway status
	gatewayStatus, err := mapGatewayStatus(webhookReq.Status)
	if err != nil {
		h.logger.Warn("invalid gateway status from stripe adapter",
			"status", webhookReq.Status,
			"tenant_id", tenantID.String(),
		)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidGatewayStatus.Error())
		return
	}

	idempotencyKey := h.webhookHandler.generateIdempotencyKey(webhookReq)

	h.logger.Info("processing stripe webhook",
		"gateway_reference_id", webhookReq.GatewayReferenceID,
		"payment_order_id", webhookReq.PaymentOrderID,
		"status", webhookReq.Status,
		"tenant_id", tenantID.String(),
	)

	h.webhookHandler.processWebhookRequest(ctx, w, webhookReq, gatewayStatus, idempotencyKey)
}

func (h *StripeWebhookHandler) writeSuccessResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := WebhookResponse{Acknowledged: true, Message: message}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode success response", "error", err)
	}
}

func (h *StripeWebhookHandler) writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := WebhookResponse{Acknowledged: false, Error: message}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode error response", "error", err)
	}
}
