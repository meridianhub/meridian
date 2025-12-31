package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPagerDutyConfig_Validate_Enabled(t *testing.T) {
	tests := []struct {
		name      string
		config    AlertingConfig
		wantErr   error
		errSubstr string
	}{
		{
			name: "valid enabled config",
			config: AlertingConfig{
				PagerDuty: PagerDutyConfig{
					Enabled:    true,
					RoutingKey: "valid-routing-key",
					Source:     "test-source",
				},
			},
			wantErr: nil,
		},
		{
			name: "enabled without routing key",
			config: AlertingConfig{
				PagerDuty: PagerDutyConfig{
					Enabled:    true,
					RoutingKey: "",
					Source:     "test-source",
				},
			},
			wantErr: ErrPagerDutyRoutingKeyRequired,
		},
		{
			name: "disabled without routing key is valid",
			config: AlertingConfig{
				PagerDuty: PagerDutyConfig{
					Enabled:    false,
					RoutingKey: "",
					Source:     "",
				},
			},
			wantErr: nil,
		},
		{
			name: "disabled with routing key is valid",
			config: AlertingConfig{
				PagerDuty: PagerDutyConfig{
					Enabled:    false,
					RoutingKey: "some-key",
					Source:     "some-source",
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSlackConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  AlertingConfig
		wantErr error
	}{
		{
			name: "valid enabled config",
			config: AlertingConfig{
				Slack: SlackConfig{
					Enabled:     true,
					WebhookURL:  "https://hooks.slack.com/services/T00/B00/XXX",
					ServiceName: "test-service",
				},
			},
			wantErr: nil,
		},
		{
			name: "enabled without webhook URL",
			config: AlertingConfig{
				Slack: SlackConfig{
					Enabled:     true,
					WebhookURL:  "",
					ServiceName: "test-service",
				},
			},
			wantErr: ErrSlackWebhookURLRequired,
		},
		{
			name: "enabled with invalid URL",
			config: AlertingConfig{
				Slack: SlackConfig{
					Enabled:     true,
					WebhookURL:  "not-a-url",
					ServiceName: "test-service",
				},
			},
			wantErr: ErrSlackWebhookURLInvalid,
		},
		{
			name: "enabled with HTTP URL",
			config: AlertingConfig{
				Slack: SlackConfig{
					Enabled:     true,
					WebhookURL:  "http://hooks.slack.com/services/T00/B00/XXX",
					ServiceName: "test-service",
				},
			},
			wantErr: ErrSlackWebhookURLNotHTTPS,
		},
		{
			name: "disabled without webhook URL is valid",
			config: AlertingConfig{
				Slack: SlackConfig{
					Enabled:     false,
					WebhookURL:  "",
					ServiceName: "",
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadAlertingConfig_Defaults(t *testing.T) {
	// Clear environment
	os.Unsetenv("PAGERDUTY_ENABLED")
	os.Unsetenv("PAGERDUTY_ROUTING_KEY")
	os.Unsetenv("PAGERDUTY_SOURCE")
	os.Unsetenv("SLACK_WEBHOOK_URL")
	os.Unsetenv("SERVICE_NAME")

	config, err := LoadAlertingConfig()

	require.NoError(t, err)
	assert.False(t, config.PagerDuty.Enabled)
	assert.Empty(t, config.PagerDuty.RoutingKey)
	assert.Equal(t, "tenant-service", config.PagerDuty.Source) // Default
	assert.False(t, config.Slack.Enabled)
	assert.Empty(t, config.Slack.WebhookURL)
	assert.Equal(t, "tenant-service", config.Slack.ServiceName) // Default
}

func TestLoadAlertingConfig_PagerDutyEnabled(t *testing.T) {
	// Set environment
	os.Setenv("PAGERDUTY_ENABLED", "true")
	os.Setenv("PAGERDUTY_ROUTING_KEY", "my-routing-key")
	os.Setenv("PAGERDUTY_SOURCE", "custom-source")
	defer func() {
		os.Unsetenv("PAGERDUTY_ENABLED")
		os.Unsetenv("PAGERDUTY_ROUTING_KEY")
		os.Unsetenv("PAGERDUTY_SOURCE")
	}()

	config, err := LoadAlertingConfig()

	require.NoError(t, err)
	assert.True(t, config.PagerDuty.Enabled)
	assert.Equal(t, "my-routing-key", config.PagerDuty.RoutingKey)
	assert.Equal(t, "custom-source", config.PagerDuty.Source)
}

func TestLoadAlertingConfig_PagerDutyEnabledMissingKey(t *testing.T) {
	// Set environment
	os.Setenv("PAGERDUTY_ENABLED", "true")
	os.Unsetenv("PAGERDUTY_ROUTING_KEY")
	defer func() {
		os.Unsetenv("PAGERDUTY_ENABLED")
	}()

	_, err := LoadAlertingConfig()

	assert.ErrorIs(t, err, ErrPagerDutyRoutingKeyRequired)
}

func TestLoadAlertingConfig_SlackEnabled(t *testing.T) {
	// Set environment
	os.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/T00/B00/XXX")
	os.Setenv("SERVICE_NAME", "custom-service")
	defer func() {
		os.Unsetenv("SLACK_WEBHOOK_URL")
		os.Unsetenv("SERVICE_NAME")
	}()

	config, err := LoadAlertingConfig()

	require.NoError(t, err)
	assert.True(t, config.Slack.Enabled)
	assert.Equal(t, "https://hooks.slack.com/services/T00/B00/XXX", config.Slack.WebhookURL)
	assert.Equal(t, "custom-service", config.Slack.ServiceName)
}

func TestLoadAlertingConfig_BothEnabled(t *testing.T) {
	// Set environment
	os.Setenv("PAGERDUTY_ENABLED", "true")
	os.Setenv("PAGERDUTY_ROUTING_KEY", "pd-routing-key")
	os.Setenv("PAGERDUTY_SOURCE", "pd-source")
	os.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/T00/B00/XXX")
	os.Setenv("SERVICE_NAME", "my-service")
	defer func() {
		os.Unsetenv("PAGERDUTY_ENABLED")
		os.Unsetenv("PAGERDUTY_ROUTING_KEY")
		os.Unsetenv("PAGERDUTY_SOURCE")
		os.Unsetenv("SLACK_WEBHOOK_URL")
		os.Unsetenv("SERVICE_NAME")
	}()

	config, err := LoadAlertingConfig()

	require.NoError(t, err)
	assert.True(t, config.PagerDuty.Enabled)
	assert.Equal(t, "pd-routing-key", config.PagerDuty.RoutingKey)
	assert.Equal(t, "pd-source", config.PagerDuty.Source)
	assert.True(t, config.Slack.Enabled)
	assert.Equal(t, "https://hooks.slack.com/services/T00/B00/XXX", config.Slack.WebhookURL)
	assert.Equal(t, "my-service", config.Slack.ServiceName)
}
