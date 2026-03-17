package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
)

var (
	// ErrServiceAuthClientIDRequired is returned when SERVICE_CLIENT_ID is missing
	ErrServiceAuthClientIDRequired = errors.New("SERVICE_CLIENT_ID is required when service auth is enabled")
	// ErrServiceAuthClientSecretRequired is returned when SERVICE_CLIENT_SECRET is missing
	ErrServiceAuthClientSecretRequired = errors.New("SERVICE_CLIENT_SECRET is required when service auth is enabled")
	// ErrServiceAuthTokenURLRequired is returned when SERVICE_TOKEN_URL is missing
	ErrServiceAuthTokenURLRequired = errors.New("SERVICE_TOKEN_URL is required when service auth is enabled")
)

// ServiceAuthConfig holds configuration for service-to-service JWT authentication.
// When Enabled, outbound gRPC calls will include a bearer token obtained via
// OAuth2 client credentials flow.
type ServiceAuthConfig struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	TokenURL     string
	Scopes       []string
}

// NewServiceAuthConfigFromEnv reads service auth configuration from environment variables.
//
// Environment variables:
//   - SERVICE_AUTH_ENABLED: "true" to enable (default: false)
//   - SERVICE_CLIENT_ID: OAuth2 client ID (required when enabled)
//   - SERVICE_CLIENT_SECRET: OAuth2 client secret (required when enabled)
//   - SERVICE_TOKEN_URL: OAuth2 token endpoint (required when enabled)
//   - SERVICE_SCOPES: Comma-separated scopes (optional)
func NewServiceAuthConfigFromEnv() ServiceAuthConfig {
	enabled := strings.EqualFold(os.Getenv("SERVICE_AUTH_ENABLED"), "true")

	cfg := ServiceAuthConfig{
		Enabled:      enabled,
		ClientID:     os.Getenv("SERVICE_CLIENT_ID"),
		ClientSecret: os.Getenv("SERVICE_CLIENT_SECRET"),
		TokenURL:     os.Getenv("SERVICE_TOKEN_URL"),
	}

	if scopes := os.Getenv("SERVICE_SCOPES"); scopes != "" {
		cfg.Scopes = splitScopes(scopes)
	}

	return cfg
}

// NewCredentials creates ServiceCredentials from this configuration.
// Returns (nil, nil) when service auth is disabled.
func (c ServiceAuthConfig) NewCredentials() (*ServiceCredentials, error) {
	if !c.Enabled {
		return nil, nil //nolint:nilnil // Disabled mode intentionally returns no credentials and no error
	}

	if c.ClientID == "" {
		return nil, fmt.Errorf("failed to create service credentials: %w", ErrServiceAuthClientIDRequired)
	}
	if c.ClientSecret == "" {
		return nil, fmt.Errorf("failed to create service credentials: %w", ErrServiceAuthClientSecretRequired)
	}
	if c.TokenURL == "" {
		return nil, fmt.Errorf("failed to create service credentials: %w", ErrServiceAuthTokenURLRequired)
	}

	oauthClient, err := NewOAuth2Client(&OAuth2Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		TokenURL:     c.TokenURL,
		Scopes:       c.Scopes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth2 client for service auth: %w", err)
	}

	return NewServiceCredentials(oauthClient)
}

// NewServiceCredentialsDialOption returns a grpc.DialOption that attaches service
// credentials to outbound calls. Returns nil if creds is nil (service auth disabled).
func NewServiceCredentialsDialOption(creds *ServiceCredentials) grpc.DialOption {
	if creds == nil {
		return nil
	}
	return grpc.WithPerRPCCredentials(creds)
}
