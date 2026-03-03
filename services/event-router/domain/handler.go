// Package domain provides core domain logic for the event-router service.
package domain

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// EventHandler processes events from a specific channel or event type.
type EventHandler interface {
	// Handle processes a single event from the given channel.
	// metadata contains Kafka headers or other transport-level key-value pairs.
	Handle(ctx context.Context, channel string, event proto.Message, metadata map[string]string) error
}
