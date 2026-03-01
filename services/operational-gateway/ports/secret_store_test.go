package ports_test

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// stubSecretStore is a minimal in-test implementation used to verify the interface contract.
type stubSecretStore struct {
	secrets map[string]string
}

// Resolve returns the secret for the given tenant and ref, or ErrSecretNotFound.
func (s *stubSecretStore) Resolve(_ context.Context, tenantID, secretRef string) (string, error) {
	key := tenantID + ":" + secretRef
	if v, ok := s.secrets[key]; ok {
		return v, nil
	}
	return "", ports.ErrSecretNotFound
}

// TestSecretStore_Interface is a compile-time assertion that stubSecretStore
// satisfies the SecretStore interface.
func TestSecretStore_Interface(_ *testing.T) {
	// Verify stubSecretStore satisfies the interface at compile time.
	var _ ports.SecretStore = &stubSecretStore{}
}

// TestErrSecretNotFound_IsSentinel verifies that ErrSecretNotFound can be identified
// via errors.Is for use in error handling chains.
func TestErrSecretNotFound_IsSentinel(t *testing.T) {
	err := ports.ErrSecretNotFound
	if !errors.Is(err, ports.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound to be detectable via errors.Is")
	}
}

// TestSecretStore_Resolve_ReturnsValue verifies that Resolve returns the correct secret
// value when the tenant and ref key combination exists.
func TestSecretStore_Resolve_ReturnsValue(t *testing.T) {
	store := &stubSecretStore{
		secrets: map[string]string{
			"tenant-abc:STRIPE_KEY": "sk_live_abc123",
		},
	}

	got, err := store.Resolve(context.Background(), "tenant-abc", "STRIPE_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk_live_abc123" {
		t.Fatalf("expected %q, got %q", "sk_live_abc123", got)
	}
}

// TestSecretStore_Resolve_ReturnsErrSecretNotFound verifies that Resolve returns
// ErrSecretNotFound when no secret exists for the given tenant and ref.
func TestSecretStore_Resolve_ReturnsErrSecretNotFound(t *testing.T) {
	store := &stubSecretStore{secrets: map[string]string{}}

	_, err := store.Resolve(context.Background(), "tenant-abc", "MISSING_KEY")
	if !errors.Is(err, ports.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}
