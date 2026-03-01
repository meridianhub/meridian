// Package domain contains tests for the operational-gateway provider connection domain model.
package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProtocolConstants verifies all protocol constants are defined correctly.
func TestProtocolConstants(t *testing.T) {
	assert.Equal(t, Protocol("HTTPS"), ProtocolHTTPS)
	assert.Equal(t, Protocol("GRPC"), ProtocolGRPC)
	assert.Equal(t, Protocol("WEBHOOK"), ProtocolWebhook)
	assert.Equal(t, Protocol("MQTT"), ProtocolMQTT)
	assert.Equal(t, Protocol("AMQP"), ProtocolAMQP)
}

// TestCircuitStateConstants verifies all circuit state constants are defined correctly.
func TestCircuitStateConstants(t *testing.T) {
	assert.Equal(t, CircuitState("CLOSED"), CircuitStateClosed)
	assert.Equal(t, CircuitState("OPEN"), CircuitStateOpen)
	assert.Equal(t, CircuitState("HALF_OPEN"), CircuitStateHalfOpen)
}

// TestHealthStatusConstants verifies all health status constants are defined correctly.
func TestHealthStatusConstants(t *testing.T) {
	assert.Equal(t, HealthStatus("UNKNOWN"), HealthStatusUnknown)
	assert.Equal(t, HealthStatus("HEALTHY"), HealthStatusHealthy)
	assert.Equal(t, HealthStatus("DEGRADED"), HealthStatusDegraded)
	assert.Equal(t, HealthStatus("UNHEALTHY"), HealthStatusUnhealthy)
}

// TestAuthConfigImplementations verifies all AuthConfig implementations satisfy the interface.
func TestAuthConfigImplementations(_ *testing.T) {
	var _ AuthConfig = (*APIKeyAuth)(nil)
	var _ AuthConfig = (*BasicAuth)(nil)
	var _ AuthConfig = (*OAuth2Auth)(nil)
	var _ AuthConfig = (*HMACAuth)(nil)
	var _ AuthConfig = (*MTLSAuth)(nil)
}

// TestAPIKeyAuth verifies APIKeyAuth stores secret references, not raw values.
func TestAPIKeyAuth(t *testing.T) {
	auth := &APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "secrets/tenant-a/provider-api-key",
	}
	assert.Equal(t, "X-API-Key", auth.HeaderName)
	assert.Equal(t, "secrets/tenant-a/provider-api-key", auth.SecretRef)
	assert.Equal(t, "api_key", auth.AuthType())
}

// TestBasicAuth verifies BasicAuth stores username as a plain value and password as a secret reference.
func TestBasicAuth(t *testing.T) {
	auth := &BasicAuth{
		Username:    "service-account",
		PasswordRef: "secrets/tenant-a/provider-password",
	}
	assert.Equal(t, "service-account", auth.Username)
	assert.Equal(t, "secrets/tenant-a/provider-password", auth.PasswordRef)
	assert.Equal(t, "basic", auth.AuthType())
}

// TestOAuth2Auth verifies OAuth2Auth stores client ID as a plain value and client secret as a secret reference.
func TestOAuth2Auth(t *testing.T) {
	auth := &OAuth2Auth{
		TokenURL:        "https://auth.example.com/oauth/token",
		ClientID:        "my-client-id",
		ClientSecretRef: "secrets/tenant-a/oauth-client-secret",
		Scopes:          []string{"read", "write"},
	}
	assert.Equal(t, "https://auth.example.com/oauth/token", auth.TokenURL)
	assert.Equal(t, "my-client-id", auth.ClientID)
	assert.Equal(t, "secrets/tenant-a/oauth-client-secret", auth.ClientSecretRef)
	assert.Equal(t, []string{"read", "write"}, auth.Scopes)
	assert.Equal(t, "oauth2", auth.AuthType())
}

// TestHMACAuth verifies HMACAuth stores secret references and algorithm.
func TestHMACAuth(t *testing.T) {
	auth := &HMACAuth{
		SecretRef:       "secrets/tenant-a/hmac-secret",
		Algorithm:       "sha256",
		SignatureHeader: "X-Signature",
	}
	assert.Equal(t, "secrets/tenant-a/hmac-secret", auth.SecretRef)
	assert.Equal(t, "sha256", auth.Algorithm)
	assert.Equal(t, "X-Signature", auth.SignatureHeader)
	assert.Equal(t, "hmac", auth.AuthType())
}

// TestMTLSAuth verifies MTLSAuth stores certificate, key, and optional CA cert references.
func TestMTLSAuth(t *testing.T) {
	auth := &MTLSAuth{
		ClientCertRef: "secrets/tenant-a/mtls-cert",
		ClientKeyRef:  "secrets/tenant-a/mtls-key",
		CACertRef:     "secrets/tenant-a/mtls-ca",
	}
	assert.Equal(t, "secrets/tenant-a/mtls-cert", auth.ClientCertRef)
	assert.Equal(t, "secrets/tenant-a/mtls-key", auth.ClientKeyRef)
	assert.Equal(t, "secrets/tenant-a/mtls-ca", auth.CACertRef)
	assert.Equal(t, "mtls", auth.AuthType())
}

// TestMTLSAuth_OptionalCACert verifies MTLSAuth works without a CA cert (uses system pool).
func TestMTLSAuth_OptionalCACert(t *testing.T) {
	auth := &MTLSAuth{
		ClientCertRef: "secrets/tenant-a/mtls-cert",
		ClientKeyRef:  "secrets/tenant-a/mtls-key",
	}
	assert.Empty(t, auth.CACertRef)
	assert.Equal(t, "mtls", auth.AuthType())
}

// TestRetryPolicy verifies RetryPolicy stores duration-based and numeric configuration.
func TestRetryPolicy(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
	}
	assert.Equal(t, 3, policy.MaxAttempts)
	assert.Equal(t, 100*time.Millisecond, policy.InitialBackoff)
	assert.Equal(t, 10*time.Second, policy.MaxBackoff)
	assert.Equal(t, 2.0, policy.BackoffMultiplier)
}

// TestRateLimitConfig verifies RateLimitConfig stores rate limiting parameters.
func TestRateLimitConfig(t *testing.T) {
	config := RateLimitConfig{
		RequestsPerSecond: 100.0,
		BurstSize:         20,
	}
	assert.Equal(t, 100.0, config.RequestsPerSecond)
	assert.Equal(t, 20, config.BurstSize)
}

// TestNewProviderConnection verifies that a new ProviderConnection is created correctly.
func TestNewProviderConnection(t *testing.T) {
	tenantID := "tenant-a"
	auth := &APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "secrets/key"}
	retryPolicy := RetryPolicy{MaxAttempts: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 10 * time.Second}
	rateLimitConfig := RateLimitConfig{RequestsPerSecond: 50.0, BurstSize: 10}

	conn, err := NewProviderConnection(
		tenantID,
		"acme-bank",
		"bank",
		ProtocolHTTPS,
		"https://api.acme-bank.com",
		auth,
		retryPolicy,
		rateLimitConfig,
	)
	require.NoError(t, err)

	assert.Equal(t, tenantID, conn.TenantID)
	assert.NotEmpty(t, conn.ConnectionID)
	_, parseErr := uuid.Parse(conn.ConnectionID)
	assert.NoError(t, parseErr, "ConnectionID should be a valid UUID")
	assert.Equal(t, "acme-bank", conn.ProviderName)
	assert.Equal(t, "bank", conn.ProviderType)
	assert.Equal(t, ProtocolHTTPS, conn.Protocol)
	assert.Equal(t, "https://api.acme-bank.com", conn.BaseURL)
	assert.Equal(t, auth, conn.AuthConfig)
	assert.Equal(t, retryPolicy, conn.RetryPolicy)
	assert.Equal(t, rateLimitConfig, conn.RateLimitConfig)
	assert.Equal(t, HealthStatusUnknown, conn.HealthStatus)
	assert.Nil(t, conn.LastHealthCheckAt)
	assert.Equal(t, CircuitStateClosed, conn.CircuitState)
	assert.Nil(t, conn.CircuitOpenedAt)
	assert.Equal(t, 0, conn.FailureCount)
	assert.Equal(t, 0, conn.SuccessCount)
	assert.False(t, conn.CreatedAt.IsZero())
	assert.False(t, conn.UpdatedAt.IsZero())
}

// TestNewProviderConnectionValidation verifies validation on constructor.
func TestNewProviderConnectionValidation(t *testing.T) {
	validAuth := &APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "secrets/key"}
	validRetry := RetryPolicy{MaxAttempts: 3}
	validRate := RateLimitConfig{RequestsPerSecond: 10}

	tests := []struct {
		name         string
		tenantID     string
		providerName string
		providerType string
		protocol     Protocol
		baseURL      string
		auth         AuthConfig
		expectErr    error
	}{
		{
			name:         "empty tenant ID",
			tenantID:     "",
			providerName: "acme",
			providerType: "bank",
			protocol:     ProtocolHTTPS,
			baseURL:      "https://api.acme.com",
			auth:         validAuth,
			expectErr:    ErrTenantIDRequired,
		},
		{
			name:         "empty provider name",
			tenantID:     "tenant-a",
			providerName: "",
			providerType: "bank",
			protocol:     ProtocolHTTPS,
			baseURL:      "https://api.acme.com",
			auth:         validAuth,
			expectErr:    ErrProviderNameRequired,
		},
		{
			name:         "empty base URL",
			tenantID:     "tenant-a",
			providerName: "acme",
			providerType: "bank",
			protocol:     ProtocolHTTPS,
			baseURL:      "",
			auth:         validAuth,
			expectErr:    ErrBaseURLRequired,
		},
		{
			name:         "nil auth config",
			tenantID:     "tenant-a",
			providerName: "acme",
			providerType: "bank",
			protocol:     ProtocolHTTPS,
			baseURL:      "https://api.acme.com",
			auth:         nil,
			expectErr:    ErrAuthConfigRequired,
		},
		{
			name:         "invalid protocol",
			tenantID:     "tenant-a",
			providerName: "acme",
			providerType: "bank",
			protocol:     Protocol("FTP"),
			baseURL:      "https://api.acme.com",
			auth:         validAuth,
			expectErr:    ErrInvalidProtocol,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProviderConnection(
				tc.tenantID,
				tc.providerName,
				tc.providerType,
				tc.protocol,
				tc.baseURL,
				tc.auth,
				validRetry,
				validRate,
			)
			assert.ErrorIs(t, err, tc.expectErr)
		})
	}
}

// TestCircuitBreaker_RecordSuccess_ResetFailureCount verifies that RecordSuccess resets
// the failure count when the circuit is closed (consecutive failure tracking).
func TestCircuitBreaker_RecordSuccess_ResetFailureCount(t *testing.T) {
	conn := newTestConnection(t)
	threshold := 5

	// Accumulate some failures below the trip threshold
	for i := 0; i < threshold-1; i++ {
		require.NoError(t, conn.RecordFailure(threshold))
	}
	assert.Equal(t, threshold-1, conn.FailureCount)
	assert.Equal(t, CircuitStateClosed, conn.CircuitState)

	// A success resets the streak
	conn.RecordSuccess()
	assert.Equal(t, 0, conn.FailureCount)
	assert.Equal(t, 1, conn.SuccessCount)
	assert.Equal(t, CircuitStateClosed, conn.CircuitState)
}

// TestCircuitBreaker_RecordFailureBelowThreshold verifies failures below threshold keep circuit closed.
func TestCircuitBreaker_RecordFailureBelowThreshold(t *testing.T) {
	conn := newTestConnection(t)
	threshold := 5

	for i := 1; i < threshold; i++ {
		require.NoError(t, conn.RecordFailure(threshold))
		assert.Equal(t, i, conn.FailureCount)
		assert.Equal(t, CircuitStateClosed, conn.CircuitState, "circuit should stay closed below threshold")
	}
}

// TestCircuitBreaker_RecordFailureAtThreshold verifies that reaching the threshold opens the circuit.
func TestCircuitBreaker_RecordFailureAtThreshold(t *testing.T) {
	conn := newTestConnection(t)
	threshold := 5

	for i := 0; i < threshold; i++ {
		require.NoError(t, conn.RecordFailure(threshold))
	}

	assert.Equal(t, threshold, conn.FailureCount)
	assert.Equal(t, CircuitStateOpen, conn.CircuitState)
	assert.NotNil(t, conn.CircuitOpenedAt)
}

// TestCircuitBreaker_RecordFailureInvalidThreshold verifies that threshold <= 0 returns an error.
func TestCircuitBreaker_RecordFailureInvalidThreshold(t *testing.T) {
	conn := newTestConnection(t)
	assert.ErrorIs(t, conn.RecordFailure(0), ErrInvalidThreshold)
	assert.ErrorIs(t, conn.RecordFailure(-1), ErrInvalidThreshold)
}

// TestCircuitBreaker_IsAvailableWhenClosed verifies circuit is available when closed.
func TestCircuitBreaker_IsAvailableWhenClosed(t *testing.T) {
	conn := newTestConnection(t)
	assert.Equal(t, CircuitStateClosed, conn.CircuitState)
	assert.True(t, conn.IsAvailable())
}

// TestCircuitBreaker_IsAvailableWhenOpen verifies circuit is not available when open.
func TestCircuitBreaker_IsAvailableWhenOpen(t *testing.T) {
	conn := newTestConnection(t)
	conn.TripCircuit()
	assert.Equal(t, CircuitStateOpen, conn.CircuitState)
	assert.False(t, conn.IsAvailable())
}

// TestCircuitBreaker_IsAvailableWhenHalfOpen verifies circuit is available for a probe when half-open.
func TestCircuitBreaker_IsAvailableWhenHalfOpen(t *testing.T) {
	conn := newTestConnection(t)
	conn.TripCircuit()
	conn.AttemptReset()
	assert.Equal(t, CircuitStateHalfOpen, conn.CircuitState)
	assert.True(t, conn.IsAvailable())
}

// TestCircuitBreaker_TripCircuit verifies TripCircuit transitions closed → open.
func TestCircuitBreaker_TripCircuit(t *testing.T) {
	conn := newTestConnection(t)
	assert.Equal(t, CircuitStateClosed, conn.CircuitState)
	assert.Nil(t, conn.CircuitOpenedAt)

	conn.TripCircuit()

	assert.Equal(t, CircuitStateOpen, conn.CircuitState)
	require.NotNil(t, conn.CircuitOpenedAt)
	assert.WithinDuration(t, time.Now(), *conn.CircuitOpenedAt, time.Second)
}

// TestCircuitBreaker_TripCircuit_PreservesOpenedAt verifies that re-tripping preserves the original time.
func TestCircuitBreaker_TripCircuit_PreservesOpenedAt(t *testing.T) {
	conn := newTestConnection(t)
	conn.TripCircuit()
	original := *conn.CircuitOpenedAt

	// Trip again — original timestamp must be preserved
	conn.TripCircuit()
	assert.Equal(t, original, *conn.CircuitOpenedAt)
}

// TestCircuitBreaker_AttemptReset verifies AttemptReset transitions open → half-open.
func TestCircuitBreaker_AttemptReset(t *testing.T) {
	conn := newTestConnection(t)
	conn.TripCircuit()
	assert.Equal(t, CircuitStateOpen, conn.CircuitState)

	conn.AttemptReset()

	assert.Equal(t, CircuitStateHalfOpen, conn.CircuitState)
}

// TestCircuitBreaker_AttemptReset_NoopWhenClosed verifies AttemptReset is a no-op when already closed.
func TestCircuitBreaker_AttemptReset_NoopWhenClosed(t *testing.T) {
	conn := newTestConnection(t)
	assert.Equal(t, CircuitStateClosed, conn.CircuitState)

	conn.AttemptReset()

	assert.Equal(t, CircuitStateClosed, conn.CircuitState)
}

// TestCircuitBreaker_FullCycle verifies the full circuit breaker cycle:
// closed → open → half-open → closed (via success).
func TestCircuitBreaker_FullCycle(t *testing.T) {
	conn := newTestConnection(t)
	threshold := 3

	// Trip the circuit
	for i := 0; i < threshold; i++ {
		require.NoError(t, conn.RecordFailure(threshold))
	}
	assert.Equal(t, CircuitStateOpen, conn.CircuitState)
	assert.False(t, conn.IsAvailable())

	// Move to half-open (allow probe)
	conn.AttemptReset()
	assert.Equal(t, CircuitStateHalfOpen, conn.CircuitState)
	assert.True(t, conn.IsAvailable())

	// Successful probe closes circuit and resets counters
	conn.RecordSuccess()
	assert.Equal(t, CircuitStateClosed, conn.CircuitState)
	assert.Equal(t, 0, conn.FailureCount)
	assert.Nil(t, conn.CircuitOpenedAt)
}

// TestCircuitBreaker_HalfOpenFailureReopens verifies that a failure in half-open state re-trips the circuit.
func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	conn := newTestConnection(t)
	threshold := 3

	// Trip then allow probe
	for i := 0; i < threshold; i++ {
		require.NoError(t, conn.RecordFailure(threshold))
	}
	conn.AttemptReset()
	assert.Equal(t, CircuitStateHalfOpen, conn.CircuitState)

	// Failure in half-open re-trips
	require.NoError(t, conn.RecordFailure(threshold))
	assert.Equal(t, CircuitStateOpen, conn.CircuitState)
	assert.NotNil(t, conn.CircuitOpenedAt)
}

// TestUpdateHealthStatus verifies health status and last check time are updated.
func TestUpdateHealthStatus(t *testing.T) {
	conn := newTestConnection(t)
	assert.Equal(t, HealthStatusUnknown, conn.HealthStatus)

	before := time.Now()
	conn.UpdateHealthStatus(HealthStatusHealthy)
	after := time.Now()

	assert.Equal(t, HealthStatusHealthy, conn.HealthStatus)
	require.NotNil(t, conn.LastHealthCheckAt)
	assert.True(t, !conn.LastHealthCheckAt.Before(before))
	assert.True(t, !conn.LastHealthCheckAt.After(after))
}

// newTestConnection is a test helper that creates a valid ProviderConnection.
func newTestConnection(t *testing.T) *ProviderConnection {
	t.Helper()
	conn, err := NewProviderConnection(
		"tenant-a",
		"test-provider",
		"test",
		ProtocolHTTPS,
		"https://api.test.com",
		&APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "secrets/key"},
		RetryPolicy{MaxAttempts: 3},
		RateLimitConfig{RequestsPerSecond: 10},
	)
	require.NoError(t, err)
	return conn
}
