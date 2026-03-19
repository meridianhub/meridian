package service

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutboxEventPublisher(t *testing.T) {
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(nil, publisher)

	require.NotNil(t, oep)
	assert.NotNil(t, oep.publisher)
}

func TestOutboxEventPublisher_Publish_UnsupportedType(t *testing.T) {
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(nil, publisher)

	err := oep.Publish(context.Background(), "unsupported-event")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedEventType)
}
