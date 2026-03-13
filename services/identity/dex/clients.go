package dex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/dexidp/dex/storage"
)

// ClientConfig holds the registration details for an OIDC client.
type ClientConfig struct {
	// ID is the OAuth2 client_id.
	ID string
	// Secret is the OAuth2 client_secret. Empty for public clients.
	Secret string
	// Public marks the client as a public client (PKCE flow, no secret).
	Public bool
	// RedirectURIs is the set of allowed redirect URIs after authentication.
	RedirectURIs []string
	// Name is a human-readable display name for the client.
	Name string
}

// ErrClientIDRequired is returned when a ClientConfig has an empty ID.
var ErrClientIDRequired = errors.New("dex: client ID is required")

// ErrRedirectURIsRequired is returned when a ClientConfig has no redirect URIs.
var ErrRedirectURIsRequired = errors.New("dex: at least one redirect URI is required")

// ErrSecretRequiredForConfidentialClient is returned when a non-public client has no secret.
var ErrSecretRequiredForConfidentialClient = errors.New("dex: secret is required for confidential (non-public) clients")

// validate checks that required ClientConfig fields are set.
func (c *ClientConfig) validate() error {
	if c.ID == "" {
		return ErrClientIDRequired
	}
	if len(c.RedirectURIs) == 0 {
		return ErrRedirectURIsRequired
	}
	if !c.Public && c.Secret == "" {
		return ErrSecretRequiredForConfidentialClient
	}
	return nil
}

// DefaultDemoClient returns a public OIDC client configured for the Meridian
// demo environment. The baseDomain is used to construct the default redirect URI
// (e.g. "https://meridian.example.com/callback").
//
// Additional redirect URIs can be supplied via the DEX_REDIRECT_URIS environment
// variable as a comma-separated list.
func DefaultDemoClient(baseDomain string) ClientConfig {
	redirectURIs := []string{
		fmt.Sprintf("https://%s/api/auth/callback", baseDomain),
		fmt.Sprintf("https://%s/oauth/callback", baseDomain),
		"http://localhost:8090/api/auth/callback",
		"http://localhost:3000/api/auth/callback",
		"http://localhost:8091/oauth/callback",
	}

	if extra := os.Getenv("DEX_REDIRECT_URIS"); extra != "" {
		for _, uri := range strings.Split(extra, ",") {
			uri = strings.TrimSpace(uri)
			if uri != "" {
				redirectURIs = append(redirectURIs, uri)
			}
		}
	}

	return ClientConfig{
		ID:           "meridian-service",
		Public:       true,
		RedirectURIs: redirectURIs,
		Name:         "Meridian Service",
	}
}

// registerClients writes clients to Dex storage idempotently. If a client
// already exists with the same ID, it is updated to match the provided config.
func registerClients(ctx context.Context, s storage.Storage, clients []ClientConfig, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	for _, c := range clients {
		if err := c.validate(); err != nil {
			return fmt.Errorf("dex: invalid client %q: %w", c.ID, err)
		}

		client := storage.Client{
			ID:           c.ID,
			Secret:       c.Secret,
			Public:       c.Public,
			RedirectURIs: c.RedirectURIs,
			Name:         c.Name,
		}

		err := s.CreateClient(ctx, client)
		if err == nil {
			logger.Info("dex: registered OIDC client",
				"client_id", c.ID,
				"public", c.Public,
				"redirect_uris", c.RedirectURIs)
			continue
		}

		if errors.Is(err, storage.ErrAlreadyExists) {
			// Update the existing client to ensure config consistency.
			if updateErr := s.UpdateClient(ctx, c.ID, func(_ storage.Client) (storage.Client, error) {
				return client, nil
			}); updateErr != nil {
				return fmt.Errorf("dex: updating existing client %q: %w", c.ID, updateErr)
			}
			logger.Info("dex: updated existing OIDC client",
				"client_id", c.ID)
			continue
		}

		return fmt.Errorf("dex: registering client %q: %w", c.ID, err)
	}
	return nil
}
