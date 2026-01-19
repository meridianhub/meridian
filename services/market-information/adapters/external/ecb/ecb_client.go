// Package ecb provides an HTTP client for fetching daily FX rates from the European Central Bank.
package ecb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// DefaultEndpoint is the ECB SDMX Web Service endpoint for daily FX rates.
// D=daily, EUR=quote currency, SP00=spot, A=average.
const DefaultEndpoint = "https://sdw-wsrest.ecb.europa.eu/service/data/EXR/D..EUR.SP00.A"

// Default timeouts.
const (
	DefaultTimeout = 30 * time.Second
)

// Sentinel errors for specific conditions.
var (
	// ErrNotConfigured is returned when attempting to use an ECB client that is nil.
	ErrNotConfigured = errors.New("ECB client not configured")
	// ErrAPIError is returned when the ECB API returns a non-successful response.
	ErrAPIError = errors.New("ECB API error")
	// ErrRateLimited is returned when rate limited by the ECB API.
	ErrRateLimited = errors.New("ECB API rate limited")
	// ErrPartialIngestion is returned when some observations fail to record.
	ErrPartialIngestion = errors.New("partial ingestion failure")
)

// Config holds ECB client configuration.
type Config struct {
	// Endpoint is the ECB SDMX Web Service URL.
	// Defaults to DefaultEndpoint if empty.
	Endpoint string

	// Timeout is the HTTP request timeout.
	// Defaults to DefaultTimeout (30s) if zero.
	Timeout time.Duration
}

// Client fetches daily FX rates from the European Central Bank.
type Client struct {
	httpClient *http.Client
	endpoint   string
	logger     *slog.Logger
}

// ClientOption configures the ECB client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client for the ECB client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// WithEndpoint sets a custom endpoint URL for the ECB client.
func WithEndpoint(endpoint string) ClientOption {
	return func(c *Client) {
		c.endpoint = endpoint
	}
}

// WithLogger sets a custom logger for the ECB client.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

// NewClient creates a new ECB client.
func NewClient(cfg Config, opts ...ClientOption) *Client {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	client := &Client{
		httpClient: &http.Client{Timeout: timeout},
		endpoint:   endpoint,
		logger:     slog.Default(),
	}

	for _, opt := range opts {
		opt(client)
	}

	client.logger = client.logger.With("component", "ecb_client")

	return client
}

// FetchDailyRates retrieves the latest FX rates from ECB.
// Returns raw CSV data for parsing. Caller must close the returned ReadCloser.
func (c *Client) FetchDailyRates(ctx context.Context) (io.ReadCloser, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ECB request: %w", err)
	}

	// Request CSV format instead of default XML
	req.Header.Set("Accept", "text/csv")

	c.logger.DebugContext(ctx, "fetching ECB daily rates", "endpoint", c.endpoint)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ECB rates: %w", err)
	}

	// Handle HTTP status codes
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusTooManyRequests:
		_ = resp.Body.Close()
		return nil, ErrRateLimited
	default:
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: status %d, body: %s", ErrAPIError, resp.StatusCode, string(body))
	}
}
