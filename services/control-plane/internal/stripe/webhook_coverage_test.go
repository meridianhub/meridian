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
)

// --- Payment intent succeeded: missing charge ID ---

func TestHandleWebhook_PaymentIntentSucceeded_MissingChargeID(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Build payload WITHOUT latest_charge field.
	payload, err := json.Marshal(map[string]any{
		"id":      "evt_test_no_charge",
		"type":    "payment_intent.succeeded",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":       "pi_no_charge",
				"object":   "payment_intent",
				"amount":   5000,
				"currency": "gbp",
				"metadata": map[string]string{
					"tenant_id": "meridian-ops",
					"party_id":  "party-123",
				},
				"status": "succeeded",
				// no latest_charge
			},
		},
	})
	require.NoError(t, err)

	sig := signPayload(t, payload, testWebhookSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Returns 500 to trigger Stripe retry (charge may populate on re-delivery).
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Empty(t, pub.events)
}

// --- Payment intent failed: valid event with minimal data object ---

func TestHandleWebhook_PaymentIntentFailed_MinimalPayload(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Minimal valid payment_intent object - just enough for the Stripe SDK to parse.
	payload, err := json.Marshal(map[string]any{
		"id":      "evt_fail_minimal",
		"type":    "payment_intent.payment_failed",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":     "pi_minimal",
				"object": "payment_intent",
				// no last_payment_error, no metadata
			},
		},
	})
	require.NoError(t, err)

	sig := signPayload(t, payload, testWebhookSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Failure logged, 200 returned (failure is just logged, not retried).
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.events)
}

// --- Payment intent failed: nil LastPaymentError (no error code) ---

func TestHandleWebhook_PaymentIntentFailed_NoErrorMessage(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Payload WITHOUT last_payment_error.
	payload, err := json.Marshal(map[string]any{
		"id":      "evt_fail_no_error",
		"type":    "payment_intent.payment_failed",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":       "pi_fail_no_err",
				"object":   "payment_intent",
				"amount":   3000,
				"currency": "gbp",
				"metadata": map[string]string{
					"tenant_id": "meridian-ops",
					"party_id":  "party-456",
				},
				"status": "requires_payment_method",
				// no last_payment_error
			},
		},
	})
	require.NoError(t, err)

	sig := signPayload(t, payload, testWebhookSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should still return 200 - failure is logged, no event published.
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.events)
}

// --- Charge refunded: missing tenant_id in all metadata ---

func TestHandleWebhook_ChargeRefunded_MissingTenantID(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Charge payload with no tenant_id in either charge or PI metadata.
	payload, err := json.Marshal(map[string]any{
		"id":      "evt_refund_no_tenant",
		"type":    "charge.refunded",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":              "ch_no_tenant",
				"object":          "charge",
				"amount_refunded": 2000,
				"currency":        "gbp",
				"metadata": map[string]string{
					// no tenant_id
					"party_id": "party-789",
				},
				"payment_intent": map[string]any{
					"id":       "pi_no_tenant",
					"object":   "payment_intent",
					"metadata": map[string]string{
						// no tenant_id
					},
				},
			},
		},
	})
	require.NoError(t, err)

	sig := signPayload(t, payload, testWebhookSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Acknowledged without processing.
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.events)

	var resp WebhookResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Received)
	assert.Contains(t, resp.Message, "without tenant context")
}

// --- Charge refunded: has tenant_id but missing party_id ---

func TestHandleWebhook_ChargeRefunded_MissingPartyID(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload, err := json.Marshal(map[string]any{
		"id":      "evt_refund_no_party",
		"type":    "charge.refunded",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":              "ch_no_party",
				"object":          "charge",
				"amount_refunded": 1500,
				"currency":        "gbp",
				"metadata": map[string]string{
					"tenant_id": "meridian-ops",
					// no party_id
				},
			},
		},
	})
	require.NoError(t, err)

	sig := signPayload(t, payload, testWebhookSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Acknowledged without processing.
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.events)

	var resp WebhookResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Received)
	assert.Contains(t, resp.Message, "without party context")
}

// --- Charge refunded: publish failure ---

func TestHandleWebhook_ChargeRefunded_PublishFailure(t *testing.T) {
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

	payload := buildChargeRefundedPayload(t, "ch_pub_fail", 3000)
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Returns 500 to trigger Stripe retry.
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

// --- Charge refunded: no PaymentIntent in charge (uses charge.Metadata only) ---

func TestHandleWebhook_ChargeRefunded_NilPaymentIntent(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Charge without a payment_intent, metadata on the charge directly.
	payload, err := json.Marshal(map[string]any{
		"id":      "evt_refund_no_pi",
		"type":    "charge.refunded",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":              "ch_no_pi",
				"object":          "charge",
				"amount_refunded": 800,
				"currency":        "eur",
				"metadata": map[string]string{
					"tenant_id": "meridian-ops",
					"party_id":  "party-no-pi",
				},
				// no payment_intent field
			},
		},
	})
	require.NoError(t, err)

	sig := signPayload(t, payload, testWebhookSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, pub.events, 1)
	evt := pub.events[0]
	assert.Equal(t, "charge.refunded", evt.EventType)
	assert.Equal(t, "ch_no_pi", evt.ChargeID)
	assert.Equal(t, "EUR", evt.Currency)
	assert.Equal(t, int64(800), evt.AmountCents)
	assert.Empty(t, evt.PaymentIntentID) // no PI
}
