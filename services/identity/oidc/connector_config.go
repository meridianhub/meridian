// Package oidc provides configuration models for external OIDC identity providers
// that can be registered with the Dex sidecar container.
package oidc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ConnectorType identifies the kind of external identity provider.
type ConnectorType string

const (
	// ConnectorTypeOIDC is a generic OpenID Connect provider.
	ConnectorTypeOIDC ConnectorType = "oidc"
	// ConnectorTypeGoogle is a Google OAuth2 connector.
	ConnectorTypeGoogle ConnectorType = "google"
	// ConnectorTypeGitHub is a GitHub OAuth connector.
	ConnectorTypeGitHub ConnectorType = "github"
	// ConnectorTypeMicrosoft is a Microsoft/Azure AD connector.
	ConnectorTypeMicrosoft ConnectorType = "microsoft"
)

// Connector configuration errors.
var (
	// ErrConnectorIDRequired is returned when a connector has no ID.
	ErrConnectorIDRequired = errors.New("connector: id is required")
	// ErrConnectorTypeRequired is returned when a connector has no type.
	ErrConnectorTypeRequired = errors.New("connector: type is required")
	// ErrConnectorTypeInvalid is returned when a connector has an unrecognized type.
	ErrConnectorTypeInvalid = errors.New("connector: type is invalid")
	// ErrClientIDRequired is returned when client_id is missing.
	ErrClientIDRequired = errors.New("connector: client_id is required")
	// ErrClientSecretRequired is returned when client_secret is missing.
	ErrClientSecretRequired = errors.New("connector: client_secret is required")
	// ErrIssuerURLRequired is returned when issuer_url is missing for OIDC connectors.
	ErrIssuerURLRequired = errors.New("connector: issuer_url is required for oidc type")
	// ErrDuplicateConnectorID is returned when two connectors share the same ID.
	ErrDuplicateConnectorID = errors.New("connector: duplicate connector id")
)

// ConnectorConfig represents the configuration for an external identity provider
// registered with the Dex sidecar. This model is used both for generating the
// Dex sidecar configuration and for the provider discovery API.
type ConnectorConfig struct {
	// ID is the unique identifier for this connector (e.g., "google", "github").
	ID string `json:"id"`
	// Type is the connector type (oidc, google, github, microsoft).
	Type ConnectorType `json:"type"`
	// Name is the human-readable display name (e.g., "Google", "GitHub").
	Name string `json:"name"`
	// ClientID is the OAuth2 client ID.
	ClientID string `json:"clientId"`
	// ClientSecret is the OAuth2 client secret.
	ClientSecret string `json:"clientSecret"`
	// IssuerURL is the OIDC issuer URL. Required for generic OIDC connectors.
	IssuerURL string `json:"issuerUrl,omitempty"`
	// RedirectURI is the OAuth2 callback URI registered with the provider.
	RedirectURI string `json:"redirectUri,omitempty"`
	// Scopes overrides the default OAuth2 scopes requested.
	Scopes []string `json:"scopes,omitempty"`
	// HostedDomain restricts login to a specific domain (Google-specific).
	HostedDomain string `json:"hostedDomain,omitempty"`
	// Tenant is the Azure AD tenant ID (Microsoft-specific).
	Tenant string `json:"tenant,omitempty"`
}

// Validate checks that the connector configuration has all required fields
// for its type.
func (c *ConnectorConfig) Validate() error {
	if c.ID == "" {
		return ErrConnectorIDRequired
	}
	if c.Type == "" {
		return ErrConnectorTypeRequired
	}
	if !isValidConnectorType(c.Type) {
		return fmt.Errorf("%w: %s", ErrConnectorTypeInvalid, c.Type)
	}
	if c.ClientID == "" {
		return ErrClientIDRequired
	}
	if c.ClientSecret == "" {
		return ErrClientSecretRequired
	}
	if c.Type == ConnectorTypeOIDC && c.IssuerURL == "" {
		return ErrIssuerURLRequired
	}
	return nil
}

func isValidConnectorType(t ConnectorType) bool {
	switch t {
	case ConnectorTypeOIDC, ConnectorTypeGoogle, ConnectorTypeGitHub, ConnectorTypeMicrosoft:
		return true
	default:
		return false
	}
}

// ValidateConnectors validates a slice of connector configurations,
// checking each individually and ensuring no duplicate IDs.
func ValidateConnectors(connectors []ConnectorConfig) error {
	seen := make(map[string]struct{}, len(connectors))
	for i := range connectors {
		if err := connectors[i].Validate(); err != nil {
			return fmt.Errorf("connector[%d] (%s): %w", i, connectors[i].ID, err)
		}
		if _, exists := seen[connectors[i].ID]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateConnectorID, connectors[i].ID)
		}
		seen[connectors[i].ID] = struct{}{}
	}
	return nil
}

// LoadConnectorsFromEnv loads connector configurations from environment variables.
// It supports two modes:
//
//  1. JSON mode: DEX_CONNECTORS contains a JSON array of connector configs.
//  2. Individual mode: DEX_CONNECTOR_{TYPE}_{FIELD} environment variables
//     (e.g., DEX_CONNECTOR_GOOGLE_CLIENT_ID, DEX_CONNECTOR_GITHUB_CLIENT_SECRET).
//
// JSON mode takes precedence. Returns nil (no error) when no connectors are configured.
func LoadConnectorsFromEnv() ([]ConnectorConfig, error) {
	// JSON mode
	if raw := os.Getenv("DEX_CONNECTORS"); raw != "" {
		var connectors []ConnectorConfig
		if err := json.Unmarshal([]byte(raw), &connectors); err != nil {
			return nil, fmt.Errorf("connector: failed to parse DEX_CONNECTORS: %w", err)
		}
		if err := ValidateConnectors(connectors); err != nil {
			return nil, err
		}
		return connectors, nil
	}

	// Individual environment variable mode
	connectors := loadConnectorsFromIndividualEnv()

	if len(connectors) > 0 {
		if err := ValidateConnectors(connectors); err != nil {
			return nil, err
		}
	}

	return connectors, nil
}

// envConnectorDef describes a well-known connector that can be configured
// via individual environment variables.
type envConnectorDef struct {
	envPrefix string
	id        string
	connType  ConnectorType
	name      string
}

// knownConnectorDefs lists the well-known connector types that can be
// configured via individual environment variables.
var knownConnectorDefs = []envConnectorDef{
	{"DEX_CONNECTOR_GOOGLE", "google", ConnectorTypeGoogle, "Google"},
	{"DEX_CONNECTOR_GITHUB", "github", ConnectorTypeGitHub, "GitHub"},
	{"DEX_CONNECTOR_MICROSOFT", "microsoft", ConnectorTypeMicrosoft, "Microsoft"},
}

// loadConnectorsFromIndividualEnv scans for connectors configured via
// DEX_CONNECTOR_{TYPE}_{FIELD} environment variables.
func loadConnectorsFromIndividualEnv() []ConnectorConfig {
	var connectors []ConnectorConfig

	for _, kc := range knownConnectorDefs {
		if cc, ok := loadSingleConnectorFromEnv(kc); ok {
			connectors = append(connectors, cc)
		}
	}

	return connectors
}

// loadSingleConnectorFromEnv attempts to load a single connector from
// environment variables matching the given definition.
func loadSingleConnectorFromEnv(def envConnectorDef) (ConnectorConfig, bool) {
	clientID := os.Getenv(def.envPrefix + "_CLIENT_ID")
	clientSecret := os.Getenv(def.envPrefix + "_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return ConnectorConfig{}, false
	}

	cc := ConnectorConfig{
		ID:           def.id,
		Type:         def.connType,
		Name:         def.name,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  os.Getenv(def.envPrefix + "_REDIRECT_URI"),
	}

	if scopesRaw := os.Getenv(def.envPrefix + "_SCOPES"); scopesRaw != "" {
		cc.Scopes = splitAndTrim(scopesRaw, ",")
	}

	if def.connType == ConnectorTypeGoogle {
		cc.HostedDomain = os.Getenv(def.envPrefix + "_HOSTED_DOMAIN")
	}
	if def.connType == ConnectorTypeMicrosoft {
		cc.Tenant = os.Getenv(def.envPrefix + "_TENANT")
	}

	return cc, true
}

// splitAndTrim splits a string by sep and trims whitespace from each element.
func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
