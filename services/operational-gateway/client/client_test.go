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
	c, cleanup, err := New(Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NotNil(t, cleanup)
	assert.NotNil(t, c.conn)
	assert.NotNil(t, c.operationalGateway)
	assert.Equal(t, DefaultTimeout, c.timeout)
	cleanup()
}

func TestNew_WithTargetAndCustomDialOptions(t *testing.T) {
	c, cleanup, err := New(Config{
		Target: "localhost:50051",
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	cleanup()
}

func TestNew_WithCustomTimeout(t *testing.T) {
	c, cleanup, err := New(Config{
		Target:  "localhost:50051",
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, c.timeout)
	cleanup()
}

func TestNew_WithResilience(t *testing.T) {
	c, cleanup, err := New(Config{
		Target: "localhost:50051",
		Resilience: &clients.ResilientClientConfig{
			MaxRetries: 3,
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, c.resilient)
	cleanup()
}

func TestNew_NoTargetOrServiceName(t *testing.T) {
	_, _, err := New(Config{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTargetRequired)
}

func TestClose_Success(t *testing.T) {
	c, _, err := New(Config{Target: "localhost:50051"})
	require.NoError(t, err)
	assert.NoError(t, c.Close())
}

func TestClose_NilConn(t *testing.T) {
	c := &Client{}
	assert.NoError(t, c.Close())
}

func TestConn_ReturnsConnection(t *testing.T) {
	c, cleanup, err := New(Config{Target: "localhost:50051"})
	require.NoError(t, err)
	defer cleanup()

	conn := c.Conn()
	assert.NotNil(t, conn)
	assert.Equal(t, c.conn, conn)
}

func TestDefaults_Applied(t *testing.T) {
	c, cleanup, err := New(Config{Target: "localhost:50051"})
	require.NoError(t, err)
	defer cleanup()

	assert.Equal(t, DefaultTimeout, c.timeout)
}
