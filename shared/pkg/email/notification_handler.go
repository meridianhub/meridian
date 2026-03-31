package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PartyEmailResolver resolves a party ID to an email address.
type PartyEmailResolver interface {
	ResolveEmail(ctx context.Context, partyID string) (string, error)
}

// NotificationHandlerDeps holds dependencies for the notification.send handler.
type NotificationHandlerDeps struct {
	Outbox             OutboxRepository
	EmailResolver      PartyEmailResolver
	PreferenceEnforcer *PreferenceEnforcer
	Logger             *slog.Logger
}

// Sentinel errors for notification handler operations.
var (
	ErrUnsupportedNotificationType = fmt.Errorf("email: unsupported notification type")
	ErrMissingRecipient            = fmt.Errorf("email: missing required parameter: recipient")
	ErrMissingType                 = fmt.Errorf("email: missing required parameter: type")
	ErrMissingOutbox               = fmt.Errorf("email: outbox repository is required")
	ErrMissingEmailResolver        = fmt.Errorf("email: email resolver is required")
)

// NewNotificationSendHandler creates a saga handler for notification.send.
// The handler validates the notification type, resolves the party's email address,
// and enqueues the email to the outbox for delivery.
func NewNotificationSendHandler(deps NotificationHandlerDeps) saga.Handler {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Outbox == nil {
		return func(*saga.StarlarkContext, map[string]any) (any, error) {
			return nil, ErrMissingOutbox
		}
	}
	if deps.EmailResolver == nil {
		return func(*saga.StarlarkContext, map[string]any) (any, error) {
			return nil, ErrMissingEmailResolver
		}
	}
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		if err := validateNotificationParams(params); err != nil {
			return nil, err
		}

		recipient, _ := params["recipient"].(string)

		if deps.PreferenceEnforcer != nil {
			suppressed, result, err := checkPreferences(ctx, deps, params, recipient)
			if err != nil {
				return nil, err
			}
			if suppressed {
				return result, nil
			}
		}

		emailAddr, err := deps.EmailResolver.ResolveEmail(ctx, recipient)
		if err != nil {
			return nil, fmt.Errorf("email: failed to resolve email for party %s: %w", recipient, err)
		}

		entry := buildOutboxEntry(ctx, params, emailAddr)
		err = deps.Outbox.Enqueue(ctx, entry)
		if err != nil {
			return handleEnqueueResult(deps.Logger, entry, err)
		}

		deps.Logger.Info("notification enqueued",
			"outbox_id", entry.ID.String(),
			"recipient", recipient,
			"template", entry.TemplateName,
			"idempotency_key", entry.IdempotencyKey)

		return map[string]any{
			"status":          "QUEUED",
			"outbox_id":       entry.ID.String(),
			"idempotency_key": entry.IdempotencyKey,
		}, nil
	}
}

// checkPreferences evaluates communication preferences. Returns (true, result, nil)
// when the message should be suppressed, or (false, nil, nil) when sending is allowed.
func checkPreferences(ctx *saga.StarlarkContext, deps NotificationHandlerDeps, params map[string]any, recipient string) (bool, any, error) {
	category, _ := params["category"].(string)
	if category == "" {
		category = CategoryTransactional // Default: legacy callers are transactional
	}
	channel, _ := params["type"].(string)
	templateName, _ := params["template"].(string)
	if templateName == "" {
		templateName = "generic-notification"
	}

	tenantID, _ := tenant.FromContext(ctx)
	allowed, reason, err := deps.PreferenceEnforcer.ShouldSend(
		ctx, string(tenantID), recipient, channel, templateName, category)
	if err != nil {
		return false, nil, fmt.Errorf("email: preference enforcement failed: %w", err)
	}
	if !allowed {
		deps.Logger.Info("notification suppressed by preference",
			"recipient", recipient, "category", category, "reason", reason)
		return true, map[string]any{
			"status":             "SUPPRESSED",
			"suppression_reason": reason,
		}, nil
	}
	return false, nil, nil
}

func validateNotificationParams(params map[string]any) error {
	notifType, _ := params["type"].(string)
	if notifType == "" {
		return ErrMissingType
	}
	if notifType != "EMAIL" {
		return fmt.Errorf("%w: %s (only EMAIL is supported)", ErrUnsupportedNotificationType, notifType)
	}
	recipient, _ := params["recipient"].(string)
	if recipient == "" {
		return ErrMissingRecipient
	}
	return nil
}

func buildOutboxEntry(ctx *saga.StarlarkContext, params map[string]any, emailAddr string) *OutboxEntry {
	templateName, _ := params["template"].(string)
	if templateName == "" {
		templateName = "generic-notification"
	}

	templateData := make(map[string]any)
	if data, ok := params["data"].(map[string]any); ok {
		for k, v := range data {
			templateData[k] = v
		}
	}

	idempotencyKey, _ := params["idempotency_key"].(string)
	if idempotencyKey == "" {
		idempotencyKey = fmt.Sprintf("saga_%s_step_%s",
			ctx.SagaExecutionID.String(), ctx.IdempotencyKey)
	}

	return &OutboxEntry{
		IdempotencyKey: idempotencyKey,
		ToAddresses:    []string{emailAddr},
		Subject:        buildSubjectFromTemplate(templateName, templateData),
		TemplateName:   templateName,
		TemplateData:   templateData,
	}
}

func handleEnqueueResult(logger *slog.Logger, entry *OutboxEntry, err error) (any, error) {
	if errors.Is(err, ErrDuplicateIdempotency) {
		logger.Info("notification.send idempotent replay detected",
			"idempotency_key", entry.IdempotencyKey)
		result := map[string]any{
			"status":          "QUEUED",
			"idempotency_key": entry.IdempotencyKey,
			"replay":          true,
		}
		// entry.ID is not populated by Enqueue on duplicate - only include if set
		if entry.ID != (uuid.UUID{}) {
			result["outbox_id"] = entry.ID.String()
		}
		return result, nil
	}
	return nil, fmt.Errorf("email: failed to enqueue notification: %w", err)
}

// buildSubjectFromTemplate generates an email subject line from the template name and data.
func buildSubjectFromTemplate(templateName string, data map[string]any) string {
	switch templateName {
	case "dunning-notice":
		severity, ok := data["severity"]
		if !ok || severity == nil {
			return "Payment reminder"
		}
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
