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

	// Verify core current-account handlers are registered
	coreHandlers := []string{
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

	for _, name := range coreHandlers {
		handler, err := registry.Get(name)
		require.NoError(t, err, "handler %q should be registered", name)
		assert.NotNil(t, handler, "handler %q should not be nil", name)
	}

	// Verify platform-wide stub handlers are also registered
	platformStubs := []string{
		"notification.send",
		"payment_order.create_lien",
		"reconciliation.initiate_run",
		"party.get_default_payment_method",
		"operational_gateway.dispatch_instruction",
		"financial_gateway.dispatch_payment",
		"forecasting.compute_forward_curve",
		"market_information.publish_observation",
		"reference_data.register_instrument",
		"internal_account.initiate",
	}

	for _, name := range platformStubs {
		handler, err := registry.Get(name)
		require.NoError(t, err, "platform stub handler %q should be registered", name)
		assert.NotNil(t, handler, "platform stub handler %q should not be nil", name)
	}
}

func TestRegisterCurrentAccountHandlers_StubHandlersReturnNotImplemented(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	err := RegisterCurrentAccountHandlers(registry)
	require.NoError(t, err)

	// Stub handlers (not yet implemented) should return errHandlerNotImplemented
	stubHandlers := []string{
		"position_keeping.update_log",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
		"financial_accounting.create_booking",
		"current_account.control",
		"current_account.create_lien",
		"current_account.execute_lien",
		"current_account.terminate_lien",
		// Platform-wide stubs (notification.send is tested separately with real handler)
		"payment_order.create_lien",
		"reconciliation.initiate_run",
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
	// Ensure registration does not silently skip any handler.
	// Uses registry.List() to get the actual count rather than iterating a
	// hardcoded list, so this test catches both missing and extra handlers.
	//
	// The full set includes 15 core handlers plus platform-wide stubs for
	// cross-service handlers defined in the saga schema (payment_order,
	// reconciliation, party, operational_gateway, financial_gateway,
	// forecasting, market_information, reference_data, internal_account, etc.).
	registry := saga.NewHandlerRegistry()
	err := RegisterCurrentAccountHandlers(registry)
	require.NoError(t, err)

	const expectedCount = 47
	registered := registry.List()
	assert.Equal(t, expectedCount, len(registered), "expected %d handlers to be registered, got %d: %v", expectedCount, len(registered), registered)
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
