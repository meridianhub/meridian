// Package oidc provides a thin HTTP client for communicating with an external
// OIDC provider (e.g. Dex running as a sidecar container). It replaces the
// former embedded Dex server with simple HTTP calls for discovery and health.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	// ErrBaseURLRequired is returned when Config.BaseURL is empty.
	ErrBaseURLRequired = errors.New("oidc: BaseURL is required")

	// ErrUnexpectedStatus is returned when the provider returns a non-200 status.
	ErrUnexpectedStatus = errors.New("oidc: unexpected status")
)

// Config holds settings for the OIDC client.
type Config struct {
	// BaseURL is the issuer URL of the OIDC provider (e.g. "http://dex:5556/dex").
	BaseURL string

	// Timeout for HTTP requests. Defaults to 5s if zero.
	Timeout time.Duration
}

// Client is a thin HTTP client for an external OIDC provider.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new OIDC client from the given config.
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, ErrBaseURLRequired
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	return &Client{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// WellKnownConfig represents the OpenID Connect discovery document.
type WellKnownConfig struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

// Discovery fetches the OIDC well-known configuration from the provider.
func (c *Client) Discovery(ctx context.Context) (*WellKnownConfig, error) {
	url := c.baseURL + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery: %w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	var cfg WellKnownConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	return &cfg, nil
}

// HealthCheck verifies the OIDC provider is reachable by hitting its /healthz endpoint.
func (c *Client) HealthCheck(ctx context.Context) error {
	url := c.baseURL + "/healthz"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("oidc health: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("oidc health: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc health: %w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	return nil
}
