package stripe

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82/webhook"
)

const testAdapterSecret = "whsec_test_adapter_secret"

// buildStripeEventPayload creates a Stripe event JSON payload for testing.
func buildStripeEventPayload(t *testing.T, eventID, eventType string, data map[string]any) []byte {
	t.Helper()
	payload := map[string]any{
		"id":      eventID,
		"type":    eventType,
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": data,
		},
	}
	out, err := json.Marshal(payload)
	require.NoError(t, err)
	return out
}

func signAdapterPayload(t *testing.T, payload []byte, secret string) string {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  secret,
	})
	return signed.Header
}

func TestNewWebhookAdapter(t *testing.T) {
	t.Run("valid secret", func(t *testing.T) {
		adapter, err := NewWebhookAdapter(testAdapterSecret)
		require.NoError(t, err)
		assert.NotNil(t, adapter)
	})

	t.Run("empty secret", func(t *testing.T) {
		adapter, err := NewWebhookAdapter("")
		assert.ErrorIs(t, err, ErrEmptyEndpointSecret)
		assert.Nil(t, adapter)
	})
}

func TestStripeWebhookAdapter_ParseWebhook_PaymentIntentSucceeded(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	payload := buildStripeEventPayload(t, "evt_succeeded_1", "payment_intent.succeeded", map[string]any{
		"id":       "pi_test_123",
		"object":   "payment_intent",
		"amount":   10000,
		"currency": "gbp",
		"metadata": map[string]string{
			"payment_order_id": "po-order-456",
		},
		"latest_charge": map[string]any{
			"id":     "ch_charge_789",
			"object": "charge",
		},
		"status": "succeeded",
	})
	sig := signAdapterPayload(t, payload, testAdapterSecret)

	req := &http.Request{Header: http.Header{}}
	req.Header.Set("Stripe-Signature", sig)

	result, err := adapter.ParseWebhook(req, payload)
	require.NoError(t, err)

	assert.Equal(t, "pi_test_123", result.GatewayReferenceID)
	assert.Equal(t, "po-order-456", result.PaymentOrderID)
	assert.Equal(t, "SETTLED", result.Status)
	assert.False(t, result.Timestamp.IsZero())
}

func TestStripeWebhookAdapter_ParseWebhook_PaymentIntentFailed(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	payload := buildStripeEventPayload(t, "evt_failed_1", "payment_intent.payment_failed", map[string]any{
		"id":       "pi_fail_123",
		"object":   "payment_intent",
		"amount":   5000,
		"currency": "gbp",
		"metadata": map[string]string{
			"payment_order_id": "po-order-789",
		},
		"status": "requires_payment_method",
		"last_payment_error": map[string]any{
			"message": "Your card was declined",
			"code":    "card_declined",
		},
	})
	sig := signAdapterPayload(t, payload, testAdapterSecret)

	req := &http.Request{Header: http.Header{}}
	req.Header.Set("Stripe-Signature", sig)

	result, err := adapter.ParseWebhook(req, payload)
	require.NoError(t, err)

	assert.Equal(t, "pi_fail_123", result.GatewayReferenceID)
	assert.Equal(t, "po-order-789", result.PaymentOrderID)
	assert.Equal(t, "REJECTED", result.Status)
	assert.Equal(t, "Your card was declined", result.Message)
}

func TestStripeWebhookAdapter_ParseWebhook_ChargeRefunded(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	payload := buildStripeEventPayload(t, "evt_refund_1", "charge.refunded", map[string]any{
		"id":              "ch_refund_123",
		"object":          "charge",
		"amount_refunded": 3000,
		"currency":        "gbp",
		"metadata":        map[string]string{},
		"payment_intent": map[string]any{
			"id":     "pi_original_123",
			"object": "payment_intent",
			"metadata": map[string]string{
				"payment_order_id": "po-order-refund",
			},
		},
	})
	sig := signAdapterPayload(t, payload, testAdapterSecret)

	req := &http.Request{Header: http.Header{}}
	req.Header.Set("Stripe-Signature", sig)

	result, err := adapter.ParseWebhook(req, payload)
	require.NoError(t, err)

	assert.Equal(t, "ch_refund_123", result.GatewayReferenceID)
	assert.Equal(t, "po-order-refund", result.PaymentOrderID)
	assert.Equal(t, "REFUNDED", result.Status)
}

func TestStripeWebhookAdapter_ParseWebhook_ChargeDisputed(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	payload := buildStripeEventPayload(t, "evt_dispute_1", "charge.dispute.created", map[string]any{
		"id":     "dp_dispute_123",
		"object": "dispute",
		"charge": map[string]any{
			"id":     "ch_disputed_charge",
			"object": "charge",
			"metadata": map[string]string{
				"payment_order_id": "po-order-dispute",
			},
		},
		"status": "needs_response",
		"reason": "fraudulent",
	})
	sig := signAdapterPayload(t, payload, testAdapterSecret)

	req := &http.Request{Header: http.Header{}}
	req.Header.Set("Stripe-Signature", sig)

	result, err := adapter.ParseWebhook(req, payload)
	require.NoError(t, err)

	assert.Equal(t, "ch_disputed_charge", result.GatewayReferenceID)
	assert.Equal(t, "po-order-dispute", result.PaymentOrderID)
	assert.Equal(t, "DISPUTED", result.Status)
	assert.Contains(t, result.Message, "fraudulent")
}

func TestStripeWebhookAdapter_ParseWebhook_InvalidSignature(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	payload := buildStripeEventPayload(t, "evt_1", "payment_intent.succeeded", map[string]any{
		"id":     "pi_test",
		"object": "payment_intent",
	})
	// Sign with wrong secret
	sig := signAdapterPayload(t, payload, "whsec_wrong_secret")

	req := &http.Request{Header: http.Header{}}
	req.Header.Set("Stripe-Signature", sig)

	_, err = adapter.ParseWebhook(req, payload)
	assert.ErrorIs(t, err, ErrWebhookInvalidSignature)
}

func TestStripeWebhookAdapter_ParseWebhook_MissingSignature(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	payload := []byte(`{"id":"evt_1","type":"payment_intent.succeeded"}`)

	req := &http.Request{Header: http.Header{}}
	// No Stripe-Signature header

	_, err = adapter.ParseWebhook(req, payload)
	assert.ErrorIs(t, err, ErrWebhookMissingSignature)
}

func TestStripeWebhookAdapter_ParseWebhook_UnsupportedEventType(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	payload := buildStripeEventPayload(t, "evt_unknown_1", "customer.created", map[string]any{
		"id":     "cus_123",
		"object": "customer",
	})
	sig := signAdapterPayload(t, payload, testAdapterSecret)

	req := &http.Request{Header: http.Header{}}
	req.Header.Set("Stripe-Signature", sig)

	_, err = adapter.ParseWebhook(req, payload)
	assert.ErrorIs(t, err, ErrWebhookUnsupportedEvent)
}

func TestStripeWebhookAdapter_ParseWebhook_MetadataExtraction(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	t.Run("payment_order_id from PI metadata", func(t *testing.T) {
		payload := buildStripeEventPayload(t, "evt_meta_1", "payment_intent.succeeded", map[string]any{
			"id":       "pi_meta_test",
			"object":   "payment_intent",
			"amount":   1000,
			"currency": "usd",
			"metadata": map[string]string{
				"payment_order_id": "po-from-metadata",
			},
			"latest_charge": map[string]any{
				"id":     "ch_meta",
				"object": "charge",
			},
			"status": "succeeded",
		})
		sig := signAdapterPayload(t, payload, testAdapterSecret)

		req := &http.Request{Header: http.Header{}}
		req.Header.Set("Stripe-Signature", sig)

		result, err := adapter.ParseWebhook(req, payload)
		require.NoError(t, err)
		assert.Equal(t, "po-from-metadata", result.PaymentOrderID)
	})

	t.Run("no payment_order_id still succeeds", func(t *testing.T) {
		payload := buildStripeEventPayload(t, "evt_meta_2", "payment_intent.succeeded", map[string]any{
			"id":       "pi_no_po_id",
			"object":   "payment_intent",
			"amount":   1000,
			"currency": "usd",
			"metadata": map[string]string{},
			"latest_charge": map[string]any{
				"id":     "ch_meta_2",
				"object": "charge",
			},
			"status": "succeeded",
		})
		sig := signAdapterPayload(t, payload, testAdapterSecret)

		req := &http.Request{Header: http.Header{}}
		req.Header.Set("Stripe-Signature", sig)

		result, err := adapter.ParseWebhook(req, payload)
		require.NoError(t, err)
		assert.Equal(t, "pi_no_po_id", result.GatewayReferenceID)
		assert.Empty(t, result.PaymentOrderID)
	})
}

func TestStripeWebhookAdapter_ParseWebhook_RefundMetadataFallback(t *testing.T) {
	adapter, err := NewWebhookAdapter(testAdapterSecret)
	require.NoError(t, err)

	t.Run("fallback to charge metadata when PI metadata missing", func(t *testing.T) {
		payload := buildStripeEventPayload(t, "evt_refund_fb", "charge.refunded", map[string]any{
			"id":              "ch_refund_fallback",
			"object":          "charge",
			"amount_refunded": 1500,
			"currency":        "usd",
			"metadata": map[string]string{
				"payment_order_id": "po-from-charge-meta",
			},
			"payment_intent": map[string]any{
				"id":       "pi_no_meta",
				"object":   "payment_intent",
				"metadata": map[string]string{},
			},
		})
		sig := signAdapterPayload(t, payload, testAdapterSecret)

		req := &http.Request{Header: http.Header{}}
		req.Header.Set("Stripe-Signature", sig)

		result, err := adapter.ParseWebhook(req, payload)
		require.NoError(t, err)
		assert.Equal(t, "po-from-charge-meta", result.PaymentOrderID)
	})
}
