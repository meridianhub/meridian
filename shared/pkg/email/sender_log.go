package email

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "github.com/google/uuid"
)

// LogSender logs email details via slog instead of delivering them.
// Intended for EMAIL_MODE=log environments (local dev, staging previews).
type LogSender struct {
    logger *slog.Logger
}

// NewLogSender creates a LogSender backed by the given logger.
func NewLogSender(logger *slog.Logger) *LogSender {
    if logger == nil {
        logger = slog.Default()
    }
    return &LogSender{logger: logger}
}

// Send logs the email and returns a fake delivery ID.
func (s *LogSender) Send(_ context.Context, msg Message) (SendResult, error) {
    fakeID := fmt.Sprintf("log-%s", uuid.NewString())
    s.logger.Info("email send (log mode)",
        "provider_id", fakeID,
        "from", msg.From,
        "to", msg.To,
        "subject", msg.Subject,
        "idempotency_key", msg.IdempotencyKey,
    )
    return SendResult{
        ProviderID: fakeID,
        SentAt:     time.Now().UTC(),
    }, nil
}
