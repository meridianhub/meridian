package stripe

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82/webhook"
)

// mockPublisher implements EventPublisher for testing.
type mockPublisher struct {
	publishFunc func(ctx context.Context, event *PaymentEvent) error
	events      []*PaymentEvent
}

func (m *mockPublisher) PublishPaymentEvent(ctx context.Context, event *PaymentEvent) error {
	m.events = append(m.events, event)
	if m.publishFunc != nil {
		return m.publishFunc(ctx, event)
	}
	return nil
}

const testWebhookSecret = "whsec_test_secret_key_123"

// buildPaymentIntentSucceededPayload creates a Stripe event JSON payload for testing.
func buildPaymentIntentSucceededPayload(t *testing.T, piID string, amount int64, currency string, metadata map[string]string) []byte {
	t.Helper()

	metaJSON, err := json.Marshal(metadata)
	require.NoError(t, err)

	// Build a minimal Stripe event payload matching the SDK expectations
	payload := map[string]any{
		"id":      "evt_test_" + piID,
		"type":    "payment_intent.succeeded",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":       piID,
				"object":   "payment_intent",
				"amount":   amount,
				"currency": currency,
				"metadata": json.RawMessage(metaJSON),
				"latest_charge": map[string]any{
					"id":     "ch_test_charge_123",
					"object": "charge",
				},
				"status": "succeeded",
			},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return data
}

func buildPaymentIntentFailedPayload(t *testing.T, piID string) []byte {
	t.Helper()

	payload := map[string]any{
		"id":      "evt_test_failed_" + piID,
		"type":    "payment_intent.payment_failed",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":       piID,
				"object":   "payment_intent",
				"amount":   5000,
				"currency": "gbp",
				"metadata": map[string]string{
					"tenant_id": "meridian-ops",
					"party_id":  "party-123",
				},
				"status": "requires_payment_method",
				"last_payment_error": map[string]any{
					"message": "Your card was declined",
					"code":    "card_declined",
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return data
}

func buildChargeRefundedPayload(t *testing.T, chargeID string, amountRefunded int64) []byte {
	t.Helper()

	payload := map[string]any{
		"id":      "evt_test_refund_" + chargeID,
		"type":    "charge.refunded",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":              chargeID,
				"object":          "charge",
				"amount_refunded": amountRefunded,
				"currency":        "gbp",
				"metadata": map[string]string{
					"tenant_id": "meridian-ops",
					"party_id":  "party-456",
				},
				"payment_intent": map[string]any{
					"id":     "pi_test_refund",
					"object": "payment_intent",
					"metadata": map[string]string{
						"tenant_id": "meridian-ops",
						"party_id":  "party-456",
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return data
}

// signPayload creates a properly signed request for testing.
func signPayload(t *testing.T, payload []byte, secret string) string {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  secret,
	})
	return signed.Header
}

func TestNewWebhookHandler(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WebhookHandlerConfig
		wantErr error
	}{
		{
			name: "valid config",
			cfg: WebhookHandlerConfig{
				Publisher:     &mockPublisher{},
				WebhookSecret: testWebhookSecret,
			},
			wantErr: nil,
		},
		{
			name: "nil publisher",
			cfg: WebhookHandlerConfig{
				Publisher:     nil,
				WebhookSecret: testWebhookSecret,
			},
			wantErr: ErrNilEventPublisher,
		},
		{
			name: "empty webhook secret",
			cfg: WebhookHandlerConfig{
				Publisher:     &mockPublisher{},
				WebhookSecret: "",
			},
			wantErr: ErrEmptyWebhookSecret,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewWebhookHandler(tt.cfg)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, handler)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, handler)
			}
		})
	}
}

func TestHandleWebhook_PaymentIntentSucceeded(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload := buildPaymentIntentSucceededPayload(t, "pi_test_123", 10000, "gbp", map[string]string{
		"tenant_id": "meridian-ops",
		"party_id":  "party-abc",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp.Received)

	// Verify published event
	require.Len(t, pub.events, 1)
	evt := pub.events[0]
	assert.Equal(t, "payment_intent.succeeded", evt.EventType)
	assert.Equal(t, "meridian-ops", evt.TenantID)
	assert.Equal(t, "party-abc", evt.PartyID)
	assert.Equal(t, int64(10000), evt.AmountCents)
	assert.Equal(t, "GBP", evt.Currency)
	assert.Equal(t, "pi_test_123", evt.PaymentIntentID)
	assert.Equal(t, "ch_test_charge_123", evt.ChargeID)
	assert.NotEmpty(t, evt.IdempotencyKey)
	assert.True(t, len(evt.IdempotencyKey) > 0)
}

func TestHandleWebhook_PaymentIntentFailed(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload := buildPaymentIntentFailedPayload(t, "pi_fail_456")
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// No event should be published for failures
	assert.Empty(t, pub.events)
}

func TestHandleWebhook_ChargeRefunded(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload := buildChargeRefundedPayload(t, "ch_refund_789", 5000)
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	require.Len(t, pub.events, 1)
	evt := pub.events[0]
	assert.Equal(t, "charge.refunded", evt.EventType)
	assert.Equal(t, int64(5000), evt.AmountCents)
	assert.Equal(t, "ch_refund_789", evt.ChargeID)
}

func TestHandleWebhook_InvalidSignature(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     &mockPublisher{},
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload := buildPaymentIntentSucceededPayload(t, "pi_test", 1000, "gbp", map[string]string{
		"tenant_id": "meridian-ops",
		"party_id":  "party-1",
	})

	// Sign with wrong secret
	sig := signPayload(t, payload, "whsec_wrong_secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleWebhook_MissingSignature(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     &mockPublisher{},
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader([]byte("{}")))
	// No Stripe-Signature header

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleWebhook_MethodNotAllowed(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     &mockPublisher{},
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/webhooks/stripe", nil)
	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleWebhook_MissingTenantID(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Missing tenant_id in metadata
	payload := buildPaymentIntentSucceededPayload(t, "pi_no_tenant", 1000, "gbp", map[string]string{
		"party_id": "party-1",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Empty(t, pub.events)
}

func TestHandleWebhook_MissingPartyID(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Missing party_id in metadata
	payload := buildPaymentIntentSucceededPayload(t, "pi_no_party", 1000, "gbp", map[string]string{
		"tenant_id": "meridian-ops",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Empty(t, pub.events)
}

func TestHandleWebhook_PublishFailure(t *testing.T) {
	pub := &mockPublisher{
		publishFunc: func(_ context.Context, _ *PaymentEvent) error {
			return ErrPublishFailed
		},
	}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload := buildPaymentIntentSucceededPayload(t, "pi_pub_fail", 1000, "gbp", map[string]string{
		"tenant_id": "meridian-ops",
		"party_id":  "party-1",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should return 500 so Stripe retries
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleWebhook_UnhandledEventType(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload, err := json.Marshal(map[string]any{
		"id":      "evt_test_unknown",
		"type":    "customer.created",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":     "cus_123",
				"object": "customer",
			},
		},
	})
	require.NoError(t, err)

	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should acknowledge unknown events to prevent retries
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.events)
}

func TestHandleWebhook_PayloadTooLarge(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     &mockPublisher{},
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Create a payload larger than MaxBodySize (512KB)
	largePayload := make([]byte, MaxBodySize+100)
	for i := range largePayload {
		largePayload[i] = 'x'
	}

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(largePayload))
	req.Header.Set(StripeSignatureHeader, "t=123,v1=abc")

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestIdempotencyKeyDeterminism(t *testing.T) {
	key1 := generateIdempotencyKey("evt_123", "payment_intent.succeeded")
	key2 := generateIdempotencyKey("evt_123", "payment_intent.succeeded")
	key3 := generateIdempotencyKey("evt_456", "payment_intent.succeeded")

	assert.Equal(t, key1, key2, "same inputs should produce same key")
	assert.NotEqual(t, key1, key3, "different inputs should produce different keys")
	assert.True(t, len(key1) > 0)
	assert.Contains(t, key1, "stripe:")
}

func TestGenerateTestSignature(t *testing.T) {
	payload := []byte(`{"test": true}`)
	sig := GenerateTestSignature(payload, testWebhookSecret)

	assert.NotEmpty(t, sig)
	assert.Contains(t, sig, "t=")
	assert.Contains(t, sig, "v1=")
}
