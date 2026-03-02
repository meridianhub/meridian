package applier

import (
	"errors"
	"testing"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildUpsertConnectionRequest_ApiKey(t *testing.T) {
	params := map[string]any{
		"connection_id": "stripe-payments",
		"provider_name": "Stripe",
		"provider_type": "payment_gateway",
		"protocol":      "PROTOCOL_HTTPS",
		"base_url":      "https://api.stripe.com",
		"auth_type":     "api_key",
		"auth_config": map[string]any{
			"header_name": "X-API-Key",
			"secret_ref":  "stripe-secret",
		},
		"retry_policy": map[string]any{
			"max_attempts":            int64(3),
			"initial_backoff_seconds": int64(1),
			"max_backoff_seconds":     int64(30),
			"backoff_multiplier":      float64(2.0),
		},
	}

	req, err := buildUpsertConnectionRequest(params)
	require.NoError(t, err)

	assert.Equal(t, "stripe-payments", req.ConnectionId)
	assert.Equal(t, "Stripe", req.ProviderName)
	assert.Equal(t, "payment_gateway", req.ProviderType)
	assert.Equal(t, opgatewayv1.Protocol_PROTOCOL_HTTPS, req.Protocol)
	assert.Equal(t, "https://api.stripe.com", req.BaseUrl)

	apiKey, ok := req.AuthConfig.(*opgatewayv1.UpsertConnectionRequest_ApiKey)
	require.True(t, ok, "expected ApiKey auth config")
	assert.Equal(t, "X-API-Key", apiKey.ApiKey.HeaderName)
	assert.Equal(t, "stripe-secret", apiKey.ApiKey.SecretRef)

	require.NotNil(t, req.RetryPolicy)
	assert.Equal(t, int32(3), req.RetryPolicy.MaxAttempts)
	assert.Equal(t, int32(1), req.RetryPolicy.InitialBackoffSeconds)
	assert.Equal(t, int32(30), req.RetryPolicy.MaxBackoffSeconds)
	assert.Equal(t, 2.0, req.RetryPolicy.BackoffMultiplier)
}

func TestBuildUpsertConnectionRequest_OAuth2(t *testing.T) {
	params := map[string]any{
		"connection_id": "oauth-service",
		"provider_name": "OAuth Provider",
		"protocol":      "PROTOCOL_HTTPS",
		"base_url":      "https://api.example.com",
		"auth_type":     "oauth2",
		"auth_config": map[string]any{
			"token_url":         "https://auth.example.com/token",
			"client_id":         "client-123",
			"client_secret_ref": "oauth-secret",
			"scopes":            []any{"read", "write"},
		},
	}

	req, err := buildUpsertConnectionRequest(params)
	require.NoError(t, err)

	oauth2, ok := req.AuthConfig.(*opgatewayv1.UpsertConnectionRequest_Oauth2)
	require.True(t, ok, "expected OAuth2 auth config")
	assert.Equal(t, "https://auth.example.com/token", oauth2.Oauth2.TokenUrl)
	assert.Equal(t, "client-123", oauth2.Oauth2.ClientId)
	assert.Equal(t, "oauth-secret", oauth2.Oauth2.ClientSecretRef)
	assert.Equal(t, []string{"read", "write"}, oauth2.Oauth2.Scopes)
}

func TestBuildUpsertConnectionRequest_HMAC(t *testing.T) {
	params := map[string]any{
		"connection_id": "webhook-provider",
		"provider_name": "Webhook Provider",
		"protocol":      "PROTOCOL_WEBHOOK",
		"base_url":      "https://webhooks.example.com",
		"auth_type":     "hmac",
		"auth_config": map[string]any{
			"algorithm":        "sha256",
			"secret_ref":       "hmac-secret",
			"signature_header": "X-Signature",
		},
	}

	req, err := buildUpsertConnectionRequest(params)
	require.NoError(t, err)

	hmac, ok := req.AuthConfig.(*opgatewayv1.UpsertConnectionRequest_Hmac)
	require.True(t, ok, "expected HMAC auth config")
	assert.Equal(t, "sha256", hmac.Hmac.Algorithm)
	assert.Equal(t, "hmac-secret", hmac.Hmac.SecretRef)
	assert.Equal(t, "X-Signature", hmac.Hmac.SignatureHeader)
}

func TestBuildUpsertConnectionRequest_MTLS(t *testing.T) {
	params := map[string]any{
		"connection_id": "mtls-service",
		"provider_name": "mTLS Provider",
		"protocol":      "PROTOCOL_GRPC",
		"base_url":      "https://grpc.example.com",
		"auth_type":     "mtls",
		"auth_config": map[string]any{
			"client_cert_ref": "cert-secret",
			"client_key_ref":  "key-secret",
			"ca_cert_ref":     "ca-secret",
		},
	}

	req, err := buildUpsertConnectionRequest(params)
	require.NoError(t, err)

	mtls, ok := req.AuthConfig.(*opgatewayv1.UpsertConnectionRequest_Mtls)
	require.True(t, ok, "expected mTLS auth config")
	assert.Equal(t, "cert-secret", mtls.Mtls.ClientCertSecretRef)
	assert.Equal(t, "key-secret", mtls.Mtls.ClientKeySecretRef)
	assert.Equal(t, "ca-secret", mtls.Mtls.CaCertSecretRef)
}

func TestBuildUpsertConnectionRequest_UnknownAuthType(t *testing.T) {
	params := map[string]any{
		"connection_id": "bad-conn",
		"provider_name": "Bad Provider",
		"protocol":      "PROTOCOL_HTTPS",
		"base_url":      "https://example.com",
		"auth_type":     "unknown_auth",
		"auth_config":   map[string]any{},
	}

	_, err := buildUpsertConnectionRequest(params)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnsupportedAuthType))
}

func TestBuildUpsertRouteRequest(t *testing.T) {
	params := map[string]any{
		"instruction_type":       "payment.initiate",
		"connection_id":          "stripe-conn-uuid",
		"fallback_connection_id": "backup-conn-uuid",
		"outbound_mapping":       "stripe-outbound",
		"inbound_mapping":        "stripe-inbound",
		"http_method":            "POST",
		"path_template":          "/v1/payment_intents",
	}

	req, err := buildUpsertRouteRequest(params)
	require.NoError(t, err)

	assert.Equal(t, "payment.initiate", req.InstructionType)
	assert.Equal(t, "stripe-conn-uuid", req.ConnectionId)
	assert.Equal(t, "backup-conn-uuid", req.FallbackConnectionId)
	assert.Equal(t, "stripe-outbound", req.OutboundMapping)
	assert.Equal(t, "stripe-inbound", req.InboundMapping)
	assert.Equal(t, "POST", req.HttpMethod)
	assert.Equal(t, "/v1/payment_intents", req.PathTemplate)
}

func TestBuildUpsertRouteRequest_MinimalParams(t *testing.T) {
	params := map[string]any{
		"instruction_type": "kyc.verify",
		"connection_id":    "kyc-provider-uuid",
	}

	req, err := buildUpsertRouteRequest(params)
	require.NoError(t, err)

	assert.Equal(t, "kyc.verify", req.InstructionType)
	assert.Equal(t, "kyc-provider-uuid", req.ConnectionId)
	assert.Empty(t, req.FallbackConnectionId)
	assert.Empty(t, req.OutboundMapping)
	assert.Empty(t, req.InboundMapping)
	assert.Empty(t, req.HttpMethod)
	assert.Empty(t, req.PathTemplate)
}

func TestParseProtocol(t *testing.T) {
	tests := []struct {
		input    string
		expected opgatewayv1.Protocol
	}{
		{"PROVIDER_PROTOCOL_HTTPS", opgatewayv1.Protocol_PROTOCOL_HTTPS},
		{"PROTOCOL_HTTPS", opgatewayv1.Protocol_PROTOCOL_HTTPS},
		{"PROVIDER_PROTOCOL_GRPC", opgatewayv1.Protocol_PROTOCOL_GRPC},
		{"PROTOCOL_GRPC", opgatewayv1.Protocol_PROTOCOL_GRPC},
		{"PROVIDER_PROTOCOL_WEBHOOK", opgatewayv1.Protocol_PROTOCOL_WEBHOOK},
		{"PROTOCOL_WEBHOOK", opgatewayv1.Protocol_PROTOCOL_WEBHOOK},
		{"PROVIDER_PROTOCOL_MQTT", opgatewayv1.Protocol_PROTOCOL_MQTT},
		{"PROTOCOL_MQTT", opgatewayv1.Protocol_PROTOCOL_MQTT},
		{"PROVIDER_PROTOCOL_AMQP", opgatewayv1.Protocol_PROTOCOL_AMQP},
		{"PROTOCOL_AMQP", opgatewayv1.Protocol_PROTOCOL_AMQP},
		{"unknown", opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED},
		{"", opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseProtocol(tt.input))
		})
	}
}

func TestBuildRetryPolicy(t *testing.T) {
	m := map[string]any{
		"max_attempts":            int64(5),
		"initial_backoff_seconds": int64(2),
		"max_backoff_seconds":     int64(60),
		"backoff_multiplier":      float64(1.5),
	}

	rp := buildRetryPolicy(m)
	assert.Equal(t, int32(5), rp.MaxAttempts)
	assert.Equal(t, int32(2), rp.InitialBackoffSeconds)
	assert.Equal(t, int32(60), rp.MaxBackoffSeconds)
	assert.Equal(t, 1.5, rp.BackoffMultiplier)
}

func TestBuildRateLimit(t *testing.T) {
	m := map[string]any{
		"requests_per_second": float64(100.0),
		"burst_size":          int64(200),
	}

	rl := buildRateLimit(m)
	assert.Equal(t, 100.0, rl.RequestsPerSecond)
	assert.Equal(t, int32(200), rl.BurstSize)
}
