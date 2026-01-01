// Package pagerduty provides an HTTP client for PagerDuty Events API v2 integration.
package pagerduty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/tenant/config"
)

// PagerDuty Events API v2 endpoint.
const eventsURL = "https://events.pagerduty.com/v2/enqueue"

// PagerDuty event actions.
const (
	// EventActionTrigger creates a new incident or adds to an existing one.
	EventActionTrigger = "trigger"
	// EventActionAcknowledge acknowledges an existing incident.
	EventActionAcknowledge = "acknowledge"
	// EventActionResolve resolves an existing incident.
	EventActionResolve = "resolve"
)

// Severity levels for PagerDuty alerts.
type Severity string

const (
	// SeverityCritical indicates an urgent issue requiring immediate attention.
	SeverityCritical Severity = "critical"
	// SeverityWarning indicates a potential issue that should be investigated.
	SeverityWarning Severity = "warning"
	// SeverityInfo indicates an informational alert.
	SeverityInfo Severity = "info"
	// SeverityError indicates an error condition (maps to PagerDuty "error" severity).
	SeverityError Severity = "error"
)

// PagerDuty client errors.
var (
	// ErrNotConfigured is returned when attempting to use PagerDuty without configuration.
	ErrNotConfigured = errors.New("PagerDuty client not configured")
	// ErrAPIError is returned when the PagerDuty API returns an error.
	ErrAPIError = errors.New("PagerDuty API error")
	// ErrRateLimited is returned when rate limited by PagerDuty.
	ErrRateLimited = errors.New("PagerDuty rate limited")
	// ErrInvalidRequest is returned when the request is malformed.
	ErrInvalidRequest = errors.New("PagerDuty invalid request")
)

// Event represents the Events API v2 request payload.
type Event struct {
	RoutingKey  string       `json:"routing_key"`
	EventAction string       `json:"event_action"`
	DedupKey    string       `json:"dedup_key,omitempty"`
	Payload     EventPayload `json:"payload"`
}

// EventPayload contains the alert details.
type EventPayload struct {
	Summary       string         `json:"summary"`
	Severity      string         `json:"severity"`
	Source        string         `json:"source"`
	Timestamp     string         `json:"timestamp,omitempty"`
	Component     string         `json:"component,omitempty"`
	Group         string         `json:"group,omitempty"`
	Class         string         `json:"class,omitempty"`
	CustomDetails map[string]any `json:"custom_details,omitempty"`
}

// Response represents the Events API v2 response.
type Response struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	DedupKey string `json:"dedup_key,omitempty"`
}

// Client sends alerts to PagerDuty using Events API v2.
type Client struct {
	config       config.PagerDutyConfig
	httpClient   *http.Client
	eventsURLVal string // Allow override for testing
}

// ClientOption configures the PagerDuty client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client for the PagerDuty client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// WithEventsURL sets a custom events URL for testing.
func WithEventsURL(url string) ClientOption {
	return func(c *Client) {
		c.eventsURLVal = url
	}
}

// NewClient creates a new PagerDuty client.
// Returns nil if PagerDuty is not enabled in the configuration.
func NewClient(cfg config.PagerDutyConfig, opts ...ClientOption) *Client {
	if !cfg.Enabled {
		return nil
	}

	client := &Client{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		eventsURLVal: eventsURL,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// TriggerAlert sends a trigger event to PagerDuty.
// The dedupKey is used for deduplication - alerts with the same key are grouped.
func (c *Client) TriggerAlert(ctx context.Context, summary, dedupKey string, severity Severity, customDetails map[string]any) error {
	if c == nil {
		return ErrNotConfigured
	}

	event := Event{
		RoutingKey:  c.config.RoutingKey,
		EventAction: EventActionTrigger,
		DedupKey:    dedupKey,
		Payload: EventPayload{
			Summary:       summary,
			Severity:      string(severity),
			Source:        c.config.Source,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			CustomDetails: customDetails,
		},
	}

	return c.sendEvent(ctx, event)
}

// ResolveAlert sends a resolve event to PagerDuty.
// The dedupKey must match the original trigger event to resolve the correct incident.
func (c *Client) ResolveAlert(ctx context.Context, dedupKey string) error {
	if c == nil {
		return ErrNotConfigured
	}

	event := Event{
		RoutingKey:  c.config.RoutingKey,
		EventAction: EventActionResolve,
		DedupKey:    dedupKey,
	}

	return c.sendEvent(ctx, event)
}

// sendEvent sends an event to the PagerDuty Events API.
func (c *Client) sendEvent(ctx context.Context, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal PagerDuty event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.eventsURLVal, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create PagerDuty request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PagerDuty event: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read PagerDuty response: %w", err)
	}

	// Parse response
	var pdResp Response
	if err := json.Unmarshal(body, &pdResp); err != nil {
		// Include raw body in error for debugging
		return fmt.Errorf("%w: status %d, body: %s", ErrAPIError, resp.StatusCode, string(body))
	}

	// Handle response status codes
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		// Success
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrInvalidRequest, pdResp.Message)
	case http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return fmt.Errorf("%w: status %d, message: %s", ErrAPIError, resp.StatusCode, pdResp.Message)
	}
}

// MapAlertSeverity converts an alert severity string to PagerDuty severity.
// Supports: CRITICAL, WARNING, INFO (case-insensitive).
// Unknown severities default to "warning".
func MapAlertSeverity(severity string) Severity {
	switch strings.ToUpper(severity) {
	case "CRITICAL":
		return SeverityCritical
	case "WARNING":
		return SeverityWarning
	case "INFO":
		return SeverityInfo
	case "ERROR":
		return SeverityError
	default:
		return SeverityWarning
	}
}

// IsEnabled returns true if the PagerDuty client is configured and enabled.
func (c *Client) IsEnabled() bool {
	return c != nil && c.config.Enabled
}
