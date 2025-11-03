package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	// ErrOAuthProviderNil is returned when a nil OAuth provider is provided
	ErrOAuthProviderNil = errors.New("OAuth provider cannot be nil")
	// ErrOAuthHTTPError is returned when OAuth endpoint returns non-200 status
	ErrOAuthHTTPError = errors.New("OAuth endpoint returned error")
	// ErrInvalidOAuthResponse is returned when OAuth response is invalid
	ErrInvalidOAuthResponse = errors.New("invalid OAuth response")
	// ErrIntrospectionURLEmpty is returned when introspection URL is empty
	ErrIntrospectionURLEmpty = errors.New("introspection URL cannot be empty")
	// ErrTokenEmpty is returned when token is empty
	ErrTokenEmpty = errors.New("token cannot be empty")
)

// TokenResponse represents the OAuth 2.0 token response
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope,omitempty"`
}

// OAuth2Config holds configuration for OAuth 2.0 client
type OAuth2Config struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
	Scopes       []string
	Client       *http.Client
}

// OAuth2Client manages OAuth 2.0 client credentials flow with token caching
type OAuth2Client struct {
	config OAuth2Config

	mu          sync.RWMutex
	cachedToken string
	expiresAt   time.Time
}

// NewOAuth2Client creates a new OAuth 2.0 client with the specified configuration
func NewOAuth2Client(config *OAuth2Config) (*OAuth2Client, error) {
	if config == nil {
		return nil, fmt.Errorf("failed to create OAuth2 client: %w", ErrOAuthProviderNil)
	}

	if config.Client == nil {
		config.Client = http.DefaultClient
	}

	return &OAuth2Client{
		config: *config,
	}, nil
}

// GetToken retrieves an access token using client credentials flow.
// Tokens are cached and automatically refreshed when expired.
func (c *OAuth2Client) GetToken(ctx context.Context) (string, error) {
	// Check if cached token is still valid
	c.mu.RLock()
	if c.cachedToken != "" && time.Now().Before(c.expiresAt) {
		token := c.cachedToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	// Fetch new token
	tokenResp, err := c.fetchToken(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OAuth token: %w", err)
	}

	// Cache token with expiry
	c.mu.Lock()
	c.cachedToken = tokenResp.AccessToken
	// Refresh token shortly before the real expiry without going negative for short-lived tokens
	now := time.Now()
	expiresIn := time.Duration(tokenResp.ExpiresIn) * time.Second
	const refreshLead = 30 * time.Second
	switch {
	case expiresIn <= 0:
		c.expiresAt = now
	case expiresIn > refreshLead:
		c.expiresAt = now.Add(expiresIn - refreshLead)
	default:
		// For very short-lived tokens, refresh halfway through their lifetime
		c.expiresAt = now.Add(expiresIn / 2)
	}
	c.mu.Unlock()

	return tokenResp.AccessToken, nil
}

// fetchToken performs the actual token request using client credentials
func (c *OAuth2Client) fetchToken(ctx context.Context) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", c.config.ClientID)
	data.Set("client_secret", c.config.ClientSecret)

	if len(c.config.Scopes) > 0 {
		data.Set("scope", strings.Join(c.config.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.config.TokenURL,
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.config.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send token request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: status %d: %s", ErrOAuthHTTPError, resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w: %w", err, ErrInvalidOAuthResponse)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access token in response: %w", ErrInvalidOAuthResponse)
	}

	return &tokenResp, nil
}

// ClearCache clears the cached token, forcing a refresh on next GetToken call
func (c *OAuth2Client) ClearCache() {
	c.mu.Lock()
	c.cachedToken = ""
	c.expiresAt = time.Time{}
	c.mu.Unlock()
}

// TokenIntrospection represents the response from a token introspection endpoint
type TokenIntrospection struct {
	Active    bool     `json:"active"`
	ClientID  string   `json:"client_id,omitempty"`
	Username  string   `json:"username,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	ExpiresAt int64    `json:"exp,omitempty"`
	Audience  []string `json:"aud,omitempty"`
}

// OAuth2Introspector validates tokens using OAuth 2.0 token introspection (RFC 7662)
type OAuth2Introspector struct {
	introspectionURL string
	clientID         string
	clientSecret     string
	client           *http.Client
}

// NewOAuth2Introspector creates a new OAuth 2.0 token introspector
func NewOAuth2Introspector(introspectionURL, clientID, clientSecret string, client *http.Client) (*OAuth2Introspector, error) {
	if introspectionURL == "" {
		return nil, fmt.Errorf("failed to create introspector: %w", ErrIntrospectionURLEmpty)
	}

	if client == nil {
		client = http.DefaultClient
	}

	return &OAuth2Introspector{
		introspectionURL: introspectionURL,
		clientID:         clientID,
		clientSecret:     clientSecret,
		client:           client,
	}, nil
}

// IntrospectToken validates a token using the introspection endpoint
func (i *OAuth2Introspector) IntrospectToken(ctx context.Context, token string) (*TokenIntrospection, error) {
	if token == "" {
		return nil, fmt.Errorf("failed to introspect token: %w", ErrTokenEmpty)
	}

	data := url.Values{}
	data.Set("token", token)
	data.Set("client_id", i.clientID)
	data.Set("client_secret", i.clientSecret)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		i.introspectionURL,
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create introspection request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send introspection request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: status %d: %s", ErrOAuthHTTPError, resp.StatusCode, string(body))
	}

	var introspection TokenIntrospection
	if err := json.NewDecoder(resp.Body).Decode(&introspection); err != nil {
		return nil, fmt.Errorf("failed to decode introspection response: %w: %w", err, ErrInvalidOAuthResponse)
	}

	return &introspection, nil
}
