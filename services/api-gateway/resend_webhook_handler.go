package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/email"
)

// Sentinel errors for webhook verification.
var (
	ErrMissingSignatureHeaders     = errors.New("webhook: missing svix-id, svix-timestamp, or svix-signature header")
	ErrInvalidTimestamp            = errors.New("webhook: invalid svix-timestamp value")
	ErrTimestampOutsideTolerance   = errors.New("webhook: timestamp outside 5-minute tolerance window")
	ErrSignatureVerificationFailed = errors.New("webhook: no matching svix signature found")
	ErrInvalidWebhookSecret        = errors.New("webhook: cannot decode webhook secret")
)

// resendWebhookPayload is the outer envelope of a Resend webhook event.
type resendWebhookPayload struct {
	Type      string         `json:"type"`
	CreatedAt string         `json:"created_at"`
	Data      map[string]any `json:"data"`
}

// ResendWebhookHandler handles POST /api/v1/webhooks/resend.
//
// Resend uses Svix for webhook delivery. Each request carries three headers:
// Svix-Id, Svix-Timestamp, and Svix-Signature. The handler verifies the
// HMAC-SHA256 signature before processing the payload. No auth middleware
// is applied to this route — the signature IS the authentication.
type ResendWebhookHandler struct {
	auditRepo  email.AuditRepository
	webhookKey string
	logger     *slog.Logger
}

// NewResendWebhookHandler creates a webhook handler.
// webhookKey must be the raw secret from Resend (format: "whsec_<base64>").
func NewResendWebhookHandler(auditRepo email.AuditRepository, webhookKey string, logger *slog.Logger) *ResendWebhookHandler {
	return &ResendWebhookHandler{
		auditRepo:  auditRepo,
		webhookKey: webhookKey,
		logger:     logger,
	}
}

// readAndVerifyWebhook reads the request body and verifies the Svix signature.
// Returns the raw body or an error with the appropriate HTTP status code.
func (h *ResendWebhookHandler) readAndVerifyWebhook(r *http.Request) ([]byte, int, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if err := r.Body.Close(); err != nil {
		h.logger.WarnContext(r.Context(), "resend webhook: failed to close request body", "error", err)
	}
	if err := verifySvixSignature(body, r.Header, h.webhookKey); err != nil {
		return nil, http.StatusUnauthorized, err
	}
	return body, 0, nil
}

// ServeHTTP processes a Resend webhook delivery status callback.
func (h *ResendWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, status, err := h.readAndVerifyWebhook(r)
	if err != nil {
		h.logger.WarnContext(r.Context(), "resend webhook: request rejected", "error", err)
		http.Error(w, http.StatusText(status), status)
		return
	}

	var payload resendWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.WarnContext(r.Context(), "resend webhook: failed to parse payload", "error", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	auditStatus, ok := resendEventToAuditStatus(payload.Type)
	if !ok {
		// Unknown event types are acknowledged without processing (Resend may
		// add new event types in future; silently ignore to avoid retries).
		h.logger.DebugContext(r.Context(), "resend webhook: unknown event type, ignoring", "type", payload.Type)
		w.WriteHeader(http.StatusOK)
		return
	}

	providerID, _ := payload.Data["email_id"].(string)
	if providerID == "" {
		h.logger.WarnContext(r.Context(), "resend webhook: missing email_id in payload", "type", payload.Type)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if err := h.auditRepo.RecordByProviderID(r.Context(), providerID, auditStatus, payload.Data); err != nil {
		if errors.Is(err, email.ErrAuditEntryNotFound) {
			// Not found: may be a replay of an old event or a race with cleanup.
			h.logger.WarnContext(r.Context(), "resend webhook: no audit entry for provider ID",
				"provider_id", providerID, "type", payload.Type)
			w.WriteHeader(http.StatusOK)
			return
		}
		h.logger.ErrorContext(r.Context(), "resend webhook: failed to record audit entry",
			"provider_id", providerID, "type", payload.Type, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.logger.InfoContext(r.Context(), "resend webhook: recorded delivery status",
		"provider_id", providerID, "type", payload.Type, "status", auditStatus)

	w.WriteHeader(http.StatusOK)
}

// resendEventToAuditStatus maps a Resend event type to an AuditStatus.
// Returns (status, true) for known event types, ("", false) for unknown ones.
func resendEventToAuditStatus(eventType string) (email.AuditStatus, bool) {
	switch eventType {
	case "email.delivered":
		return email.AuditStatusDelivered, true
	case "email.bounced":
		return email.AuditStatusBounced, true
	case "email.complained":
		return email.AuditStatusComplained, true
	default:
		return "", false
	}
}

// verifySvixSignature validates the Svix HMAC-SHA256 webhook signature.
//
// Svix signature verification algorithm:
//  1. Read Svix-Id, Svix-Timestamp, Svix-Signature headers.
//  2. Reject if timestamp is outside ±5 minutes to prevent replay attacks.
//  3. Compute HMAC-SHA256("{msgId}.{timestamp}.{body}", key).
//  4. key = base64.StdEncoding.Decode(secret after stripping "whsec_" prefix).
//  5. Compare against each "v1,<base64sig>" token in Svix-Signature.
func verifySvixSignature(payload []byte, headers http.Header, secret string) error {
	msgID := headers.Get("svix-id")
	msgTimestamp := headers.Get("svix-timestamp")
	msgSignature := headers.Get("svix-signature")

	if msgID == "" || msgTimestamp == "" || msgSignature == "" {
		return ErrMissingSignatureHeaders
	}

	ts, err := strconv.ParseInt(msgTimestamp, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}
	delta := time.Now().Unix() - ts
	if delta < -300 || delta > 300 {
		return ErrTimestampOutsideTolerance
	}

	keyB64 := strings.TrimPrefix(secret, "whsec_")
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWebhookSecret, err)
	}

	toSign := fmt.Sprintf("%s.%s.%s", msgID, msgTimestamp, string(payload))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	computed := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	for _, sig := range strings.Fields(msgSignature) {
		parts := strings.SplitN(sig, ",", 2)
		if len(parts) != 2 || parts[0] != "v1" {
			continue
		}
		if hmac.Equal([]byte(parts[1]), []byte(computed)) {
			return nil
		}
	}

	return ErrSignatureVerificationFailed
}

// WithResendWebhookHandler registers the Resend delivery status webhook handler.
func WithResendWebhookHandler(handler *ResendWebhookHandler) ServerOption {
	return func(s *Server) {
		s.resendWebhookHandler = handler
	}
}
