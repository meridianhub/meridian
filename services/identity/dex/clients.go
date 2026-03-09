package dex

import (
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

// DefaultDemoClient returns a public OIDC client configured for the Meridian
// demo environment. The baseDomain is used to construct the default redirect URI
// (e.g. "https://meridian.example.com/callback").
//
// Additional redirect URIs can be supplied via the DEX_REDIRECT_URIS environment
// variable as a comma-separated list.
func DefaultDemoClient(baseDomain string) ClientConfig {
	redirectURIs := []string{
		fmt.Sprintf("https://%s/callback", baseDomain),
		"http://localhost:8080/callback",
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
// already exists with the same ID, the registration is skipped.
func registerClients(s storage.Storage, clients []ClientConfig, logger *slog.Logger) error {
	for _, c := range clients {
		client := storage.Client{
			ID:           c.ID,
			Secret:       c.Secret,
			Public:       c.Public,
			RedirectURIs: c.RedirectURIs,
			Name:         c.Name,
		}

		err := s.CreateClient(client)
		if err == nil {
			logger.Info("dex: registered OIDC client",
				"client_id", c.ID,
				"public", c.Public,
				"redirect_uris", c.RedirectURIs)
			continue
		}

		// Handle already-exists: Dex's in-memory storage returns storage.ErrAlreadyExists
		// but the interface only guarantees a generic error, so check both.
		if isAlreadyExistsError(err) {
			logger.Info("dex: OIDC client already registered, skipping",
				"client_id", c.ID)
			continue
		}

		return fmt.Errorf("dex: registering client %q: %w", c.ID, err)
	}
	return nil
}

// isAlreadyExistsError checks whether an error indicates a duplicate resource.
// Dex storage implementations may use different error types, so we check the
// error message as a fallback.
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	// Check for the well-known sentinel from dex/storage package.
	if errors.Is(err, storage.ErrAlreadyExists) {
		return true
	}
	// Fallback: some storage backends wrap the error differently.
	return strings.Contains(err.Error(), "already exists")
}
