package observability

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisChecker_Healthy(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	checker := NewRedisChecker(client)
	assert.Equal(t, "redis", checker.Name())

	result := checker.Check(context.Background())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "successful")
	assert.NoError(t, result.Error)
}

func TestRedisChecker_Unhealthy(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	// Close the miniredis server to simulate connection failure
	mr.Close()

	checker := NewRedisChecker(client)
	result := checker.Check(context.Background())
	assert.Equal(t, health.StatusUnhealthy, result.Status)
	assert.Contains(t, result.Message, "ping failed")
	assert.Error(t, result.Error)
}

func TestNewRedisChecker_NilPanics(t *testing.T) {
	require.Panics(t, func() {
		NewRedisChecker(nil)
	})
}
