package saga

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStarlarkSagaRunner_WithLogger(t *testing.T) {
	runtime, err := NewRuntime(nil)
	require.NoError(t, err)

	registry := NewHandlerRegistry()
	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: registry,
	})
	require.NoError(t, err)

	t.Run("sets logger", func(t *testing.T) {
		logger := slog.Default()
		result := runner.WithLogger(logger)
		assert.NotNil(t, result)
	})

	t.Run("nil logger falls back to default", func(t *testing.T) {
		result := runner.WithLogger(nil)
		assert.NotNil(t, result)
	})
}

func TestStepExecutor_WithLogger(t *testing.T) {
	executor := NewStepExecutor(nil, nil)
	logger := slog.Default()
	result := executor.WithLogger(logger)
	assert.Same(t, executor, result, "WithLogger should return same executor for chaining")
}

func TestTransactionalStepExecutor_WithEventPublisher(t *testing.T) {
	executor := NewTransactionalStepExecutor(nil)
	publisher := &MockEventPublisher{
		PublishFunc: func(_ context.Context, _ Event) error { return nil },
	}
	result := executor.WithEventPublisher(publisher)
	assert.Same(t, executor, result, "WithEventPublisher should return same executor for chaining")
}
