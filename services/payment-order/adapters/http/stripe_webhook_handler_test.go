package http

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

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway/stripe"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

const testStripeWebhookSecret = "whsec_test_integration_secret"

func testStripeConfig() stripe.Config {
	cfg := stripe.DefaultConfig()
	cfg.APIKey = "sk_test_key"
	return cfg
}

// testConfigProvider implements stripe.TenantConfigProvider for testing.
type testConfigProvider struct {
	configs map[string]stripe.TenantConfig
}

func (p *testConfigProvider) GetTenantConfig(tenantID string) (stripe.TenantConfig, error) {
	cfg, ok := p.configs[tenantID]
	if !ok {
		return stripe.TenantConfig{}, stripe.ErrTenantConfigNotFound
	}
	return cfg, nil
}

func buildTestStripePayload(t *testing.T, eventID, eventType string, data map[string]any) []byte {
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

func signTestPayload(t *testing.T, payload []byte, secret string) string {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  secret,
	})
	return signed.Header
}

func setupStripeWebhookHandler(t *testing.T) (*StripeWebhookHandler, *mockPaymentOrderService) {
	t.Helper()

	mockSvc := &mockPaymentOrderService{
		updateFunc: func(_ context.Context, _ *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
			return &pb.UpdatePaymentOrderResponse{
				PaymentOrder: &pb.PaymentOrder{
					PaymentOrderId: "test-order",
					Status:         pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
				},
			}, nil
		},
	}

	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: mockSvc,
		HMACSecret:          []byte("generic-secret"),
	})
	require.NoError(t, err)

	provider := &testConfigProvider{
		configs: map[string]stripe.TenantConfig{
			"test-tenant": {
				ConnectedAccountID:    "acct_test_123",
				WebhookEndpointSecret: testStripeWebhookSecret,
			},
		},
	}

	factory, err := stripe.NewClientFactory(testStripeConfig(), provider, nil)
	require.NoError(t, err)

	stripeHandler, err := NewStripeWebhookHandler(StripeWebhookHandlerConfig{
		ClientFactory:  factory,
		WebhookHandler: webhookHandler,
	})
	require.NoError(t, err)

	return stripeHandler, mockSvc
}

func TestNewStripeWebhookHandler(t *testing.T) {
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	provider := &testConfigProvider{configs: map[string]stripe.TenantConfig{}}
	factory, err := stripe.NewClientFactory(testStripeConfig(), provider, nil)
	require.NoError(t, err)

	t.Run("valid config", func(t *testing.T) {
		h, err := NewStripeWebhookHandler(StripeWebhookHandlerConfig{
			ClientFactory:  factory,
			WebhookHandler: webhookHandler,
		})
		assert.NoError(t, err)
		assert.NotNil(t, h)
	})

	t.Run("nil client factory", func(t *testing.T) {
		h, err := NewStripeWebhookHandler(StripeWebhookHandlerConfig{
			ClientFactory:  nil,
			WebhookHandler: webhookHandler,
		})
		assert.ErrorIs(t, err, ErrNilClientFactory)
		assert.Nil(t, h)
	})

	t.Run("nil webhook handler", func(t *testing.T) {
		h, err := NewStripeWebhookHandler(StripeWebhookHandlerConfig{
			ClientFactory:  factory,
			WebhookHandler: nil,
		})
		assert.ErrorIs(t, err, ErrNilWebhookHandler)
		assert.Nil(t, h)
	})
}

func TestStripeWebhookHandler_PaymentIntentSucceeded(t *testing.T) {
	handler, mockSvc := setupStripeWebhookHandler(t)

	var capturedReq *pb.UpdatePaymentOrderRequest
	mockSvc.updateFunc = func(_ context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
		capturedReq = req
		return &pb.UpdatePaymentOrderResponse{
			PaymentOrder: &pb.PaymentOrder{
				PaymentOrderId: "po-settled",
				Status:         pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
			},
		}, nil
	}

	payload := buildTestStripePayload(t, "evt_1", "payment_intent.succeeded", map[string]any{
		"id":       "pi_settled_123",
		"object":   "payment_intent",
		"amount":   5000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": "po-settled"},
		"latest_charge": map[string]any{
			"id":     "ch_123",
			"object": "charge",
		},
		"status": "succeeded",
	})
	sig := signTestPayload(t, payload, testStripeWebhookSecret)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	req := httptest.NewRequest(http.MethodPost, "/webhook/stripe", bytes.NewReader(payload)).WithContext(ctx)
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	handler.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	require.NotNil(t, capturedReq)
	assert.Equal(t, "pi_settled_123", capturedReq.GatewayReferenceId)
	assert.Equal(t, "po-settled", capturedReq.PaymentOrderId)
	assert.Equal(t, pb.GatewayStatus_GATEWAY_STATUS_SETTLED, capturedReq.GatewayStatus)
}

func TestStripeWebhookHandler_MissingTenantContext(t *testing.T) {
	handler, _ := setupStripeWebhookHandler(t)

	payload := buildTestStripePayload(t, "evt_2", "payment_intent.succeeded", map[string]any{
		"id":     "pi_test",
		"object": "payment_intent",
	})
	sig := signTestPayload(t, payload, testStripeWebhookSecret)

	// No tenant in context
	req := httptest.NewRequest(http.MethodPost, "/webhook/stripe", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	handler.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestStripeWebhookHandler_InvalidSignature(t *testing.T) {
	handler, _ := setupStripeWebhookHandler(t)

	payload := buildTestStripePayload(t, "evt_3", "payment_intent.succeeded", map[string]any{
		"id":     "pi_test",
		"object": "payment_intent",
	})
	wrongSig := signTestPayload(t, payload, "whsec_wrong_secret")

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	req := httptest.NewRequest(http.MethodPost, "/webhook/stripe", bytes.NewReader(payload)).WithContext(ctx)
	req.Header.Set("Stripe-Signature", wrongSig)

	rr := httptest.NewRecorder()
	handler.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestStripeWebhookHandler_UnsupportedEvent(t *testing.T) {
	handler, _ := setupStripeWebhookHandler(t)

	payload := buildTestStripePayload(t, "evt_4", "customer.created", map[string]any{
		"id":     "cus_123",
		"object": "customer",
	})
	sig := signTestPayload(t, payload, testStripeWebhookSecret)

	ctx := tenant.WithTenant(context.Background(), "test-tenant")
	req := httptest.NewRequest(http.MethodPost, "/webhook/stripe", bytes.NewReader(payload)).WithContext(ctx)
	req.Header.Set("Stripe-Signature", sig)

	rr := httptest.NewRecorder()
	handler.HandleStripeWebhook(rr, req)

	// Unsupported events should be acknowledged (200) to prevent Stripe retries
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestStripeWebhookHandler_MethodNotAllowed(t *testing.T) {
	handler, _ := setupStripeWebhookHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/webhook/stripe", nil)
	rr := httptest.NewRecorder()
	handler.HandleStripeWebhook(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}
