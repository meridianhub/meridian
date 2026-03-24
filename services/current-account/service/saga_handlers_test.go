package service

import (
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// RegisterCurrentAccountHandlers - handler registration completeness
// =============================================================================

func TestRegisterCurrentAccountHandlers_AllHandlersPresent(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	err := RegisterCurrentAccountHandlers(registry)
	require.NoError(t, err)

	// Verify all expected handlers are registered
	expectedHandlers := []string{
		"position_keeping.initiate_log",
		"position_keeping.update_log",
		"position_keeping.cancel_log",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
		"financial_accounting.create_booking",
		"financial_accounting.initiate_booking_log",
		"financial_accounting.capture_posting",
		"financial_accounting.update_booking_log",
		"financial_accounting.compensate_posting",
		"current_account.save",
		"current_account.control",
		"current_account.create_lien",
		"current_account.execute_lien",
		"current_account.terminate_lien",
	}

	for _, name := range expectedHandlers {
		handler, err := registry.Get(name)
		require.NoError(t, err, "handler %q should be registered", name)
		assert.NotNil(t, handler, "handler %q should not be nil", name)
	}
}

func TestRegisterCurrentAccountHandlers_StubHandlersReturnNotImplemented(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	err := RegisterCurrentAccountHandlers(registry)
	require.NoError(t, err)

	// Stub handlers should return "not implemented" error
	stubHandlers := []string{
		"position_keeping.update_log",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
		"financial_accounting.create_booking",
		"current_account.control",
		"current_account.create_lien",
		"current_account.execute_lien",
		"current_account.terminate_lien",
	}

	for _, name := range stubHandlers {
		handler, err := registry.Get(name)
		require.NoError(t, err, "handler %q should be registered", name)

		// Call the stub handler - it should return errHandlerNotImplemented
		_, handlerErr := handler(nil, nil)
		require.Error(t, handlerErr, "stub handler %q should return error", name)
		assert.ErrorIs(t, handlerErr, errHandlerNotImplemented, "stub handler %q should return errHandlerNotImplemented", name)
	}
}

func TestRegisterCurrentAccountHandlers_HandlerCount(t *testing.T) {
	// Ensure registration does not silently skip any handler
	registry := saga.NewHandlerRegistry()
	err := RegisterCurrentAccountHandlers(registry)
	require.NoError(t, err)

	// The registry should have all 15 handlers registered
	const expectedCount = 15
	count := 0
	for _, name := range []string{
		"position_keeping.initiate_log",
		"position_keeping.update_log",
		"position_keeping.cancel_log",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
		"financial_accounting.create_booking",
		"financial_accounting.initiate_booking_log",
		"financial_accounting.capture_posting",
		"financial_accounting.update_booking_log",
		"financial_accounting.compensate_posting",
		"current_account.save",
		"current_account.control",
		"current_account.create_lien",
		"current_account.execute_lien",
		"current_account.terminate_lien",
	} {
		_, err := registry.Get(name)
		if err == nil {
			count++
		}
	}
	assert.Equal(t, expectedCount, count, "expected %d handlers to be registered", expectedCount)
}

func TestRegisterCurrentAccountHandlers_CompensationMetadata(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	err := RegisterCurrentAccountHandlers(registry)
	require.NoError(t, err)

	// Verify handlers with compensation strategy have the correct metadata
	_, metadata, err := registry.GetWithMetadata("position_keeping.initiate_log")
	require.NoError(t, err)
	require.NotNil(t, metadata)
	assert.Equal(t, "position_keeping.cancel_log", metadata.Compensate)
	assert.True(t, metadata.HasAutoCompensation)

	// financial_accounting.capture_posting should also have compensation
	_, captureMetadata, err := registry.GetWithMetadata("financial_accounting.capture_posting")
	require.NoError(t, err)
	require.NotNil(t, captureMetadata)
	assert.Equal(t, "financial_accounting.compensate_posting", captureMetadata.Compensate)
}
