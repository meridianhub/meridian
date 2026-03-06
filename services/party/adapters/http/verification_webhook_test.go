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

	"github.com/meridianhub/meridian/services/party/service"
)

// mockVerificationService implements VerificationUpdater for testing.
type mockVerificationService struct {
	updateFunc func(ctx context.Context, req service.UpdateVerificationRequest) error
	calls      []service.UpdateVerificationRequest
}

func (m *mockVerificationService) UpdateVerification(ctx context.Context, req service.UpdateVerificationRequest) error {
	m.calls = append(m.calls, req)
	if m.updateFunc != nil {
		return m.updateFunc(ctx, req)
	}
	return nil
}

func TestNewVerificationWebhookHandler(t *testing.T) {
	tests := []struct {
		name    string
		cfg     VerificationWebhookHandlerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with single provider",
			cfg: VerificationWebhookHandlerConfig{
				VerificationService: &mockVerificationService{},
				HMACSecrets:         map[string][]byte{"default": []byte("secret")},
			},
			wantErr: false,
		},
		{
			name: "valid config with multiple providers",
			cfg: VerificationWebhookHandlerConfig{
				VerificationService: &mockVerificationService{},
				HMACSecrets: map[string][]byte{
					"onfido": []byte("onfido-secret"),
					"stripe": []byte("stripe-secret"),
				},
			},
			wantErr: false,
		},
		{
			name: "nil verification service",
			cfg: VerificationWebhookHandlerConfig{
				VerificationService: nil,
				HMACSecrets:         map[string][]byte{"default": []byte("secret")},
			},
			wantErr: true,
		},
		{
			name: "empty HMAC secrets map",
			cfg: VerificationWebhookHandlerConfig{
				VerificationService: &mockVerificationService{},
				HMACSecrets:         map[string][]byte{},
			},
			wantErr: true,
		},
		{
			name: "empty secret value for provider",
			cfg: VerificationWebhookHandlerConfig{
				VerificationService: &mockVerificationService{},
				HMACSecrets:         map[string][]byte{"onfido": {}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewVerificationWebhookHandler(tt.cfg)
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

func TestVerificationWebhookHandler_HandleWebhook_Success(t *testing.T) {
	secret := []byte("test-secret-key")
	riskScore := 0.25

	mockService := &mockVerificationService{}
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		RiskScore:      &riskScore,
		Reason:         "Identity verified successfully",
		Timestamp:      time.Now().UTC(),
		Metadata:       map[string]string{"check_id": "chk-abc"},
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp.Acknowledged)
	assert.NotEmpty(t, resp.Message)

	// Verify the service was called with correct parameters
	require.Len(t, mockService.calls, 1)
	assert.Equal(t, "verify-123", mockService.calls[0].ProviderVerificationID)
	assert.Equal(t, "APPROVED", mockService.calls[0].Status)
	assert.NotNil(t, mockService.calls[0].RiskScore)
	assert.Equal(t, 0.25, *mockService.calls[0].RiskScore)
	require.NotNil(t, mockService.calls[0].Reason)
	assert.Equal(t, "Identity verified successfully", *mockService.calls[0].Reason)
}

func TestVerificationWebhookHandler_HandleWebhook_InvalidSignature(t *testing.T) {
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": []byte("correct-secret")},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	// Generate signature with wrong secret
	wrongSignature := GenerateWebhookSignature(body, []byte("wrong-secret"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, wrongSignature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrInvalidSignature.Error(), resp.Error)
}

func TestVerificationWebhookHandler_HandleWebhook_MissingSignature(t *testing.T) {
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": []byte("secret")},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No signature header

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrMissingSignature.Error(), resp.Error)
}

func TestVerificationWebhookHandler_HandleWebhook_VerificationNotFound(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{
			updateFunc: func(_ context.Context, _ service.UpdateVerificationRequest) error {
				return ErrVerificationNotFound
			},
		},
		HMACSecrets: map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "unknown-verify-id",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrVerificationNotFound.Error(), resp.Error)
}

func TestVerificationWebhookHandler_HandleWebhook_IdempotentDuplicateDelivery(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{
			updateFunc: func(_ context.Context, _ service.UpdateVerificationRequest) error {
				// Simulate verification already in terminal state
				return service.ErrVerificationAlreadyCompleted
			},
		},
		HMACSecrets: map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should return 200 OK for idempotent handling
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp.Acknowledged)
	assert.Contains(t, resp.Message, "already processed")
}

func TestVerificationWebhookHandler_HandleWebhook_InvalidJSON(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	body := []byte("not valid json")
	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
}

func TestVerificationWebhookHandler_HandleWebhook_MissingVerificationID(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		// No VerificationID
		Status:    "APPROVED",
		Timestamp: time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrMissingVerificationID.Error(), resp.Error)
}

func TestVerificationWebhookHandler_HandleWebhook_InvalidStatus(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "INVALID_STATUS",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrInvalidVerificationStatus.Error(), resp.Error)
}

func TestVerificationWebhookHandler_HandleWebhook_MethodNotAllowed(t *testing.T) {
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": []byte("secret")},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/webhooks/verification/default", nil)
	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestVerificationWebhookHandler_HandleWebhook_MultipleProviders(t *testing.T) {
	onfidoSecret := []byte("onfido-secret")
	stripeSecret := []byte("stripe-secret")

	mockService := &mockVerificationService{}
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets: map[string][]byte{
			"onfido": onfidoSecret,
			"stripe": stripeSecret,
		},
	})
	require.NoError(t, err)

	// Test with onfido provider
	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-onfido",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, onfidoSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/onfido", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Test with stripe provider - should fail with onfido signature
	body2, _ := json.Marshal(webhookReq)
	wrongSig := GenerateWebhookSignature(body2, onfidoSecret) // Using wrong secret

	req2 := httptest.NewRequest(http.MethodPost, "/webhooks/verification/stripe", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set(WebhookSignatureHeader, wrongSig)

	rr2 := httptest.NewRecorder()
	handler.HandleWebhook(rr2, req2)

	assert.Equal(t, http.StatusUnauthorized, rr2.Code)
}

func TestVerificationWebhookHandler_HandleWebhook_ProviderFromHeader(t *testing.T) {
	secret := []byte("test-secret")
	mockService := &mockVerificationService{}
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets:         map[string][]byte{"onfido": secret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	// Use a path without provider, but set provider header
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)
	req.Header.Set(ProviderHeader, "onfido")

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestVerificationWebhookHandler_HandleWebhook_ExpiredTimestamp(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	// Create a webhook request with an old timestamp (10 minutes ago)
	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().Add(-10 * time.Minute),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Contains(t, resp.Error, "expired")
}

func TestVerificationWebhookHandler_HandleWebhook_FutureTimestamp(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	// Create a webhook request with a future timestamp (1 hour in the future)
	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().Add(1 * time.Hour),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Contains(t, resp.Error, "future")
}

func TestVerificationWebhookHandler_HandleWebhook_ServiceError_Returns500(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{
			updateFunc: func(_ context.Context, _ service.UpdateVerificationRequest) error {
				return ErrVerificationServiceError
			},
		},
		HMACSecrets: map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// 500 triggers retry from provider
	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	var resp VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Equal(t, ErrVerificationServiceError.Error(), resp.Error)
}

func TestGenerateWebhookSignature(t *testing.T) {
	body := []byte(`{"verification_id":"test-123","status":"APPROVED"}`)
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

func TestNewVerificationWebhookRequest(t *testing.T) {
	riskScore := 0.5
	req := NewVerificationWebhookRequest("verify-123", "APPROVED", &riskScore, "Identity verified")

	assert.Equal(t, "verify-123", req.VerificationID)
	assert.Equal(t, "APPROVED", req.Status)
	assert.NotNil(t, req.RiskScore)
	assert.Equal(t, 0.5, *req.RiskScore)
	assert.Equal(t, "Identity verified", req.Reason)
	assert.False(t, req.Timestamp.IsZero())
}

func TestExtractProvider(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/webhooks/verification/onfido", "onfido"},
		{"/webhooks/verification/stripe", "stripe"},
		{"/webhooks/verification/onfido/", "onfido"},
		{"/api/v1/webhooks/verification/provider", "provider"},
		{"/webhooks/verification", ""},
		{"/webhooks", ""},
		{"/", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := extractProvider(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVerificationWebhookHandler_HandleWebhook_CaseInsensitiveStatus(t *testing.T) {
	secret := []byte("test-secret")
	mockService := &mockVerificationService{}
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	// Test lowercase status
	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "approved", // lowercase
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, mockService.calls, 1)
	assert.Equal(t, "APPROVED", mockService.calls[0].Status) // Should be normalized to uppercase
}

func TestVerificationWebhookHandler_HandleWebhook_AllValidStatuses(t *testing.T) {
	secret := []byte("test-secret")

	statuses := []string{"PENDING", "APPROVED", "REJECTED", "MANUAL_REVIEW"}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			mockService := &mockVerificationService{}
			handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
				VerificationService: mockService,
				HMACSecrets:         map[string][]byte{"default": secret},
			})
			require.NoError(t, err)

			webhookReq := VerificationWebhookRequest{
				VerificationID: "verify-123",
				Status:         status,
				Timestamp:      time.Now().UTC(),
			}
			body, err := json.Marshal(webhookReq)
			require.NoError(t, err)

			signature := GenerateWebhookSignature(body, secret)

			req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(WebhookSignatureHeader, signature)

			rr := httptest.NewRecorder()
			handler.HandleWebhook(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code, "status %s should be valid", status)
		})
	}
}

func TestVerificationWebhookHandler_HandleWebhook_UnknownProvider_FallsBackToDefault(t *testing.T) {
	defaultSecret := []byte("default-secret")
	mockService := &mockVerificationService{}
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets:         map[string][]byte{"default": defaultSecret},
	})
	require.NoError(t, err)

	webhookReq := VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	// Sign with default secret
	signature := GenerateWebhookSignature(body, defaultSecret)

	// Use unknown provider in path
	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/unknown-provider", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should fall back to default secret and succeed
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestConstantTimeComparison verifies that HMAC comparison uses constant-time comparison.
// This is a security test to ensure timing attacks are prevented.
// Note: This test provides a basic check but may not catch all timing vulnerabilities.
func TestConstantTimeComparison(t *testing.T) {
	secret := []byte("test-secret")
	handler, err := NewVerificationWebhookHandler(VerificationWebhookHandlerConfig{
		VerificationService: &mockVerificationService{},
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	body := []byte(`{"verification_id":"test","status":"APPROVED","timestamp":"2024-01-01T00:00:00Z"}`)
	correctSig := GenerateWebhookSignature(body, secret)

	// Test with correct signature
	result := handler.validateSignature(body, correctSig, secret)
	assert.True(t, result)

	// Test with completely wrong signature (same length)
	wrongSig := "0000000000000000000000000000000000000000000000000000000000000000"
	result = handler.validateSignature(body, wrongSig, secret)
	assert.False(t, result)

	// Test with partially matching signature (different in last byte)
	partialSig := correctSig[:len(correctSig)-2] + "00"
	result = handler.validateSignature(body, partialSig, secret)
	assert.False(t, result)

	// Test with invalid hex
	result = handler.validateSignature(body, "not-hex", secret)
	assert.False(t, result)
}
