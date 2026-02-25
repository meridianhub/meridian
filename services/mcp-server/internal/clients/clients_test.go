package clients_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"github.com/meridianhub/meridian/services/mcp-server/internal/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_NilAuthConfig verifies that nil config is rejected.
func TestNew_NilAuthConfig(t *testing.T) {
	_, err := clients.New(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, clients.ErrNilAuthConfig)
}

// TestNew_MissingAPIURL verifies that an empty APIUrl is rejected.
// ErrMissingAPIURL lives in the auth package since LoadFromEnv now also validates it.
func TestNew_MissingAPIURL(t *testing.T) {
	cfg := &auth.Config{
		APIKey: "key",
		APIUrl: "",
	}
	_, err := clients.New(cfg)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrMissingAPIURL)
}

// TestNew_AllClientsInitialized verifies that all service clients are
// non-nil after successful construction. We use a dummy address because
// grpc.NewClient is lazy — it does not attempt a connection until the first RPC.
func TestNew_AllClientsInitialized(t *testing.T) {
	cfg := &auth.Config{
		APIKey: "test-key",
		APIUrl: "localhost:9999",
	}

	mc, err := clients.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Close() })

	assert.NotNil(t, mc.ApplyManifest, "ApplyManifest client must not be nil")
	assert.NotNil(t, mc.ManifestHistory, "ManifestHistory client must not be nil")
	assert.NotNil(t, mc.ReferenceData, "ReferenceData client must not be nil")
	assert.NotNil(t, mc.PositionKeeping, "PositionKeeping client must not be nil")
	assert.NotNil(t, mc.Accounting, "Accounting client must not be nil")
	assert.NotNil(t, mc.Reconciliation, "Reconciliation client must not be nil")
	assert.NotNil(t, mc.MarketInfo, "MarketInfo client must not be nil")
	assert.NotNil(t, mc.Health, "Health client must not be nil")
}
