// Package ports defines the interfaces (ports) for the operational-gateway service.
package ports

import (
	"context"
	"errors"
)

// ErrSecretNotFound is returned when a secret cannot be found for the given tenant and reference.
var ErrSecretNotFound = errors.New("secret not found")

// SecretStore resolves tenant secrets by reference at dispatch time.
// Implementations may read from environment variables, a secrets manager (e.g. AWS SSM,
// HashiCorp Vault), or other backends. The interface is intentionally narrow so that
// adapters can be swapped without changing the dispatch logic.
type SecretStore interface {
	// Resolve returns the plaintext secret value for the given tenant and secret reference.
	// Returns ErrSecretNotFound if no value exists for the combination.
	Resolve(ctx context.Context, tenantID, secretRef string) (string, error)
}
