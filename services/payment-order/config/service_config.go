package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/shared/platform/env"
)

// ServiceConfig holds all payment-order service configuration loaded from
// environment variables. Call LoadServiceConfig to populate and Validate to
// verify constraints.
type ServiceConfig struct {
	// PaymentGatewayProvider selects the payment gateway implementation.
	// Valid values: "stripe", "mock", "financial-gateway". Default: "mock".
	PaymentGatewayProvider string

	// StripeAPIKey is the platform Stripe API key. Required when
	// PaymentGatewayProvider is "stripe".
	StripeAPIKey string

	// StripeWebhookSecret is the Stripe webhook endpoint secret. Required when
	// PaymentGatewayProvider is "stripe".
	StripeWebhookSecret string

	// FinancialGatewayAddr is the gRPC address of the financial-gateway service.
	// Required when PaymentGatewayProvider is "financial-gateway".
	// Example: "financial-gateway:50064" or "localhost:50064".
	FinancialGatewayAddr string

	// BillingEnabled controls whether billing background workers are started.
	// Default: false.
	BillingEnabled bool

	// BillingCronSchedule is the cron expression for the billing scheduler.
	// Default: "0 0 * * *" (midnight daily).
	BillingCronSchedule string

	// BillingShadowMode runs billing in shadow mode (dry-run). Default: false.
	BillingShadowMode bool

	// DunningPollInterval is how often the dunning worker polls for overdue
	// billing runs. Default: 5m.
	DunningPollInterval time.Duration

	// SagaOrchestrationEnabled controls whether payment orders are orchestrated
	// via Starlark saga scripts. When false (default), saga orchestration is
	// disabled and payment orchestration fails fast. When true, the orchestrator
	// loads and executes saga scripts from reference-data service.
	SagaOrchestrationEnabled bool
}

// Configuration validation errors.
var (
	ErrMissingStripeAPIKey         = errors.New("STRIPE_API_KEY is required when PAYMENT_GATEWAY_PROVIDER is \"stripe\"")
	ErrMissingStripeWebhookSecret  = errors.New("STRIPE_WEBHOOK_SECRET is required when PAYMENT_GATEWAY_PROVIDER is \"stripe\"")
	ErrMissingFinancialGatewayAddr = errors.New("FINANCIAL_GATEWAY_ADDR is required when PAYMENT_GATEWAY_PROVIDER is \"financial-gateway\"")
	ErrInvalidGatewayProvider      = errors.New("unsupported PAYMENT_GATEWAY_PROVIDER value")
)

// LoadServiceConfig reads all payment-order environment variables and returns
// a populated ServiceConfig. It does NOT validate — call Validate separately.
func LoadServiceConfig() ServiceConfig {
	return ServiceConfig{
		PaymentGatewayProvider:   env.GetEnvOrDefault("PAYMENT_GATEWAY_PROVIDER", gateway.ProviderMock),
		StripeAPIKey:             env.GetEnvOrDefault("STRIPE_API_KEY", ""),
		StripeWebhookSecret:      env.GetEnvOrDefault("STRIPE_WEBHOOK_SECRET", ""),
		FinancialGatewayAddr:     env.GetEnvOrDefault("FINANCIAL_GATEWAY_ADDR", ""),
		BillingEnabled:           env.GetEnvAsBool("BILLING_ENABLED", false),
		BillingCronSchedule:      env.GetEnvOrDefault("BILLING_CRON_SCHEDULE", "0 0 * * *"),
		BillingShadowMode:        env.GetEnvAsBool("BILLING_SHADOW_MODE", false),
		DunningPollInterval:      env.GetEnvAsDuration("DUNNING_POLL_INTERVAL", 5*time.Minute),
		SagaOrchestrationEnabled: env.GetEnvAsBool("USE_SAGA_ORCHESTRATION", false),
	}
}

// Validate checks that the ServiceConfig is internally consistent. It returns
// an error for the first violated constraint.
func (c ServiceConfig) Validate() error {
	switch c.PaymentGatewayProvider {
	case gateway.ProviderStripe:
		if c.StripeAPIKey == "" {
			return ErrMissingStripeAPIKey
		}
		if c.StripeWebhookSecret == "" {
			return ErrMissingStripeWebhookSecret
		}
	case gateway.ProviderFinancialGateway:
		if c.FinancialGatewayAddr == "" {
			return ErrMissingFinancialGatewayAddr
		}
	case gateway.ProviderMock:
		// No additional requirements.
	default:
		return fmt.Errorf("%w: %q (valid: %q, %q, %q)", ErrInvalidGatewayProvider, c.PaymentGatewayProvider, gateway.ProviderStripe, gateway.ProviderFinancialGateway, gateway.ProviderMock)
	}
	return nil
}

// LogValues logs the configuration at startup. Sensitive values (API keys,
// secrets) are redacted to show only the first and last 4 characters.
func (c ServiceConfig) LogValues(logger *slog.Logger) {
	logger.Info("payment-order service configuration",
		"payment_gateway_provider", c.PaymentGatewayProvider,
		"stripe_api_key", redact(c.StripeAPIKey),
		"stripe_webhook_secret", redact(c.StripeWebhookSecret),
		"financial_gateway_addr", c.FinancialGatewayAddr,
		"billing_enabled", c.BillingEnabled,
		"billing_cron_schedule", c.BillingCronSchedule,
		"billing_shadow_mode", c.BillingShadowMode,
		"dunning_poll_interval", c.DunningPollInterval,
		"saga_orchestration_enabled", c.SagaOrchestrationEnabled,
	)
}

// redact masks a sensitive string, showing only the first and last 4
// characters. Strings shorter than 12 characters are fully masked.
func redact(s string) string {
	if s == "" {
		return "(not set)"
	}
	if len(s) < 12 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}
