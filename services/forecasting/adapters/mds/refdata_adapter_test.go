package mds

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoOpRefDataClient_AlwaysReturnsConfiguredError(t *testing.T) {
	client := &NoOpRefDataClient{}

	result, err := client.GetNodeByResolutionKey(context.Background(), "tenant-1", "some-key")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrRefDataNotConfigured)
}

func TestNoOpRefDataClient_ErrorMessageContainsResolutionKey(t *testing.T) {
	client := &NoOpRefDataClient{}
	resolutionKey := "GB/SOUTH/GRID"

	_, err := client.GetNodeByResolutionKey(context.Background(), "tenant-42", resolutionKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), resolutionKey)
}

func TestNoOpRefDataClient_DifferentTenantsAndKeys(t *testing.T) {
	client := &NoOpRefDataClient{}

	tests := []struct {
		tenantID      string
		resolutionKey string
	}{
		{"tenant-1", "region:us-east-1"},
		{"tenant-2", "node:grid-point-42"},
		{"tenant-3", ""},
	}

	for _, tc := range tests {
		t.Run(tc.tenantID+"/"+tc.resolutionKey, func(t *testing.T) {
			result, err := client.GetNodeByResolutionKey(context.Background(), tc.tenantID, tc.resolutionKey)

			require.Error(t, err)
			assert.Nil(t, result)
			assert.True(t, errors.Is(err, ErrRefDataNotConfigured))
		})
	}
}

func TestNoOpRefDataClient_CancelledContextStillReturnsError(t *testing.T) {
	client := &NoOpRefDataClient{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result, err := client.GetNodeByResolutionKey(ctx, "tenant-1", "some-key")

	require.Error(t, err)
	assert.Nil(t, result)
	// NoOpRefDataClient always returns ErrRefDataNotConfigured regardless of context
	assert.ErrorIs(t, err, ErrRefDataNotConfigured)
}
