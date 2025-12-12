package clients

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPartyClient_RequiresTarget(t *testing.T) {
	_, err := NewPartyClient(&PartyClientConfig{})
	assert.ErrorIs(t, err, ErrPartyTargetRequired)
}

func TestNewPartyClient_Success(t *testing.T) {
	client, err := NewPartyClient(&PartyClientConfig{
		Target: "localhost:50054",
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
		Target: "localhost:50054",
	})
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, client.timeout)
	_ = client.Close()
}

func TestNewPartyClient_CustomTimeout(t *testing.T) {
	customTimeout := 10 * time.Second
	client, err := NewPartyClient(&PartyClientConfig{
		Target:  "localhost:50054",
		Timeout: customTimeout,
	})
	require.NoError(t, err)
	assert.Equal(t, customTimeout, client.timeout)
	_ = client.Close()
}

func TestNewPartyClient_ServiceNameMode(t *testing.T) {
	client, err := NewPartyClient(&PartyClientConfig{
		ServiceName: "party-service",
		Namespace:   "default",
		Port:        50054,
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
