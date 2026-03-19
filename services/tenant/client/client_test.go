package client

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNew_WithTarget(t *testing.T) {
	client, cleanup, err := New(Config{
		Target:  "localhost:50056",
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.tenant)
	assert.Equal(t, 10*time.Second, client.timeout)

	cleanup()
}

func TestNew_WithServiceName(t *testing.T) {
	client, cleanup, err := New(Config{
		ServiceName: "tenant",
		Namespace:   "default",
		Port:        50056,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.tenant)
	assert.Equal(t, DefaultTimeout, client.timeout)

	cleanup()
}

func TestNew_Defaults(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50056",
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
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "empty port uses default",
			cfg:  Config{ServiceName: "tenant"},
		},
		{
			name: "custom port accepted",
			cfg:  Config{ServiceName: "tenant", Port: 9999},
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
		Target: "localhost:50056",
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
	assert.Equal(t, 50056, DefaultPort)
	assert.Equal(t, 30*time.Second, DefaultTimeout)
	assert.Equal(t, "default", DefaultNamespace)
	assert.Equal(t, "tenant", ServiceName)
}

func TestNew_WithResilience(t *testing.T) {
	resilienceConfig := clients.DefaultResilientClientConfig("tenant-client")
	client, cleanup, err := New(Config{
		Target:     "localhost:50056",
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
		Target: "localhost:50056",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was not created
	assert.Nil(t, client.resilient)
}

func TestConn(t *testing.T) {
	c, cleanup, err := New(Config{
		Target: "localhost:50056",
	})
	require.NoError(t, err)
	defer cleanup()

	conn := c.Conn()
	assert.NotNil(t, conn)
	assert.Equal(t, c.conn, conn)
}

func TestNew_WithCustomDialOpts(t *testing.T) {
	c, cleanup, err := New(Config{
		Target: "localhost:50056",
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	defer cleanup()
}

func TestNew_ServiceNameWithCustomDialOpts(t *testing.T) {
	c, cleanup, err := New(Config{
		ServiceName: "tenant",
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	defer cleanup()
}

func TestNew_DefaultTimeoutApplied(t *testing.T) {
	c, cleanup, err := New(Config{
		ServiceName: "tenant",
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	defer cleanup()

	// Default timeout should be applied
	assert.Equal(t, DefaultTimeout, c.timeout)
}

func TestNew_CustomTimeout(t *testing.T) {
	c, cleanup, err := New(Config{
		Target:  "localhost:50056",
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer cleanup()

	assert.Equal(t, 5*time.Second, c.timeout)
}

func TestClose_Succeeds(t *testing.T) {
	c, _, err := New(Config{
		Target: "localhost:50056",
	})
	require.NoError(t, err)

	err = c.Close()
	assert.NoError(t, err)
}

func TestNew_WithResilienceAndServiceName(t *testing.T) {
	resilienceConfig := clients.DefaultResilientClientConfig("tenant-client")
	c, cleanup, err := New(Config{
		ServiceName: "tenant",
		Resilience:  &resilienceConfig,
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	defer cleanup()

	assert.NotNil(t, c.resilient)
}

func TestNew_TargetWithNilDialOpts(t *testing.T) {
	// When DialOptions is nil, default insecure credentials should be applied
	c, cleanup, err := New(Config{
		Target: "localhost:50056",
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	defer cleanup()
}
