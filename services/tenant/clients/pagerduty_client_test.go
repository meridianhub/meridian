package clients

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapAlertSeverity(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected Severity
	}{
		{"critical uppercase", "CRITICAL", SeverityCritical},
		{"critical lowercase", "critical", SeverityCritical},
		{"critical mixed case", "Critical", SeverityCritical},
		{"warning uppercase", "WARNING", SeverityWarning},
		{"warning lowercase", "warning", SeverityWarning},
		{"info uppercase", "INFO", SeverityInfo},
		{"info lowercase", "info", SeverityInfo},
		{"error uppercase", "ERROR", SeverityError},
		{"error lowercase", "error", SeverityError},
		{"unknown defaults to warning", "unknown", SeverityWarning},
		{"empty defaults to warning", "", SeverityWarning},
		{"random string defaults to warning", "foobar", SeverityWarning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapAlertSeverity(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewPagerDutyClient_Disabled(t *testing.T) {
	cfg := config.PagerDutyConfig{
		Enabled:    false,
		RoutingKey: "test-key",
		Source:     "test-source",
	}

	client := NewPagerDutyClient(cfg)

	assert.Nil(t, client, "client should be nil when PagerDuty is disabled")
}

func TestNewPagerDutyClient_Enabled(t *testing.T) {
	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-routing-key",
		Source:     "test-source",
	}

	client := NewPagerDutyClient(cfg)

	require.NotNil(t, client)
	assert.True(t, client.IsEnabled())
	assert.Equal(t, cfg.RoutingKey, client.config.RoutingKey)
	assert.Equal(t, cfg.Source, client.config.Source)
}

func TestPagerDutyClient_IsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		client   *PagerDutyClient
		expected bool
	}{
		{
			name:     "nil client",
			client:   nil,
			expected: false,
		},
		{
			name: "disabled config",
			client: &PagerDutyClient{
				config: config.PagerDutyConfig{Enabled: false},
			},
			expected: false,
		},
		{
			name: "enabled config",
			client: &PagerDutyClient{
				config: config.PagerDutyConfig{Enabled: true},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.client.IsEnabled()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPagerDutyClient_TriggerAlert_NilClient(t *testing.T) {
	var client *PagerDutyClient

	err := client.TriggerAlert(context.Background(), "test summary", "test-key", SeverityCritical, nil)

	assert.ErrorIs(t, err, ErrPagerDutyNotConfigured)
}

func TestPagerDutyClient_ResolveAlert_NilClient(t *testing.T) {
	var client *PagerDutyClient

	err := client.ResolveAlert(context.Background(), "test-key")

	assert.ErrorIs(t, err, ErrPagerDutyNotConfigured)
}

func TestPagerDutyClient_TriggerAlert_Success(t *testing.T) {
	// Create test server
	var receivedEvent PagerDutyEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Parse request body
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		err = json.Unmarshal(body, &receivedEvent)
		require.NoError(t, err)

		// Send success response
		w.WriteHeader(http.StatusAccepted)
		resp := PagerDutyResponse{
			Status:   "success",
			Message:  "Event processed",
			DedupKey: receivedEvent.DedupKey,
		}
		respBody, _ := json.Marshal(resp)
		w.Write(respBody)
	}))
	defer server.Close()

	// Create client with test server
	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-routing-key",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	// Send alert
	customDetails := map[string]any{
		"tenant_id": "tenant-123",
		"status":    "provisioning_failed",
	}
	err := client.TriggerAlert(context.Background(), "Test alert summary", "dedup-key-123", SeverityCritical, customDetails)

	// Verify
	require.NoError(t, err)
	assert.Equal(t, "test-routing-key", receivedEvent.RoutingKey)
	assert.Equal(t, EventActionTrigger, receivedEvent.EventAction)
	assert.Equal(t, "dedup-key-123", receivedEvent.DedupKey)
	assert.Equal(t, "Test alert summary", receivedEvent.Payload.Summary)
	assert.Equal(t, "critical", receivedEvent.Payload.Severity)
	assert.Equal(t, "test-source", receivedEvent.Payload.Source)
	assert.NotEmpty(t, receivedEvent.Payload.Timestamp)
	assert.Equal(t, "tenant-123", receivedEvent.Payload.CustomDetails["tenant_id"])
	assert.Equal(t, "provisioning_failed", receivedEvent.Payload.CustomDetails["status"])
}

func TestPagerDutyClient_TriggerAlert_PayloadFormat(t *testing.T) {
	// Create test server that captures the raw JSON
	var rawPayload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		rawPayload, err = io.ReadAll(r.Body)
		require.NoError(t, err)

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"success","message":"Event processed"}`))
	}))
	defer server.Close()

	// Create client
	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "routing-key-123",
		Source:     "meridian-prod",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	// Send alert
	err := client.TriggerAlert(context.Background(), "Summary text", "my-dedup-key", SeverityWarning, nil)
	require.NoError(t, err)

	// Verify JSON structure matches PagerDuty Events API v2 spec
	var parsed map[string]any
	err = json.Unmarshal(rawPayload, &parsed)
	require.NoError(t, err)

	// Required top-level fields
	assert.Equal(t, "routing-key-123", parsed["routing_key"])
	assert.Equal(t, "trigger", parsed["event_action"])
	assert.Equal(t, "my-dedup-key", parsed["dedup_key"])

	// Payload structure
	payload, ok := parsed["payload"].(map[string]any)
	require.True(t, ok, "payload should be an object")
	assert.Equal(t, "Summary text", payload["summary"])
	assert.Equal(t, "warning", payload["severity"])
	assert.Equal(t, "meridian-prod", payload["source"])

	// Timestamp should be RFC3339 formatted
	timestamp, ok := payload["timestamp"].(string)
	require.True(t, ok, "timestamp should be a string")
	_, err = time.Parse(time.RFC3339, timestamp)
	assert.NoError(t, err, "timestamp should be RFC3339 formatted")
}

func TestPagerDutyClient_ResolveAlert_Success(t *testing.T) {
	var receivedEvent PagerDutyEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		err = json.Unmarshal(body, &receivedEvent)
		require.NoError(t, err)

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"success","message":"Event processed"}`))
	}))
	defer server.Close()

	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-routing-key",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	err := client.ResolveAlert(context.Background(), "resolve-dedup-key")

	require.NoError(t, err)
	assert.Equal(t, EventActionResolve, receivedEvent.EventAction)
	assert.Equal(t, "resolve-dedup-key", receivedEvent.DedupKey)
	assert.Equal(t, "test-routing-key", receivedEvent.RoutingKey)
}

func TestPagerDutyClient_TriggerAlert_BadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"invalid event","message":"Missing routing_key"}`))
	}))
	defer server.Close()

	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	err := client.TriggerAlert(context.Background(), "Test", "key", SeverityCritical, nil)

	assert.ErrorIs(t, err, ErrPagerDutyInvalidRequest)
	assert.Contains(t, err.Error(), "Missing routing_key")
}

func TestPagerDutyClient_TriggerAlert_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":"rate limited","message":"Too many requests"}`))
	}))
	defer server.Close()

	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-key",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	err := client.TriggerAlert(context.Background(), "Test", "key", SeverityCritical, nil)

	assert.ErrorIs(t, err, ErrPagerDutyRateLimited)
}

func TestPagerDutyClient_TriggerAlert_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","message":"Internal server error"}`))
	}))
	defer server.Close()

	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-key",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	err := client.TriggerAlert(context.Background(), "Test", "key", SeverityCritical, nil)

	assert.ErrorIs(t, err, ErrPagerDutyAPIError)
	assert.Contains(t, err.Error(), "500")
}

func TestPagerDutyClient_TriggerAlert_NetworkError(t *testing.T) {
	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-key",
		Source:     "test-source",
	}
	// Use a URL that will fail to connect
	client := NewPagerDutyClient(cfg, WithEventsURL("http://localhost:1"))

	err := client.TriggerAlert(context.Background(), "Test", "key", SeverityCritical, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send PagerDuty event")
}

func TestPagerDutyClient_TriggerAlert_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Delay response to allow context cancellation
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-key",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.TriggerAlert(ctx, "Test", "key", SeverityCritical, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestPagerDutyClient_TriggerAlert_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`not valid json`))
	}))
	defer server.Close()

	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-key",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithEventsURL(server.URL))

	err := client.TriggerAlert(context.Background(), "Test", "key", SeverityCritical, nil)

	assert.ErrorIs(t, err, ErrPagerDutyAPIError)
	assert.Contains(t, err.Error(), "not valid json")
}

func TestPagerDutyClient_WithCustomHTTPClient(t *testing.T) {
	customClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	cfg := config.PagerDutyConfig{
		Enabled:    true,
		RoutingKey: "test-key",
		Source:     "test-source",
	}
	client := NewPagerDutyClient(cfg, WithHTTPClient(customClient))

	require.NotNil(t, client)
	assert.Equal(t, customClient, client.httpClient)
}

func TestPagerDutyClient_SeverityConstants(t *testing.T) {
	// Verify severity constants match PagerDuty API values
	assert.Equal(t, Severity("critical"), SeverityCritical)
	assert.Equal(t, Severity("warning"), SeverityWarning)
	assert.Equal(t, Severity("info"), SeverityInfo)
	assert.Equal(t, Severity("error"), SeverityError)
}

func TestPagerDutyClient_EventActionConstants(t *testing.T) {
	// Verify event action constants match PagerDuty API values
	assert.Equal(t, "trigger", EventActionTrigger)
	assert.Equal(t, "acknowledge", EventActionAcknowledge)
	assert.Equal(t, "resolve", EventActionResolve)
}
