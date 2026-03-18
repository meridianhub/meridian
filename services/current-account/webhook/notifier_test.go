package webhook

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockURLProvider implements URLProvider for testing.
type mockURLProvider struct {
	urls map[string]string
	err  error
}

func (m *mockURLProvider) GetWebhookURL(_ context.Context, tenantID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.urls[tenantID], nil
}

// mockDeliveryRecorder implements DeliveryRecorder for testing.
type mockDeliveryRecorder struct {
	records []*DeliveryRecord
}

func (m *mockDeliveryRecorder) RecordDelivery(_ context.Context, record *DeliveryRecord) error {
	// Make a copy to preserve state at time of recording
	recordCopy := *record
	m.records = append(m.records, &recordCopy)
	return nil
}

// tlsClient returns an HTTP client that trusts test server certificates.
func tlsClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // Test server uses self-signed cert
			},
		},
	}
}

func TestHTTPNotifier_NotifyAccountFrozen_Success(t *testing.T) {
	// Create TLS test server that returns 200 OK
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Meridian-Webhook/1.0", r.Header.Get("User-Agent"))

		var payload Payload
		err := json.NewDecoder(r.Body).Decode(&payload)
		require.NoError(t, err)

		assert.Equal(t, EventTypeAccountFrozen, payload.EventType)
		assert.Equal(t, "account-123", payload.AccountID)
		assert.Equal(t, "tenant-456", payload.TenantID)
		assert.Equal(t, "suspicious activity", payload.Reason)
		assert.NotEmpty(t, payload.EventID)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urlProvider := &mockURLProvider{
		urls: map[string]string{
			"tenant-456": server.URL,
		},
	}

	recorder := &mockDeliveryRecorder{}

	notifier := NewHTTPNotifier(Config{
		URLProvider:      urlProvider,
		DeliveryRecorder: recorder,
		MaxRetries:       3,
		RequestTimeout:   5 * time.Second,
		HTTPClient:       tlsClient(),
	})

	ctx := context.Background()
	err := notifier.NotifyAccountFrozen(ctx, "tenant-456", "account-123", "suspicious activity", time.Now())

	assert.NoError(t, err)
	assert.Len(t, recorder.records, 2) // pending + success

	// Verify final record is success
	finalRecord := recorder.records[len(recorder.records)-1]
	assert.Equal(t, DeliveryStatusSuccess, finalRecord.Status)
	assert.Equal(t, 1, finalRecord.Attempts)
	assert.NotNil(t, finalRecord.CompletedAt)
}

func TestHTTPNotifier_NotifyAccountClosed_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload Payload
		err := json.NewDecoder(r.Body).Decode(&payload)
		require.NoError(t, err)

		assert.Equal(t, EventTypeAccountClosed, payload.EventType)
		assert.Equal(t, "account-789", payload.AccountID)
		assert.NotNil(t, payload.FinalBalance)
		assert.Equal(t, int64(0), payload.FinalBalance.Amount)
		assert.Equal(t, "GBP", payload.FinalBalance.CurrencyCode)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urlProvider := &mockURLProvider{
		urls: map[string]string{
			"tenant-456": server.URL,
		},
	}

	notifier := NewHTTPNotifier(Config{
		URLProvider: urlProvider,
		HTTPClient:  tlsClient(),
	})

	ctx := context.Background()
	balance := &BalanceInfo{Amount: 0, CurrencyCode: "GBP"}
	err := notifier.NotifyAccountClosed(ctx, "tenant-456", "account-789", "customer request", balance, time.Now())

	assert.NoError(t, err)
}

func TestHTTPNotifier_NoWebhookURL_SkipsSilently(t *testing.T) {
	urlProvider := &mockURLProvider{
		urls: map[string]string{}, // No webhook URL for any tenant
	}

	recorder := &mockDeliveryRecorder{}

	notifier := NewHTTPNotifier(Config{
		URLProvider:      urlProvider,
		DeliveryRecorder: recorder,
	})

	ctx := context.Background()
	err := notifier.NotifyAccountFrozen(ctx, "tenant-no-webhook", "account-123", "reason", time.Now())

	// Should succeed silently (not an error)
	assert.NoError(t, err)
	// No delivery records should be created
	assert.Empty(t, recorder.records)
}

func TestHTTPNotifier_RetryOnServerError(t *testing.T) {
	var attemptCount atomic.Int32

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := attemptCount.Add(1)
		if count < 3 {
			// First two attempts fail with 500
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Third attempt succeeds
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urlProvider := &mockURLProvider{
		urls: map[string]string{
			"tenant-456": server.URL,
		},
	}

	recorder := &mockDeliveryRecorder{}

	notifier := NewHTTPNotifier(Config{
		URLProvider:      urlProvider,
		DeliveryRecorder: recorder,
		MaxRetries:       3,
		HTTPClient:       tlsClient(),
		// Use short delays for testing
		RetryDelays: []time.Duration{
			10 * time.Millisecond,
			10 * time.Millisecond,
			10 * time.Millisecond,
		},
	})

	ctx := context.Background()
	err := notifier.NotifyAccountFrozen(ctx, "tenant-456", "account-123", "reason", time.Now())

	assert.NoError(t, err)
	assert.Equal(t, int32(3), attemptCount.Load())

	// Verify final record shows success after retries
	finalRecord := recorder.records[len(recorder.records)-1]
	assert.Equal(t, DeliveryStatusSuccess, finalRecord.Status)
	assert.Equal(t, 3, finalRecord.Attempts)
}

func TestHTTPNotifier_FailAfterMaxRetries(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Always fail
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	urlProvider := &mockURLProvider{
		urls: map[string]string{
			"tenant-456": server.URL,
		},
	}

	recorder := &mockDeliveryRecorder{}

	notifier := NewHTTPNotifier(Config{
		URLProvider:      urlProvider,
		DeliveryRecorder: recorder,
		MaxRetries:       2,
		HTTPClient:       tlsClient(),
		RetryDelays: []time.Duration{
			10 * time.Millisecond,
			10 * time.Millisecond,
		},
	})

	ctx := context.Background()
	err := notifier.NotifyAccountFrozen(ctx, "tenant-456", "account-123", "reason", time.Now())

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrWebhookDeliveryFailed)

	// Verify final record shows failure
	finalRecord := recorder.records[len(recorder.records)-1]
	assert.Equal(t, DeliveryStatusFailed, finalRecord.Status)
	assert.Equal(t, 3, finalRecord.Attempts) // Initial + 2 retries
	assert.NotNil(t, finalRecord.CompletedAt)
}

func TestHTTPNotifier_ContextCancellation(t *testing.T) {
	// Server that waits longer than the context timeout
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Intentional sleep: Simulate slow server to test context timeout handling
		time.Sleep(1 * time.Second) //nolint:forbidigo // simulates slow server response to trigger context timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	urlProvider := &mockURLProvider{
		urls: map[string]string{
			"tenant-456": server.URL,
		},
	}

	notifier := NewHTTPNotifier(Config{
		URLProvider:    urlProvider,
		RequestTimeout: 50 * time.Millisecond,
		HTTPClient:     tlsClient(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := notifier.NotifyAccountFrozen(ctx, "tenant-456", "account-123", "reason", time.Now())

	// Should fail due to timeout
	assert.Error(t, err)
}

func TestNoOpNotifier(t *testing.T) {
	notifier := &NoOpNotifier{}

	ctx := context.Background()

	// Both methods should return nil without doing anything
	err := notifier.NotifyAccountFrozen(ctx, "tenant", "account", "reason", time.Now())
	assert.NoError(t, err)

	err = notifier.NotifyAccountClosed(ctx, "tenant", "account", "reason", nil, time.Now())
	assert.NoError(t, err)
}

func TestHTTPNotifier_RejectsHTTPURLs(t *testing.T) {
	// Use HTTP URL (not HTTPS) - should be rejected
	urlProvider := &mockURLProvider{
		urls: map[string]string{
			"tenant-456": "http://example.com/webhook",
		},
	}

	recorder := &mockDeliveryRecorder{}

	notifier := NewHTTPNotifier(Config{
		URLProvider:      urlProvider,
		DeliveryRecorder: recorder,
	})

	ctx := context.Background()
	err := notifier.NotifyAccountFrozen(ctx, "tenant-456", "account-123", "reason", time.Now())

	// Should fail because HTTP URLs are not allowed
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsecureWebhookURL)
	// No delivery records should be created for invalid URLs
	assert.Empty(t, recorder.records)
}

func TestHTTPNotifier_DefaultConfig(t *testing.T) {
	urlProvider := &mockURLProvider{
		urls: map[string]string{},
	}

	notifier := NewHTTPNotifier(Config{
		URLProvider: urlProvider,
	})

	// Verify defaults were applied
	assert.Equal(t, 3, notifier.maxRetries)
	assert.Len(t, notifier.retryDelays, 3)
	assert.Equal(t, 1*time.Second, notifier.retryDelays[0])
	assert.Equal(t, 2*time.Second, notifier.retryDelays[1])
	assert.Equal(t, 4*time.Second, notifier.retryDelays[2])
	assert.NotNil(t, notifier.httpClient)
	assert.NotNil(t, notifier.logger)
}
