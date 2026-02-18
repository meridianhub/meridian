package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/party/service"
	"github.com/meridianhub/meridian/services/party/verification"
)

// generateStripeSignature generates a valid Stripe-Signature header for testing.
func generateStripeSignature(t *testing.T, payload []byte, secret []byte) string {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signedPayload := timestamp + "." + string(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%s,v1=%s", timestamp, sig)
}

// generateStripeSignatureWithTime generates a Stripe-Signature header with a specific timestamp.
func generateStripeSignatureWithTime(t *testing.T, payload []byte, secret []byte, ts time.Time) string {
	t.Helper()
	timestamp := strconv.FormatInt(ts.Unix(), 10)
	signedPayload := timestamp + "." + string(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%s,v1=%s", timestamp, sig)
}

// buildAdapter constructs a StripeWebhookAdapter with a mock inner handler for testing.
func buildAdapter(t *testing.T, stripeSecret, innerSecret []byte, svc VerificationUpdater) *StripeWebhookAdapter {
	t.Helper()
	if svc == nil {
		svc = &mockVerificationService{}
	}
	inner, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: svc,
		HMACSecrets:         map[string][]byte{"stripe": innerSecret},
	})
	require.NoError(t, err)

	adapter, err := NewStripeWebhookAdapter(StripeWebhookAdapterConfig{
		InnerHandler:    inner,
		WebhookSecret:   stripeSecret,
		InnerHMACSecret: innerSecret,
	})
	require.NoError(t, err)
	return adapter
}

// buildStripeBody creates a Stripe event JSON payload.
func buildStripeBody(t *testing.T, eventType, sessionID string, meta map[string]string) []byte {
	t.Helper()
	if meta == nil {
		meta = map[string]string{}
	}
	event := stripeEvent{
		ID:   "evt_test_123",
		Type: eventType,
		Data: stripeEventData{
			Object: stripeVerificationSession{
				ID:       sessionID,
				Status:   "verified",
				Metadata: meta,
			},
		},
	}
	body, err := json.Marshal(event)
	require.NoError(t, err)
	return body
}

// --- Constructor tests ---

func TestNewStripeWebhookAdapter_NilInnerHandler(t *testing.T) {
	_, err := NewStripeWebhookAdapter(StripeWebhookAdapterConfig{
		InnerHandler:    nil,
		WebhookSecret:   []byte("secret"),
		InnerHMACSecret: []byte("inner"),
	})
	assert.ErrorIs(t, err, ErrNilInnerHandler)
}

func TestNewStripeWebhookAdapter_EmptyStripeSecret(t *testing.T) {
	inner, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": []byte("s")},
	})
	require.NoError(t, err)

	_, err = NewStripeWebhookAdapter(StripeWebhookAdapterConfig{
		InnerHandler:    inner,
		WebhookSecret:   []byte{},
		InnerHMACSecret: []byte("inner"),
	})
	assert.ErrorIs(t, err, ErrEmptyStripeSecret)
}

func TestNewStripeWebhookAdapter_EmptyInnerSecret(t *testing.T) {
	inner, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": []byte("s")},
	})
	require.NoError(t, err)

	_, err = NewStripeWebhookAdapter(StripeWebhookAdapterConfig{
		InnerHandler:    inner,
		WebhookSecret:   []byte("stripe-secret"),
		InnerHMACSecret: []byte{},
	})
	assert.ErrorIs(t, err, ErrEmptyInnerSecret)
}

func TestNewStripeWebhookAdapter_ValidConfig(t *testing.T) {
	inner, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"stripe": []byte("inner-secret")},
	})
	require.NoError(t, err)

	adapter, err := NewStripeWebhookAdapter(StripeWebhookAdapterConfig{
		InnerHandler:    inner,
		WebhookSecret:   []byte("stripe-secret"),
		InnerHMACSecret: []byte("inner-secret"),
	})
	require.NoError(t, err)
	assert.NotNil(t, adapter)
}

// --- Signature validation tests ---

func TestStripeAdapter_MissingSignatureHeader(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	adapter := buildAdapter(t, stripeSecret, innerSecret, nil)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_test", nil)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	var resp VerificationWebhookResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrStripeSignatureMissing.Error(), resp.Error)
}

func TestStripeAdapter_InvalidSignature(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	adapter := buildAdapter(t, stripeSecret, innerSecret, nil)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_test", nil)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	// Sign with wrong secret
	req.Header.Set(stripeSignatureHeader, generateStripeSignature(t, body, []byte("wrong-secret")))
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	var resp VerificationWebhookResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrStripeSignatureInvalid.Error(), resp.Error)
}

func TestStripeAdapter_ExpiredTimestamp(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	adapter := buildAdapter(t, stripeSecret, innerSecret, nil)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_test", nil)
	// Timestamp 10 minutes in the past
	oldTime := time.Now().Add(-10 * time.Minute)
	sig := generateStripeSignatureWithTime(t, body, stripeSecret, oldTime)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	var resp VerificationWebhookResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrStripeTimestampExpired.Error(), resp.Error)
}

func TestStripeAdapter_ValidSignature_Accepted(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_abc123", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestStripeAdapter_MultipleV1Signatures_OneValid(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_test", nil)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	// Generate one invalid and one valid v1 sig
	badSig := "0000000000000000000000000000000000000000000000000000000000000000"
	signedPayload := timestamp + "." + string(body)
	mac := hmac.New(sha256.New, stripeSecret)
	mac.Write([]byte(signedPayload))
	goodSig := hex.EncodeToString(mac.Sum(nil))
	sigHeader := fmt.Sprintf("t=%s,v1=%s,v1=%s", timestamp, badSig, goodSig)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sigHeader)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestStripeAdapter_FutureTimestampWithinTolerance_Accepted(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_test", nil)
	// 10 seconds in the future — within the stripe tolerance (5 min max age check)
	// Note: tolerance is on age, not future drift; Stripe docs don't check future drift
	futureTime := time.Now().Add(10 * time.Second)
	sig := generateStripeSignatureWithTime(t, body, stripeSecret, futureTime)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	// Future timestamps within tolerance are accepted (age < tolerance)
	assert.Equal(t, http.StatusOK, rr.Code)
}

// --- Event type mapping tests ---

func TestMapStripeEventToStatus(t *testing.T) {
	tests := []struct {
		eventType      string
		expectedStatus verification.Status
		expectedOK     bool
	}{
		{"identity.verification_session.verified", verification.StatusApproved, true},
		{"identity.verification_session.canceled", verification.StatusRejected, true},
		{"identity.verification_session.requires_input", verification.StatusPending, true},
		{"identity.verification_session.processing", verification.StatusPending, true},
		{"identity.verification_session.redacted", verification.StatusRejected, true},
		{"identity.verification_session.created", "", false},
		{"payment_intent.succeeded", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			status, ok := mapStripeEventToStatus(tt.eventType)
			assert.Equal(t, tt.expectedOK, ok)
			assert.Equal(t, tt.expectedStatus, status)
		})
	}
}

func TestStripeAdapter_VerifiedEvent_MapsToApproved(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_verified_123", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, mock.calls, 1)
	assert.Equal(t, "vs_verified_123", mock.calls[0].ProviderVerificationID)
	assert.Equal(t, "APPROVED", mock.calls[0].Status)
}

func TestStripeAdapter_CanceledEvent_MapsToRejected(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.canceled", "vs_canceled_456", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, mock.calls, 1)
	assert.Equal(t, "vs_canceled_456", mock.calls[0].ProviderVerificationID)
	assert.Equal(t, "REJECTED", mock.calls[0].Status)
}

func TestStripeAdapter_RequiresInputEvent_MapsToPending(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.requires_input", "vs_pending_789", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, mock.calls, 1)
	assert.Equal(t, "PENDING", mock.calls[0].Status)
}

func TestStripeAdapter_ProcessingEvent_MapsToPending(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.processing", "vs_processing_000", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, mock.calls, 1)
	assert.Equal(t, "PENDING", mock.calls[0].Status)
}

func TestStripeAdapter_UnknownEventType_Ignored(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.created", "vs_created_000", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	// Should return 200 with acknowledged=true and "not relevant" message
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp VerificationWebhookResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Acknowledged)
	assert.Contains(t, resp.Message, "not relevant")
	// Inner handler must NOT have been called
	assert.Empty(t, mock.calls)
}

// --- Method not allowed ---

func TestStripeAdapter_MethodNotAllowed(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	adapter := buildAdapter(t, stripeSecret, innerSecret, nil)

	req := httptest.NewRequest(http.MethodGet, "/webhooks/verification/stripe", nil)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// --- End-to-end flow tests ---

func TestStripeAdapter_EndToEnd_InnerHandlerReceivesCorrectRequest(t *testing.T) {
	stripeSecret := []byte("whsec_e2e_test")
	innerSecret := []byte("inner-hmac-secret")
	mock := &mockVerificationService{}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	meta := map[string]string{
		"party_id":   "550e8400-e29b-41d4-a716-446655440000",
		"party_type": "PERSON",
	}
	body := buildStripeBody(t, "identity.verification_session.verified", "vs_e2e_test", meta)
	sig := generateStripeSignature(t, body, stripeSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, mock.calls, 1)

	call := mock.calls[0]
	assert.Equal(t, "vs_e2e_test", call.ProviderVerificationID)
	assert.Equal(t, "APPROVED", call.Status)
	assert.Equal(t, meta, call.Metadata)
	assert.NotNil(t, call.CompletedAt)
}

func TestStripeAdapter_EndToEnd_InnerHMACCorrectlyComputed(t *testing.T) {
	// This test verifies the inner HMAC is computed correctly by using a
	// wrong innerHMACSecret and confirming the inner handler rejects it.
	stripeSecret := []byte("whsec_test")
	correctInnerSecret := []byte("correct-inner-secret")
	wrongInnerSecret := []byte("wrong-inner-secret")

	// Build inner handler with correct secret
	inner, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"stripe": correctInnerSecret},
	})
	require.NoError(t, err)

	// Build adapter with WRONG inner secret — it will sign with wrong key
	adapter, err := NewStripeWebhookAdapter(StripeWebhookAdapterConfig{
		InnerHandler:    inner,
		WebhookSecret:   stripeSecret,
		InnerHMACSecret: wrongInnerSecret,
	})
	require.NoError(t, err)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_test", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	// Inner handler should reject the badly-signed synthetic request
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestStripeAdapter_IdempotentAlreadyCompleted(t *testing.T) {
	stripeSecret := []byte("whsec_test")
	innerSecret := []byte("inner-secret")
	mock := &mockVerificationService{
		updateFunc: func(_ context.Context, _ service.UpdateVerificationRequest) error {
			return service.ErrVerificationAlreadyCompleted
		},
	}
	adapter := buildAdapter(t, stripeSecret, innerSecret, mock)

	body := buildStripeBody(t, "identity.verification_session.verified", "vs_done", nil)
	sig := generateStripeSignature(t, body, stripeSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body))
	req.Header.Set(stripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	adapter.ServeHTTP(rr, req)

	// Idempotent — inner handler returns 200 for already-completed
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp VerificationWebhookResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Acknowledged)
}

// --- parseStripeSignature tests ---

func TestParseStripeSignature_Valid(t *testing.T) {
	header := "t=1614556800,v1=abc123,v1=def456"
	ts, sigs, err := parseStripeSignature(header)
	require.NoError(t, err)
	assert.Equal(t, "1614556800", ts)
	assert.Equal(t, []string{"abc123", "def456"}, sigs)
}

func TestParseStripeSignature_MissingTimestamp(t *testing.T) {
	header := "v1=abc123"
	_, _, err := parseStripeSignature(header)
	assert.Error(t, err)
}

func TestParseStripeSignature_MissingSignature(t *testing.T) {
	header := "t=1614556800"
	_, _, err := parseStripeSignature(header)
	assert.Error(t, err)
}

func TestParseStripeSignature_ExtraFields_Ignored(t *testing.T) {
	header := "t=1614556800,v0=old,v1=good,v2=unknown"
	ts, sigs, err := parseStripeSignature(header)
	require.NoError(t, err)
	assert.Equal(t, "1614556800", ts)
	// Only v1 signatures are returned
	assert.Equal(t, []string{"good"}, sigs)
}
