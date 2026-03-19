package http

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

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
)

func TestWebhookHandler_HandleWebhook_ServiceError(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{
			updateFunc: func(_ context.Context, _ *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
				return nil, errors.New("internal service error")
			},
		},
		HMACSecret: secret,
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		Timestamp:          time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrPaymentOrderService.Error(), resp.Error)
}

func TestWebhookHandler_HandleWebhook_NilResponse(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{
			updateFunc: func(_ context.Context, _ *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
				return nil, nil // No error but nil response
			},
		},
		HMACSecret: secret,
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		Timestamp:          time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestWebhookHandler_HandleWebhook_PaymentOrderIDOnly(t *testing.T) {
	// Test that a webhook with PaymentOrderID but no GatewayReferenceID is accepted
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          secret,
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		PaymentOrderID: "po-456", // No GatewayReferenceID
		Status:         "Settled",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestWebhookHandler_HandleWebhook_ZeroTimestamp(t *testing.T) {
	// Test that a webhook with zero timestamp skips freshness check
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          secret,
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		// Timestamp is zero value
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestValidateSignature_InvalidHex(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	// Invalid hex string should fail validation
	result := handler.validateSignature([]byte("test body"), "not-valid-hex-gggg")
	assert.False(t, result)
}

func TestGenerateIdempotencyKey_Deterministic(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	req1 := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		Timestamp:          ts,
	}

	req2 := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		Timestamp:          ts,
	}

	key1 := handler.generateIdempotencyKey(req1)
	key2 := handler.generateIdempotencyKey(req2)

	// Same inputs produce same key
	assert.Equal(t, key1, key2)
	assert.True(t, len(key1) > 0)
	assert.Contains(t, key1, "webhook:")

	// Different inputs produce different key
	req3 := WebhookRequest{
		GatewayReferenceID: "gw-ref-456",
		Status:             "Rejected",
		Timestamp:          ts,
	}
	key3 := handler.generateIdempotencyKey(req3)
	assert.NotEqual(t, key1, key3)
}

func TestRequestID_IsUnique(t *testing.T) {
	id1 := RequestID()
	id2 := RequestID()
	assert.NotEqual(t, id1, id2)
	assert.NotEmpty(t, id1)
}

func TestNewWebhookHandler_NilLogger(t *testing.T) {
	// Should use slog.Default() when logger is nil
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
		Logger:              nil,
	})
	require.NoError(t, err)
	assert.NotNil(t, handler)
}
