// Package config provides configuration types for the Tenant service.
package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"time"
)

// AlertingConfig holds configuration for the alerting subsystem.
type AlertingConfig struct {
	// PagerDuty configures PagerDuty integration for critical alerts.
	PagerDuty PagerDutyConfig

	// Slack configures Slack incoming webhook integration for team notifications.
	Slack SlackConfig

	// RateLimit configures rate limiting for alert delivery.
	RateLimit AlertRateLimitConfig
}

// AlertRateLimitConfig holds configuration for alert rate limiting using token bucket algorithm.
type AlertRateLimitConfig struct {
	// MaxAlertsPerMinute is the maximum number of alerts per alert type per minute.
	// Defaults to 10 if not set.
	MaxAlertsPerMinute int

	// BurstSize is the maximum number of alerts that can be sent in a burst.
	// Defaults to MaxAlertsPerMinute if not set.
	BurstSize int
}

// AlertRetryConfig holds configuration for alert retry logic.
type AlertRetryConfig struct {
	// MaxRetries is the maximum number of retry attempts.
	// Defaults to 4 if not set.
	MaxRetries int

	// InitialBackoff is the initial backoff duration before first retry.
	// Defaults to 1 second if not set.
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff duration between retries.
	// Defaults to 8 seconds if not set.
	MaxBackoff time.Duration
}

// PagerDutyConfig holds configuration for PagerDuty Events API v2 integration.
type PagerDutyConfig struct {
	// Enabled indicates whether PagerDuty alerting is active.
	// When false, alerts are logged but not sent to PagerDuty.
	Enabled bool

	// RoutingKey is the integration key (routing key) from PagerDuty.
	// This determines which PagerDuty service receives the events.
	// Required when Enabled is true.
	RoutingKey string

	// Source identifies the origin of alerts (e.g., "tenant-service", "meridian-prod").
	// Defaults to "tenant-service" if not set.
	Source string
}

// SlackConfig holds configuration for Slack incoming webhook integration.
type SlackConfig struct {
	// Enabled indicates whether Slack alerting is active.
	// When false, alerts are logged but not sent to Slack.
	Enabled bool

	// WebhookURL is the Slack incoming webhook URL.
	// Required when Enabled is true.
	WebhookURL string

	// ServiceName identifies the origin of alerts (e.g., "tenant-service").
	// Defaults to "tenant-service" if not set.
	ServiceName string
}

// Configuration errors.
var (
	// ErrPagerDutyRoutingKeyRequired is returned when PagerDuty is enabled but no routing key is set.
	ErrPagerDutyRoutingKeyRequired = errors.New("PAGERDUTY_ROUTING_KEY is required when PagerDuty alerting is enabled")

	// ErrSlackWebhookURLRequired is returned when Slack is enabled but no webhook URL is set.
	ErrSlackWebhookURLRequired = errors.New("SLACK_WEBHOOK_URL is required when Slack alerting is enabled")

	// ErrSlackWebhookURLInvalid is returned when the Slack webhook URL is invalid.
	ErrSlackWebhookURLInvalid = errors.New("SLACK_WEBHOOK_URL is not a valid URL")

	// ErrSlackWebhookURLNotHTTPS is returned when the Slack webhook URL does not use HTTPS.
	ErrSlackWebhookURLNotHTTPS = errors.New("SLACK_WEBHOOK_URL must use HTTPS")
)

// DefaultAlertRateLimitConfig returns the default rate limit configuration.
func DefaultAlertRateLimitConfig() AlertRateLimitConfig {
	return AlertRateLimitConfig{
		MaxAlertsPerMinute: 10,
		BurstSize:          10,
	}
}

// DefaultAlertRetryConfig returns the default retry configuration.
func DefaultAlertRetryConfig() AlertRetryConfig {
	return AlertRetryConfig{
		MaxRetries:     4,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     8 * time.Second,
	}
}

// LoadAlertingConfig loads alerting configuration from environment variables.
func LoadAlertingConfig() (*AlertingConfig, error) {
	config := &AlertingConfig{
		PagerDuty: PagerDutyConfig{
			Enabled:    os.Getenv("PAGERDUTY_ENABLED") == "true",
			RoutingKey: os.Getenv("PAGERDUTY_ROUTING_KEY"),
			Source:     os.Getenv("PAGERDUTY_SOURCE"),
		},
		Slack: SlackConfig{
			Enabled:     os.Getenv("SLACK_WEBHOOK_URL") != "",
			WebhookURL:  os.Getenv("SLACK_WEBHOOK_URL"),
			ServiceName: os.Getenv("SERVICE_NAME"),
		},
		RateLimit: DefaultAlertRateLimitConfig(),
	}

	// Set default source if not provided
	if config.PagerDuty.Source == "" {
		config.PagerDuty.Source = "tenant-service"
	}

	// Set default service name if not provided
	if config.Slack.ServiceName == "" {
		config.Slack.ServiceName = "tenant-service"
	}

	// Override rate limit from environment if set
	if maxAlerts := os.Getenv("ALERT_RATE_LIMIT_PER_MINUTE"); maxAlerts != "" {
		if n, err := strconv.Atoi(maxAlerts); err == nil && n > 0 {
			config.RateLimit.MaxAlertsPerMinute = n
			// Set burst size to match if not explicitly configured
			config.RateLimit.BurstSize = n
		}
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// Validate checks that the alerting configuration is valid.
func (c *AlertingConfig) Validate() error {
	if c.PagerDuty.Enabled && c.PagerDuty.RoutingKey == "" {
		return ErrPagerDutyRoutingKeyRequired
	}

	if c.Slack.Enabled {
		if c.Slack.WebhookURL == "" {
			return ErrSlackWebhookURLRequired
		}
		parsed, err := url.Parse(c.Slack.WebhookURL)
		if err != nil || parsed.Host == "" {
			return ErrSlackWebhookURLInvalid
		}
		if parsed.Scheme != "https" {
			return ErrSlackWebhookURLNotHTTPS
		}
	}

	return nil
}
