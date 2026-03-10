package oidc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectorConfig_Validate_Success(t *testing.T) {
	tests := []struct {
		name      string
		connector ConnectorConfig
	}{
		{
			name: "valid google connector",
			connector: ConnectorConfig{
				ID:           "google",
				Type:         ConnectorTypeGoogle,
				Name:         "Google",
				ClientID:     "google-client-id",
				ClientSecret: "google-client-secret",
			},
		},
		{
			name: "valid github connector",
			connector: ConnectorConfig{
				ID:           "github",
				Type:         ConnectorTypeGitHub,
				Name:         "GitHub",
				ClientID:     "github-client-id",
				ClientSecret: "github-client-secret",
			},
		},
		{
			name: "valid microsoft connector",
			connector: ConnectorConfig{
				ID:           "microsoft",
				Type:         ConnectorTypeMicrosoft,
				Name:         "Microsoft",
				ClientID:     "ms-client-id",
				ClientSecret: "ms-client-secret",
				Tenant:       "my-tenant",
			},
		},
		{
			name: "valid generic oidc connector with issuer",
			connector: ConnectorConfig{
				ID:           "custom-idp",
				Type:         ConnectorTypeOIDC,
				Name:         "Custom IDP",
				ClientID:     "custom-client-id",
				ClientSecret: "custom-client-secret",
				IssuerURL:    "https://idp.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.connector.Validate()
			require.NoError(t, err)
		})
	}
}

func TestConnectorConfig_Validate_Errors(t *testing.T) {
	tests := []struct {
		name      string
		connector ConnectorConfig
		wantErr   error
	}{
		{
			name:      "missing id",
			connector: ConnectorConfig{Type: ConnectorTypeGoogle, ClientID: "x", ClientSecret: "y"},
			wantErr:   ErrConnectorIDRequired,
		},
		{
			name:      "missing type",
			connector: ConnectorConfig{ID: "google", ClientID: "x", ClientSecret: "y"},
			wantErr:   ErrConnectorTypeRequired,
		},
		{
			name:      "invalid type",
			connector: ConnectorConfig{ID: "x", Type: "invalid", ClientID: "x", ClientSecret: "y"},
			wantErr:   ErrConnectorTypeInvalid,
		},
		{
			name:      "missing client_id",
			connector: ConnectorConfig{ID: "google", Type: ConnectorTypeGoogle, ClientSecret: "y"},
			wantErr:   ErrClientIDRequired,
		},
		{
			name:      "missing client_secret",
			connector: ConnectorConfig{ID: "google", Type: ConnectorTypeGoogle, ClientID: "x"},
			wantErr:   ErrClientSecretRequired,
		},
		{
			name: "oidc type missing issuer_url",
			connector: ConnectorConfig{
				ID: "custom", Type: ConnectorTypeOIDC,
				ClientID: "x", ClientSecret: "y",
			},
			wantErr: ErrIssuerURLRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.connector.Validate()
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestValidateConnectors_DuplicateID(t *testing.T) {
	connectors := []ConnectorConfig{
		{ID: "google", Type: ConnectorTypeGoogle, Name: "Google", ClientID: "a", ClientSecret: "b"},
		{ID: "google", Type: ConnectorTypeGoogle, Name: "Google 2", ClientID: "c", ClientSecret: "d"},
	}

	err := ValidateConnectors(connectors)
	require.ErrorIs(t, err, ErrDuplicateConnectorID)
}

func TestValidateConnectors_PropagatesValidationError(t *testing.T) {
	connectors := []ConnectorConfig{
		{ID: "google", Type: ConnectorTypeGoogle, Name: "Google", ClientID: "a", ClientSecret: "b"},
		{ID: "bad", Type: ConnectorTypeOIDC, Name: "Bad", ClientID: "c", ClientSecret: "d"},
		// Missing IssuerURL for OIDC type
	}

	err := ValidateConnectors(connectors)
	require.ErrorIs(t, err, ErrIssuerURLRequired)
	assert.Contains(t, err.Error(), "connector[1]")
}

func TestValidateConnectors_EmptySlice(t *testing.T) {
	err := ValidateConnectors(nil)
	require.NoError(t, err)
}

func TestLoadConnectorsFromEnv_JSONMode(t *testing.T) {
	t.Setenv("DEX_CONNECTORS", `[
		{"id":"google","type":"google","name":"Google","clientId":"gid","clientSecret":"gsecret"},
		{"id":"github","type":"github","name":"GitHub","clientId":"ghid","clientSecret":"ghsecret"}
	]`)

	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	require.Len(t, connectors, 2)
	assert.Equal(t, "google", connectors[0].ID)
	assert.Equal(t, ConnectorTypeGoogle, connectors[0].Type)
	assert.Equal(t, "github", connectors[1].ID)
}

func TestLoadConnectorsFromEnv_JSONMode_InvalidJSON(t *testing.T) {
	t.Setenv("DEX_CONNECTORS", `{invalid`)

	_, err := LoadConnectorsFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse DEX_CONNECTORS")
}

func TestLoadConnectorsFromEnv_JSONMode_ValidationError(t *testing.T) {
	t.Setenv("DEX_CONNECTORS", `[{"id":"","type":"google","clientId":"x","clientSecret":"y"}]`)

	_, err := LoadConnectorsFromEnv()
	require.ErrorIs(t, err, ErrConnectorIDRequired)
}

func TestLoadConnectorsFromEnv_IndividualMode_Google(t *testing.T) {
	t.Setenv("DEX_CONNECTOR_GOOGLE_CLIENT_ID", "google-id")
	t.Setenv("DEX_CONNECTOR_GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("DEX_CONNECTOR_GOOGLE_HOSTED_DOMAIN", "example.com")
	t.Setenv("DEX_CONNECTOR_GOOGLE_REDIRECT_URI", "https://dex.example.com/callback")
	t.Setenv("DEX_CONNECTOR_GOOGLE_SCOPES", "openid, email, profile")

	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	require.Len(t, connectors, 1)

	c := connectors[0]
	assert.Equal(t, "google", c.ID)
	assert.Equal(t, ConnectorTypeGoogle, c.Type)
	assert.Equal(t, "Google", c.Name)
	assert.Equal(t, "google-id", c.ClientID)
	assert.Equal(t, "google-secret", c.ClientSecret)
	assert.Equal(t, "example.com", c.HostedDomain)
	assert.Equal(t, "https://dex.example.com/callback", c.RedirectURI)
	assert.Equal(t, []string{"openid", "email", "profile"}, c.Scopes)
}

func TestLoadConnectorsFromEnv_IndividualMode_GitHub(t *testing.T) {
	t.Setenv("DEX_CONNECTOR_GITHUB_CLIENT_ID", "gh-id")
	t.Setenv("DEX_CONNECTOR_GITHUB_CLIENT_SECRET", "gh-secret")

	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	require.Len(t, connectors, 1)
	assert.Equal(t, "github", connectors[0].ID)
	assert.Equal(t, ConnectorTypeGitHub, connectors[0].Type)
}

func TestLoadConnectorsFromEnv_IndividualMode_Microsoft(t *testing.T) {
	t.Setenv("DEX_CONNECTOR_MICROSOFT_CLIENT_ID", "ms-id")
	t.Setenv("DEX_CONNECTOR_MICROSOFT_CLIENT_SECRET", "ms-secret")
	t.Setenv("DEX_CONNECTOR_MICROSOFT_TENANT", "my-org-tenant")

	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	require.Len(t, connectors, 1)
	assert.Equal(t, "microsoft", connectors[0].ID)
	assert.Equal(t, "my-org-tenant", connectors[0].Tenant)
}

func TestLoadConnectorsFromEnv_IndividualMode_MultipleProviders(t *testing.T) {
	t.Setenv("DEX_CONNECTOR_GOOGLE_CLIENT_ID", "g-id")
	t.Setenv("DEX_CONNECTOR_GOOGLE_CLIENT_SECRET", "g-secret")
	t.Setenv("DEX_CONNECTOR_GITHUB_CLIENT_ID", "gh-id")
	t.Setenv("DEX_CONNECTOR_GITHUB_CLIENT_SECRET", "gh-secret")

	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	require.Len(t, connectors, 2)
	assert.Equal(t, "google", connectors[0].ID)
	assert.Equal(t, "github", connectors[1].ID)
}

func TestLoadConnectorsFromEnv_NoConnectors(t *testing.T) {
	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	assert.Nil(t, connectors)
}

func TestLoadConnectorsFromEnv_PartialEnvVars_Skipped(t *testing.T) {
	// Only client_id set, no secret - should be skipped
	t.Setenv("DEX_CONNECTOR_GOOGLE_CLIENT_ID", "g-id")

	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	assert.Nil(t, connectors)
}

func TestLoadConnectorsFromEnv_JSONTakesPrecedence(t *testing.T) {
	t.Setenv("DEX_CONNECTORS", `[{"id":"google","type":"google","name":"From JSON","clientId":"j-id","clientSecret":"j-secret"}]`)
	t.Setenv("DEX_CONNECTOR_GOOGLE_CLIENT_ID", "env-id")
	t.Setenv("DEX_CONNECTOR_GOOGLE_CLIENT_SECRET", "env-secret")

	connectors, err := LoadConnectorsFromEnv()
	require.NoError(t, err)
	require.Len(t, connectors, 1)
	assert.Equal(t, "From JSON", connectors[0].Name)
	assert.Equal(t, "j-id", connectors[0].ClientID)
}
