// Package secrets provides adapters for resolving tenant secrets.
package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// TenantSlugResolver looks up the human-readable slug for a tenant ID.
// The slug is used to construct environment variable names so that secrets
// are namespaced per tenant without embedding UUIDs in variable names.
type TenantSlugResolver interface {
	// GetSlug returns the slug for the given tenantID, e.g. "acme-corp".
	// Returns an error if the tenant cannot be found or the lookup fails.
	GetSlug(ctx context.Context, tenantID string) (string, error)
}

// EnvSecretStore resolves tenant secrets from environment variables.
//
// Environment variable naming convention:
//
//	TENANT_{SLUG}_{SECRET_REF}
//
// where SLUG and SECRET_REF are uppercased and hyphens are replaced with
// underscores. For example, a tenant with slug "acme-corp" and secret
// reference "STRIPE_API_KEY" resolves to:
//
//	TENANT_ACME_CORP_STRIPE_API_KEY
//
// This adapter is intended for Phase 1 deployments where secrets are
// injected as environment variables via Kubernetes Secrets. For production
// workloads at scale, replace this adapter with one backed by a secrets
// manager (e.g. AWS SSM Parameter Store, HashiCorp Vault).
//
// Security note: secret values are never logged.
type EnvSecretStore struct {
	slugResolver TenantSlugResolver
}

// NewEnvSecretStore creates a new EnvSecretStore with the given slug resolver.
func NewEnvSecretStore(resolver TenantSlugResolver) *EnvSecretStore {
	return &EnvSecretStore{slugResolver: resolver}
}

// Resolve looks up the secret value from an environment variable.
// It implements ports.SecretStore.
//
// Returns ports.ErrSecretNotFound if the environment variable is not set.
// Returns a wrapped error if the slug resolver fails.
func (s *EnvSecretStore) Resolve(ctx context.Context, tenantID, secretRef string) (string, error) {
	slug, err := s.slugResolver.GetSlug(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve slug for tenant %q: %w", tenantID, err)
	}

	envName := buildEnvName(slug, secretRef)

	value, ok := os.LookupEnv(envName)
	if !ok {
		return "", fmt.Errorf("%w: %s", ports.ErrSecretNotFound, envName)
	}

	return value, nil
}

// buildEnvName constructs the environment variable name for a given tenant slug
// and secret reference using the convention TENANT_{SLUG}_{SECRET_REF}.
func buildEnvName(slug, secretRef string) string {
	return "TENANT_" + toEnvName(slug) + "_" + toEnvName(secretRef)
}

// toEnvName normalises a string for use in an environment variable name:
// uppercases all characters and replaces hyphens with underscores.
func toEnvName(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
}
