package clients

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPartyClient_RequiresServiceName(t *testing.T) {
	_, err := NewPartyClient(&PartyClientConfig{})
	assert.ErrorIs(t, err, ErrPartyServiceNameRequired)
}

func TestNewPartyClient_Success(t *testing.T) {
	client, err := NewPartyClient(&PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.Equal(t, 30*time.Second, client.timeout)

	err = client.Close()
	assert.NoError(t, err)
}

func TestNewPartyClient_DefaultTimeout(t *testing.T) {
	client, err := NewPartyClient(&PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
	})
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, client.timeout)
	_ = client.Close()
}

func TestNewPartyClient_CustomTimeout(t *testing.T) {
	customTimeout := 10 * time.Second
	client, err := NewPartyClient(&PartyClientConfig{
		ServiceName: "party",
		Namespace:   "default",
		Port:        50055,
		Timeout:     customTimeout,
	})
	require.NoError(t, err)
	assert.Equal(t, customTimeout, client.timeout)
	_ = client.Close()
}

func TestNewPartyClient_DefaultNamespace(t *testing.T) {
	// Empty namespace should default to "default" in the platform grpc client
	client, err := NewPartyClient(&PartyClientConfig{
		ServiceName: "party",
		Namespace:   "",
		Port:        50055,
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
	_ = client.Close()
}

func TestPartyGRPCClient_Close_NoConnection(t *testing.T) {
	client := &PartyGRPCClient{}
	err := client.Close()
	assert.NoError(t, err)
}
