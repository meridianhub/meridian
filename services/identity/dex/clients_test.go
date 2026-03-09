package dex

import (
	"context"
	"log/slog"
	"testing"

	"github.com/dexidp/dex/storage"
	"github.com/dexidp/dex/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultDemoClient(t *testing.T) {
	client := DefaultDemoClient("meridian.example.com")

	assert.Equal(t, "meridian-service", client.ID)
	assert.True(t, client.Public)
	assert.Equal(t, "Meridian Service", client.Name)
	assert.Empty(t, client.Secret)
	assert.Contains(t, client.RedirectURIs, "https://meridian.example.com/callback")
	assert.Contains(t, client.RedirectURIs, "http://localhost:8080/callback")
}

func TestDefaultDemoClient_WithEnvRedirectURIs(t *testing.T) {
	t.Setenv("DEX_REDIRECT_URIS", "https://custom.example.com/cb, https://other.example.com/cb")

	client := DefaultDemoClient("meridian.example.com")

	assert.Contains(t, client.RedirectURIs, "https://custom.example.com/cb")
	assert.Contains(t, client.RedirectURIs, "https://other.example.com/cb")
	// Default URIs should still be present.
	assert.Contains(t, client.RedirectURIs, "https://meridian.example.com/callback")
}

func TestRegisterClients_Success(t *testing.T) {
	store := memory.New(slog.Default())
	ctx := context.Background()

	clients := []ClientConfig{
		{
			ID:           "test-client",
			Secret:       "test-secret",
			Public:       false,
			RedirectURIs: []string{"http://localhost/callback"},
			Name:         "Test Client",
		},
	}

	err := registerClients(ctx, store, clients, slog.Default())
	require.NoError(t, err)

	// Verify client was created in storage.
	stored, err := store.GetClient("test-client")
	require.NoError(t, err)
	assert.Equal(t, "test-client", stored.ID)
	assert.Equal(t, "test-secret", stored.Secret)
	assert.Equal(t, "Test Client", stored.Name)
	assert.False(t, stored.Public)
}

func TestRegisterClients_Idempotent(t *testing.T) {
	store := memory.New(slog.Default())
	ctx := context.Background()

	clients := []ClientConfig{
		{
			ID:           "idempotent-client",
			Public:       true,
			RedirectURIs: []string{"http://localhost/callback"},
			Name:         "Idempotent",
		},
	}

	// Register twice -- second call should not error.
	err := registerClients(ctx, store, clients, slog.Default())
	require.NoError(t, err)

	err = registerClients(ctx, store, clients, slog.Default())
	require.NoError(t, err)
}

func TestRegisterClients_MultipleClients(t *testing.T) {
	store := memory.New(slog.Default())
	ctx := context.Background()

	clients := []ClientConfig{
		{ID: "client-a", Name: "A", RedirectURIs: []string{"http://a/cb"}},
		{ID: "client-b", Name: "B", RedirectURIs: []string{"http://b/cb"}},
	}

	err := registerClients(ctx, store, clients, slog.Default())
	require.NoError(t, err)

	a, err := store.GetClient("client-a")
	require.NoError(t, err)
	assert.Equal(t, "A", a.Name)

	b, err := store.GetClient("client-b")
	require.NoError(t, err)
	assert.Equal(t, "B", b.Name)
}

func TestErrAlreadyExists(t *testing.T) {
	assert.ErrorIs(t, storage.ErrAlreadyExists, storage.ErrAlreadyExists)
}
