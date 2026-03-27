package clients_test

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConn_RequiresTargetOrServiceName(t *testing.T) {
	_, _, err := clients.NewConn(context.Background(), clients.ConnConfig{})
	assert.ErrorIs(t, err, clients.ErrConnTargetRequired)
}

func TestNewConn_DirectTarget(t *testing.T) {
	conn, cleanup, err := clients.NewConn(context.Background(), clients.ConnConfig{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	assert.NotNil(t, conn)
	assert.NotNil(t, cleanup)
	cleanup()
}

func TestNewConn_ServiceName(t *testing.T) {
	conn, cleanup, err := clients.NewConn(context.Background(), clients.ConnConfig{
		ServiceName: "test-service",
		Port:        50051,
	})
	require.NoError(t, err)
	assert.NotNil(t, conn)
	assert.NotNil(t, cleanup)
	cleanup()
}

func TestNewConn_ServiceNamePreferred(t *testing.T) {
	// When both ServiceName and Target are set, ServiceName takes precedence
	conn, cleanup, err := clients.NewConn(context.Background(), clients.ConnConfig{
		ServiceName: "test-service",
		Port:        50051,
		Target:      "localhost:50051",
	})
	require.NoError(t, err)
	assert.NotNil(t, conn)
	cleanup()
}
