package email

import (
    "context"
    "fmt"
    "log/slog"
    "net/http"
    "net/url"
    "time"

    resendgo "github.com/resend/resend-go/v2"

    "github.com/meridianhub/meridian/shared/pkg/clients"
)

// ResendSender delivers email via the Resend API, wrapped with circuit breaker protection.
type ResendSender struct {
    client *resendgo.Client
    cb     *clients.CircuitBreaker
    logger *slog.Logger
}

// NewResendSender creates a Resend-backed Sender. It trips the circuit after 5
// consecutive failures as per DefaultCircuitBreakerConfig.
func NewResendSender(apiKey string, logger *slog.Logger) *ResendSender {
    return NewResendSenderWithBaseURL(apiKey, logger, "")
}

// NewResendSenderWithBaseURL creates a ResendSender with a custom API base URL.
// Provide a non-empty baseURL only in tests; pass "" for production.
func NewResendSenderWithBaseURL(apiKey string, logger *slog.Logger, baseURL string) *ResendSender {
    if logger == nil {
        logger = slog.Default()
    }
    cb := clients.NewCircuitBreaker(clients.DefaultCircuitBreakerConfig("resend-email"), logger)
    c := resendgo.NewCustomClient(&http.Client{}, apiKey)
    if baseURL != "" {
        parsed, err := url.Parse(baseURL)
        if err == nil {
            c.BaseURL = parsed
        }
    }
    return &ResendSender{
        client: c,
        cb:     cb,
        logger: logger,
    }
}

// Send delivers msg via Resend, forwarding IdempotencyKey when set.
func (s *ResendSender) Send(ctx context.Context, msg Message) (SendResult, error) {
    params := buildSendEmailRequest(msg)

    raw, err := s.cb.Execute(ctx, func() (any, error) {
        var opts *resendgo.SendEmailOptions
        if msg.IdempotencyKey != "" {
            opts = &resendgo.SendEmailOptions{IdempotencyKey: msg.IdempotencyKey}
        }
        if opts != nil {
            return s.client.Emails.SendWithOptions(ctx, params, opts)
        }
        return s.client.Emails.SendWithContext(ctx, params)
    })
    if err != nil {
        return SendResult{}, fmt.Errorf("resend send failed: %w", err)
    }

    resp, ok := raw.(*resendgo.SendEmailResponse)
    if !ok || resp == nil {
        return SendResult{}, fmt.Errorf("resend: unexpected response type")
    }

    return SendResult{
        ProviderID: resp.Id,
        SentAt:     time.Now().UTC(),
    }, nil
}

func buildSendEmailRequest(msg Message) *resendgo.SendEmailRequest {
    req := &resendgo.SendEmailRequest{
        From:    msg.From,
        To:      msg.To,
        Subject: msg.Subject,
        Html:    msg.HTMLBody,
        Text:    msg.TextBody,
        ReplyTo: msg.ReplyTo,
        Headers: msg.Headers,
    }

    if len(msg.Attachments) > 0 {
        req.Attachments = make([]*resendgo.Attachment, len(msg.Attachments))
        for i, a := range msg.Attachments {
            req.Attachments[i] = &resendgo.Attachment{
                Filename:    a.Filename,
                ContentType: a.ContentType,
                Content:     a.Data,
            }
        }
    }

    return req
}
