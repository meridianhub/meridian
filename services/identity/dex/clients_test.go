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
	assert.Contains(t, client.RedirectURIs, "https://meridian.example.com/api/auth/callback")
	assert.Contains(t, client.RedirectURIs, "https://meridian.example.com/oauth/callback")
	assert.Contains(t, client.RedirectURIs, "http://localhost:8090/api/auth/callback")
}

func TestDefaultDemoClient_WithEnvRedirectURIs(t *testing.T) {
	t.Setenv("DEX_REDIRECT_URIS", "https://custom.example.com/cb, https://other.example.com/cb")

	client := DefaultDemoClient("meridian.example.com")

	assert.Contains(t, client.RedirectURIs, "https://custom.example.com/cb")
	assert.Contains(t, client.RedirectURIs, "https://other.example.com/cb")
	assert.Contains(t, client.RedirectURIs, "https://meridian.example.com/api/auth/callback")
}

func TestClientConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ClientConfig
		wantErr error
	}{
		{
			name:    "empty ID",
			config:  ClientConfig{RedirectURIs: []string{"http://localhost/cb"}},
			wantErr: ErrClientIDRequired,
		},
		{
			name:    "no redirect URIs",
			config:  ClientConfig{ID: "test"},
			wantErr: ErrRedirectURIsRequired,
		},
		{
			name:   "valid",
			config: ClientConfig{ID: "test", RedirectURIs: []string{"http://localhost/cb"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
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

	stored, err := store.GetClient(ctx, "test-client")
	require.NoError(t, err)
	assert.Equal(t, "test-client", stored.ID)
	assert.Equal(t, "test-secret", stored.Secret)
	assert.Equal(t, "Test Client", stored.Name)
	assert.False(t, stored.Public)
}

func TestRegisterClients_UpdatesExisting(t *testing.T) {
	store := memory.New(slog.Default())
	ctx := context.Background()

	// Register initial client.
	initial := []ClientConfig{
		{ID: "upsert-client", Name: "Old Name", RedirectURIs: []string{"http://old/cb"}},
	}
	err := registerClients(ctx, store, initial, slog.Default())
	require.NoError(t, err)

	// Re-register with different config.
	updated := []ClientConfig{
		{ID: "upsert-client", Name: "New Name", RedirectURIs: []string{"http://new/cb"}},
	}
	err = registerClients(ctx, store, updated, slog.Default())
	require.NoError(t, err)

	// Verify the client was updated.
	stored, err := store.GetClient(ctx, "upsert-client")
	require.NoError(t, err)
	assert.Equal(t, "New Name", stored.Name)
	assert.Equal(t, []string{"http://new/cb"}, stored.RedirectURIs)
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

	a, err := store.GetClient(ctx, "client-a")
	require.NoError(t, err)
	assert.Equal(t, "A", a.Name)

	b, err := store.GetClient(ctx, "client-b")
	require.NoError(t, err)
	assert.Equal(t, "B", b.Name)
}

func TestRegisterClients_InvalidClient(t *testing.T) {
	store := memory.New(slog.Default())
	ctx := context.Background()

	clients := []ClientConfig{
		{ID: "", RedirectURIs: []string{"http://bad/cb"}}, // empty ID
	}

	err := registerClients(ctx, store, clients, slog.Default())
	assert.ErrorIs(t, err, ErrClientIDRequired)
}

func TestErrAlreadyExists(t *testing.T) {
	assert.ErrorIs(t, storage.ErrAlreadyExists, storage.ErrAlreadyExists)
}
