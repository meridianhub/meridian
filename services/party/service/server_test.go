package service

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestNewService_WithBuilders_SetsFields verifies that With* option methods
// actually populate the service fields with the provided non-nil values.
// (TestWithBuilders in update_control_test.go only verifies chaining with nil args.)

func TestNewService_WithBuilders_SetsFields(t *testing.T) {
	t.Parallel()

	t.Run("WithPaymentMethodRepository sets pmRepo field", func(t *testing.T) {
		mock := newMockRepository()
		svc, err := NewService(mock, nil)
		require.NoError(t, err)

		pmMock := newMockPMRepo()
		_ = svc.WithPaymentMethodRepository(pmMock)

		assert.Equal(t, pmMock, svc.pmRepo)
	})

	t.Run("WithVerificationProvider sets verificationProvider field", func(t *testing.T) {
		mock := newMockRepository()
		svc, err := NewService(mock, nil)
		require.NoError(t, err)

		provider := verification.NewMockProvider().WithAlwaysApprove(true)
		_ = svc.WithVerificationProvider(provider)

		assert.Equal(t, provider, svc.verificationProvider)
	})

	t.Run("WithAttributeValidator sets attributeValidator field", func(t *testing.T) {
		mock := newMockRepository()
		svc, err := NewService(mock, nil)
		require.NoError(t, err)

		validator := &AttributeValidator{}
		_ = svc.WithAttributeValidator(validator)

		assert.Equal(t, validator, svc.attributeValidator)
	})

	t.Run("WithOutboxPublisher sets both outboxPublisher and db fields", func(t *testing.T) {
		mock := newMockRepository()
		svc, err := NewService(mock, nil)
		require.NoError(t, err)

		publisher := events.NewOutboxPublisher("party-test")
		db := &gorm.DB{}
		_ = svc.WithOutboxPublisher(publisher, db)

		assert.Equal(t, publisher, svc.outboxPublisher)
		assert.Equal(t, db, svc.db)
	})
}

// TestSavePartyWithEvent_NoOutbox_PublishFnNotCalled verifies that when no outbox publisher
// is configured, the publishFn callback is never invoked - only Save is called.

func TestSavePartyWithEvent_NoOutbox_PublishFnNotCalled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := newMockRepository()
	svc := newTestService(mock)

	party, err := domain.NewParty(domain.PartyTypePerson, "Callback Test")
	require.NoError(t, err)

	publishCalled := false
	err = svc.savePartyWithEvent(ctx, party, func(_ *gorm.DB) error {
		publishCalled = true
		return nil
	})

	require.NoError(t, err)
	assert.False(t, publishCalled, "publishFn should not be called when outbox is not configured")
	assert.Contains(t, mock.parties, party.ID())
}

// TestSavePartyWithEvent_NoOutbox_PropagatesSaveError verifies error propagation
// when the fallback Save fails.

func TestSavePartyWithEvent_NoOutbox_PropagatesSaveError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := newMockRepository()
	mock.saveErr = errDatabaseFailed
	svc := newTestService(mock)

	party, err := domain.NewParty(domain.PartyTypePerson, "Error Test")
	require.NoError(t, err)

	err = svc.savePartyWithEvent(ctx, party, nil)

	require.Error(t, err)
	assert.Equal(t, errDatabaseFailed, err)
}
