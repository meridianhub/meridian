package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82/webhook"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	financialgatewayeventsv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway_events/v1"
	fghttp "github.com/meridianhub/meridian/services/financial-gateway/adapters/http"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/shared/platform/events"
)

const testWebhookSecret = "whsec_test_webhook_secret_for_financial_gateway"

// --- Test helpers ---

func buildStripePayload(t *testing.T, eventID, eventType string, data map[string]any) []byte {
	t.Helper()
	payload := map[string]any{
		"id":      eventID,
		"type":    eventType,
		"created": time.Now().Unix(),
		"data":    map[string]any{"object": data},
	}
	out, err := json.Marshal(payload)
	require.NoError(t, err)
	return out
}

func signPayload(t *testing.T, payload []byte, secret string) string {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  secret,
	})
	return signed.Header
}

// testTenantConfigProvider implements stripeadapter.TenantConfigProvider.
type testTenantConfigProvider struct {
	configs map[string]stripeadapter.TenantConfig
}

func (p *testTenantConfigProvider) GetTenantConfig(tenantID string) (stripeadapter.TenantConfig, error) {
	cfg, ok := p.configs[tenantID]
	if !ok {
		return stripeadapter.TenantConfig{}, stripeadapter.ErrTenantConfigNotFound
	}
	return cfg, nil
}

// stubOutboxPublisher records published events.
type stubOutboxPublisher struct {
	published []capturedPublish
	err       error
}

type capturedPublish struct {
	topic string
	aggID string
	event interface{}
}

func (s *stubOutboxPublisher) Publish(_ context.Context, _ *gorm.DB, event proto.Message, cfg events.PublishConfig) error {
	if s.err != nil {
		return s.err
	}
	s.published = append(s.published, capturedPublish{
		topic: cfg.Topic,
		aggID: cfg.AggregateID,
		event: event,
	})
	return nil
}

func setupHandler(t *testing.T, stub *stubOutboxPublisher) *fghttp.WebhookHandler {
	t.Helper()
	if stub == nil {
		stub = &stubOutboxPublisher{}
	}
	provider := &testTenantConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{
			"test-tenant": {
				ConnectedAccountID:    "acct_test",
				WebhookEndpointSecret: testWebhookSecret,
			},
		},
	}
	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}, provider, nil)
	require.NoError(t, err)

	return fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
		ClientFactory:   factory,
		OutboxPublisher: stub,
		DB:              nil,
	})
}

// --- Tests ---

func TestWebhookHandler_MethodNotAllowed(t *testing.T) {
	h := setupHandler(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/webhooks/stripe", nil)
	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestWebhookHandler_MissingTenantContext(t *testing.T) {
	h := setupHandler(t, nil)

	payload := buildStripePayload(t, "evt_1", "payment_intent.succeeded", map[string]any{})
	sig := signPayload(t, payload, testWebhookSecret)

	// No {tenantID} path value set — simulates request without tenant in URL.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestWebhookHandler_MissingSignature(t *testing.T) {
	h := setupHandler(t, nil)

	payload := buildStripePayload(t, "evt_2", "payment_intent.succeeded", map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	h := setupHandler(t, nil)

	payload := buildStripePayload(t, "evt_3", "payment_intent.succeeded", map[string]any{})
	sig := signPayload(t, payload, "whsec_wrong_secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestWebhookHandler_UnsupportedEvent_Returns200(t *testing.T) {
	h := setupHandler(t, nil)

	payload := buildStripePayload(t, "evt_4", "customer.created", map[string]any{
		"id": "cus_123",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, true, resp["acknowledged"])
}

func TestWebhookHandler_PaymentCaptured_PublishesToOutbox(t *testing.T) {
	stub := &stubOutboxPublisher{}
	h := setupHandler(t, stub)

	payload := buildStripePayload(t, "evt_cap_1", "payment_intent.succeeded", map[string]any{
		"id":       "pi_test_cap_123",
		"object":   "payment_intent",
		"amount":   5000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": "po-cap-123"},
		"status":   "succeeded",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	require.Len(t, stub.published, 1)
	assert.Equal(t, "financial-gateway.payment-captured.v1", stub.published[0].topic)

	evt, ok := stub.published[0].event.(*financialgatewayeventsv1.PaymentCapturedEvent)
	require.True(t, ok, "expected *PaymentCapturedEvent, got %T", stub.published[0].event)
	assert.Equal(t, "pi_test_cap_123", evt.GetProviderReferenceId())
	assert.Equal(t, "po-cap-123", evt.GetPaymentOrderId())
	assert.NotEmpty(t, evt.GetEventId())
	assert.Equal(t, int32(1), evt.GetVersion())
	assert.Equal(t, "evt_cap_1", evt.GetProviderEventId())
}

func TestWebhookHandler_PaymentFailed_PublishesToOutbox(t *testing.T) {
	stub := &stubOutboxPublisher{}
	h := setupHandler(t, stub)

	payload := buildStripePayload(t, "evt_fail_1", "payment_intent.payment_failed", map[string]any{
		"id":     "pi_test_fail_456",
		"object": "payment_intent",
		"metadata": map[string]string{
			"payment_order_id": "po-fail-456",
		},
		"last_payment_error": map[string]any{
			"message": "Your card was declined.",
			"code":    "card_declined",
		},
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	require.Len(t, stub.published, 1)
	assert.Equal(t, "financial-gateway.payment-failed.v1", stub.published[0].topic)

	evt, ok := stub.published[0].event.(*financialgatewayeventsv1.PaymentFailedEvent)
	require.True(t, ok, "expected *PaymentFailedEvent, got %T", stub.published[0].event)
	assert.Equal(t, "pi_test_fail_456", evt.GetProviderReferenceId())
	assert.Equal(t, "po-fail-456", evt.GetPaymentOrderId())
	assert.Equal(t, "Your card was declined.", evt.GetFailureReason())
	assert.Equal(t, "evt_fail_1", evt.GetProviderEventId())
}

func TestWebhookHandler_OutboxPublishFails_Returns500(t *testing.T) {
	stub := &stubOutboxPublisher{err: errors.New("outbox write failed")}
	h := setupHandler(t, stub)

	payload := buildStripePayload(t, "evt_err_1", "payment_intent.succeeded", map[string]any{
		"id":       "pi_test_err",
		"object":   "payment_intent",
		"metadata": map[string]string{"payment_order_id": "po-err"},
		"amount":   1000,
		"currency": "usd",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestWebhookHandler_PaymentRefunded_PublishesToOutbox(t *testing.T) {
	stub := &stubOutboxPublisher{}
	h := setupHandler(t, stub)

	payload := buildStripePayload(t, "evt_ref_1", "charge.refunded", map[string]any{
		"id":              "ch_test_ref_789",
		"object":          "charge",
		"amount_refunded": 3000,
		"currency":        "usd",
		"metadata":        map[string]string{},
		"payment_intent": map[string]any{
			"id":       "pi_original_789",
			"metadata": map[string]string{"payment_order_id": "po-ref-789"},
		},
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	require.Len(t, stub.published, 1)
	assert.Equal(t, "financial-gateway.payment-refunded.v1", stub.published[0].topic)
	assert.Equal(t, "po-ref-789", stub.published[0].aggID)

	evt, ok := stub.published[0].event.(*financialgatewayeventsv1.PaymentRefundedEvent)
	require.True(t, ok, "expected *PaymentRefundedEvent, got %T", stub.published[0].event)
	assert.Equal(t, "ch_test_ref_789", evt.GetProviderReferenceId())
	assert.Equal(t, "po-ref-789", evt.GetPaymentOrderId())
	assert.Equal(t, int64(3000), evt.GetAmountRefundedMinorUnits())
	assert.Equal(t, "usd", evt.GetCurrency())
	assert.Equal(t, "evt_ref_1", evt.GetProviderEventId())
	assert.NotEmpty(t, evt.GetEventId())
	assert.Equal(t, int32(1), evt.GetVersion())
}

func TestWebhookHandler_PaymentDisputed_PublishesToOutbox(t *testing.T) {
	stub := &stubOutboxPublisher{}
	h := setupHandler(t, stub)

	payload := buildStripePayload(t, "evt_disp_1", "charge.dispute.created", map[string]any{
		"id":     "dp_test_disp_101",
		"object": "dispute",
		"reason": "fraudulent",
		"status": "needs_response",
		"charge": map[string]any{
			"id":       "ch_disputed_101",
			"metadata": map[string]string{"payment_order_id": "po-disp-101"},
		},
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	require.Len(t, stub.published, 1)
	assert.Equal(t, "financial-gateway.payment-disputed.v1", stub.published[0].topic)
	assert.Equal(t, "po-disp-101", stub.published[0].aggID)

	evt, ok := stub.published[0].event.(*financialgatewayeventsv1.PaymentDisputedEvent)
	require.True(t, ok, "expected *PaymentDisputedEvent, got %T", stub.published[0].event)
	assert.Equal(t, "ch_disputed_101", evt.GetProviderReferenceId())
	assert.Equal(t, "po-disp-101", evt.GetPaymentOrderId())
	assert.Equal(t, "dispute reason: fraudulent", evt.GetDisputeReason())
	assert.Equal(t, "evt_disp_1", evt.GetProviderEventId())
	assert.NotEmpty(t, evt.GetEventId())
	assert.Equal(t, int32(1), evt.GetVersion())
}

func TestWebhookHandler_RefundedWithoutPaymentOrderID_AcknowledgesOnly(t *testing.T) {
	stub := &stubOutboxPublisher{}
	h := setupHandler(t, stub)

	payload := buildStripePayload(t, "evt_ref_noid", "charge.refunded", map[string]any{
		"id":              "ch_no_po_id",
		"object":          "charge",
		"amount_refunded": 1000,
		"currency":        "gbp",
		"metadata":        map[string]string{},
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, stub.published, 0, "should not publish event without payment_order_id")
}

func TestWebhookHandler_BodyTooLarge_Returns413(t *testing.T) {
	h := setupHandler(t, nil)

	// Body exceeding StripeWebhookMaxBodySize (512KB)
	largeBody := make([]byte, fghttp.StripeWebhookMaxBodySize+1)
	for i := range largeBody {
		largeBody[i] = 'x'
	}

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(largeBody))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", "t=1234,v1=test")

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestWebhookHandler_EmptyWebhookSecret_Returns500(t *testing.T) {
	stub := &stubOutboxPublisher{}
	provider := &testTenantConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{
			"no-secret-tenant": {
				ConnectedAccountID:    "acct_test",
				WebhookEndpointSecret: "", // empty secret
			},
		},
	}
	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}, provider, nil)
	require.NoError(t, err)

	h := fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
		ClientFactory:   factory,
		OutboxPublisher: stub,
	})

	payload := buildStripePayload(t, "evt_1", "payment_intent.succeeded", map[string]any{})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/no-secret-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "no-secret-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestWebhookHandler_MissingPaymentOrderID_AcknowledgesOnly(t *testing.T) {
	stub := &stubOutboxPublisher{}
	h := setupHandler(t, stub)

	// payment_intent.succeeded but with no payment_order_id in metadata
	payload := buildStripePayload(t, "evt_no_po", "payment_intent.succeeded", map[string]any{
		"id":       "pi_no_po_id",
		"object":   "payment_intent",
		"amount":   2000,
		"currency": "usd",
		"metadata": map[string]string{},
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, stub.published, 0, "should not publish when payment_order_id is missing")
}

// stubProcessedEventChecker implements ProcessedEventChecker for testing.
type stubProcessedEventChecker struct {
	processed map[string]bool
	err       error
}

func (s *stubProcessedEventChecker) IsProcessed(_ context.Context, providerEventID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.processed[providerEventID], nil
}

func TestWebhookHandler_EmptyWebhookSecret_ReturnsCorrectError(t *testing.T) {
	stub := &stubOutboxPublisher{}
	provider := &testTenantConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{
			"no-secret-tenant": {
				ConnectedAccountID:    "acct_test",
				WebhookEndpointSecret: "",
			},
		},
	}
	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}, provider, nil)
	require.NoError(t, err)

	h := fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
		ClientFactory:   factory,
		OutboxPublisher: stub,
	})

	payload := buildStripePayload(t, "evt_1", "payment_intent.succeeded", map[string]any{})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/no-secret-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "no-secret-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "no webhook secret configured for tenant", resp["error"])
}

func TestWebhookHandler_DuplicateEvent_SkipsPublish(t *testing.T) {
	stub := &stubOutboxPublisher{}
	checker := &stubProcessedEventChecker{
		processed: map[string]bool{"evt_dup_1": true},
	}

	provider := &testTenantConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{
			"test-tenant": {
				ConnectedAccountID:    "acct_test",
				WebhookEndpointSecret: testWebhookSecret,
			},
		},
	}
	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}, provider, nil)
	require.NoError(t, err)

	h := fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
		ClientFactory:   factory,
		OutboxPublisher: stub,
		EventChecker:    checker,
	})

	payload := buildStripePayload(t, "evt_dup_1", "payment_intent.succeeded", map[string]any{
		"id":       "pi_dup_123",
		"object":   "payment_intent",
		"amount":   5000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": "po-dup-123"},
		"status":   "succeeded",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, stub.published, 0, "duplicate event should not be re-published")
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, true, resp["acknowledged"])
}

func TestWebhookHandler_IdempotencyCheckError_FallsThroughToPublish(t *testing.T) {
	stub := &stubOutboxPublisher{}
	checker := &stubProcessedEventChecker{
		err: errors.New("db unavailable"),
	}

	provider := &testTenantConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{
			"test-tenant": {
				ConnectedAccountID:    "acct_test",
				WebhookEndpointSecret: testWebhookSecret,
			},
		},
	}
	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}, provider, nil)
	require.NoError(t, err)

	h := fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
		ClientFactory:   factory,
		OutboxPublisher: stub,
		EventChecker:    checker,
	})

	payload := buildStripePayload(t, "evt_fallthrough_1", "payment_intent.succeeded", map[string]any{
		"id":       "pi_fallthrough_123",
		"object":   "payment_intent",
		"amount":   2000,
		"currency": "usd",
		"metadata": map[string]string{"payment_order_id": "po-fallthrough-123"},
		"status":   "succeeded",
	})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/test-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "test-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	// On idempotency check failure, handler falls through and publishes the event.
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, stub.published, 1, "event should be published when idempotency check fails")
}

func TestWebhookHandler_NewWebhookHandler_PanicsOnNilClientFactory(t *testing.T) {
	stub := &stubOutboxPublisher{}
	assert.PanicsWithValue(t, fghttp.ErrNilClientFactory.Error(), func() {
		fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
			OutboxPublisher: stub,
		})
	})
}

func TestWebhookHandler_NewWebhookHandler_PanicsOnNilPublisher(t *testing.T) {
	provider := &testTenantConfigProvider{configs: map[string]stripeadapter.TenantConfig{}}
	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}, provider, nil)
	require.NoError(t, err)

	assert.PanicsWithValue(t, fghttp.ErrNilOutboxPublisher.Error(), func() {
		fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
			ClientFactory: factory,
		})
	})
}

func TestWebhookHandler_TenantNotFound_Returns500(t *testing.T) {
	stub := &stubOutboxPublisher{}
	provider := &testTenantConfigProvider{
		configs: map[string]stripeadapter.TenantConfig{}, // no tenants configured
	}
	factory, err := stripeadapter.NewClientFactory(stripeadapter.Config{
		APIKey:             "sk_test_key",
		TenantCacheSize:    10,
		TenantCacheTTL:     time.Minute,
		CircuitBreakerName: "test-cb",
	}, provider, nil)
	require.NoError(t, err)

	h := fghttp.NewWebhookHandler(fghttp.WebhookHandlerConfig{
		ClientFactory:   factory,
		OutboxPublisher: stub,
	})

	payload := buildStripePayload(t, "evt_notfound_1", "payment_intent.succeeded", map[string]any{})
	sig := signPayload(t, payload, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/unknown-tenant", bytes.NewReader(payload))
	req.SetPathValue("tenantID", "unknown-tenant")
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	h.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}
