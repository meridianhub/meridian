// Package dex provides an embedded Dex OIDC identity provider that runs
// in-process with the identity service. It uses Dex's in-memory storage
// and delegates credential validation to the Meridian connector.
package dex

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/meridianhub/meridian/services/identity/connector"
)

// Config holds the configuration for the embedded Dex server.
type Config struct {
	// Issuer is the OIDC issuer URL (e.g. "https://auth.example.com/dex").
	Issuer string

	// Connector is the Meridian password connector used for credential validation.
	Connector connector.PasswordConnector

	// Logger is the structured logger. If nil, a default logger is used.
	Logger *slog.Logger

	// Clients is the list of OIDC clients to register on startup.
	Clients []ClientConfig

	// SkipApprovalScreen skips the Dex consent screen when true.
	SkipApprovalScreen bool
}

// ErrIssuerRequired is returned when Config.Issuer is empty.
var ErrIssuerRequired = errors.New("dex: issuer URL is required")

// ErrConnectorRequired is returned when Config.Connector is nil.
var ErrConnectorRequired = errors.New("dex: password connector is required")

// validate checks that required configuration fields are set.
func (c *Config) validate() error {
	if c.Issuer == "" {
		return ErrIssuerRequired
	}
	if c.Connector == nil {
		return ErrConnectorRequired
	}
	for i := range c.Clients {
		if err := c.Clients[i].validate(); err != nil {
			return fmt.Errorf("dex: client[%d]: %w", i, err)
		}
	}
	return nil
}
