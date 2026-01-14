package client

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_WithTarget(t *testing.T) {
	client, cleanup, err := New(Config{
		Target:  "localhost:50057",
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.internalBankAccount)
	assert.Equal(t, 10*time.Second, client.timeout)

	cleanup()
}

func TestNew_WithServiceName(t *testing.T) {
	client, cleanup, err := New(Config{
		ServiceName: "internal-bank-account",
		Namespace:   "default",
		Port:        50057,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.internalBankAccount)
	assert.Equal(t, DefaultTimeout, client.timeout)

	cleanup()
}

func TestNew_Defaults(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	assert.Equal(t, DefaultTimeout, client.timeout)
}

func TestNew_RequiresTargetOrServiceName(t *testing.T) {
	_, _, err := New(Config{})
	assert.ErrorIs(t, err, ErrTargetRequired)
}

func TestNew_DefaultsApplied(t *testing.T) {
	// Port is used during connection setup but not stored in Client struct.
	// These tests verify defaults are applied during connection establishment.
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "empty port defaults to 50057",
			cfg:  Config{ServiceName: "internal-bank-account"},
		},
		{
			name: "custom port preserved",
			cfg:  Config{ServiceName: "internal-bank-account", Port: 9999},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup, err := New(tt.cfg)
			require.NoError(t, err)
			defer cleanup()
			require.NotNil(t, client)
		})
	}
}

func TestClose(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
	})
	require.NoError(t, err)
	defer cleanup()

	err = client.Close()
	assert.NoError(t, err)
}

func TestClose_NilConn(t *testing.T) {
	client := &Client{}
	err := client.Close()
	assert.NoError(t, err)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, 50057, DefaultPort)
	assert.Equal(t, 30*time.Second, DefaultTimeout)
	assert.Equal(t, "default", DefaultNamespace)
	assert.Equal(t, "internal-bank-account", ServiceName)
}

func TestNew_WithResilience(t *testing.T) {
	resilienceConfig := clients.DefaultResilientClientConfig("internal-bank-account-client")
	client, cleanup, err := New(Config{
		Target:     "localhost:50057",
		Resilience: &resilienceConfig,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was created
	assert.NotNil(t, client.resilient)
}

func TestNew_WithoutResilience(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was not created
	assert.Nil(t, client.resilient)
}

func TestConn(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
	})
	require.NoError(t, err)
	defer cleanup()

	conn := client.Conn()
	assert.NotNil(t, conn)
	assert.Equal(t, client.conn, conn)
}
