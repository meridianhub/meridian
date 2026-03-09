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

func TestNew_CreatesInstance(t *testing.T) {
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
	assert.Nil(t, embedded.Handler(), "handler should be nil until SetHandler is called")
	assert.NotNil(t, embedded.Storage())
	assert.NotNil(t, embedded.Adapter())
}

func TestNew_RegistersClient(t *testing.T) {
	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (meridianconnector.Identity, bool, error) {
			return meridianconnector.Identity{}, false, nil
		},
	}

	ctx := context.Background()
	embedded, err := New(ctx, Config{
		Issuer:    "http://127.0.0.1:0/dex",
		Connector: stub,
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

func TestNew_ConnectorInStorage(t *testing.T) {
	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (meridianconnector.Identity, bool, error) {
			return meridianconnector.Identity{}, false, nil
		},
	}

	ctx := context.Background()
	embedded, err := New(ctx, Config{
		Issuer:    "http://127.0.0.1:0/dex",
		Connector: stub,
	})
	require.NoError(t, err)

	// Verify connector is in storage.
	conn, err := embedded.Storage().GetConnector(ConnectorID)
	require.NoError(t, err)
	assert.Equal(t, ConnectorType, conn.Type)
}

func TestEmbeddedDex_SetHandler(t *testing.T) {
	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (meridianconnector.Identity, bool, error) {
			return meridianconnector.Identity{}, false, nil
		},
	}

	embedded, err := New(context.Background(), Config{
		Issuer:    "http://127.0.0.1:0/dex",
		Connector: stub,
	})
	require.NoError(t, err)

	assert.Nil(t, embedded.Handler())

	// Simulate setting a handler from the application layer.
	embedded.SetHandler(nil) // no-op but exercises the method
	assert.Nil(t, embedded.Handler())
}
