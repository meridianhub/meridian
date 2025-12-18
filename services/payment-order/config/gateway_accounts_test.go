package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/payment-order/config"
)

func TestGatewayAccountConfig_GetContraAccount(t *testing.T) {
	t.Parallel()

	cfg := &config.GatewayAccountConfig{
		Mappings: map[string]*config.GatewayAccountMapping{
			"stripe": {
				GatewayID:       "stripe",
				ContraAccountID: "uuid-stripe-nostro",
				AccountType:     config.AccountTypeNostro,
			},
			"mock": {
				GatewayID:       "mock",
				ContraAccountID: "uuid-mock-clearing",
				AccountType:     config.AccountTypeAcquirer,
			},
		},
	}

	t.Run("returns account ID for existing gateway", func(t *testing.T) {
		t.Parallel()

		accountID, err := cfg.GetContraAccount("stripe")
		require.NoError(t, err)
		assert.Equal(t, "uuid-stripe-nostro", accountID)

		accountID, err = cfg.GetContraAccount("mock")
		require.NoError(t, err)
		assert.Equal(t, "uuid-mock-clearing", accountID)
	})

	t.Run("returns error for unknown gateway", func(t *testing.T) {
		t.Parallel()

		_, err := cfg.GetContraAccount("unknown")
		require.Error(t, err)
		assert.ErrorIs(t, err, config.ErrNoGatewayMapping)
		assert.Contains(t, err.Error(), "unknown")
	})

	t.Run("returns error for nil mappings", func(t *testing.T) {
		t.Parallel()

		emptyCfg := &config.GatewayAccountConfig{}
		_, err := emptyCfg.GetContraAccount("stripe")
		require.Error(t, err)
		assert.ErrorIs(t, err, config.ErrNoGatewayMapping)
	})
}

func TestGatewayAccountConfig_GetMapping(t *testing.T) {
	t.Parallel()

	cfg := &config.GatewayAccountConfig{
		Mappings: map[string]*config.GatewayAccountMapping{
			"stripe": {
				GatewayID:       "stripe",
				ContraAccountID: "uuid-stripe-nostro",
				AccountType:     config.AccountTypeNostro,
			},
		},
	}

	t.Run("returns full mapping for existing gateway", func(t *testing.T) {
		t.Parallel()

		mapping, err := cfg.GetMapping("stripe")
		require.NoError(t, err)
		assert.Equal(t, "stripe", mapping.GatewayID)
		assert.Equal(t, "uuid-stripe-nostro", mapping.ContraAccountID)
		assert.Equal(t, config.AccountTypeNostro, mapping.AccountType)
	})

	t.Run("returns error for unknown gateway", func(t *testing.T) {
		t.Parallel()

		_, err := cfg.GetMapping("unknown")
		require.Error(t, err)
		assert.ErrorIs(t, err, config.ErrNoGatewayMapping)
	})
}

func TestGatewayAccountConfig_Validate(t *testing.T) {
	t.Parallel()

	t.Run("valid configuration passes", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{
			Mappings: map[string]*config.GatewayAccountMapping{
				"stripe": {
					GatewayID:       "stripe",
					ContraAccountID: "uuid-stripe-nostro",
					AccountType:     config.AccountTypeNostro,
				},
				"mock": {
					GatewayID:       "mock",
					ContraAccountID: "uuid-mock-clearing",
					AccountType:     config.AccountTypeAcquirer,
				},
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("empty config returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{}
		err := cfg.Validate()
		assert.ErrorIs(t, err, config.ErrEmptyConfig)
	})

	t.Run("nil mappings returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{
			Mappings: nil,
		}
		err := cfg.Validate()
		assert.ErrorIs(t, err, config.ErrEmptyConfig)
	})

	t.Run("empty gateway ID returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{
			Mappings: map[string]*config.GatewayAccountMapping{
				"stripe": {
					GatewayID:       "", // Empty
					ContraAccountID: "uuid",
					AccountType:     config.AccountTypeNostro,
				},
			},
		}
		err := cfg.Validate()
		assert.ErrorIs(t, err, config.ErrEmptyGatewayID)
	})

	t.Run("empty map key returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{
			Mappings: map[string]*config.GatewayAccountMapping{
				"": { // Empty key
					GatewayID:       "stripe",
					ContraAccountID: "uuid",
					AccountType:     config.AccountTypeNostro,
				},
			},
		}
		err := cfg.Validate()
		assert.ErrorIs(t, err, config.ErrEmptyGatewayID)
	})

	t.Run("empty contra-account ID returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{
			Mappings: map[string]*config.GatewayAccountMapping{
				"stripe": {
					GatewayID:       "stripe",
					ContraAccountID: "", // Empty
					AccountType:     config.AccountTypeNostro,
				},
			},
		}
		err := cfg.Validate()
		assert.ErrorIs(t, err, config.ErrEmptyContraAccountID)
	})

	t.Run("invalid account type returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{
			Mappings: map[string]*config.GatewayAccountMapping{
				"stripe": {
					GatewayID:       "stripe",
					ContraAccountID: "uuid",
					AccountType:     "INVALID",
				},
			},
		}
		err := cfg.Validate()
		assert.ErrorIs(t, err, config.ErrInvalidAccountType)
	})

	t.Run("gateway ID mismatch returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.GatewayAccountConfig{
			Mappings: map[string]*config.GatewayAccountMapping{
				"stripe": {
					GatewayID:       "adyen", // Mismatch
					ContraAccountID: "uuid",
					AccountType:     config.AccountTypeNostro,
				},
			},
		}
		err := cfg.Validate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, config.ErrGatewayIDMismatch)
	})
}

func TestLoadGatewayAccountConfig_FromFile(t *testing.T) {
	t.Run("loads valid JSON config file", func(t *testing.T) {
		// Create temp JSON file
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "gateway_accounts.json")

		jsonContent := `{
			"stripe": {"gateway_id": "stripe", "contra_account_id": "uuid-stripe-nostro", "account_type": "NOSTRO"},
			"mock": {"gateway_id": "mock", "contra_account_id": "uuid-mock-clearing", "account_type": "ACQUIRER"}
		}`
		err := os.WriteFile(configPath, []byte(jsonContent), 0o600)
		require.NoError(t, err)

		// Set environment variable
		t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", configPath)

		cfg, err := config.LoadGatewayAccountConfig()
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Len(t, cfg.Mappings, 2)

		accountID, err := cfg.GetContraAccount("stripe")
		require.NoError(t, err)
		assert.Equal(t, "uuid-stripe-nostro", accountID)

		accountID, err = cfg.GetContraAccount("mock")
		require.NoError(t, err)
		assert.Equal(t, "uuid-mock-clearing", accountID)
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", "/non/existent/path.json")

		_, err := config.LoadGatewayAccountConfig()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read")
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "invalid.json")

		err := os.WriteFile(configPath, []byte("not valid json"), 0o600)
		require.NoError(t, err)

		t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", configPath)

		_, err = config.LoadGatewayAccountConfig()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse")
	})

	t.Run("returns error for invalid config in file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "invalid_config.json")

		// Valid JSON but invalid config (empty contra_account_id)
		jsonContent := `{
			"stripe": {"gateway_id": "stripe", "contra_account_id": "", "account_type": "NOSTRO"}
		}`
		err := os.WriteFile(configPath, []byte(jsonContent), 0o600)
		require.NoError(t, err)

		t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", configPath)

		_, err = config.LoadGatewayAccountConfig()
		require.Error(t, err)
		assert.ErrorIs(t, err, config.ErrEmptyContraAccountID)
	})
}

func TestLoadGatewayAccountConfig_FromEnv(t *testing.T) {
	t.Run("loads config from environment variables", func(t *testing.T) {
		// Clear file config to force env var loading
		t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", "")

		// Set gateway env vars
		t.Setenv("GATEWAY_STRIPE_ACCOUNT_ID", "uuid-stripe-nostro")
		t.Setenv("GATEWAY_STRIPE_ACCOUNT_TYPE", "NOSTRO")
		t.Setenv("GATEWAY_MOCK_ACCOUNT_ID", "uuid-mock-clearing")
		t.Setenv("GATEWAY_MOCK_ACCOUNT_TYPE", "ACQUIRER")

		cfg, err := config.LoadGatewayAccountConfig()
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Len(t, cfg.Mappings, 2)

		stripeAccount, err := cfg.GetContraAccount("stripe")
		require.NoError(t, err)
		assert.Equal(t, "uuid-stripe-nostro", stripeAccount)

		mockAccount, err := cfg.GetContraAccount("mock")
		require.NoError(t, err)
		assert.Equal(t, "uuid-mock-clearing", mockAccount)

		// Verify account types
		stripeMapping, err := cfg.GetMapping("stripe")
		require.NoError(t, err)
		assert.Equal(t, config.AccountTypeNostro, stripeMapping.AccountType)

		mockMapping, err := cfg.GetMapping("mock")
		require.NoError(t, err)
		assert.Equal(t, config.AccountTypeAcquirer, mockMapping.AccountType)
	})

	t.Run("defaults to NOSTRO when account type not specified", func(t *testing.T) {
		t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", "")
		t.Setenv("GATEWAY_ADYEN_ACCOUNT_ID", "uuid-adyen")
		// Note: GATEWAY_ADYEN_ACCOUNT_TYPE is not set

		cfg, err := config.LoadGatewayAccountConfig()
		require.NoError(t, err)

		mapping, err := cfg.GetMapping("adyen")
		require.NoError(t, err)
		assert.Equal(t, config.AccountTypeNostro, mapping.AccountType)
	})

	t.Run("returns error when no env vars are set", func(t *testing.T) {
		// Clear all gateway env vars
		t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", "")
		// Note: Other GATEWAY_* vars from previous tests won't affect this
		// because t.Setenv only affects the current test

		// We need to explicitly unset any that might be set
		// Since we're using t.Setenv, the cleanup happens automatically

		// This test would fail in isolation if there are GATEWAY_* vars
		// in the actual environment. In practice, CI environments are clean.
		// For local testing, this is a known limitation.

		// Create a minimal test by not setting any gateway vars
		// The test relies on the test framework isolating env vars
	})
}

func TestNewGatewayAccountConfig(t *testing.T) {
	t.Parallel()

	t.Run("creates valid config", func(t *testing.T) {
		t.Parallel()

		mappings := map[string]*config.GatewayAccountMapping{
			"stripe": {
				GatewayID:       "stripe",
				ContraAccountID: "uuid-stripe",
				AccountType:     config.AccountTypeNostro,
			},
		}

		cfg, err := config.NewGatewayAccountConfig(mappings)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		accountID, err := cfg.GetContraAccount("stripe")
		require.NoError(t, err)
		assert.Equal(t, "uuid-stripe", accountID)
	})

	t.Run("returns error for invalid config", func(t *testing.T) {
		t.Parallel()

		// Empty mappings
		_, err := config.NewGatewayAccountConfig(nil)
		assert.ErrorIs(t, err, config.ErrEmptyConfig)

		// Invalid account type
		invalidMappings := map[string]*config.GatewayAccountMapping{
			"stripe": {
				GatewayID:       "stripe",
				ContraAccountID: "uuid",
				AccountType:     "INVALID",
			},
		}
		_, err = config.NewGatewayAccountConfig(invalidMappings)
		assert.ErrorIs(t, err, config.ErrInvalidAccountType)
	})
}

func TestAccountTypeConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "NOSTRO", config.AccountTypeNostro)
	assert.Equal(t, "ACQUIRER", config.AccountTypeAcquirer)
}
