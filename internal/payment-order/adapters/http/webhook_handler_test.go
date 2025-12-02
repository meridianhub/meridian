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

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
)

// mockPaymentOrderService implements PaymentOrderServiceClient for testing.
type mockPaymentOrderService struct {
	updateFunc func(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error)
}

func (m *mockPaymentOrderService) UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, req)
	}
	return &pb.UpdatePaymentOrderResponse{
		PaymentOrder: &pb.PaymentOrder{
			PaymentOrderId: "test-order-id",
			Status:         pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
		},
	}, nil
}

func TestNewWebhookHandler(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WebhookHandlerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: WebhookHandlerConfig{
				PaymentOrderService: &mockPaymentOrderService{},
				HMACSecret:          []byte("secret"),
			},
			wantErr: false,
		},
		{
			name: "nil payment order service",
			cfg: WebhookHandlerConfig{
				PaymentOrderService: nil,
				HMACSecret:          []byte("secret"),
			},
			wantErr: true,
		},
		{
			name: "empty HMAC secret",
			cfg: WebhookHandlerConfig{
				PaymentOrderService: &mockPaymentOrderService{},
				HMACSecret:          []byte{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewWebhookHandler(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, handler)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, handler)
			}
		})
	}
}

func TestWebhookHandler_HandleWebhook_Success(t *testing.T) {
	secret := []byte("test-secret-key")

	var capturedReq *pb.UpdatePaymentOrderRequest
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{
			updateFunc: func(_ context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
				capturedReq = req
				return &pb.UpdatePaymentOrderResponse{
					PaymentOrder: &pb.PaymentOrder{
						PaymentOrderId: "test-order-id",
						Status:         pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					},
				}, nil
			},
		},
		HMACSecret: secret,
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		PaymentOrderID:     "po-456",
		Status:             "Settled",
		Message:            "Payment successful",
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

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp.Acknowledged)
	assert.NotEmpty(t, resp.Message)

	// Verify the captured request
	require.NotNil(t, capturedReq)
	assert.Equal(t, "gw-ref-123", capturedReq.GatewayReferenceId)
	assert.Equal(t, "po-456", capturedReq.PaymentOrderId)
	assert.Equal(t, pb.GatewayStatus_GATEWAY_STATUS_SETTLED, capturedReq.GatewayStatus)
	assert.Equal(t, "Payment successful", capturedReq.GatewayMessage)
	assert.NotNil(t, capturedReq.IdempotencyKey)
}

func TestWebhookHandler_HandleWebhook_InvalidSignature(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("correct-secret"),
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		Timestamp:          time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	// Generate signature with wrong secret
	wrongSignature := GenerateWebhookSignature(body, []byte("wrong-secret"))

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, wrongSignature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrInvalidSignature.Error(), resp.Error)
}

func TestWebhookHandler_HandleWebhook_MissingSignature(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		Timestamp:          time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No signature header

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrMissingSignature.Error(), resp.Error)
}

func TestWebhookHandler_HandleWebhook_InvalidJSON(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          secret,
	})
	require.NoError(t, err)

	body := []byte("not valid json")
	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
}

func TestWebhookHandler_HandleWebhook_MissingReferenceID(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          secret,
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		// No GatewayReferenceID or PaymentOrderID
		Status:    "Settled",
		Timestamp: time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrMissingReferenceID.Error(), resp.Error)
}

func TestWebhookHandler_HandleWebhook_InvalidGatewayStatus(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          secret,
	})
	require.NoError(t, err)

	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "INVALID_STATUS",
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

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrInvalidGatewayStatus.Error(), resp.Error)
}

func TestWebhookHandler_HandleWebhook_MethodNotAllowed(t *testing.T) {
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/webhook/payment-gateway", nil)
	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestMapGatewayStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected pb.GatewayStatus
		wantErr  bool
	}{
		{"Settled", "Settled", pb.GatewayStatus_GATEWAY_STATUS_SETTLED, false},
		{"SETTLED", "SETTLED", pb.GatewayStatus_GATEWAY_STATUS_SETTLED, false},
		{"settled", "settled", pb.GatewayStatus_GATEWAY_STATUS_SETTLED, false},
		{"Rejected", "Rejected", pb.GatewayStatus_GATEWAY_STATUS_REJECTED, false},
		{"REJECTED", "REJECTED", pb.GatewayStatus_GATEWAY_STATUS_REJECTED, false},
		{"rejected", "rejected", pb.GatewayStatus_GATEWAY_STATUS_REJECTED, false},
		{"Pending", "Pending", pb.GatewayStatus_GATEWAY_STATUS_PENDING, false},
		{"PENDING", "PENDING", pb.GatewayStatus_GATEWAY_STATUS_PENDING, false},
		{"pending", "pending", pb.GatewayStatus_GATEWAY_STATUS_PENDING, false},
		{"unknown_status", "Unknown", pb.GatewayStatus_GATEWAY_STATUS_UNSPECIFIED, true},
		{"empty_status", "", pb.GatewayStatus_GATEWAY_STATUS_UNSPECIFIED, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := mapGatewayStatus(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGenerateWebhookSignature(t *testing.T) {
	body := []byte(`{"gateway_reference_id":"test-123","status":"Settled"}`)
	secret := []byte("my-secret-key")

	sig1 := GenerateWebhookSignature(body, secret)
	sig2 := GenerateWebhookSignature(body, secret)

	// Same input should produce same signature
	assert.Equal(t, sig1, sig2)

	// Different secret should produce different signature
	sig3 := GenerateWebhookSignature(body, []byte("different-secret"))
	assert.NotEqual(t, sig1, sig3)

	// Different body should produce different signature
	sig4 := GenerateWebhookSignature([]byte(`{"other":"data"}`), secret)
	assert.NotEqual(t, sig1, sig4)
}

func TestNewWebhookRequest(t *testing.T) {
	req := NewWebhookRequest("gw-123", "po-456", "Settled", "Success")

	assert.Equal(t, "gw-123", req.GatewayReferenceID)
	assert.Equal(t, "po-456", req.PaymentOrderID)
	assert.Equal(t, "Settled", req.Status)
	assert.Equal(t, "Success", req.Message)
	assert.False(t, req.Timestamp.IsZero())
}

func TestWebhookHandler_HandleWebhook_GatewayIdempotencyKey(t *testing.T) {
	secret := []byte("test-secret-key")
	gatewayIdempotencyKey := "gateway-provided-key-123"

	var capturedReq *pb.UpdatePaymentOrderRequest
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{
			updateFunc: func(_ context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
				capturedReq = req
				return &pb.UpdatePaymentOrderResponse{
					PaymentOrder: &pb.PaymentOrder{
						PaymentOrderId: "test-order-id",
						Status:         pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					},
				}, nil
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
	req.Header.Set(IdempotencyKeyHeader, gatewayIdempotencyKey)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify the gateway-provided idempotency key was used
	require.NotNil(t, capturedReq)
	require.NotNil(t, capturedReq.IdempotencyKey)
	assert.Equal(t, gatewayIdempotencyKey, capturedReq.IdempotencyKey.Key)
}

func TestWebhookHandler_HandleWebhook_ExpiredTimestamp(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          secret,
	})
	require.NoError(t, err)

	// Create a webhook request with an old timestamp (10 minutes ago)
	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-ref-123",
		Status:             "Settled",
		Timestamp:          time.Now().Add(-10 * time.Minute),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment-gateway", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp WebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrTimestampExpired.Error(), resp.Error)
}
