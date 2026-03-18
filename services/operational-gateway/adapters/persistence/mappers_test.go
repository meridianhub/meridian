package persistence

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========== authConfigToJSON / authConfigFromJSON roundtrip ==========

func TestAuthConfigRoundtrip_APIKey(t *testing.T) {
	auth := &domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"}
	j, err := authConfigToJSON(auth)
	require.NoError(t, err)
	assert.Equal(t, "api_key", j.AuthType)

	back, err := authConfigFromJSON(j)
	require.NoError(t, err)
	apiKey, ok := back.(*domain.APIKeyAuth)
	require.True(t, ok)
	assert.Equal(t, "X-Key", apiKey.HeaderName)
}

func TestAuthConfigRoundtrip_Basic(t *testing.T) {
	auth := &domain.BasicAuth{Username: "user", PasswordRef: "pass"}
	j, err := authConfigToJSON(auth)
	require.NoError(t, err)
	assert.Equal(t, "basic", j.AuthType)

	back, err := authConfigFromJSON(j)
	require.NoError(t, err)
	basic, ok := back.(*domain.BasicAuth)
	require.True(t, ok)
	assert.Equal(t, "user", basic.Username)
}

func TestAuthConfigRoundtrip_OAuth2(t *testing.T) {
	auth := &domain.OAuth2Auth{
		TokenURL:        "https://auth.example.com/token",
		ClientID:        "c1",
		ClientSecretRef: "cs",
		Scopes:          []string{"read"},
	}
	j, err := authConfigToJSON(auth)
	require.NoError(t, err)
	assert.Equal(t, "oauth2", j.AuthType)

	back, err := authConfigFromJSON(j)
	require.NoError(t, err)
	oauth, ok := back.(*domain.OAuth2Auth)
	require.True(t, ok)
	assert.Equal(t, "https://auth.example.com/token", oauth.TokenURL)
}

func TestAuthConfigRoundtrip_HMAC(t *testing.T) {
	auth := &domain.HMACAuth{Algorithm: "SHA256", SecretRef: "ref", SignatureHeader: "X-Sig"}
	j, err := authConfigToJSON(auth)
	require.NoError(t, err)
	assert.Equal(t, "hmac", j.AuthType)

	back, err := authConfigFromJSON(j)
	require.NoError(t, err)
	hmac, ok := back.(*domain.HMACAuth)
	require.True(t, ok)
	assert.Equal(t, "SHA256", hmac.Algorithm)
}

func TestAuthConfigRoundtrip_MTLS(t *testing.T) {
	auth := &domain.MTLSAuth{ClientCertRef: "cert", ClientKeyRef: "key", CACertRef: "ca"}
	j, err := authConfigToJSON(auth)
	require.NoError(t, err)
	assert.Equal(t, "mtls", j.AuthType)

	back, err := authConfigFromJSON(j)
	require.NoError(t, err)
	mtls, ok := back.(*domain.MTLSAuth)
	require.True(t, ok)
	assert.Equal(t, "cert", mtls.ClientCertRef)
}

func TestAuthConfigToJSON_UnknownType(t *testing.T) {
	_, err := authConfigToJSON(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownAuthConfigType)
}

func TestAuthConfigFromJSON_UnknownType(t *testing.T) {
	_, err := authConfigFromJSON(AuthConfigJSON{AuthType: "bogus"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownAuthType)
}

// ========== healthStatusForDB / healthStatusFromDB ==========

func TestHealthStatusForDB_AllStatuses(t *testing.T) {
	tests := []struct {
		domain   domain.HealthStatus
		expected string
	}{
		{domain.HealthStatusUnknown, "UNKNOWN"},
		{domain.HealthStatusHealthy, "HEALTHY"},
		{domain.HealthStatusDegraded, "DEGRADED"},
		{domain.HealthStatusUnhealthy, "UNHEALTHY"},
		{"BOGUS", "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.expected, healthStatusForDB(tt.domain))
		})
	}
}

func TestHealthStatusFromDB_AllStatuses(t *testing.T) {
	tests := []struct {
		input    string
		expected domain.HealthStatus
	}{
		{"HEALTHY", domain.HealthStatusHealthy},
		{"DEGRADED", domain.HealthStatusDegraded},
		{"UNHEALTHY", domain.HealthStatusUnhealthy},
		{"UNKNOWN", domain.HealthStatusUnknown},
		{"BOGUS", domain.HealthStatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, healthStatusFromDB(tt.input))
		})
	}
}

// ========== instructionToEntity / instructionFromEntity roundtrip ==========

func TestInstructionEntityRoundtrip(t *testing.T) {
	tid := uuid.New()
	connID := uuid.New().String()
	inst, err := domain.NewInstruction(tid, "device.command", connID, map[string]any{"k": "v"},
		domain.WithPriority(domain.PriorityHigh),
		domain.WithCorrelationID("corr-1"),
		domain.WithCausationID("cause-1"),
		domain.WithMetadata(map[string]string{"env": "test"}),
	)
	require.NoError(t, err)
	inst.ID = uuid.New()

	entity, err := instructionToEntity(inst, "idem-key")
	require.NoError(t, err)
	assert.Equal(t, inst.ID, entity.ID)
	assert.Equal(t, "idem-key", entity.IdempotencyKey)
	assert.Equal(t, int16(3), entity.Priority) // HIGH = 3

	back, err := instructionFromEntity(entity, nil)
	require.NoError(t, err)
	assert.Equal(t, inst.ID, back.ID)
	assert.Equal(t, inst.InstructionType, back.InstructionType)
	assert.Equal(t, domain.PriorityHigh, back.Priority)
	assert.Equal(t, "corr-1", back.CorrelationID)
	assert.Equal(t, "cause-1", back.CausationID)
}

func TestInstructionFromEntity_WithAttempts(t *testing.T) {
	entity := &InstructionEntity{
		ID:                   uuid.New(),
		TenantID:             uuid.New(),
		InstructionType:      "test.cmd",
		ProviderConnectionID: uuid.New(),
		Payload:              JSONB{"k": "v"},
		Priority:             2,
		Status:               "PENDING",
		MaxAttempts:          3,
		Version:              1,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	attempts := []InstructionAttemptEntity{
		{
			AttemptNumber: 1,
			DispatchedAt:  time.Now(),
			ErrorMessage:  nullableString("timeout"),
		},
	}
	back, err := instructionFromEntity(entity, attempts)
	require.NoError(t, err)
	assert.Len(t, back.Attempts, 1)
	assert.Equal(t, "timeout", back.Attempts[0].FailureReason)
}

func TestInstructionFromEntity_WithMetadata(t *testing.T) {
	entity := &InstructionEntity{
		ID:                   uuid.New(),
		TenantID:             uuid.New(),
		InstructionType:      "test.cmd",
		ProviderConnectionID: uuid.New(),
		Payload:              JSONB{"k": "v"},
		Metadata:             JSONB{"env": "prod"},
		Priority:             2,
		Status:               "PENDING",
		Version:              1,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	back, err := instructionFromEntity(entity, nil)
	require.NoError(t, err)
	assert.Equal(t, "prod", back.Metadata["env"])
}

// ========== connectionToEntity / connectionFromEntity roundtrip ==========

func TestConnectionEntityRoundtrip(t *testing.T) {
	tid := uuid.New().String()
	conn, err := domain.NewProviderConnection(
		tid, "Onfido", "kyc_provider", domain.ProtocolHTTPS,
		"https://api.onfido.com",
		&domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "key"},
		domain.RetryPolicy{MaxAttempts: 3, InitialBackoff: 1 * time.Second, MaxBackoff: 60 * time.Second, BackoffMultiplier: 2.0},
		domain.RateLimitConfig{RequestsPerSecond: 100, BurstSize: 50},
	)
	require.NoError(t, err)

	entity, err := connectionToEntity(conn)
	require.NoError(t, err)
	assert.Equal(t, "Onfido", entity.ProviderName)
	assert.Equal(t, "HTTPS", entity.Protocol)

	back, err := connectionFromEntity(entity)
	require.NoError(t, err)
	assert.Equal(t, "Onfido", back.ProviderName)
	assert.Equal(t, domain.ProtocolHTTPS, back.Protocol)
	assert.Equal(t, 3, back.RetryPolicy.MaxAttempts)
	assert.Equal(t, 100.0, back.RateLimitConfig.RequestsPerSecond)
}
