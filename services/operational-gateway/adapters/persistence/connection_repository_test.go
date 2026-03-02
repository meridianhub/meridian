package persistence

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeProviderConnection builds a ProviderConnection domain object for tests.
func makeProviderConnection(t *testing.T, tenantID string, authConfig domain.AuthConfig) *domain.ProviderConnection {
	t.Helper()
	conn, err := domain.NewProviderConnection(
		tenantID,
		"acme-bank",
		"bank",
		domain.ProtocolHTTPS,
		"https://api.acme-bank.example.com",
		authConfig,
		domain.RetryPolicy{
			MaxAttempts:       3,
			InitialBackoff:    200 * time.Millisecond,
			MaxBackoff:        5 * time.Second,
			BackoffMultiplier: 2.0,
		},
		domain.RateLimitConfig{
			RequestsPerSecond: 10.0,
			BurstSize:         20,
		},
	)
	require.NoError(t, err)
	return conn
}

// TestConnectionRepository_Upsert_Create verifies a new connection can be inserted.
func TestConnectionRepository_Upsert_Create(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)
	tenantID := uuid.New().String()

	conn := makeProviderConnection(t, tenantID, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "secret/api-key",
	})

	require.NoError(t, repo.Upsert(ctx, conn))

	found, err := repo.FindByID(ctx, tenantID, conn.ConnectionID)
	require.NoError(t, err)
	assert.Equal(t, conn.ConnectionID, found.ConnectionID)
	assert.Equal(t, "acme-bank", found.ProviderName)
	assert.Equal(t, domain.ProtocolHTTPS, found.Protocol)
	assert.Equal(t, domain.HealthStatusUnknown, found.HealthStatus)
	assert.Equal(t, domain.CircuitStateClosed, found.CircuitState)
}

// TestConnectionRepository_Upsert_Idempotent verifies repeated upserts update the record.
func TestConnectionRepository_Upsert_Idempotent(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)
	tenantID := uuid.New().String()

	conn := makeProviderConnection(t, tenantID, &domain.BasicAuth{
		Username:    "user",
		PasswordRef: "secret/password",
	})
	require.NoError(t, repo.Upsert(ctx, conn))

	// Update the provider name and re-upsert (same connection_id)
	conn.ProviderName = "acme-bank-v2"
	conn.BaseURL = "https://api-v2.acme-bank.example.com"
	require.NoError(t, repo.Upsert(ctx, conn))

	found, err := repo.FindByID(ctx, tenantID, conn.ConnectionID)
	require.NoError(t, err)
	assert.Equal(t, "acme-bank-v2", found.ProviderName)
	assert.Equal(t, "https://api-v2.acme-bank.example.com", found.BaseURL)
}

// TestConnectionRepository_FindByID_NotFound returns ErrConnectionNotFound for missing records.
func TestConnectionRepository_FindByID_NotFound(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)
	_, err := repo.FindByID(ctx, uuid.New().String(), uuid.New().String())
	require.ErrorIs(t, err, ports.ErrConnectionNotFound)
}

// TestConnectionRepository_ListByTenant returns only connections for the given tenant.
func TestConnectionRepository_ListByTenant(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)
	tenantA := uuid.New().String()
	tenantB := uuid.New().String()

	connA1 := makeProviderConnection(t, tenantA, &domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "s1"})
	connA2 := makeProviderConnection(t, tenantA, &domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "s2"})
	connB1 := makeProviderConnection(t, tenantB, &domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "s3"})

	require.NoError(t, repo.Upsert(ctx, connA1))
	require.NoError(t, repo.Upsert(ctx, connA2))
	require.NoError(t, repo.Upsert(ctx, connB1))

	connsA, err := repo.ListByTenant(ctx, tenantA)
	require.NoError(t, err)
	assert.Len(t, connsA, 2)

	connsB, err := repo.ListByTenant(ctx, tenantB)
	require.NoError(t, err)
	assert.Len(t, connsB, 1)
}

// TestConnectionRepository_UpdateHealth persists health and circuit state changes.
func TestConnectionRepository_UpdateHealth(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)
	tenantID := uuid.New().String()

	conn := makeProviderConnection(t, tenantID, &domain.HMACAuth{
		SecretRef:       "secret/hmac",
		Algorithm:       "sha256",
		SignatureHeader: "X-Signature",
	})
	require.NoError(t, repo.Upsert(ctx, conn))

	// Record failures until circuit trips
	require.NoError(t, conn.RecordFailure(1))
	require.NoError(t, repo.UpdateHealth(ctx, conn))

	found, err := repo.FindByID(ctx, tenantID, conn.ConnectionID)
	require.NoError(t, err)
	assert.Equal(t, domain.CircuitStateOpen, found.CircuitState)
	assert.Equal(t, 1, found.FailureCount)
	assert.NotNil(t, found.CircuitOpenedAt)
}

// TestConnectionRepository_UpdateHealth_NotFound returns ErrConnectionNotFound for missing record.
func TestConnectionRepository_UpdateHealth_NotFound(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)

	conn := &domain.ProviderConnection{
		TenantID:     uuid.New().String(),
		ConnectionID: uuid.New().String(),
		HealthStatus: domain.HealthStatusHealthy,
		CircuitState: domain.CircuitStateClosed,
		UpdatedAt:    time.Now(),
	}

	err := repo.UpdateHealth(ctx, conn)
	require.ErrorIs(t, err, ports.ErrConnectionNotFound)
}

// TestConnectionRepository_AuthConfig_AllVariants verifies that all AuthConfig variants
// round-trip correctly through serialization.
func TestConnectionRepository_AuthConfig_AllVariants(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)
	tenantID := uuid.New().String()

	authConfigs := []domain.AuthConfig{
		&domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "s/api-key"},
		&domain.BasicAuth{Username: "alice", PasswordRef: "s/pass"},
		&domain.OAuth2Auth{
			TokenURL:        "https://auth.example.com/token",
			ClientID:        "client-id",
			ClientSecretRef: "s/client-secret",
			Scopes:          []string{"read", "write"},
		},
		&domain.HMACAuth{SecretRef: "s/hmac", Algorithm: "sha256", SignatureHeader: "X-Sig"},
		&domain.MTLSAuth{ClientCertRef: "s/cert", ClientKeyRef: "s/key", CACertRef: "s/ca"},
	}

	for _, auth := range authConfigs {
		conn := makeProviderConnection(t, tenantID, auth)
		require.NoError(t, repo.Upsert(ctx, conn), "auth_type=%s", auth.AuthType())

		found, err := repo.FindByID(ctx, tenantID, conn.ConnectionID)
		require.NoError(t, err, "auth_type=%s", auth.AuthType())
		assert.Equal(t, auth.AuthType(), found.AuthConfig.AuthType(), "auth_type mismatch for %T", auth)
	}
}

// TestConnectionRepository_RetryPolicy_RoundTrip verifies retry policy fields survive persistence.
func TestConnectionRepository_RetryPolicy_RoundTrip(t *testing.T) {
	db, ctx := setupTestDB(t)

	repo := NewConnectionRepository(db)
	tenantID := uuid.New().String()

	conn := makeProviderConnection(t, tenantID, &domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "s1"})
	conn.RetryPolicy = domain.RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    500 * time.Millisecond,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 1.5,
	}
	conn.RateLimitConfig = domain.RateLimitConfig{
		RequestsPerSecond: 25.5,
		BurstSize:         50,
	}
	require.NoError(t, repo.Upsert(ctx, conn))

	found, err := repo.FindByID(ctx, tenantID, conn.ConnectionID)
	require.NoError(t, err)
	assert.Equal(t, 5, found.RetryPolicy.MaxAttempts)
	assert.InDelta(t, 0.5, found.RetryPolicy.InitialBackoff.Seconds(), 0.001)
	assert.InDelta(t, 30.0, found.RetryPolicy.MaxBackoff.Seconds(), 0.001)
	assert.InDelta(t, 1.5, found.RetryPolicy.BackoffMultiplier, 0.001)
	assert.InDelta(t, 25.5, found.RateLimitConfig.RequestsPerSecond, 0.001)
	assert.Equal(t, 50, found.RateLimitConfig.BurstSize)
}

// TestConnectionRepository_HealthStatus_AllValues verifies that all HealthStatus values round-trip.
func TestConnectionRepository_HealthStatus_AllValues(t *testing.T) {
	db, ctx := setupTestDB(t)
	repo := NewConnectionRepository(db)

	cases := []struct {
		domainStatus domain.HealthStatus
	}{
		{domain.HealthStatusUnknown},
		{domain.HealthStatusHealthy},
		{domain.HealthStatusDegraded},
		{domain.HealthStatusUnhealthy},
	}

	tenantID := uuid.New().String()
	for _, tc := range cases {
		conn := makeProviderConnection(t, tenantID, &domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "s"})
		require.NoError(t, repo.Upsert(ctx, conn))

		// Update health via domain method
		conn.UpdateHealthStatus(tc.domainStatus)
		require.NoError(t, repo.UpdateHealth(ctx, conn))

		found, err := repo.FindByID(ctx, tenantID, conn.ConnectionID)
		require.NoError(t, err)
		assert.Equal(t, tc.domainStatus, found.HealthStatus, "health status %q did not round-trip", tc.domainStatus)
	}
}
