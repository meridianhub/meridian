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

// TestDuplicateWebhookIdempotency verifies that duplicate Stripe webhook deliveries
// produce the same idempotency key and saga_id, resulting in a single ledger entry
// when the saga engine enforces idempotency.
func TestDuplicateWebhookIdempotency(t *testing.T) {
	pub := &mockPublisher{}
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	// Create a single payload that will be delivered twice
	payload := buildPaymentIntentSucceededPayload(t, "pi_idem_test", 25000, "gbp", map[string]string{
		"tenant_id": "meridian-ops",
		"party_id":  "party-idem",
	})

	// First delivery
	sig1 := signPayload(t, payload, testWebhookSecret)
	req1 := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req1.Header.Set(StripeSignatureHeader, sig1)

	rr1 := httptest.NewRecorder()
	handler.HandleWebhook(rr1, req1)
	assert.Equal(t, http.StatusOK, rr1.Code)

	// Second delivery (duplicate)
	sig2 := signPayload(t, payload, testWebhookSecret)
	req2 := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req2.Header.Set(StripeSignatureHeader, sig2)

	rr2 := httptest.NewRecorder()
	handler.HandleWebhook(rr2, req2)
	assert.Equal(t, http.StatusOK, rr2.Code)

	// Both deliveries should produce events with the same idempotency key
	require.Len(t, pub.events, 2)
	assert.Equal(t, pub.events[0].IdempotencyKey, pub.events[1].IdempotencyKey,
		"duplicate webhooks must produce identical idempotency keys")

	// Same Stripe event ID
	assert.Equal(t, pub.events[0].StripeEventID, pub.events[1].StripeEventID)

	// Same tenant/party/amount
	assert.Equal(t, pub.events[0].TenantID, pub.events[1].TenantID)
	assert.Equal(t, pub.events[0].PartyID, pub.events[1].PartyID)
	assert.Equal(t, pub.events[0].AmountCents, pub.events[1].AmountCents)
}

// TestDuplicateConsumerIdempotency verifies that the consumer propagates the
// idempotency key to the saga trigger, enabling the saga engine to deduplicate.
func TestDuplicateConsumerIdempotency(t *testing.T) {
	trigger := &mockSagaTrigger{}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	event := &PaymentEvent{
		EventID:         "evt-idem-1",
		StripeEventID:   "evt_stripe_dup",
		EventType:       "payment_intent.succeeded",
		TenantID:        "meridian-ops",
		PartyID:         "party-dup",
		AmountCents:     7500,
		Currency:        "gbp",
		ChargeID:        "ch_dup",
		PaymentIntentID: "pi_dup",
		IdempotencyKey:  "stripe:deterministic_key_xyz",
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Process the same event three times (simulating Kafka redelivery)
	for i := 0; i < 3; i++ {
		err = consumer.HandlePaymentEvent(context.Background(), data)
		require.NoError(t, err)
	}

	// All three calls should have the same idempotency key
	require.Len(t, trigger.calls, 3)
	for i := 0; i < 3; i++ {
		assert.Equal(t, "stripe:deterministic_key_xyz", trigger.calls[i].IdempotencyKey,
			"call %d should have the same idempotency key", i)
		assert.Equal(t, "stripe_payment_received", trigger.calls[i].SagaName)
	}
}

// TestIdempotencyKeyStability verifies that the idempotency key generation is
// deterministic and stable across time (not dependent on current time).
func TestIdempotencyKeyStability(t *testing.T) {
	eventID := "evt_stable_123"
	eventType := "payment_intent.succeeded"

	// Generate keys at different points
	key1 := generateIdempotencyKey(eventID, eventType)

	// Simulate passage of time
	time.Sleep(10 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps to test key stability

	key2 := generateIdempotencyKey(eventID, eventType)

	assert.Equal(t, key1, key2, "idempotency key must be stable over time")
}

// TestIdempotencyKeyUniqueness verifies that different events produce different keys.
func TestIdempotencyKeyUniqueness(t *testing.T) {
	keys := make(map[string]bool)

	testCases := []struct {
		eventID   string
		eventType string
	}{
		{"evt_1", "payment_intent.succeeded"},
		{"evt_2", "payment_intent.succeeded"},
		{"evt_1", "charge.refunded"},
		{"evt_3", "payment_intent.payment_failed"},
	}

	for _, tc := range testCases {
		key := generateIdempotencyKey(tc.eventID, tc.eventType)
		assert.False(t, keys[key], "duplicate key generated for %s/%s", tc.eventID, tc.eventType)
		keys[key] = true
	}
}

// TestEndToEndIdempotencyFlow verifies the complete flow from webhook to saga trigger
// ensuring idempotency keys are preserved throughout.
func TestEndToEndIdempotencyFlow(t *testing.T) {
	// Step 1: Webhook handler receives and publishes events
	pub := &mockPublisher{}
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		Publisher:     pub,
		WebhookSecret: testWebhookSecret,
	})
	require.NoError(t, err)

	payload := buildPaymentIntentSucceededPayload(t, "pi_e2e_idem", 15000, "gbp", map[string]string{
		"tenant_id": "meridian-ops",
		"party_id":  "party-e2e",
	})

	sig := signPayload(t, payload, testWebhookSecret)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set(StripeSignatureHeader, sig)
	rr := httptest.NewRecorder()
	webhookHandler.HandleWebhook(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	require.Len(t, pub.events, 1)
	publishedEvent := pub.events[0]

	// Step 2: Consumer receives the published event and triggers saga
	trigger := &mockSagaTrigger{}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	eventData, err := json.Marshal(publishedEvent)
	require.NoError(t, err)

	err = consumer.HandlePaymentEvent(context.Background(), eventData)
	require.NoError(t, err)

	// Step 3: Verify the idempotency key was preserved through the entire chain
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, publishedEvent.IdempotencyKey, trigger.calls[0].IdempotencyKey,
		"idempotency key must be preserved from webhook through consumer to saga trigger")

	// Step 4: Simulate duplicate delivery
	err = consumer.HandlePaymentEvent(context.Background(), eventData)
	require.NoError(t, err)

	require.Len(t, trigger.calls, 2)
	assert.Equal(t, trigger.calls[0].IdempotencyKey, trigger.calls[1].IdempotencyKey,
		"duplicate deliveries must produce identical idempotency keys")
}
