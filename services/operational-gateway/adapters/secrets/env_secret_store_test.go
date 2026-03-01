package secrets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/operational-gateway/adapters/secrets"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// staticSlugResolver returns a fixed slug for any tenant ID.
type staticSlugResolver struct {
	slug string
	err  error
}

func (r *staticSlugResolver) GetSlug(_ context.Context, _ string) (string, error) {
	return r.slug, r.err
}

// errorSlugResolver always returns an error.
type errorSlugResolver struct{ err error }

func (r *errorSlugResolver) GetSlug(_ context.Context, _ string) (string, error) {
	return "", r.err
}

func TestEnvSecretStore_Resolve_Success(t *testing.T) {
	resolver := &staticSlugResolver{slug: "acme-corp"}
	store := secrets.NewEnvSecretStore(resolver)

	// env var name: TENANT_ACME_CORP_STRIPE_API_KEY
	t.Setenv("TENANT_ACME_CORP_STRIPE_API_KEY", "sk_live_xyz")

	got, err := store.Resolve(context.Background(), "tenant-001", "STRIPE_API_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk_live_xyz" {
		t.Fatalf("expected %q, got %q", "sk_live_xyz", got)
	}
}

func TestEnvSecretStore_Resolve_SecretNotFound(t *testing.T) {
	resolver := &staticSlugResolver{slug: "acme"}
	store := secrets.NewEnvSecretStore(resolver)

	// No environment variable set for this secret.
	_, err := store.Resolve(context.Background(), "tenant-001", "MISSING_SECRET")
	if !errors.Is(err, ports.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}

func TestEnvSecretStore_Resolve_SlugResolverError(t *testing.T) {
	slugErr := errors.New("slug lookup failed")
	resolver := &errorSlugResolver{err: slugErr}
	store := secrets.NewEnvSecretStore(resolver)

	_, err := store.Resolve(context.Background(), "tenant-001", "SOME_KEY")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, slugErr) {
		t.Fatalf("expected wrapped slug error, got: %v", err)
	}
}

func TestEnvSecretStore_Resolve_HyphensAndUnderscores(t *testing.T) {
	// Slugs with hyphens and secret refs with underscores should both normalise correctly.
	// slug "my-tenant" → "MY_TENANT", ref "WEBHOOK_SECRET" → TENANT_MY_TENANT_WEBHOOK_SECRET
	resolver := &staticSlugResolver{slug: "my-tenant"}
	store := secrets.NewEnvSecretStore(resolver)

	t.Setenv("TENANT_MY_TENANT_WEBHOOK_SECRET", "whsec_abc")

	got, err := store.Resolve(context.Background(), "ignored", "WEBHOOK_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "whsec_abc" {
		t.Fatalf("expected %q, got %q", "whsec_abc", got)
	}
}

func TestEnvSecretStore_Resolve_SecretRefWithHyphens(t *testing.T) {
	// Secret references that contain hyphens should be normalised to underscores.
	resolver := &staticSlugResolver{slug: "acme"}
	store := secrets.NewEnvSecretStore(resolver)

	// ref "stripe-api-key" → "STRIPE_API_KEY" → env var TENANT_ACME_STRIPE_API_KEY
	t.Setenv("TENANT_ACME_STRIPE_API_KEY", "sk_test_321")

	got, err := store.Resolve(context.Background(), "ignored", "stripe-api-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk_test_321" {
		t.Fatalf("expected %q, got %q", "sk_test_321", got)
	}
}

func TestNewEnvSecretStore_ImplementsSecretStore(_ *testing.T) {
	// Compile-time assertion: EnvSecretStore must satisfy the SecretStore port.
	var _ ports.SecretStore = secrets.NewEnvSecretStore(&staticSlugResolver{})
}
