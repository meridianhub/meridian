package verification_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	partyhttp "github.com/meridianhub/meridian/services/party/adapters/http"
	"github.com/meridianhub/meridian/services/party/config"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/service"
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// TestProviderFactory_SwitchProviderViaConfig tests that we can switch providers via config change
func TestProviderFactory_SwitchProviderViaConfig(t *testing.T) {
	// Start with mock provider
	mockCfg := &config.VerificationConfig{
		Provider: "mock",
	}

	mockProvider, err := verification.NewProvider(mockCfg)
	require.NoError(t, err)
	require.NotNil(t, mockProvider)

	// Verify mock provider works
	party, err := domain.NewParty(domain.PartyTypePerson, "John Doe")
	require.NoError(t, err)

	result, err := mockProvider.VerifyIdentity(context.Background(), party)
	require.NoError(t, err)
	assert.Equal(t, verification.StatusApproved, result.Status)

	// Attempt unsupported provider (should return error)
	unsupportedCfg := &config.VerificationConfig{
		Provider:       "unsupported",
		WebhookSecret:  "secret",
		WebhookURL:     "https://example.com/webhook",
		ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
	}

	unsupportedProvider, err := verification.NewProvider(unsupportedCfg)
	assert.Nil(t, unsupportedProvider)
	assert.ErrorIs(t, err, verification.ErrUnsupportedProvider)

	// Can switch back to mock with different options
	mockCfg2 := &config.VerificationConfig{
		Provider: "mock",
	}
	opts := verification.ProviderOptions{
		MockAlwaysApprove: false,
		MockAsyncMode:     false,
	}

	mockProvider2, err := verification.NewProviderWithOptions(mockCfg2, opts)
	require.NoError(t, err)

	// This provider should reject verifications
	result2, err := mockProvider2.VerifyIdentity(context.Background(), party)
	require.NoError(t, err)
	assert.Equal(t, verification.StatusRejected, result2.Status)
}

// TestMockProvider_EndToEnd_SyncFlow tests a complete synchronous verification flow
func TestMockProvider_EndToEnd_SyncFlow(t *testing.T) {
	// 1. Create configuration
	cfg := &config.VerificationConfig{
		Provider: "mock",
	}

	// 2. Create provider from factory
	provider, err := verification.NewProvider(cfg)
	require.NoError(t, err)

	// 3. Create a party
	party, err := domain.NewParty(domain.PartyTypePerson, "Jane Smith")
	require.NoError(t, err)

	// 4. Initiate verification
	result, err := provider.VerifyIdentity(context.Background(), party)
	require.NoError(t, err)

	// 5. Verify immediate result (sync mode)
	assert.NotEmpty(t, result.VerificationID)
	assert.Equal(t, verification.StatusApproved, result.Status)
	assert.Equal(t, "Identity verified successfully", result.Reason)
	assert.InDelta(t, 0.1, result.RiskScore, 0.001)
	assert.NotNil(t, result.CompletedAt)

	// 6. Verify status is consistent on subsequent queries
	statusResult, err := provider.GetVerificationStatus(context.Background(), result.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, result.VerificationID, statusResult.VerificationID)
	assert.Equal(t, result.Status, statusResult.Status)

	// 7. Verify sanctions check
	sanctionsResult, err := provider.CheckSanctions(context.Background(), party)
	require.NoError(t, err)
	assert.Equal(t, verification.SanctionsStatusClear, sanctionsResult.Status)
	assert.Empty(t, sanctionsResult.Matches)
}

// TestMockProvider_EndToEnd_AsyncFlow tests a complete async verification with webhook simulation
func TestMockProvider_EndToEnd_AsyncFlow(t *testing.T) {
	ctx := context.Background()

	// 1. Configure async mock provider with short delay
	cfg := &config.VerificationConfig{
		Provider: "mock",
	}
	opts := verification.ProviderOptions{
		MockAlwaysApprove: true,
		MockAsyncMode:     true,
	}

	provider, err := verification.NewProviderWithOptions(cfg, opts)
	require.NoError(t, err)

	// Configure simulated delay on the mock provider
	mockProvider := provider.(*verification.MockProvider)
	mockProvider.WithSimulatedDelay(50 * time.Millisecond)

	// 2. Create a party
	party, err := domain.NewParty(domain.PartyTypePerson, "Bob Johnson")
	require.NoError(t, err)

	// 3. Initiate verification - should return PENDING immediately
	result, err := provider.VerifyIdentity(ctx, party)
	require.NoError(t, err)
	assert.Equal(t, verification.StatusPending, result.Status)
	assert.Nil(t, result.CompletedAt)

	// 4. Poll for completion using await (no time.Sleep!)
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			status, err := provider.GetVerificationStatus(ctx, result.VerificationID)
			if err != nil {
				return false
			}
			return status.Status == verification.StatusApproved
		})
	require.NoError(t, err, "verification should complete within timeout")

	// 5. Verify final status
	finalStatus, err := provider.GetVerificationStatus(ctx, result.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, verification.StatusApproved, finalStatus.Status)
	assert.NotNil(t, finalStatus.CompletedAt)
}

// TestWebhookSecurity_ReplayAttackPrevention tests that old webhooks are rejected
func TestWebhookSecurity_ReplayAttackPrevention(t *testing.T) {
	secret := []byte("test-webhook-secret")

	mockService := &mockWebhookService{}
	handler, err := partyhttp.NewVerificationWebhookHandler(partyhttp.VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	// Create a webhook request with a timestamp from 10 minutes ago
	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().Add(-10 * time.Minute), // Old timestamp
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := partyhttp.GenerateWebhookSignature(body, secret)

	req := httptest.NewRequest("POST", "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(partyhttp.WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should be rejected as expired
	assert.Equal(t, 400, rr.Code)

	var resp partyhttp.VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Contains(t, resp.Error, "expired")

	// Verify service was never called
	assert.Empty(t, mockService.calls)
}

// TestWebhookSecurity_InvalidSignatureRejected tests that tampered webhooks are rejected
func TestWebhookSecurity_InvalidSignatureRejected(t *testing.T) {
	correctSecret := []byte("correct-secret")
	wrongSecret := []byte("wrong-secret")

	mockService := &mockWebhookService{}
	handler, err := partyhttp.NewVerificationWebhookHandler(partyhttp.VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets:         map[string][]byte{"default": correctSecret},
	})
	require.NoError(t, err)

	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	// Sign with wrong secret
	wrongSignature := partyhttp.GenerateWebhookSignature(body, wrongSecret)

	req := httptest.NewRequest("POST", "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(partyhttp.WebhookSignatureHeader, wrongSignature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should be rejected
	assert.Equal(t, 401, rr.Code)

	// Verify service was never called
	assert.Empty(t, mockService.calls)
}

// TestWebhookSecurity_TamperedBodyRejected tests that body modifications invalidate signature
func TestWebhookSecurity_TamperedBodyRejected(t *testing.T) {
	secret := []byte("test-secret")

	mockService := &mockWebhookService{}
	handler, err := partyhttp.NewVerificationWebhookHandler(partyhttp.VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets:         map[string][]byte{"default": secret},
	})
	require.NoError(t, err)

	// Create original request
	originalReq := partyhttp.VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now(),
	}
	originalBody, err := json.Marshal(originalReq)
	require.NoError(t, err)

	// Sign the original body
	signature := partyhttp.GenerateWebhookSignature(originalBody, secret)

	// Tamper with the body (change status)
	tamperedReq := partyhttp.VerificationWebhookRequest{
		VerificationID: "verify-123",
		Status:         "REJECTED", // Changed!
		Timestamp:      time.Now(),
	}
	tamperedBody, err := json.Marshal(tamperedReq)
	require.NoError(t, err)

	// Send tampered body with original signature
	req := httptest.NewRequest("POST", "/webhooks/verification/default", bytes.NewReader(tamperedBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(partyhttp.WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	handler.HandleWebhook(rr, req)

	// Should be rejected
	assert.Equal(t, 401, rr.Code)

	// Verify service was never called
	assert.Empty(t, mockService.calls)
}

// TestMultiProviderWebhooks tests that different providers use different secrets
func TestMultiProviderWebhooks(t *testing.T) {
	onfidoSecret := []byte("onfido-secret-key")
	stripeSecret := []byte("stripe-secret-key")

	mockService := &mockWebhookService{}
	handler, err := partyhttp.NewVerificationWebhookHandler(partyhttp.VerificationWebhookHandlerConfig{
		VerificationService: mockService,
		HMACSecrets: map[string][]byte{
			"onfido": onfidoSecret,
			"stripe": stripeSecret,
		},
	})
	require.NoError(t, err)

	t.Run("onfido webhook with correct secret succeeds", func(t *testing.T) {
		mockService.calls = nil // Reset

		webhookReq := partyhttp.VerificationWebhookRequest{
			VerificationID: "onfido-verify-123",
			Status:         "APPROVED",
			Timestamp:      time.Now(),
		}
		body, _ := json.Marshal(webhookReq)
		signature := partyhttp.GenerateWebhookSignature(body, onfidoSecret)

		req := httptest.NewRequest("POST", "/webhooks/verification/onfido", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(partyhttp.WebhookSignatureHeader, signature)

		rr := httptest.NewRecorder()
		handler.HandleWebhook(rr, req)

		assert.Equal(t, 200, rr.Code)
	})

	t.Run("stripe webhook with correct secret succeeds", func(t *testing.T) {
		mockService.calls = nil // Reset

		webhookReq := partyhttp.VerificationWebhookRequest{
			VerificationID: "stripe-verify-456",
			Status:         "REJECTED",
			Timestamp:      time.Now(),
		}
		body, _ := json.Marshal(webhookReq)
		signature := partyhttp.GenerateWebhookSignature(body, stripeSecret)

		req := httptest.NewRequest("POST", "/webhooks/verification/stripe", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(partyhttp.WebhookSignatureHeader, signature)

		rr := httptest.NewRecorder()
		handler.HandleWebhook(rr, req)

		assert.Equal(t, 200, rr.Code)
	})

	t.Run("onfido webhook with stripe secret fails", func(t *testing.T) {
		mockService.calls = nil // Reset

		webhookReq := partyhttp.VerificationWebhookRequest{
			VerificationID: "cross-provider-attempt",
			Status:         "APPROVED",
			Timestamp:      time.Now(),
		}
		body, _ := json.Marshal(webhookReq)
		// Sign with wrong provider's secret
		wrongSignature := partyhttp.GenerateWebhookSignature(body, stripeSecret)

		req := httptest.NewRequest("POST", "/webhooks/verification/onfido", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(partyhttp.WebhookSignatureHeader, wrongSignature)

		rr := httptest.NewRecorder()
		handler.HandleWebhook(rr, req)

		assert.Equal(t, 401, rr.Code)
	})
}

// TestProviderContract tests that all provider implementations behave consistently
func TestProviderContract(t *testing.T) {
	providers := []struct {
		name         string
		provider     verification.Provider
		expectError  bool
		expectStatus verification.Status
	}{
		{
			name:         "mock_approve",
			provider:     verification.NewMockProvider().WithAlwaysApprove(true),
			expectError:  false,
			expectStatus: verification.StatusApproved,
		},
		{
			name:         "mock_reject",
			provider:     verification.NewMockProvider().WithAlwaysApprove(false),
			expectError:  false,
			expectStatus: verification.StatusRejected,
		},
	}

	for _, tc := range providers {
		t.Run(tc.name+"/VerifyIdentity", func(t *testing.T) {
			party, err := domain.NewParty(domain.PartyTypePerson, "Test Person")
			require.NoError(t, err)

			result, err := tc.provider.VerifyIdentity(context.Background(), party)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectStatus, result.Status)
				assert.NotEmpty(t, result.VerificationID)
				assert.GreaterOrEqual(t, result.RiskScore, 0.0)
				assert.LessOrEqual(t, result.RiskScore, 1.0)
			}
		})

		t.Run(tc.name+"/GetVerificationStatus", func(t *testing.T) {
			party, err := domain.NewParty(domain.PartyTypePerson, "Test Person")
			require.NoError(t, err)

			// First create a verification
			result, err := tc.provider.VerifyIdentity(context.Background(), party)
			require.NoError(t, err)

			// Then query its status
			status, err := tc.provider.GetVerificationStatus(context.Background(), result.VerificationID)
			require.NoError(t, err)
			assert.Equal(t, result.VerificationID, status.VerificationID)
			assert.Equal(t, result.Status, status.Status)
		})

		t.Run(tc.name+"/GetVerificationStatus_NotFound", func(t *testing.T) {
			_, err := tc.provider.GetVerificationStatus(context.Background(), "nonexistent-id")
			assert.ErrorIs(t, err, verification.ErrVerificationNotFound)
		})

		t.Run(tc.name+"/CheckSanctions", func(t *testing.T) {
			party, err := domain.NewParty(domain.PartyTypePerson, "Test Person")
			require.NoError(t, err)

			result, err := tc.provider.CheckSanctions(context.Background(), party)
			require.NoError(t, err)
			assert.NotEmpty(t, result.ScreeningID)
			assert.True(t, result.Status.IsValid())
		})
	}
}

// mockWebhookService implements VerificationUpdater for webhook tests
type mockWebhookService struct {
	calls []service.UpdateVerificationRequest
}

func (m *mockWebhookService) UpdateVerification(_ context.Context, req service.UpdateVerificationRequest) error {
	m.calls = append(m.calls, req)
	return nil
}
