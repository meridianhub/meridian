// Package http provides the HTTP adapter for receiving KYC/AML verification webhooks.
package http

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/party/verification"
)

// Stripe webhook adapter errors.
var (
	ErrStripeSignatureMissing = errors.New("missing Stripe-Signature header")
	ErrStripeSignatureInvalid = errors.New("invalid Stripe webhook signature")
	ErrStripeTimestampExpired = errors.New("stripe webhook timestamp expired")
	ErrStripeEventParseFailed = errors.New("failed to parse Stripe event")
	ErrStripeUnknownEventType = errors.New("unknown or irrelevant Stripe event type")
	ErrNilInnerHandler        = errors.New("inner webhook handler cannot be nil")
	ErrEmptyStripeSecret      = errors.New("stripe webhook secret cannot be empty")
	ErrEmptyInnerSecret       = errors.New("inner HMAC secret cannot be empty")
)

// stripeSignatureHeader is the HTTP header Stripe uses for webhook signatures.
const stripeSignatureHeader = "Stripe-Signature"

// stripeTimestampTolerance is the maximum age of a Stripe webhook timestamp.
const stripeTimestampTolerance = 5 * time.Minute

// stripeEvent represents the top-level Stripe webhook event payload.
type stripeEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data stripeEventData `json:"data"`
}

type stripeEventData struct {
	Object stripeVerificationSession `json:"object"`
}

type stripeVerificationSession struct {
	ID                     string            `json:"id"`
	Status                 string            `json:"status"`
	LastVerificationReport string            `json:"last_verification_report"`
	Metadata               map[string]string `json:"metadata"`
}

// StripeWebhookAdapter translates Stripe Identity webhooks into the generic
// VerificationWebhookHandler format.
type StripeWebhookAdapter struct {
	inner           *VerificationWebhookHandler
	stripeSecret    []byte
	innerHMACSecret []byte
	logger          *slog.Logger
}

// StripeWebhookAdapterConfig contains configuration for creating a StripeWebhookAdapter.
type StripeWebhookAdapterConfig struct {
	// InnerHandler is the generic webhook handler to delegate to after translation.
	InnerHandler *VerificationWebhookHandler
	// WebhookSecret is the Stripe webhook endpoint secret used to validate inbound signatures.
	WebhookSecret []byte
	// InnerHMACSecret is the HMAC secret used to sign the synthetic request sent to InnerHandler.
	// This must match the secret configured in InnerHandler for the "stripe" or "default" provider.
	InnerHMACSecret []byte
	Logger          *slog.Logger
}

// NewStripeWebhookAdapter creates a new StripeWebhookAdapter.
func NewStripeWebhookAdapter(cfg StripeWebhookAdapterConfig) (*StripeWebhookAdapter, error) {
	if cfg.InnerHandler == nil {
		return nil, ErrNilInnerHandler
	}
	if len(cfg.WebhookSecret) == 0 {
		return nil, ErrEmptyStripeSecret
	}
	if len(cfg.InnerHMACSecret) == 0 {
		return nil, ErrEmptyInnerSecret
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &StripeWebhookAdapter{
		inner:           cfg.InnerHandler,
		stripeSecret:    cfg.WebhookSecret,
		innerHMACSecret: cfg.InnerHMACSecret,
		logger:          logger,
	}, nil
}

// ServeHTTP implements http.Handler. It validates the Stripe signature, translates
// the event to the generic VerificationWebhookRequest, and delegates to the inner handler.
func (a *StripeWebhookAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeStripeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := a.readAndValidateStripeRequest(r)
	if err != nil {
		a.writeStripeValidationError(w, err)
		return
	}

	var event stripeEvent
	if err := json.Unmarshal(body, &event); err != nil {
		a.logger.Error("failed to parse Stripe event", "error", err)
		writeStripeErrorResponse(w, http.StatusBadRequest, ErrStripeEventParseFailed.Error())
		return
	}

	vStatus, ok := mapStripeEventToStatus(event.Type)
	if !ok {
		a.logger.Info("ignoring irrelevant Stripe event type", "event_type", event.Type)
		writeStripeSuccessResponse(w, "event type not relevant")
		return
	}

	a.delegateToInnerHandler(w, r, event, vStatus)
}

// readAndValidateStripeRequest reads the request body and validates the Stripe signature.
func (a *StripeWebhookAdapter) readAndValidateStripeRequest(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		a.logger.Error("failed to read Stripe webhook body", "error", err)
		return nil, ErrInvalidRequestBody
	}

	sigHeader := r.Header.Get(stripeSignatureHeader)
	if sigHeader == "" {
		a.logger.Warn("missing Stripe-Signature header")
		return nil, ErrStripeSignatureMissing
	}

	if err := validateStripeSignature(body, sigHeader, a.stripeSecret, stripeTimestampTolerance); err != nil {
		return nil, err
	}

	return body, nil
}

// writeStripeValidationError writes the appropriate HTTP error response for a validation error.
func (a *StripeWebhookAdapter) writeStripeValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequestBody):
		writeStripeErrorResponse(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrStripeSignatureMissing):
		writeStripeErrorResponse(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, ErrStripeTimestampExpired):
		a.logger.Warn("Stripe webhook timestamp expired")
		writeStripeErrorResponse(w, http.StatusBadRequest, err.Error())
	default:
		a.logger.Warn("invalid Stripe webhook signature", "error", err)
		writeStripeErrorResponse(w, http.StatusUnauthorized, ErrStripeSignatureInvalid.Error())
	}
}

// delegateToInnerHandler builds a synthetic request and forwards it to the inner webhook handler.
func (a *StripeWebhookAdapter) delegateToInnerHandler(w http.ResponseWriter, r *http.Request, event stripeEvent, vStatus verification.Status) {
	now := time.Now().UTC()
	webhookReq := VerificationWebhookRequest{
		VerificationID: event.Data.Object.ID,
		Status:         string(vStatus),
		Timestamp:      now,
		Metadata:       event.Data.Object.Metadata,
	}

	syntheticBody, err := json.Marshal(webhookReq)
	if err != nil {
		a.logger.Error("failed to marshal synthetic webhook request", "error", err)
		writeStripeErrorResponse(w, http.StatusInternalServerError, "internal error")
		return
	}

	sig := GenerateWebhookSignature(syntheticBody, a.innerHMACSecret)

	syntheticReq, err := http.NewRequestWithContext(
		r.Context(), http.MethodPost,
		"/webhooks/verification/stripe",
		bytes.NewReader(syntheticBody),
	)
	if err != nil {
		a.logger.Error("failed to create synthetic request", "error", err)
		writeStripeErrorResponse(w, http.StatusInternalServerError, "internal error")
		return
	}
	syntheticReq.Header.Set("Content-Type", "application/json")
	syntheticReq.Header.Set(WebhookSignatureHeader, sig)

	rr := httptest.NewRecorder()
	a.inner.HandleWebhook(rr, syntheticReq)

	for k, vs := range rr.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rr.Code)
	_, _ = w.Write(rr.Body.Bytes())
}

// parseStripeSignature parses the Stripe-Signature header.
// Format: "t=<timestamp>,v1=<sig1>,v1=<sig2>,..."
func parseStripeSignature(header string) (timestamp string, signatures []string, err error) {
	parts := strings.Split(header, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		idx := strings.IndexByte(part, '=')
		if idx < 0 {
			continue
		}
		key := part[:idx]
		val := part[idx+1:]
		switch key {
		case "t":
			timestamp = val
		case "v1":
			signatures = append(signatures, val)
		}
	}
	if timestamp == "" {
		return "", nil, ErrStripeSignatureInvalid
	}
	if len(signatures) == 0 {
		return "", nil, ErrStripeSignatureInvalid
	}
	return timestamp, signatures, nil
}

// validateStripeSignature validates the Stripe webhook signature.
func validateStripeSignature(payload []byte, sigHeader string, secret []byte, tolerance time.Duration) error {
	timestamp, signatures, err := parseStripeSignature(sigHeader)
	if err != nil {
		return ErrStripeSignatureInvalid
	}

	// Validate timestamp
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrStripeSignatureInvalid
	}
	webhookTime := time.Unix(ts, 0)
	age := time.Since(webhookTime)
	if age > tolerance {
		return ErrStripeTimestampExpired
	}

	// Compute expected signature: HMAC-SHA256(secret, timestamp + "." + payload)
	signedPayload := timestamp + "." + string(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signedPayload))
	expected := hex.EncodeToString(mac.Sum(nil))

	// Accept if any v1 signature matches (constant-time comparison)
	for _, sig := range signatures {
		expectedBytes, err := hex.DecodeString(expected)
		if err != nil {
			continue
		}
		sigBytes, err := hex.DecodeString(sig)
		if err != nil {
			continue
		}
		if hmac.Equal(expectedBytes, sigBytes) {
			return nil
		}
	}
	return ErrStripeSignatureInvalid
}

// mapStripeEventToStatus maps a Stripe Identity event type to a verification.Status.
// Returns (status, true) for known event types, ("", false) for unknown/irrelevant ones.
func mapStripeEventToStatus(eventType string) (verification.Status, bool) {
	switch eventType {
	case "identity.verification_session.verified":
		return verification.StatusApproved, true
	case "identity.verification_session.canceled":
		return verification.StatusRejected, true
	case "identity.verification_session.requires_input":
		return verification.StatusPending, true
	case "identity.verification_session.processing":
		return verification.StatusPending, true
	case "identity.verification_session.redacted":
		return verification.StatusRejected, true
	default:
		return "", false
	}
}

// writeStripeErrorResponse writes an error response in the standard webhook format.
func writeStripeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := VerificationWebhookResponse{
		Acknowledged: false,
		Error:        message,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeStripeSuccessResponse writes a success response in the standard webhook format.
func writeStripeSuccessResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := VerificationWebhookResponse{
		Acknowledged: true,
		Message:      message,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
