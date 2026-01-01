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
//
// Environment Variables:
//
//	PAGERDUTY_ENABLED        - Set to "true" to enable PagerDuty alerting
//	PAGERDUTY_ROUTING_KEY    - PagerDuty integration/routing key (required if enabled)
//	PAGERDUTY_SOURCE         - Alert source identifier (default: "tenant-service")
//	SLACK_WEBHOOK_URL        - Slack incoming webhook URL (presence enables Slack alerts)
//	SERVICE_NAME             - Service name for Slack alerts (default: "tenant-service")
//	ALERT_RATE_LIMIT_PER_MINUTE - Max alerts per type per minute (default: 10)
//	ALERT_DLQ_MAX_SIZE       - Max entries in dead-letter queue (default: 1000)
//	ALERT_RETRY_MAX_RETRIES  - Max retry attempts (default: 4)
//	ALERT_RETRY_INITIAL_BACKOFF - Initial retry backoff in seconds (default: 1)
//	ALERT_RETRY_MAX_BACKOFF  - Max retry backoff in seconds (default: 8)
type AlertingConfig struct {
	// PagerDuty configures PagerDuty integration for critical alerts.
	PagerDuty PagerDutyConfig

	// Slack configures Slack incoming webhook integration for team notifications.
	Slack SlackConfig

	// RateLimit configures rate limiting for alert delivery.
	RateLimit AlertRateLimitConfig

	// Retry configures retry behavior for failed alerts.
	Retry AlertRetryConfig

	// DLQ configures the dead-letter queue for failed alerts.
	DLQ AlertDLQConfig
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

// AlertDLQConfig holds configuration for the alert dead-letter queue.
type AlertDLQConfig struct {
	// Enabled indicates whether the dead-letter queue is active.
	// When true, failed alerts are stored for manual review.
	// Defaults to true.
	Enabled bool

	// MaxSize is the maximum number of entries in the dead-letter queue.
	// When exceeded, oldest entries are removed.
	// Defaults to 1000.
	MaxSize int
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

// DefaultAlertDLQConfig returns the default dead-letter queue configuration.
func DefaultAlertDLQConfig() AlertDLQConfig {
	return AlertDLQConfig{
		Enabled: true,
		MaxSize: 1000,
	}
}

// getEnvInt parses an environment variable as an integer.
// Returns the default value if the variable is empty or invalid.
func getEnvInt(key string, defaultVal int, minVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < minVal {
		return defaultVal
	}
	return n
}

// LoadAlertingConfig loads alerting configuration from environment variables.
func LoadAlertingConfig() (*AlertingConfig, error) {
	config := &AlertingConfig{
		PagerDuty: loadPagerDutyConfig(),
		Slack:     loadSlackConfig(),
		RateLimit: loadRateLimitConfig(),
		Retry:     loadRetryConfig(),
		DLQ:       loadDLQConfig(),
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// loadPagerDutyConfig loads PagerDuty configuration from environment variables.
func loadPagerDutyConfig() PagerDutyConfig {
	source := os.Getenv("PAGERDUTY_SOURCE")
	if source == "" {
		source = "tenant-service"
	}
	return PagerDutyConfig{
		Enabled:    os.Getenv("PAGERDUTY_ENABLED") == "true",
		RoutingKey: os.Getenv("PAGERDUTY_ROUTING_KEY"),
		Source:     source,
	}
}

// loadSlackConfig loads Slack configuration from environment variables.
func loadSlackConfig() SlackConfig {
	serviceName := os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		serviceName = "tenant-service"
	}
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	return SlackConfig{
		Enabled:     webhookURL != "",
		WebhookURL:  webhookURL,
		ServiceName: serviceName,
	}
}

// loadRateLimitConfig loads rate limit configuration from environment variables.
func loadRateLimitConfig() AlertRateLimitConfig {
	cfg := DefaultAlertRateLimitConfig()
	maxAlerts := getEnvInt("ALERT_RATE_LIMIT_PER_MINUTE", cfg.MaxAlertsPerMinute, 1)
	cfg.MaxAlertsPerMinute = maxAlerts
	cfg.BurstSize = maxAlerts
	return cfg
}

// loadRetryConfig loads retry configuration from environment variables.
func loadRetryConfig() AlertRetryConfig {
	cfg := DefaultAlertRetryConfig()
	cfg.MaxRetries = getEnvInt("ALERT_RETRY_MAX_RETRIES", cfg.MaxRetries, 0)
	if initialSec := getEnvInt("ALERT_RETRY_INITIAL_BACKOFF", 0, 1); initialSec > 0 {
		cfg.InitialBackoff = time.Duration(initialSec) * time.Second
	}
	if maxSec := getEnvInt("ALERT_RETRY_MAX_BACKOFF", 0, 1); maxSec > 0 {
		cfg.MaxBackoff = time.Duration(maxSec) * time.Second
	}
	return cfg
}

// loadDLQConfig loads dead-letter queue configuration from environment variables.
func loadDLQConfig() AlertDLQConfig {
	cfg := DefaultAlertDLQConfig()
	cfg.MaxSize = getEnvInt("ALERT_DLQ_MAX_SIZE", cfg.MaxSize, 1)
	return cfg
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
