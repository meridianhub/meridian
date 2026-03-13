package dex

import (
	"context"

	dexconnector "github.com/dexidp/dex/connector"
	meridianconnector "github.com/meridianhub/meridian/services/identity/connector"
)

// ConnectorAdapter adapts the Meridian PasswordConnector to Dex's
// connector.PasswordConnector interface. This bridges the two type systems
// so the existing Meridian authentication logic can be used as a Dex connector.
type ConnectorAdapter struct {
	inner meridianconnector.PasswordConnector
}

// NewConnectorAdapter wraps a Meridian PasswordConnector for use with Dex.
func NewConnectorAdapter(c meridianconnector.PasswordConnector) *ConnectorAdapter {
	return &ConnectorAdapter{inner: c}
}

// Prompt returns the label shown on the Dex login form's username field.
func (a *ConnectorAdapter) Prompt() string {
	return "Email"
}

// Login delegates credential validation to the Meridian connector and
// translates the result into Dex's connector.Identity type.
func (a *ConnectorAdapter) Login(ctx context.Context, s dexconnector.Scopes, username, password string) (dexconnector.Identity, bool, error) {
	// Convert Dex scopes to a string slice for the Meridian connector.
	var scopes []string
	if s.OfflineAccess {
		scopes = append(scopes, "offline_access")
	}
	if s.Groups {
		scopes = append(scopes, "groups")
	}

	identity, valid, err := a.inner.Login(ctx, scopes, username, password)
	if err != nil {
		return dexconnector.Identity{}, false, err
	}
	if !valid {
		return dexconnector.Identity{}, false, nil
	}

	return toDexIdentity(identity), true, nil
}

// toDexIdentity converts a Meridian connector.Identity to a Dex connector.Identity.
func toDexIdentity(id meridianconnector.Identity) dexconnector.Identity {
	return dexconnector.Identity{
		UserID:        id.UserID,
		Username:      id.Username,
		Email:         id.Email,
		EmailVerified: id.EmailVerified,
		Groups:        id.Groups,
		ConnectorData: id.ConnectorData,
	}
}

// Compile-time check that ConnectorAdapter implements Dex's PasswordConnector.
var _ dexconnector.PasswordConnector = (*ConnectorAdapter)(nil)
