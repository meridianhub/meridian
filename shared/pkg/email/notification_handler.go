package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// PartyEmailResolver resolves a party ID to an email address.
type PartyEmailResolver interface {
	ResolveEmail(ctx context.Context, partyID string) (string, error)
}

// NotificationHandlerDeps holds dependencies for the notification.send handler.
type NotificationHandlerDeps struct {
	Outbox        OutboxRepository
	EmailResolver PartyEmailResolver
	Logger        *slog.Logger
}

// Sentinel errors for notification handler operations.
var (
	ErrUnsupportedNotificationType = fmt.Errorf("email: unsupported notification type")
	ErrMissingRecipient            = fmt.Errorf("email: missing required parameter: recipient")
	ErrMissingType                 = fmt.Errorf("email: missing required parameter: type")
)

// NewNotificationSendHandler creates a saga handler for notification.send.
// The handler validates the notification type, resolves the party's email address,
// and enqueues the email to the outbox for delivery.
func NewNotificationSendHandler(deps NotificationHandlerDeps) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		notifType, _ := params["type"].(string)
		if notifType == "" {
			return nil, ErrMissingType
		}
		if notifType != "EMAIL" {
			return nil, fmt.Errorf("%w: %s (only EMAIL is supported)", ErrUnsupportedNotificationType, notifType)
		}

		recipient, _ := params["recipient"].(string)
		if recipient == "" {
			return nil, ErrMissingRecipient
		}

		// Resolve party email
		emailAddr, err := deps.EmailResolver.ResolveEmail(ctx, recipient)
		if err != nil {
			return nil, fmt.Errorf("email: failed to resolve email for party %s: %w", recipient, err)
		}

		// Build template name
		templateName, _ := params["template"].(string)
		if templateName == "" {
			templateName = "generic-notification"
		}

		// Build template data
		templateData := make(map[string]any)
		if data, ok := params["data"].(map[string]any); ok {
			for k, v := range data {
				templateData[k] = v
			}
		}

		// Build subject from template name
		subject := buildSubjectFromTemplate(templateName, templateData)

		// Generate idempotency key
		idempotencyKey, _ := params["idempotency_key"].(string)
		if idempotencyKey == "" {
			idempotencyKey = fmt.Sprintf("saga_%s_step_%s",
				ctx.SagaExecutionID.String(), ctx.IdempotencyKey)
		}

		entry := &OutboxEntry{
			IdempotencyKey: idempotencyKey,
			ToAddresses:    []string{emailAddr},
			Subject:        subject,
			TemplateName:   templateName,
			TemplateData:   templateData,
		}

		err = deps.Outbox.Enqueue(ctx, entry)
		if err != nil {
			// Duplicate idempotency is not an error for saga replay
			if isDuplicateIdempotencyErr(err) {
				deps.Logger.Info("notification.send idempotent replay detected",
					"idempotency_key", idempotencyKey,
					"outbox_id", entry.ID.String())
				return map[string]any{
					"status":          "QUEUED",
					"outbox_id":       entry.ID.String(),
					"idempotency_key": idempotencyKey,
					"replay":          true,
				}, nil
			}
			return nil, fmt.Errorf("email: failed to enqueue notification: %w", err)
		}

		deps.Logger.Info("notification enqueued",
			"outbox_id", entry.ID.String(),
			"recipient", recipient,
			"template", templateName,
			"idempotency_key", idempotencyKey)

		return map[string]any{
			"status":          "QUEUED",
			"outbox_id":       entry.ID.String(),
			"idempotency_key": idempotencyKey,
		}, nil
	}
}

// buildSubjectFromTemplate generates an email subject line from the template name and data.
func buildSubjectFromTemplate(templateName string, data map[string]any) string {
	switch templateName {
	case "dunning-notice":
		severity, _ := data["severity"]
		return fmt.Sprintf("Payment reminder - severity %v", severity)
	case "account-frozen":
		return "Your account has been frozen"
	case "payment-confirmation":
		return "Payment received"
	case "dunning-resolved":
		return "Account restored - payment received"
	default:
		return fmt.Sprintf("Notification: %s", templateName)
	}
}

// isDuplicateIdempotencyErr checks if the error wraps ErrDuplicateIdempotency.
func isDuplicateIdempotencyErr(err error) bool {
	return errors.Is(err, ErrDuplicateIdempotency)
}
