package stripe

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantConfig_ValidConfigWithBothFields(t *testing.T) {
	tc := TenantConfig{
		ConnectedAccountID:    "acct_1234567890",
		WebhookEndpointSecret: "whsec_abc123",
	}
	err := tc.Validate()
	require.NoError(t, err)
}

func TestTenantConfig_MissingAccountID_WebhookSet(t *testing.T) {
	// Webhook alone is not sufficient - account ID is required
	tc := TenantConfig{
		WebhookEndpointSecret: "whsec_abc123",
	}
	err := tc.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAccountID)
}

func TestTenantConfig_AccountIDOnly_IsValid(t *testing.T) {
	// WebhookEndpointSecret is optional; AccountID alone is sufficient
	tc := TenantConfig{
		ConnectedAccountID: "acct_minimally_valid",
	}
	err := tc.Validate()
	require.NoError(t, err)
}

func TestTenantConfigErrors_AreDistinct(t *testing.T) {
	assert.NotNil(t, ErrMissingAccountID)
	assert.NotNil(t, ErrTenantConfigNotFound)
	assert.NotEqual(t, ErrMissingAccountID, ErrTenantConfigNotFound)
	assert.False(t, errors.Is(ErrTenantConfigNotFound, ErrMissingAccountID))
	assert.False(t, errors.Is(ErrMissingAccountID, ErrTenantConfigNotFound))
}

// testTenantConfigProvider is a test-local implementation of TenantConfigProvider.
type testTenantConfigProvider struct {
	configs map[string]TenantConfig
}

func (m *testTenantConfigProvider) GetTenantConfig(tenantID string) (TenantConfig, error) {
	cfg, ok := m.configs[tenantID]
	if !ok {
		return TenantConfig{}, ErrTenantConfigNotFound
	}
	return cfg, nil
}

func TestTenantConfigProvider_ReturnsConfig(t *testing.T) {
	provider := &testTenantConfigProvider{
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

func TestTenantConfigProvider_ReturnsNotFoundError(t *testing.T) {
	provider := &testTenantConfigProvider{
		configs: map[string]TenantConfig{},
	}

	_, err := provider.GetTenantConfig("unknown-tenant")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTenantConfigNotFound)
}

func TestTenantConfig_FieldAccess(t *testing.T) {
	tc := TenantConfig{
		ConnectedAccountID:    "acct_abc",
		WebhookEndpointSecret: "whsec_xyz",
	}

	assert.Equal(t, "acct_abc", tc.ConnectedAccountID)
	assert.Equal(t, "whsec_xyz", tc.WebhookEndpointSecret)
}
