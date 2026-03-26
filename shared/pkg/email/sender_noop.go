package email

import (
    "context"
    "fmt"
    "time"

    "github.com/google/uuid"
)

// NoopSender discards all emails silently.
// Intended for EMAIL_MODE=disabled environments (unit tests, CI).
type NoopSender struct{}

// NewNoopSender creates a NoopSender.
func NewNoopSender() *NoopSender {
    return &NoopSender{}
}

// Send discards the message and returns a fake delivery ID.
func (s *NoopSender) Send(_ context.Context, _ Message) (SendResult, error) {
    return SendResult{
        ProviderID: fmt.Sprintf("noop-%s", uuid.NewString()),
        SentAt:     time.Now().UTC(),
    }, nil
}
