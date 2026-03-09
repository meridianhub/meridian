package dex

import (
	"context"
	"testing"

	meridianconnector "github.com/meridianhub/meridian/services/identity/connector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_MissingIssuer(t *testing.T) {
	_, err := New(context.Background(), Config{
		Connector: &stubConnector{},
	})
	assert.ErrorIs(t, err, ErrIssuerRequired)
}

func TestNew_MissingConnector(t *testing.T) {
	_, err := New(context.Background(), Config{
		Issuer: "https://auth.example.com/dex",
	})
	assert.ErrorIs(t, err, ErrConnectorRequired)
}

func TestNew_CreatesServer(t *testing.T) {
	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (meridianconnector.Identity, bool, error) {
			return meridianconnector.Identity{}, false, nil
		},
	}

	embedded, err := New(context.Background(), Config{
		Issuer:             "http://127.0.0.1:0/dex",
		Connector:          stub,
		SkipApprovalScreen: true,
		Clients: []ClientConfig{
			{
				ID:           "test-client",
				Public:       true,
				RedirectURIs: []string{"http://localhost/callback"},
				Name:         "Test",
			},
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, embedded.Handler())
	assert.NotNil(t, embedded.Storage())
}

func TestNew_RegistersClient(t *testing.T) {
	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (meridianconnector.Identity, bool, error) {
			return meridianconnector.Identity{}, false, nil
		},
	}

	embedded, err := New(context.Background(), Config{
		Issuer:             "http://127.0.0.1:0/dex",
		Connector:          stub,
		SkipApprovalScreen: true,
		Clients: []ClientConfig{
			{
				ID:           "verify-client",
				Public:       true,
				RedirectURIs: []string{"http://localhost/callback"},
				Name:         "Verify",
			},
		},
	})
	require.NoError(t, err)

	// Verify client is in storage.
	client, err := embedded.Storage().GetClient("verify-client")
	require.NoError(t, err)
	assert.Equal(t, "Verify", client.Name)
	assert.True(t, client.Public)
}
