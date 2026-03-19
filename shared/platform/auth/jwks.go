// Package auth provides JWT authentication and OAuth 2.0 integration.
// It includes JWT token validation, JWKS-based public key rotation,
// and integration with identity providers like Auth0 and Okta.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrJWKSURLEmpty is returned when an empty JWKS URL is provided
	ErrJWKSURLEmpty = errors.New("JWKS URL cannot be empty")
	// ErrKeyNotFound is returned when a key with the specified ID is not found
	ErrKeyNotFound = errors.New("key not found")
	// ErrInvalidKeyType is returned when the key type is not RSA
	ErrInvalidKeyType = errors.New("invalid key type, only RSA is supported")
	// ErrHTTPClientNil is returned when a nil HTTP client is provided
	ErrHTTPClientNil = errors.New("HTTP client cannot be nil")
	// ErrJWKSProviderNil is returned when a nil JWKS provider is provided
	ErrJWKSProviderNil = errors.New("JWKS provider cannot be nil")
	// ErrTokenMissingKid is returned when token header is missing kid claim
	ErrTokenMissingKid = errors.New("token header missing 'kid' claim")
	// ErrJWKSHTTPError is returned when JWKS endpoint returns non-200 status
	ErrJWKSHTTPError = errors.New("JWKS endpoint returned error")
)

// JWK represents a JSON Web Key as defined in RFC 7517
type JWK struct {
	Kid string `json:"kid"` // Key ID
	Kty string `json:"kty"` // Key Type (e.g., "RSA")
	Use string `json:"use"` // Public Key Use (e.g., "sig")
	N   string `json:"n"`   // Modulus for RSA key
	E   string `json:"e"`   // Exponent for RSA key
	Alg string `json:"alg"` // Algorithm (e.g., "RS256")
}

// JWKS represents a JSON Web Key Set
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWKSProviderConfig holds configuration for the JWKS provider
type JWKSProviderConfig struct {
	URL        string        // JWKS endpoint URL
	Client     *http.Client  // HTTP client for fetching JWKS
	CacheTTL   time.Duration // Cache time-to-live
	RefreshTTL time.Duration // Automatic refresh interval
}

// JWKSProvider manages public key retrieval and rotation from a JWKS endpoint.
// It provides thread-safe caching and automatic key refresh.
type JWKSProvider struct {
	url        string
	client     *http.Client
	cacheTTL   time.Duration
	refreshTTL time.Duration

	mu              sync.RWMutex
	keys            map[string]*rsa.PublicKey
	lastFetch       time.Time
	stopRefresh     chan struct{}
	closeOnce       sync.Once
	autoRefreshOnce sync.Once
}

// NewJWKSProvider creates a new JWKS provider with the specified configuration.
// It validates the configuration and defers the initial JWKS fetch to the first
// GetKey call, enabling the provider to be created before the JWKS endpoint is
// available (e.g. when the OIDC server is embedded in the same process).
//
// Automatic key refresh starts after the first successful fetch.
func NewJWKSProvider(_ context.Context, cfg *JWKSProviderConfig) (*JWKSProvider, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("failed to create JWKS provider: %w", ErrJWKSURLEmpty)
	}

	if cfg.Client == nil {
		return nil, fmt.Errorf("failed to create JWKS provider: %w", ErrHTTPClientNil)
	}

	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 24 * time.Hour // Default 24 hours
	}

	// Validate that refresh interval is reasonable relative to cache TTL
	if cfg.RefreshTTL > 0 && cfg.RefreshTTL < cfg.CacheTTL/2 {
		// Refresh more frequently than half the cache TTL could cause thrashing
		cfg.RefreshTTL = cfg.CacheTTL / 2
	}

	provider := &JWKSProvider{
		url:         cfg.URL,
		client:      cfg.Client,
		cacheTTL:    cfg.CacheTTL,
		refreshTTL:  cfg.RefreshTTL,
		keys:        make(map[string]*rsa.PublicKey),
		stopRefresh: make(chan struct{}),
	}

	// No initial fetch — keys are fetched lazily on first GetKey call.
	// This avoids a startup race when the JWKS endpoint is served by the
	// same process (embedded Dex OIDC).

	return provider, nil
}

// GetKey retrieves a public key by its key ID (kid).
// If the key is not in cache or cache is expired, it attempts to refresh from the JWKS endpoint.
func (p *JWKSProvider) GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	// Try to get from cache first
	p.mu.RLock()
	key, found := p.keys[kid]
	cacheExpired := time.Since(p.lastFetch) > p.cacheTTL
	p.mu.RUnlock()

	if found && !cacheExpired {
		return key, nil
	}

	// Cache miss or expired - refresh
	if err := p.refresh(ctx); err != nil {
		// If refresh fails but we have a cached key, return it anyway
		if found {
			return key, nil
		}
		return nil, fmt.Errorf("failed to refresh JWKS: %w", err)
	}

	// Start auto-refresh goroutine after first successful fetch
	if p.refreshTTL > 0 {
		refreshCtx := ctx
		p.autoRefreshOnce.Do(func() {
			go p.autoRefresh(refreshCtx)
		})
	}

	// Try again after refresh
	p.mu.RLock()
	key, found = p.keys[kid]
	p.mu.RUnlock()

	if !found {
		return nil, fmt.Errorf("key %s: %w", kid, ErrKeyNotFound)
	}

	return key, nil
}

// refresh fetches the latest JWKS from the endpoint and updates the cache
func (p *JWKSProvider) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrJWKSHTTPError, resp.StatusCode)
	}

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS response: %w", err)
	}

	// Parse and store keys
	newKeys := make(map[string]*rsa.PublicKey)
	for _, jwk := range jwks.Keys {
		if jwk.Kty != "RSA" {
			continue // Skip non-RSA keys
		}

		publicKey, err := parseRSAPublicKey(jwk)
		if err != nil {
			// Log error but continue with other keys
			continue
		}

		newKeys[jwk.Kid] = publicKey
	}

	// Update cache atomically
	p.mu.Lock()
	p.keys = newKeys
	p.lastFetch = time.Now()
	p.mu.Unlock()

	return nil
}

// autoRefresh periodically refreshes the JWKS cache
func (p *JWKSProvider) autoRefresh(ctx context.Context) {
	ticker := time.NewTicker(p.refreshTTL)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Use context derived from parent to maintain proper cancellation chain
			refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			// Ignore errors during automatic refresh - the cache will continue to serve old keys
			_ = p.refresh(refreshCtx)
			cancel()
		case <-p.stopRefresh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Close stops the automatic refresh goroutine.
// This method is idempotent and safe to call multiple times.
func (p *JWKSProvider) Close() error {
	p.closeOnce.Do(func() {
		close(p.stopRefresh)
	})
	return nil
}

// parseRSAPublicKey converts a JWK to an RSA public key
func parseRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	if jwk.Kty != "RSA" {
		return nil, fmt.Errorf("failed to parse key %s: %w", jwk.Kid, ErrInvalidKeyType)
	}

	// Decode modulus (n)
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode modulus: %w", err)
	}

	// Decode exponent (e)
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exponent: %w", err)
	}

	// Convert bytes to big.Int for modulus
	n := new(big.Int).SetBytes(nBytes)

	// Convert exponent bytes to int
	var e int
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}

	return &rsa.PublicKey{
		N: n,
		E: e,
	}, nil
}

// JWTValidatorWithJWKS extends JWTValidator to support JWKS-based key rotation
type JWTValidatorWithJWKS struct {
	provider *JWKSProvider
}

// NewJWTValidatorWithJWKS creates a JWT validator that uses JWKS for public key retrieval
func NewJWTValidatorWithJWKS(provider *JWKSProvider) (*JWTValidatorWithJWKS, error) {
	if provider == nil {
		return nil, fmt.Errorf("failed to create validator: %w", ErrJWKSProviderNil)
	}

	return &JWTValidatorWithJWKS{
		provider: provider,
	}, nil
}

// ValidateToken validates a JWT token using keys from the JWKS provider.
// It extracts the key ID (kid) from the token header and retrieves the corresponding public key.
func (v *JWTValidatorWithJWKS) ValidateToken(ctx context.Context, tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("failed to validate token: %w", ErrTokenStringEmpty)
	}

	// Parse token without validation to extract kid from header
	parser := &jwt.Parser{}
	token, _, err := parser.ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token header: %w", err)
	}

	// Get key ID from header
	kid, ok := token.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, fmt.Errorf("failed to extract kid: %w", ErrTokenMissingKid)
	}

	// Get public key from JWKS provider
	publicKey, err := v.provider.GetKey(ctx, kid)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key for kid %s: %w", kid, err)
	}

	// Create validator with the retrieved public key
	validator, err := NewJWTValidator(publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create validator: %w", err)
	}

	// Validate token
	return validator.ValidateToken(tokenString)
}

// Close releases resources held by the validator
func (v *JWTValidatorWithJWKS) Close() error {
	return v.provider.Close()
}
