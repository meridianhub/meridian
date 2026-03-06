package validation

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedValidator_CacheHit(t *testing.T) {
	// Create a schema registry with a mock handler
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.echo:
    description: Test echo handler
    compensation_strategy: none
    params:
      message:
        type: string
        required: false
    returns:
      result:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	// Create the underlying validator
	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Create cached validator with short TTL for testing
	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
		TTL:       time.Minute,
		MaxSize:   100,
	})
	require.NoError(t, err)

	ctx := context.Background()
	script := `result = test.echo(message="hello")`

	// First call - cache miss
	result1, err := cached.Validate(ctx, script)
	require.NoError(t, err)
	assert.True(t, result1.Success)

	// Second call - should be cache hit
	result2, err := cached.Validate(ctx, script)
	require.NoError(t, err)
	assert.True(t, result2.Success)

	// Results should be the same object (from cache)
	assert.Equal(t, result1, result2)

	// Cache should have 1 entry
	assert.Equal(t, 1, cached.CacheSize())
}

func TestCachedValidator_CacheMissOnDifferentScript(t *testing.T) {
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.handler1:
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
  test.handler2:
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
		TTL:       time.Minute,
		MaxSize:   100,
	})
	require.NoError(t, err)

	ctx := context.Background()
	script1 := `result = test.handler1()`
	script2 := `result = test.handler2()`

	// Validate both scripts
	result1, err := cached.Validate(ctx, script1)
	require.NoError(t, err)
	assert.True(t, result1.Success)

	result2, err := cached.Validate(ctx, script2)
	require.NoError(t, err)
	assert.True(t, result2.Success)

	// Cache should have 2 entries
	assert.Equal(t, 2, cached.CacheSize())
}

func TestCachedValidator_DoesNotCacheFailures(t *testing.T) {
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.valid_handler:
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
		TTL:       time.Minute,
		MaxSize:   100,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Script with syntax error
	invalidScript := `this is not valid starlark (`

	result, err := cached.Validate(ctx, invalidScript)
	require.NoError(t, err) // Validation errors are in result, not returned as error
	assert.False(t, result.Success)
	assert.NotEmpty(t, result.Errors)

	// Cache should be empty - failed validations are not cached
	assert.Equal(t, 0, cached.CacheSize())
}

func TestCachedValidator_ClearCache(t *testing.T) {
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.handler:
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
		TTL:       time.Minute,
		MaxSize:   100,
	})
	require.NoError(t, err)

	ctx := context.Background()
	script := `result = test.handler()`

	// Validate to populate cache
	_, err = cached.Validate(ctx, script)
	require.NoError(t, err)
	assert.Equal(t, 1, cached.CacheSize())

	// Clear cache
	cached.ClearCache()
	assert.Equal(t, 0, cached.CacheSize())
}

func TestCachedValidator_DefaultConfig(t *testing.T) {
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.handler:
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	// Create with zero values - should use defaults
	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
	})
	require.NoError(t, err)

	// Should work with default TTL and MaxSize
	ctx := context.Background()
	script := `result = test.handler()`

	result, err := cached.Validate(ctx, script)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, 1, cached.CacheSize())
}

func TestCachedValidator_NilValidator(t *testing.T) {
	_, err := NewCachedValidator(CachedValidatorConfig{
		Validator: nil,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidatorRequired)
}

func TestCachedValidator_UnderlyingValidator(t *testing.T) {
	schemaReg := schema.NewRegistry()
	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
	})
	require.NoError(t, err)

	// Should return the same validator
	assert.Equal(t, validator, cached.UnderlyingValidator())
}

func TestCachedValidator_Cache(t *testing.T) {
	schemaReg := schema.NewRegistry()
	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
	})
	require.NoError(t, err)

	// Should return non-nil cache
	assert.NotNil(t, cached.Cache())
}

func TestCachedValidator_StartStop(t *testing.T) {
	schemaReg := schema.NewRegistry()
	schemaYAML := `
service: test
version: 1.0
handlers:
  test.handler:
    compensation_strategy: none
    params: {}
    returns:
      result:
        type: string
`
	require.NoError(t, schemaReg.LoadFromYAML([]byte(schemaYAML)))

	validator, err := NewMockValidatorForTesting(schemaReg)
	require.NoError(t, err)

	cached, err := NewCachedValidator(CachedValidatorConfig{
		Validator: validator,
		TTL:       50 * time.Millisecond,
		MaxSize:   100,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start background eviction
	cached.Start(ctx)

	// Validate to populate cache
	script := `result = test.handler()`
	_, err = cached.Validate(ctx, script)
	require.NoError(t, err)
	assert.Equal(t, 1, cached.CacheSize())

	// Cancel context to stop background eviction
	cancel()

	// Validator should still work after stop
	_, err = cached.Validate(context.Background(), script)
	require.NoError(t, err)
}
