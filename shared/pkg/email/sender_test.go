package email_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	resendgo "github.com/resend/resend-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/email"
)

// --- NoopSender ---

func TestNoopSender_Send_ReturnsResult(t *testing.T) {
	s := email.NewNoopSender()
	result, err := s.Send(context.Background(), email.Message{
		To:      []string{"user@example.com"},
		From:    "noreply@example.com",
		Subject: "Hello",
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(result.ProviderID, "noop-"))
	assert.False(t, result.SentAt.IsZero())
}

// --- LogSender ---

func TestLogSender_Send_ReturnsResult(t *testing.T) {
	s := email.NewLogSender(nil)
	result, err := s.Send(context.Background(), email.Message{
		To:             []string{"user@example.com"},
		From:           "noreply@example.com",
		Subject:        "Hello",
		IdempotencyKey: "idem-123",
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(result.ProviderID, "log-"))
	assert.False(t, result.SentAt.IsZero())
}

// --- ResendSender ---

func TestResendSender_Send_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/emails", r.URL.Path)
		assert.Equal(t, "test-idem-key", r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"id": "provider-abc"})
	}))
	defer srv.Close()

	t.Setenv("RESEND_BASE_URL", srv.URL+"/")
	c := resendgo.NewClient("test-api-key")
	_ = c // just validating SDK initializes; use our helper below

	s := email.NewResendSenderWithBaseURL("test-api-key", nil, srv.URL+"/")
	result, err := s.Send(context.Background(), email.Message{
		To:             []string{"user@example.com"},
		From:           "noreply@example.com",
		Subject:        "Test",
		IdempotencyKey: "test-idem-key",
	})
	require.NoError(t, err)
	assert.Equal(t, "provider-abc", result.ProviderID)
	assert.False(t, result.SentAt.IsZero())
}

func TestResendSender_Send_NoIdempotencyKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"id": "provider-xyz"})
	}))
	defer srv.Close()

	s := email.NewResendSenderWithBaseURL("test-api-key", nil, srv.URL+"/")
	result, err := s.Send(context.Background(), email.Message{
		To:      []string{"user@example.com"},
		From:    "noreply@example.com",
		Subject: "Test",
	})
	require.NoError(t, err)
	assert.Equal(t, "provider-xyz", result.ProviderID)
}

func TestResendSender_Send_ServerError_TripsCircuitBreaker(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := email.NewResendSenderWithBaseURL("test-api-key", nil, srv.URL+"/")
	msg := email.Message{
		To:      []string{"user@example.com"},
		From:    "noreply@example.com",
		Subject: "Test",
	}

	// 5 consecutive failures should trip the circuit breaker
	for i := 0; i < 5; i++ {
		_, err := s.Send(context.Background(), msg)
		require.Error(t, err)
	}

	// Next call should be rejected by circuit breaker (open state) without hitting server
	_, err := s.Send(context.Background(), msg)
	require.Error(t, err)
	assert.Equal(t, 5, callCount, "circuit breaker should stop calls after 5 failures")
}
