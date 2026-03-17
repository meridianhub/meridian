package saga

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

func TestNewStarlarkSagaRunner(t *testing.T) {
	logger := slog.Default()
	runtime, err := NewRuntime(logger)
	require.NoError(t, err)

	registry := NewHandlerRegistry()

	t.Run("creates runner with valid config", func(t *testing.T) {
		runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
			Runtime:  runtime,
			Registry: registry,
			Logger:   logger,
		})
		require.NoError(t, err)
		assert.NotNil(t, runner)
	})

	t.Run("errors without runtime", func(t *testing.T) {
		_, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
			Registry: registry,
			Logger:   logger,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "runtime")
	})

	t.Run("errors without registry", func(t *testing.T) {
		_, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
			Runtime: runtime,
			Logger:  logger,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "registry")
	})
}

func TestStarlarkSagaRunner_ExecuteSaga(t *testing.T) {
	logger := slog.Default()
	runtime, err := NewRuntime(logger, WithTimeout(10*time.Second))
	require.NoError(t, err)

	// Create a registry with a test handler
	registry := NewHandlerRegistry()

	// Register a simple test handler
	err = registry.Register("test.echo", func(ctx *StarlarkContext, params map[string]any) (any, error) {
		message, ok := params["message"].(string)
		if !ok {
			message = "no message"
		}
		return map[string]any{
			"echoed_message": "Echo: " + message,
			"saga_id":        ctx.SagaExecutionID.String(),
		}, nil
	})
	require.NoError(t, err)

	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: registry,
		Logger:   logger,
	})
	require.NoError(t, err)

	t.Run("executes simple saga", func(t *testing.T) {
		// Simple Starlark script that sets output variables
		script := `
# Simple saga that just sets variables
output = "Hello, Saga!"
completed = True
`
		input := RunnerInput{
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
			Input:           map[string]interface{}{},
		}

		output, err := runner.ExecuteSaga(context.Background(), "simple_saga", script, input)
		require.NoError(t, err)
		assert.True(t, output.Success)
		assert.Contains(t, output.Output, "output")
		assert.Equal(t, "Hello, Saga!", output.Output["output"])
	})

	t.Run("handles syntax errors", func(t *testing.T) {
		script := `
def broken_function(
    # Missing closing parenthesis
`
		input := RunnerInput{
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
			Input:           map[string]interface{}{},
		}

		output, err := runner.ExecuteSaga(context.Background(), "broken_saga", script, input)
		require.NoError(t, err) // Returns output with error, not Go error
		assert.False(t, output.Success)
		assert.NotEmpty(t, output.Error)
	})

	t.Run("passes input to script", func(t *testing.T) {
		// Note: input is passed via input_data dict, and special vars via _saga_execution_id key
		script := `
# Access input parameter via input_data
payment_id = input_data["_saga_execution_id"]
corr_id = input_data["_correlation_id"]
amount = input_data.get("amount", 0)
result = "Processed"
`
		sagaID := uuid.New()
		corrID := uuid.New()

		input := RunnerInput{
			SagaExecutionID: sagaID,
			CorrelationID:   corrID,
			KnowledgeAt:     time.Now(),
			Input: map[string]interface{}{
				"amount": 100,
			},
		}

		output, err := runner.ExecuteSaga(context.Background(), "input_saga", script, input)
		require.NoError(t, err)
		assert.True(t, output.Success)
		assert.Equal(t, sagaID.String(), output.Output["payment_id"])
		assert.Equal(t, corrID.String(), output.Output["corr_id"])
		assert.EqualValues(t, 100, output.Output["amount"])
	})
}

func TestStarlarkSagaRunner_ServiceModules(t *testing.T) {
	logger := slog.Default()
	runtime, err := NewRuntime(logger, WithTimeout(10*time.Second))
	require.NoError(t, err)

	registry := NewHandlerRegistry()

	err = registry.Register("test.echo", func(ctx *StarlarkContext, params map[string]any) (any, error) {
		msg, _ := params["message"].(string)
		return map[string]any{
			"echoed": "Echo: " + msg,
			"saga":   ctx.SagaExecutionID.String(),
		}, nil
	})
	require.NoError(t, err)

	// Build a simple service module manually (simulating schema.BuildServiceModules output)
	testModule := starlark.StringDict{
		"echo": starlark.NewBuiltin("test.echo", func(thread *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			// Get StarlarkContext from thread-local
			ctxVal := thread.Local("saga.StarlarkContext")
			ctx, ok := ctxVal.(*StarlarkContext)
			if !ok || ctx == nil {
				return nil, fmt.Errorf("StarlarkContext not set on thread")
			}

			// Convert kwargs to params map
			params := make(map[string]any)
			for _, kwarg := range kwargs {
				key := string(kwarg[0].(starlark.String))
				params[key] = starlarkToGo(kwarg[1])
			}

			// Call the handler
			handler, err := registry.Get("test.echo")
			if err != nil {
				return nil, err
			}
			result, err := handler(ctx, params)
			if err != nil {
				return nil, err
			}

			// Convert result to Starlark dict
			return goToStarlark(result), nil
		}),
	}
	serviceModules := starlark.StringDict{
		"test": starlarkstruct.FromStringDict(starlark.String("test"), testModule),
	}

	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       registry,
		ServiceModules: serviceModules,
		Logger:         logger,
	})
	require.NoError(t, err)

	t.Run("service module handler accessible in script", func(t *testing.T) {
		script := `
result = test.echo(message="hello world")
output_echoed = result["echoed"]
`
		sagaID := uuid.New()
		input := RunnerInput{
			SagaExecutionID: sagaID,
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
			Input:           map[string]interface{}{},
		}

		output, err := runner.ExecuteSaga(context.Background(), "typed_saga", script, input)
		require.NoError(t, err)
		assert.True(t, output.Success)
		assert.Equal(t, "Echo: hello world", output.Output["output_echoed"])
	})

	t.Run("service modules available via dir()", func(t *testing.T) {
		script := `
# Verify the service module is accessible
attrs = dir(test)
has_echo = "echo" in attrs
`
		input := RunnerInput{
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
			Input:           map[string]interface{}{},
		}

		output, err := runner.ExecuteSaga(context.Background(), "dir_saga", script, input)
		require.NoError(t, err)
		assert.True(t, output.Success)
		assert.Equal(t, true, output.Output["has_echo"])
	})
}

func TestStarlarkSagaRunner_StepTracking(t *testing.T) {
	logger := slog.Default()
	runtime, err := NewRuntime(logger, WithTimeout(10*time.Second))
	require.NoError(t, err)

	registry := NewHandlerRegistry()

	err = registry.Register("step.first", func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"result": "first_done"}, nil
	})
	require.NoError(t, err)

	runner, err := NewStarlarkSagaRunner(StarlarkSagaRunnerConfig{
		Runtime:  runtime,
		Registry: registry,
		Logger:   logger,
	})
	require.NoError(t, err)

	t.Run("no step results without handler calls", func(t *testing.T) {
		script := `
status = "completed"
`
		input := RunnerInput{
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
			Input:           map[string]interface{}{},
		}

		output, err := runner.ExecuteSaga(context.Background(), "tracked_saga", script, input)
		require.NoError(t, err)
		assert.True(t, output.Success)
		assert.Empty(t, output.StepResults)
	})
}
