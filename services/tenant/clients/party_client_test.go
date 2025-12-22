package clients

import (
	"testing"
	"time"

	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPartyClient_RequiresServiceName(t *testing.T) {
	_, err := NewPartyClient(&sharedclients.PartyClientConfig{})
	assert.ErrorIs(t, err, sharedclients.ErrPartyServiceNameRequired)
}

func TestNewPartyClient_Success(t *testing.T) {
	client, err := NewPartyClient(&sharedclients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.Client())
	assert.Equal(t, 30*time.Second, client.Timeout())

	err = client.Close()
	assert.NoError(t, err)
}

func TestNewPartyClient_DefaultTimeout(t *testing.T) {
	client, err := NewPartyClient(&sharedclients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
	})
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, client.Timeout())
	_ = client.Close()
}

func TestNewPartyClient_CustomTimeout(t *testing.T) {
	customTimeout := 10 * time.Second
	client, err := NewPartyClient(&sharedclients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     customTimeout,
	})
	require.NoError(t, err)
	assert.Equal(t, customTimeout, client.Timeout())
	_ = client.Close()
}

func TestNewPartyClient_DefaultNamespace(t *testing.T) {
	// Empty namespace should default to "default" in the platform grpc client
	client, err := NewPartyClient(&sharedclients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "",
		Port:        50055,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	_ = client.Close()
}

func TestPartyGRPCClient_Close_NoConnection(t *testing.T) {
	// Note: With the embedded BasePartyClient, calling Close() on a nil-embedded
	// client would panic. This test verifies that a properly created client
	// with no active connection can still close gracefully.
	client, err := NewPartyClient(&sharedclients.PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
	})
	require.NoError(t, err)

	// First close should succeed
	err = client.Close()
	assert.NoError(t, err)
}
