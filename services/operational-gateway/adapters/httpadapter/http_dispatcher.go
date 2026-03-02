// Package httpadapter provides an HTTP implementation of the Dispatcher port for
// the operational gateway. It handles outbound HTTP requests to external providers,
// including authentication, payload transformation, and response parsing.
package httpadapter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

const (
	// defaultTimeout is the default HTTP request timeout.
	defaultTimeout = 30 * time.Second

	// maxResponseBodyBytes is the maximum number of bytes read from a provider response body.
	// Responses larger than this are truncated to prevent memory exhaustion.
	maxResponseBodyBytes = 1 << 20 // 1 MiB

	// oauth2GrantType is the client credentials grant type for OAuth 2.0 token requests.
	oauth2GrantType = "client_credentials"
)

// Sentinel errors for the httpadapter package.
var (
	// ErrMTLSNotSupported is returned when MTLSAuth is used with the standard HTTPDispatcher.
	// Use NewMTLSHTTPDispatcher to create a dispatcher with a dedicated mTLS transport.
	ErrMTLSNotSupported = errors.New("mtls auth requires a dedicated per-connection client: use NewMTLSHTTPDispatcher")

	// ErrUnsupportedAuthType is returned when an unknown AuthConfig implementation is encountered.
	ErrUnsupportedAuthType = errors.New("unsupported auth type")

	// ErrTokenEndpointFailed is returned when the OAuth 2.0 token endpoint returns a non-200 response.
	ErrTokenEndpointFailed = errors.New("token endpoint returned non-200 response")

	// ErrTokenNotFound is returned when the token endpoint response does not contain an access_token field.
	ErrTokenNotFound = errors.New("access_token not found in token response")

	// ErrMalformedToken is returned when the access_token field in the token response is malformed.
	ErrMalformedToken = errors.New("malformed access_token in token response")

	// ErrUnsupportedHMACAlgorithm is returned when an HMAC algorithm other than sha256 or sha512 is requested.
	ErrUnsupportedHMACAlgorithm = errors.New("unsupported hmac algorithm: supported algorithms are sha256, sha512")

	// ErrInvalidCACert is returned when the CA certificate PEM cannot be parsed.
	ErrInvalidCACert = errors.New("failed to parse CA certificate PEM")
)

// HTTPDispatcher sends instructions to external providers over HTTPS.
// It implements ports.Dispatcher for HTTP-based provider connections.
//
// Authentication credentials are resolved at dispatch time via the SecretStore,
// so raw secret values are never held in memory beyond the scope of a single Dispatch call.
//
// HTTPDispatcher is safe for concurrent use.
type HTTPDispatcher struct {
	client      *http.Client
	secretStore ports.SecretStore
	transformer ports.PayloadTransformer
	logger      *slog.Logger
	// skipAppLevelAuth skips applyAuth when the dispatcher uses mTLS transport-level
	// authentication instead of application-level credentials. Set by NewMTLSHTTPDispatcher.
	skipAppLevelAuth bool
}

// NewHTTPDispatcher creates a new HTTPDispatcher with connection pooling configured for
// high-throughput dispatch workloads.
//
// The underlying http.Client uses:
//   - MaxIdleConnsPerHost: 100 (reduces TCP handshake overhead for bursty traffic)
//   - IdleConnTimeout: 90s (keeps connections warm without holding them indefinitely)
//   - Default timeout: 30s (overridable per-request via context deadline)
func NewHTTPDispatcher(secretStore ports.SecretStore, transformer ports.PayloadTransformer, logger *slog.Logger) *HTTPDispatcher {
	if secretStore == nil {
		panic("httpadapter.NewHTTPDispatcher: secretStore must not be nil")
	}
	if transformer == nil {
		panic("httpadapter.NewHTTPDispatcher: transformer must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	transport := &http.Transport{
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	client := &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}

	return &HTTPDispatcher{
		client:      client,
		secretStore: secretStore,
		transformer: transformer,
		logger:      logger,
	}
}

// Dispatch sends the instruction to the provider and returns a DispatchResult.
//
// Flow:
//  1. Transform the instruction payload outbound via the PayloadTransformer.
//  2. Build the target URL from conn.BaseURL + route.PathTemplate.
//  3. Create an HTTP request with context.
//  4. Apply authentication credentials from conn.AuthConfig.
//  5. Set headers: Content-Type, X-Request-ID, route static headers, transform headers.
//  6. Execute the request.
//  7. Read and limit the response body.
//  8. Transform the response inbound to produce an InstructionOutcome.
//
// Dispatch does not retry; that is the responsibility of the dispatch worker.
func (d *HTTPDispatcher) Dispatch(
	ctx context.Context,
	instruction *domain.Instruction,
	conn *domain.ProviderConnection,
	route *ports.InstructionRoute,
) ports.DispatchResult {
	start := time.Now()

	body, transformHeaders, err := d.transformer.TransformOutbound(ctx, instruction, route)
	if err != nil {
		return ports.DispatchResult{
			Duration: time.Since(start),
			Error:    fmt.Errorf("outbound transform: %w", err),
		}
	}

	targetURL := buildURL(conn.BaseURL, route.PathTemplate)

	req, err := http.NewRequestWithContext(ctx, route.HTTPMethod, targetURL, bytes.NewReader(body))
	if err != nil {
		return ports.DispatchResult{
			Duration: time.Since(start),
			Error:    fmt.Errorf("building request: %w", err),
		}
	}

	// Set standard headers first so that auth headers applied below take precedence
	// and cannot be overwritten by route or transform headers.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", uuid.New().String())

	// Route-level static headers (from mapping definition).
	for k, v := range transformHeaders {
		req.Header.Set(k, v)
	}

	// Route-level static headers defined on the InstructionRoute.
	for k, v := range route.Headers {
		req.Header.Set(k, v)
	}

	// Apply transport-level authentication last so that auth headers cannot be
	// overridden by route or transform headers set above.
	// Skipped for mTLS dispatchers where authentication is handled by the TLS handshake.
	if !d.skipAppLevelAuth {
		if err := d.applyAuth(ctx, req, body, instruction.TenantID.String(), conn.AuthConfig); err != nil {
			return ports.DispatchResult{
				Duration: time.Since(start),
				Error:    fmt.Errorf("applying auth: %w", err),
			}
		}
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return ports.DispatchResult{
			Duration: time.Since(start),
			Error:    fmt.Errorf("executing request: %w", err),
		}
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			d.logger.WarnContext(ctx, "failed to close response body",
				"error", closeErr,
				"instruction_id", instruction.ID.String(),
			)
		}
	}()

	limited := io.LimitReader(resp.Body, maxResponseBodyBytes)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		return ports.DispatchResult{
			StatusCode: resp.StatusCode,
			Duration:   time.Since(start),
			Error:      fmt.Errorf("reading response body: %w", err),
		}
	}

	outcome, err := d.transformer.TransformInbound(ctx, resp.StatusCode, respBody, route)
	if err != nil {
		return ports.DispatchResult{
			StatusCode:   resp.StatusCode,
			ResponseBody: respBody,
			Duration:     time.Since(start),
			Error:        fmt.Errorf("inbound transform: %w", err),
		}
	}

	var providerStatus string
	if outcome != nil {
		providerStatus = outcome.ProviderStatus
	}
	d.logger.DebugContext(ctx, "dispatch completed",
		"instruction_id", instruction.ID.String(),
		"status_code", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
		"provider_status", providerStatus,
	)

	return ports.DispatchResult{
		StatusCode:   resp.StatusCode,
		ResponseBody: respBody,
		Outcome:      outcome,
		Duration:     time.Since(start),
	}
}

// applyAuth applies the provider connection's authentication configuration to the
// outgoing HTTP request. Secrets are resolved via the SecretStore at dispatch time.
//
// Supported auth types: APIKeyAuth, BasicAuth, OAuth2Auth, HMACAuth, MTLSAuth.
// MTLSAuth requires modifying the HTTP client's TLS config; this is handled by
// returning an error so the caller can use a dedicated per-connection client.
func (d *HTTPDispatcher) applyAuth(ctx context.Context, req *http.Request, body []byte, tenantID string, authConfig domain.AuthConfig) error {
	switch auth := authConfig.(type) {

	case *domain.APIKeyAuth:
		secret, err := d.secretStore.Resolve(ctx, tenantID, auth.SecretRef)
		if err != nil {
			return fmt.Errorf("resolving api key secret %q: %w", auth.SecretRef, err)
		}
		req.Header.Set(auth.HeaderName, secret)

	case *domain.BasicAuth:
		password, err := d.secretStore.Resolve(ctx, tenantID, auth.PasswordRef)
		if err != nil {
			return fmt.Errorf("resolving basic auth password %q: %w", auth.PasswordRef, err)
		}
		req.SetBasicAuth(auth.Username, password)

	case *domain.OAuth2Auth:
		token, err := d.fetchOAuth2Token(ctx, tenantID, auth)
		if err != nil {
			return fmt.Errorf("fetching oauth2 token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

	case *domain.HMACAuth:
		secret, err := d.secretStore.Resolve(ctx, tenantID, auth.SecretRef)
		if err != nil {
			return fmt.Errorf("resolving hmac secret %q: %w", auth.SecretRef, err)
		}
		signature, err := computeHMAC(auth.Algorithm, []byte(secret), body)
		if err != nil {
			return fmt.Errorf("computing hmac signature: %w", err)
		}
		req.Header.Set(auth.SignatureHeader, signature)

	case *domain.MTLSAuth:
		return ErrMTLSNotSupported

	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedAuthType, authConfig)
	}
	return nil
}

// fetchOAuth2Token performs a client credentials grant to obtain a bearer token
// from the provider's token endpoint. The client secret is resolved via the SecretStore.
//
// This is a synchronous, non-cached implementation suitable for Phase 1.
// Production deployments should add token caching with TTL to avoid round-trips on every dispatch.
func (d *HTTPDispatcher) fetchOAuth2Token(ctx context.Context, tenantID string, auth *domain.OAuth2Auth) (string, error) {
	clientSecret, err := d.secretStore.Resolve(ctx, tenantID, auth.ClientSecretRef)
	if err != nil {
		return "", fmt.Errorf("resolving oauth2 client secret %q: %w", auth.ClientSecretRef, err)
	}

	formData := strings.NewReader(buildOAuth2FormBody(auth.ClientID, clientSecret, auth.Scopes))

	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, auth.TokenURL, formData)
	if err != nil {
		return "", fmt.Errorf("building token request: %w", err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := d.client.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("executing token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, maxResponseBodyBytes)
	tokenBody, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: HTTP %d: %s", ErrTokenEndpointFailed, resp.StatusCode, tokenBody)
	}

	return extractBearerToken(tokenBody)
}

// buildURL concatenates a base URL and a path template, ensuring no double slashes.
func buildURL(baseURL, pathTemplate string) string {
	base := strings.TrimRight(baseURL, "/")
	path := strings.TrimLeft(pathTemplate, "/")
	if path == "" {
		return base
	}
	return base + "/" + path
}

// buildOAuth2FormBody constructs the application/x-www-form-urlencoded body for a
// client credentials token request. All values are URL-encoded to handle special
// characters in client IDs, secrets, and scope strings.
func buildOAuth2FormBody(clientID, clientSecret string, scopes []string) string {
	values := url.Values{
		"grant_type":    {oauth2GrantType},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	if len(scopes) > 0 {
		values.Set("scope", strings.Join(scopes, " "))
	}
	return values.Encode()
}

// extractBearerToken parses an OAuth 2.0 token response JSON and returns the access_token.
// Uses a minimal string search to avoid a full JSON unmarshal for a single field.
func extractBearerToken(body []byte) (string, error) {
	// Simple extraction: find "access_token":"<value>" in JSON.
	s := string(body)
	const marker = `"access_token"`
	idx := strings.Index(s, marker)
	if idx < 0 {
		return "", ErrTokenNotFound
	}
	rest := s[idx+len(marker):]
	// rest looks like: :"<value>",... or : "<value>",...
	rest = strings.TrimLeft(rest, " \t\r\n:")
	if len(rest) == 0 || rest[0] != '"' {
		return "", ErrMalformedToken
	}
	end := strings.Index(rest[1:], `"`)
	if end < 0 {
		return "", ErrMalformedToken
	}
	token := rest[1 : end+1]
	if token == "" {
		return "", ErrMalformedToken
	}
	return token, nil
}

// computeHMAC computes an HMAC signature over body using the given algorithm and secret key.
// Supported algorithms: "sha256" (default), "sha512".
// Returns the hex-encoded signature.
func computeHMAC(algorithm string, key, body []byte) (string, error) {
	var h hash.Hash
	switch strings.ToLower(algorithm) {
	case "sha256", "":
		h = hmac.New(sha256.New, key)
	case "sha512":
		h = hmac.New(sha512.New, key)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedHMACAlgorithm, algorithm)
	}
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// NewMTLSHTTPDispatcher creates an HTTPDispatcher configured with mutual TLS using the
// provided certificate and key PEM blocks. The CA certificate is optional; when empty,
// the system CA pool is used.
//
// This constructor exists as a separate entry point because mTLS requires modifying the
// TLS configuration of the underlying http.Transport, which cannot be done per-request.
// Callers should create one MTLSHTTPDispatcher per distinct client certificate identity.
//
// The returned dispatcher skips application-level auth (applyAuth) because authentication
// is handled by the TLS handshake itself.
func NewMTLSHTTPDispatcher(
	secretStore ports.SecretStore,
	transformer ports.PayloadTransformer,
	logger *slog.Logger,
	clientCertPEM, clientKeyPEM, caCertPEM []byte,
) (*HTTPDispatcher, error) {
	if secretStore == nil {
		panic("httpadapter.NewMTLSHTTPDispatcher: secretStore must not be nil")
	}
	if transformer == nil {
		panic("httpadapter.NewMTLSHTTPDispatcher: transformer must not be nil")
	}

	cert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading client certificate: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if len(caCertPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCertPEM) {
			return nil, ErrInvalidCACert
		}
		tlsCfg.RootCAs = pool
	}

	transport := &http.Transport{
		TLSClientConfig:     tlsCfg,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	client := &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &HTTPDispatcher{
		client:           client,
		secretStore:      secretStore,
		transformer:      transformer,
		logger:           logger,
		skipAppLevelAuth: true,
	}, nil
}
