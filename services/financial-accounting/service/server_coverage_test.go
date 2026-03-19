package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/financial-accounting/domain"
)

// mockReferenceDataClient implements ReferenceDataClient for testing.
type mockReferenceDataClient struct {
	result CachedInstrumentResult
	err    error
}

func (m *mockReferenceDataClient) GetInstrument(_ context.Context, _ string, _ int) (CachedInstrumentResult, error) {
	return m.result, m.err
}

// mockCachedInstrumentResult implements CachedInstrumentResult for testing.
type mockCachedInstrumentResult struct {
	program interface{}
}

func (m *mockCachedInstrumentResult) GetBucketKeyProgram() interface{} {
	return m.program
}

func TestNewReferenceDataRegistryAdapter(t *testing.T) {
	client := &mockReferenceDataClient{}
	adapter := NewReferenceDataRegistryAdapter(client)
	require.NotNil(t, adapter)
	assert.Equal(t, client, adapter.client)
}

func TestReferenceDataRegistryAdapter_GetInstrument_Success(t *testing.T) {
	cached := &mockCachedInstrumentResult{program: nil}
	client := &mockReferenceDataClient{result: cached}
	adapter := NewReferenceDataRegistryAdapter(client)

	result, err := adapter.GetInstrument(context.Background(), "GBP", 1)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestReferenceDataRegistryAdapter_GetInstrument_Error(t *testing.T) {
	client := &mockReferenceDataClient{err: assert.AnError}
	adapter := NewReferenceDataRegistryAdapter(client)

	_, err := adapter.GetInstrument(context.Background(), "GBP", 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestCachedInstrumentAdapter_GetFungibilityKeyProgram_Nil(t *testing.T) {
	cached := &mockCachedInstrumentResult{program: nil}
	client := &mockReferenceDataClient{result: cached}
	adapter := NewReferenceDataRegistryAdapter(client)

	result, err := adapter.GetInstrument(context.Background(), "GBP", 1)
	require.NoError(t, err)

	program := result.GetFungibilityKeyProgram()
	assert.Nil(t, program)
}

func TestCachedInstrumentAdapter_GetFungibilityKeyProgram_NonNil(t *testing.T) {
	// Use a program that implements the Eval interface expected by NewCELProgramAdapter
	cached := &mockCachedInstrumentResult{program: &mockCELProgram{}}
	client := &mockReferenceDataClient{result: cached}
	adapter := NewReferenceDataRegistryAdapter(client)

	result, err := adapter.GetInstrument(context.Background(), "GBP", 1)
	require.NoError(t, err)

	program := result.GetFungibilityKeyProgram()
	// NewCELProgramAdapter wraps the program - should return non-nil
	assert.NotNil(t, program)
}

// Verify the adapter satisfies InstrumentDefinition interface
func TestCachedInstrumentAdapter_ImplementsInterface(t *testing.T) {
	cached := &mockCachedInstrumentResult{program: nil}
	adapter := &cachedInstrumentAdapter{cached: cached}

	var _ InstrumentDefinition = adapter
	program := adapter.GetFungibilityKeyProgram()
	assert.Nil(t, program)
}

// Verify CELProgramAdapter wraps correctly via domain
func TestCachedInstrumentAdapter_CELProgramAdapterType(t *testing.T) {
	// When the cached result has a non-nil program that implements Eval,
	// the adapter should wrap it in a CELProgramAdapter
	cached := &mockCachedInstrumentResult{program: &mockCELProgram{}}
	adapter := &cachedInstrumentAdapter{cached: cached}

	program := adapter.GetFungibilityKeyProgram()
	require.NotNil(t, program)

	// Verify the returned program is a *domain.CELProgramAdapter
	_, ok := program.(*domain.CELProgramAdapter)
	assert.True(t, ok, "expected *domain.CELProgramAdapter")
}

// Verify that a non-nil program that doesn't implement the Eval interface returns nil
func TestCachedInstrumentAdapter_GetFungibilityKeyProgram_InvalidProgram(t *testing.T) {
	cached := &mockCachedInstrumentResult{program: "not-a-cel-program"}
	adapter := &cachedInstrumentAdapter{cached: cached}

	program := adapter.GetFungibilityKeyProgram()
	// NewCELProgramAdapter returns nil when the program doesn't have the right interface
	assert.Nil(t, program)
}

// mockCELProgram implements the interface expected by NewCELProgramAdapter.
type mockCELProgram struct{}

type mockRefVal struct {
	val interface{}
}

func (m *mockRefVal) Value() interface{} {
	return m.val
}

func (m *mockCELProgram) Eval(_ interface{}) (interface{ Value() interface{} }, interface{}, error) {
	return &mockRefVal{val: "test-key"}, nil, nil
}
