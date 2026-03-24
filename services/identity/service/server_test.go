package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// --- Health Check ---

func TestCheck_ReturnsServing(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Check(ctx, &grpc_health_v1.HealthCheckRequest{})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestCheck_WithServiceName_ReturnsServing(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "identity",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestWatch_ReturnsUnimplemented(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.Watch(&grpc_health_v1.HealthCheckRequest{}, nil)

	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}
