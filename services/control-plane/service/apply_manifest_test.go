package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestRegisterApplyManifestService_NilPool_Direct(t *testing.T) {
	server := grpc.NewServer()
	err := RegisterApplyManifestService(server, ApplyManifestServiceConfig{
		Pool: nil,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPoolRequired)
}

func TestErrPoolRequired_Message(t *testing.T) {
	assert.EqualError(t, ErrPoolRequired, "apply manifest service: pool is required")
}

func TestApplyManifestServiceConfig_Defaults(t *testing.T) {
	// Default config has nil Pool - should fail with ErrPoolRequired
	cfg := ApplyManifestServiceConfig{}
	assert.Nil(t, cfg.Pool)
	assert.Nil(t, cfg.Logger)
	assert.Nil(t, cfg.HandlerDeps)
}
