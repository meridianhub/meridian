// Package http provides the HTTP adapter for receiving payment gateway webhooks.
package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
)

// Webhook handler errors.
var (
	ErrInvalidSignature       = errors.New("invalid webhook signature")
	ErrMissingSignature       = errors.New("missing X-Webhook-Signature header")
	ErrInvalidRequestBody     = errors.New("invalid request body")
	ErrMissingReferenceID     = errors.New("gateway_reference_id is required")
	ErrInvalidGatewayStatus   = errors.New("invalid gateway_status")
	ErrTimestampExpired       = errors.New("webhook timestamp expired")
	ErrTimestampFuture        = errors.New("webhook timestamp is in the future")
	ErrPaymentOrderService    = errors.New("payment order service error")
	ErrNilPaymentOrderService = errors.New("payment order service cannot be nil")
	ErrEmptyHMACSecret        = errors.New("HMAC secret cannot be empty")
)

// WebhookSignatureHeader is the HTTP header containing the HMAC signature.
const WebhookSignatureHeader = "X-Webhook-Signature"

// IdempotencyKeyHeader is the HTTP header containing the gateway-provided idempotency key.
const IdempotencyKeyHeader = "X-Idempotency-Key"

// DefaultWebhookMaxAge is the default maximum age for webhook timestamps to prevent replay attacks.
const DefaultWebhookMaxAge = 5 * time.Minute

// DefaultClockDriftTolerance is the maximum allowed future timestamp to account for clock drift.
const DefaultClockDriftTolerance = 30 * time.Second

// PaymentOrderServiceClient defines the interface for calling the PaymentOrder gRPC service.
type PaymentOrderServiceClient interface {
	UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error)
}

// WebhookRequest represents the JSON payload from the payment gateway webhook.
type WebhookRequest struct {
	// GatewayReferenceID is the external ID from the payment gateway.
	GatewayReferenceID string `json:"gateway_reference_id"`
	// PaymentOrderID is optionally included by some gateways for direct lookup.
	PaymentOrderID string `json:"payment_order_id,omitempty"`
	// Status is the payment status from the gateway (Settled, Rejected, Pending).
	Status string `json:"status"`
	// Message is optional details from the gateway.
	Message string `json:"message,omitempty"`
	// Timestamp is when the gateway processed the payment.
	Timestamp time.Time `json:"timestamp"`
}

// WebhookResponse represents the JSON response to the gateway.
type WebhookResponse struct {
	// Acknowledged indicates the webhook was successfully processed.
	Acknowledged bool `json:"acknowledged"`
	// Message provides additional context.
	Message string `json:"message,omitempty"`
	// Error provides error details when acknowledged is false.
	Error string `json:"error,omitempty"`
}

// WebhookHandler handles incoming payment gateway webhooks.
type WebhookHandler struct {
	paymentOrderService PaymentOrderServiceClient
	hmacSecret          []byte
	logger              *slog.Logger
}

// WebhookHandlerConfig contains configuration for creating a WebhookHandler.
type WebhookHandlerConfig struct {
	PaymentOrderService PaymentOrderServiceClient
	HMACSecret          []byte
	Logger              *slog.Logger
}

// NewWebhookHandler creates a new WebhookHandler.
func NewWebhookHandler(cfg WebhookHandlerConfig) (*WebhookHandler, error) {
	if cfg.PaymentOrderService == nil {
		return nil, ErrNilPaymentOrderService
	}
	if len(cfg.HMACSecret) == 0 {
		return nil, ErrEmptyHMACSecret
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{
		paymentOrderService: cfg.PaymentOrderService,
		hmacSecret:          cfg.HMACSecret,
		logger:              logger,
	}, nil
}

// HandleWebhook processes incoming payment gateway webhooks.
// It validates the HMAC signature, parses the request, and calls UpdatePaymentOrder.
func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	defer func() { _ = r.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		h.logger.Error("failed to read request body", "error", err)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return
	}

	if err := h.validateWebhookSignature(w, r, body); err != nil {
		return
	}

	webhookReq, err := h.parseAndValidateWebhookRequest(w, body)
	if err != nil {
		return
	}

	if err := h.validateWebhookTimestamp(w, webhookReq.Timestamp); err != nil {
		return
	}

	gatewayStatus, err := mapGatewayStatus(webhookReq.Status)
	if err != nil {
		h.logger.Warn("invalid gateway status", "status", webhookReq.Status)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidGatewayStatus.Error())
		return
	}

	idempotencyKey := r.Header.Get(IdempotencyKeyHeader)
	if idempotencyKey == "" {
		idempotencyKey = h.generateIdempotencyKey(webhookReq)
	}

	h.processWebhookRequest(r.Context(), w, webhookReq, gatewayStatus, idempotencyKey)
}

// validateWebhookSignature validates the HMAC signature header and writes error responses if invalid.
func (h *WebhookHandler) validateWebhookSignature(w http.ResponseWriter, r *http.Request, body []byte) error {
	signature := r.Header.Get(WebhookSignatureHeader)
	if signature == "" {
		h.logger.Warn("missing webhook signature")
		h.writeErrorResponse(w, http.StatusUnauthorized, ErrMissingSignature.Error())
		return ErrMissingSignature
	}
	if !h.validateSignature(body, signature) {
		h.logger.Warn("invalid webhook signature")
		h.writeErrorResponse(w, http.StatusUnauthorized, ErrInvalidSignature.Error())
		return ErrInvalidSignature
	}
	return nil
}

// parseAndValidateWebhookRequest parses the JSON body and validates required fields.
func (h *WebhookHandler) parseAndValidateWebhookRequest(w http.ResponseWriter, body []byte) (WebhookRequest, error) {
	var webhookReq WebhookRequest
	if err := json.Unmarshal(body, &webhookReq); err != nil {
		h.logger.Error("failed to parse webhook request", "error", err)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return WebhookRequest{}, err
	}
	if webhookReq.GatewayReferenceID == "" && webhookReq.PaymentOrderID == "" {
		h.logger.Warn("missing reference ID in webhook")
		h.writeErrorResponse(w, http.StatusBadRequest, ErrMissingReferenceID.Error())
		return WebhookRequest{}, ErrMissingReferenceID
	}
	return webhookReq, nil
}

// validateWebhookTimestamp validates timestamp freshness to prevent replay attacks.
func (h *WebhookHandler) validateWebhookTimestamp(w http.ResponseWriter, timestamp time.Time) error {
	if timestamp.IsZero() {
		return nil
	}
	age := time.Since(timestamp)
	if age > DefaultWebhookMaxAge {
		h.logger.Warn("webhook timestamp too old",
			"timestamp", timestamp,
			"age", age)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrTimestampExpired.Error())
		return ErrTimestampExpired
	}
	if age < -DefaultClockDriftTolerance {
		h.logger.Warn("webhook timestamp too far in the future",
			"timestamp", timestamp,
			"offset", -age)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrTimestampFuture.Error())
		return ErrTimestampFuture
	}
	return nil
}

// processWebhookRequest builds and sends the UpdatePaymentOrder request.
// This method is shared between the generic webhook handler and gateway-specific
// handlers (e.g., Stripe) that translate their events into WebhookRequest.
func (h *WebhookHandler) processWebhookRequest(ctx context.Context, w http.ResponseWriter, webhookReq WebhookRequest, gatewayStatus pb.GatewayStatus, idempotencyKey string) {
	updateReq := &pb.UpdatePaymentOrderRequest{
		GatewayReferenceId: webhookReq.GatewayReferenceID,
		PaymentOrderId:     webhookReq.PaymentOrderID,
		GatewayStatus:      gatewayStatus,
		GatewayMessage:     webhookReq.Message,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: idempotencyKey,
		},
	}

	h.logger.Info("processing webhook",
		"gateway_reference_id", webhookReq.GatewayReferenceID,
		"payment_order_id", webhookReq.PaymentOrderID,
		"status", webhookReq.Status,
		"idempotency_key", idempotencyKey)

	resp, err := h.paymentOrderService.UpdatePaymentOrder(ctx, updateReq)
	if err != nil {
		h.logger.Error("failed to update payment order",
			"error", err,
			"gateway_reference_id", webhookReq.GatewayReferenceID)
		h.writeErrorResponse(w, http.StatusInternalServerError, ErrPaymentOrderService.Error())
		return
	}

	if resp != nil && resp.PaymentOrder != nil {
		h.logger.Info("webhook processed successfully",
			"payment_order_id", resp.PaymentOrder.PaymentOrderId,
			"status", resp.PaymentOrder.Status.String())
	} else {
		h.logger.Info("webhook processed successfully")
	}

	h.writeSuccessResponse(w, "webhook processed successfully")
}

// validateSignature validates the HMAC-SHA256 signature of the request body.
// The signature is expected to be a hex-encoded HMAC-SHA256 hash.
func (h *WebhookHandler) validateSignature(body []byte, signature string) bool {
	// Decode the hex-encoded signature from the request
	providedMAC, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	// Compute the expected MAC
	mac := hmac.New(sha256.New, h.hmacSecret)
	mac.Write(body)
	expectedMAC := mac.Sum(nil)

	// Use constant-time comparison on raw bytes
	return hmac.Equal(providedMAC, expectedMAC)
}

// generateIdempotencyKey creates a deterministic idempotency key from webhook data.
// This ensures duplicate webhook deliveries are handled correctly.
func (h *WebhookHandler) generateIdempotencyKey(req WebhookRequest) string {
	// Use a combination of gateway reference ID, status, and timestamp
	// to create a unique, deterministic key
	data := req.GatewayReferenceID + ":" + req.Status + ":" + req.Timestamp.Format(time.RFC3339)
	hash := sha256.Sum256([]byte(data))
	return "webhook:" + hex.EncodeToString(hash[:16]) // Use first 16 bytes
}

// mapGatewayStatus converts the gateway status string to proto enum.
// Status comparison is case-insensitive.
func mapGatewayStatus(status string) (pb.GatewayStatus, error) {
	switch strings.ToUpper(status) {
	case "SETTLED":
		return pb.GatewayStatus_GATEWAY_STATUS_SETTLED, nil
	case "REJECTED":
		return pb.GatewayStatus_GATEWAY_STATUS_REJECTED, nil
	case "PENDING":
		return pb.GatewayStatus_GATEWAY_STATUS_PENDING, nil
	case "REFUNDED":
		return pb.GatewayStatus_GATEWAY_STATUS_REFUNDED, nil
	case "DISPUTED":
		return pb.GatewayStatus_GATEWAY_STATUS_DISPUTED, nil
	default:
		return pb.GatewayStatus_GATEWAY_STATUS_UNSPECIFIED, ErrInvalidGatewayStatus
	}
}

// writeSuccessResponse writes a success JSON response.
func (h *WebhookHandler) writeSuccessResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := WebhookResponse{
		Acknowledged: true,
		Message:      message,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode success response", "error", err)
	}
}

// writeErrorResponse writes an error JSON response.
func (h *WebhookHandler) writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := WebhookResponse{
		Acknowledged: false,
		Error:        message,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode error response", "error", err)
	}
}

// GenerateWebhookSignature generates an HMAC-SHA256 signature for a request body.
// This is useful for testing and for gateways that need to sign requests.
func GenerateWebhookSignature(body []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// NewWebhookRequest creates a WebhookRequest with a generated timestamp.
// Useful for testing.
func NewWebhookRequest(gatewayRefID, paymentOrderID, status, message string) WebhookRequest {
	return WebhookRequest{
		GatewayReferenceID: gatewayRefID,
		PaymentOrderID:     paymentOrderID,
		Status:             status,
		Message:            message,
		Timestamp:          time.Now().UTC(),
	}
}

// RequestID generates a unique request ID for logging.
func RequestID() string {
	return uuid.New().String()
}
