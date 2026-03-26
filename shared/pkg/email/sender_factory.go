package email

import (
    "fmt"
    "log/slog"
    "os"
)

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
            return nil, fmt.Errorf("EMAIL_MODE=%q requires RESEND_API_KEY to be set", mode)
        }
        return NewResendSender(apiKey, logger), nil
    default:
        return nil, fmt.Errorf("unknown EMAIL_MODE %q: expected disabled, log, or live", mode)
    }
}
