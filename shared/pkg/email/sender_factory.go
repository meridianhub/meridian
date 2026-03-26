package email

import (
	"errors"
	"log/slog"
	"os"
)

// ErrMissingResendAPIKey is returned when EMAIL_MODE is live or empty but RESEND_API_KEY is not set.
var ErrMissingResendAPIKey = errors.New("RESEND_API_KEY must be set when EMAIL_MODE is live")

// ErrUnknownEmailMode is returned when EMAIL_MODE contains an unrecognised value.
var ErrUnknownEmailMode = errors.New("unknown EMAIL_MODE: expected disabled, log, or live")

// NewSenderFromEnv constructs a Sender based on the EMAIL_MODE environment variable:
//   - "disabled" - NoopSender (no side effects)
//   - "log"      - LogSender (logs to slog)
//   - "live" / "" (default) - ResendSender (requires RESEND_API_KEY)
func NewSenderFromEnv(logger *slog.Logger) (Sender, error) {
	mode := os.Getenv("EMAIL_MODE")
	switch mode {
	case "disabled":
		return NewNoopSender(), nil
	case "log":
		return NewLogSender(logger), nil
	case "live", "":
		apiKey := os.Getenv("RESEND_API_KEY")
		if apiKey == "" {
			return nil, ErrMissingResendAPIKey
		}
		return NewResendSender(apiKey, logger), nil
	default:
		return nil, ErrUnknownEmailMode
	}
}
