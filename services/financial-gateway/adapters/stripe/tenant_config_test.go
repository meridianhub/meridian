package stripe

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantConfig_Validate_ValidConfig(t *testing.T) {
	tc := TenantConfig{
		ConnectedAccountID:    "acct_1234567890",
		WebhookEndpointSecret: "whsec_abc123",
	}
	err := tc.Validate()
	require.NoError(t, err)
}

func TestTenantConfig_Validate_MissingAccountID(t *testing.T) {
	tc := TenantConfig{
		WebhookEndpointSecret: "whsec_abc123",
	}
	err := tc.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAccountID)
}

func TestTenantConfig_Validate_EmptyConfig(t *testing.T) {
	tc := TenantConfig{}
	err := tc.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAccountID)
}

func TestTenantConfig_Validate_AccountIDOnly(t *testing.T) {
	// Validate only requires AccountID; WebhookEndpointSecret is optional
	tc := TenantConfig{
		ConnectedAccountID: "acct_minimally_valid",
	}
	err := tc.Validate()
	require.NoError(t, err)
}

func TestErrMissingAccountID_IsSentinel(t *testing.T) {
	// Verify ErrMissingAccountID is a distinct sentinel error
	assert.NotNil(t, ErrMissingAccountID)
	assert.NotEqual(t, ErrMissingAccountID, ErrTenantConfigNotFound)
}

func TestErrTenantConfigNotFound_IsSentinel(t *testing.T) {
	// Verify ErrTenantConfigNotFound is a distinct sentinel error
	assert.NotNil(t, ErrTenantConfigNotFound)
	assert.True(t, errors.Is(ErrTenantConfigNotFound, ErrTenantConfigNotFound))
	assert.False(t, errors.Is(ErrTenantConfigNotFound, ErrMissingAccountID))
}

// mockTenantConfigProvider is a test-local implementation of TenantConfigProvider.
type mockTenantConfigProvider struct {
	configs map[string]TenantConfig
}

func (m *mockTenantConfigProvider) GetTenantConfig(tenantID string) (TenantConfig, error) {
	cfg, ok := m.configs[tenantID]
	if !ok {
		return TenantConfig{}, ErrTenantConfigNotFound
	}
	return cfg, nil
}

func TestTenantConfigProvider_Found(t *testing.T) {
	provider := &mockTenantConfigProvider{
		configs: map[string]TenantConfig{
			"tenant-1": {
				ConnectedAccountID:    "acct_tenant1",
				WebhookEndpointSecret: "whsec_tenant1",
			},
		},
	}

	cfg, err := provider.GetTenantConfig("tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "acct_tenant1", cfg.ConnectedAccountID)
	assert.Equal(t, "whsec_tenant1", cfg.WebhookEndpointSecret)
}

func TestTenantConfigProvider_NotFound(t *testing.T) {
	provider := &mockTenantConfigProvider{
		configs: map[string]TenantConfig{},
	}

	_, err := provider.GetTenantConfig("unknown-tenant")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTenantConfigNotFound)
}
