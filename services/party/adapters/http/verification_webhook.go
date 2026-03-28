// Package http provides the HTTP adapter for receiving KYC/AML verification webhooks.
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

	"github.com/meridianhub/meridian/services/party/service"
	"github.com/meridianhub/meridian/services/party/verification"
)

// Webhook handler errors.
var (
	ErrInvalidSignature          = errors.New("invalid webhook signature")
	ErrMissingSignature          = errors.New("missing X-Webhook-Signature header")
	ErrInvalidRequestBody        = errors.New("invalid request body")
	ErrMissingVerificationID     = errors.New("verification_id is required")
	ErrInvalidVerificationStatus = errors.New("invalid verification status")
	ErrVerificationNotFound      = errors.New("verification not found")
	ErrVerificationServiceError  = errors.New("verification service error")
	ErrNilVerificationService    = errors.New("verification service cannot be nil")
	ErrEmptyHMACSecret           = errors.New("HMAC secret cannot be empty")
	ErrVerificationAlreadyDone   = errors.New("verification already in terminal state")
	ErrEmptyProviderSecret       = errors.New("HMAC secret for provider cannot be empty")
)

// WebhookSignatureHeader is the HTTP header containing the HMAC signature.
const WebhookSignatureHeader = "X-Webhook-Signature"

// ProviderHeader is the HTTP header containing the provider name (optional, can also be in URL).
const ProviderHeader = "X-Provider"

// DefaultWebhookMaxAge is the default maximum age for webhook timestamps to prevent replay attacks.
const DefaultWebhookMaxAge = 5 * time.Minute

// DefaultClockDriftTolerance is the maximum allowed future timestamp to account for clock drift.
const DefaultClockDriftTolerance = 30 * time.Second

// VerificationUpdater defines the interface for updating verification status.
type VerificationUpdater interface {
	UpdateVerification(ctx context.Context, req service.UpdateVerificationRequest) error
}

// VerificationWebhookRequest represents the JSON payload from a verification provider webhook.
type VerificationWebhookRequest struct {
	// VerificationID is the provider's external verification ID.
	VerificationID string `json:"verification_id"`
	// Status is the verification status from the provider (APPROVED, REJECTED, MANUAL_REVIEW).
	Status string `json:"status"`
	// RiskScore is the risk assessment score (0.0 to 1.0).
	RiskScore *float64 `json:"risk_score,omitempty"`
	// Reason provides additional context for the verification result.
	Reason string `json:"reason,omitempty"`
	// Timestamp is when the provider completed the verification.
	Timestamp time.Time `json:"timestamp"`
	// Metadata contains provider-specific additional data.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// VerificationWebhookResponse represents the JSON response to the provider.
type VerificationWebhookResponse struct {
	// Acknowledged indicates the webhook was successfully processed.
	Acknowledged bool `json:"acknowledged"`
	// Message provides additional context.
	Message string `json:"message,omitempty"`
	// Error provides error details when acknowledged is false.
	Error string `json:"error,omitempty"`
}

// VerificationWebhookHandler handles incoming KYC/AML verification webhooks.
type VerificationWebhookHandler struct {
	verificationService VerificationUpdater
	hmacSecrets         map[string][]byte // provider -> secret mapping
	logger              *slog.Logger
}

// VerificationWebhookHandlerConfig contains configuration for creating a VerificationWebhookHandler.
type VerificationWebhookHandlerConfig struct {
	VerificationService VerificationUpdater
	// HMACSecrets maps provider names to their webhook secrets.
	// If only one provider is used, use "default" as the key.
	HMACSecrets map[string][]byte
	Logger      *slog.Logger
}

// NewVerificationWebhookHandler creates a new VerificationWebhookHandler.
func NewVerificationWebhookHandler(cfg VerificationWebhookHandlerConfig) (*VerificationWebhookHandler, error) {
	if cfg.VerificationService == nil {
		return nil, ErrNilVerificationService
	}
	if len(cfg.HMACSecrets) == 0 {
		return nil, ErrEmptyHMACSecret
	}
	// Validate all secrets are non-empty
	for _, secret := range cfg.HMACSecrets {
		if len(secret) == 0 {
			return nil, ErrEmptyProviderSecret
		}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &VerificationWebhookHandler{
		verificationService: cfg.VerificationService,
		hmacSecrets:         cfg.HMACSecrets,
		logger:              logger,
	}, nil
}

// HandleWebhook processes incoming verification provider webhooks.
// It validates the HMAC signature, parses the request, and calls UpdateVerification.
// The provider name is extracted from the URL path: /webhooks/verification/{provider}
func (h *VerificationWebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider := resolveWebhookProvider(r)

	secret, ok := h.resolveProviderSecret(provider)
	if !ok {
		h.writeErrorResponse(w, http.StatusUnauthorized, "unknown provider")
		return
	}

	body, ok := h.readAndAuthenticateBody(w, r, provider, secret)
	if !ok {
		return
	}

	webhookReq, verStatus, ok := h.parseAndValidatePayload(w, body, provider)
	if !ok {
		return
	}

	if !h.checkTimestampFreshness(w, webhookReq, provider) {
		return
	}

	h.processVerificationUpdate(r.Context(), w, provider, webhookReq, verStatus)
}

// resolveWebhookProvider extracts the provider from the URL path or header, defaulting to "default".
func resolveWebhookProvider(r *http.Request) string {
	provider := extractProvider(r.URL.Path)
	if provider == "" {
		provider = r.Header.Get(ProviderHeader)
	}
	if provider == "" {
		provider = "default"
	}
	return strings.ToLower(provider)
}

// resolveProviderSecret looks up the HMAC secret for a provider, falling back to "default".
func (h *VerificationWebhookHandler) resolveProviderSecret(provider string) ([]byte, bool) {
	secret, ok := h.hmacSecrets[provider]
	if !ok {
		secret, ok = h.hmacSecrets["default"]
		if !ok {
			h.logger.Warn("no HMAC secret configured for provider", "provider", provider)
			return nil, false
		}
	}
	return secret, true
}

// readAndAuthenticateBody reads the request body and validates the HMAC signature.
// Returns the body bytes and true on success, or writes an error response and returns false.
func (h *VerificationWebhookHandler) readAndAuthenticateBody(w http.ResponseWriter, r *http.Request, provider string, secret []byte) ([]byte, bool) {
	defer func() { _ = r.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		h.logger.Error("failed to read request body", "error", err)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return nil, false
	}

	signature := r.Header.Get(WebhookSignatureHeader)
	if signature == "" {
		h.logger.Warn("missing webhook signature", "provider", provider)
		h.writeErrorResponse(w, http.StatusUnauthorized, ErrMissingSignature.Error())
		return nil, false
	}

	if !h.validateSignature(body, signature, secret) {
		h.logger.Warn("invalid webhook signature", "provider", provider)
		h.writeErrorResponse(w, http.StatusUnauthorized, ErrInvalidSignature.Error())
		return nil, false
	}

	return body, true
}

// parseAndValidatePayload unmarshals the webhook body and validates required fields.
func (h *VerificationWebhookHandler) parseAndValidatePayload(w http.ResponseWriter, body []byte, provider string) (*VerificationWebhookRequest, verification.Status, bool) {
	var webhookReq VerificationWebhookRequest
	if err := json.Unmarshal(body, &webhookReq); err != nil {
		h.logger.Error("failed to parse webhook request", "error", err)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidRequestBody.Error())
		return nil, "", false
	}

	if webhookReq.VerificationID == "" {
		h.logger.Warn("missing verification_id in webhook", "provider", provider)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrMissingVerificationID.Error())
		return nil, "", false
	}

	status := verification.Status(strings.ToUpper(webhookReq.Status))
	if !status.IsValid() {
		h.logger.Warn("invalid verification status",
			"provider", provider,
			"status", webhookReq.Status)
		h.writeErrorResponse(w, http.StatusBadRequest, ErrInvalidVerificationStatus.Error())
		return nil, "", false
	}

	return &webhookReq, status, true
}

// checkTimestampFreshness validates the webhook timestamp to prevent replay attacks.
// Returns true if the timestamp is valid or zero, false if it should be rejected.
func (h *VerificationWebhookHandler) checkTimestampFreshness(w http.ResponseWriter, webhookReq *VerificationWebhookRequest, provider string) bool {
	if webhookReq.Timestamp.IsZero() {
		return true
	}

	now := time.Now()
	age := now.Sub(webhookReq.Timestamp)

	if age > DefaultWebhookMaxAge {
		h.logger.Warn("webhook timestamp too old",
			"provider", provider,
			"verification_id", webhookReq.VerificationID,
			"timestamp", webhookReq.Timestamp,
			"age", age)
		h.writeErrorResponse(w, http.StatusBadRequest, "webhook timestamp expired")
		return false
	}

	if age < -DefaultClockDriftTolerance {
		h.logger.Warn("webhook timestamp too far in the future",
			"provider", provider,
			"verification_id", webhookReq.VerificationID,
			"timestamp", webhookReq.Timestamp,
			"offset", -age)
		h.writeErrorResponse(w, http.StatusBadRequest, "webhook timestamp is in the future")
		return false
	}

	return true
}

// processVerificationUpdate builds the update request and calls the verification service.
func (h *VerificationWebhookHandler) processVerificationUpdate(ctx context.Context, w http.ResponseWriter, provider string, webhookReq *VerificationWebhookRequest, status verification.Status) {
	completedAt := webhookReq.Timestamp
	if completedAt.IsZero() {
		completedAt = time.Now()
	}

	var reason *string
	if webhookReq.Reason != "" {
		reason = &webhookReq.Reason
	}

	updateReq := service.UpdateVerificationRequest{
		ProviderVerificationID: webhookReq.VerificationID,
		Status:                 string(status),
		RiskScore:              webhookReq.RiskScore,
		Reason:                 reason,
		CompletedAt:            &completedAt,
		Metadata:               webhookReq.Metadata,
	}

	h.logger.Info("processing verification webhook",
		"provider", provider,
		"verification_id", webhookReq.VerificationID,
		"status", webhookReq.Status)

	err := h.verificationService.UpdateVerification(ctx, updateReq)
	if err != nil {
		if errors.Is(err, service.ErrVerificationAlreadyCompleted) {
			h.logger.Info("webhook already processed (idempotent)",
				"provider", provider,
				"verification_id", webhookReq.VerificationID)
			h.writeSuccessResponse(w, "webhook already processed")
			return
		}

		if strings.Contains(err.Error(), "not found") {
			h.logger.Warn("verification not found",
				"provider", provider,
				"verification_id", webhookReq.VerificationID)
			h.writeErrorResponse(w, http.StatusNotFound, ErrVerificationNotFound.Error())
			return
		}

		h.logger.Error("failed to update verification",
			"error", err,
			"provider", provider,
			"verification_id", webhookReq.VerificationID)
		h.writeErrorResponse(w, http.StatusInternalServerError, ErrVerificationServiceError.Error())
		return
	}

	h.logger.Info("webhook processed successfully",
		"provider", provider,
		"verification_id", webhookReq.VerificationID,
		"status", webhookReq.Status)

	h.writeSuccessResponse(w, "webhook processed successfully")
}

// validateSignature validates the HMAC-SHA256 signature of the request body.
// The signature is expected to be a hex-encoded HMAC-SHA256 hash.
// Uses constant-time comparison to prevent timing attacks.
func (h *VerificationWebhookHandler) validateSignature(body []byte, signature string, secret []byte) bool {
	// Decode the hex-encoded signature from the request
	providedMAC, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	// Compute the expected MAC
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	expectedMAC := mac.Sum(nil)

	// Use constant-time comparison to prevent timing attacks
	return hmac.Equal(providedMAC, expectedMAC)
}

// extractProvider extracts the provider name from the URL path.
// Expected path format: /webhooks/verification/{provider}
func extractProvider(path string) string {
	// Normalize path
	path = strings.TrimSuffix(path, "/")

	// Split and find provider segment
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "verification" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// writeSuccessResponse writes a success JSON response.
func (h *VerificationWebhookHandler) writeSuccessResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := VerificationWebhookResponse{
		Acknowledged: true,
		Message:      message,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode success response", "error", err)
	}
}

// writeErrorResponse writes an error JSON response.
func (h *VerificationWebhookHandler) writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := VerificationWebhookResponse{
		Acknowledged: false,
		Error:        message,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to encode error response", "error", err)
	}
}

// GenerateWebhookSignature generates an HMAC-SHA256 signature for a request body.
// This is useful for testing and for providers that need to sign requests.
func GenerateWebhookSignature(body []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// NewVerificationWebhookRequest creates a VerificationWebhookRequest with a generated timestamp.
// Useful for testing.
func NewVerificationWebhookRequest(verificationID, status string, riskScore *float64, reason string) VerificationWebhookRequest {
	return VerificationWebhookRequest{
		VerificationID: verificationID,
		Status:         status,
		RiskScore:      riskScore,
		Reason:         reason,
		Timestamp:      time.Now().UTC(),
	}
}
