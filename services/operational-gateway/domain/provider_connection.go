// Package domain contains the operational-gateway domain model.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Protocol represents the communication protocol used to connect to a provider.
type Protocol string

const (
	// ProtocolHTTPS is the HTTPS REST protocol.
	ProtocolHTTPS Protocol = "HTTPS"
	// ProtocolGRPC is the gRPC protocol.
	ProtocolGRPC Protocol = "GRPC"
	// ProtocolWebhook is the outbound webhook protocol.
	ProtocolWebhook Protocol = "WEBHOOK"
	// ProtocolMQTT is the MQTT messaging protocol.
	ProtocolMQTT Protocol = "MQTT"
	// ProtocolAMQP is the AMQP messaging protocol.
	ProtocolAMQP Protocol = "AMQP"
)

// CircuitState represents the current state of the circuit breaker.
type CircuitState string

const (
	// CircuitStateClosed means the circuit is closed and requests flow normally.
	CircuitStateClosed CircuitState = "CLOSED"
	// CircuitStateOpen means the circuit is open and requests are blocked.
	CircuitStateOpen CircuitState = "OPEN"
	// CircuitStateHalfOpen means the circuit is allowing a probe request to test recovery.
	CircuitStateHalfOpen CircuitState = "HALF_OPEN"
)

// HealthStatus represents the observed health of a provider connection.
type HealthStatus string

const (
	// HealthStatusUnknown means no health check has been performed yet.
	HealthStatusUnknown HealthStatus = "UNKNOWN"
	// HealthStatusHealthy means the provider is responding normally.
	HealthStatusHealthy HealthStatus = "HEALTHY"
	// HealthStatusDegraded means the provider is responding but with elevated latency or errors.
	HealthStatusDegraded HealthStatus = "DEGRADED"
	// HealthStatusUnhealthy means the provider is not responding or returning errors.
	HealthStatusUnhealthy HealthStatus = "UNHEALTHY"
)

// Sentinel errors for domain validation.
var (
	// ErrTenantIDRequired is returned when the tenant ID is empty.
	ErrTenantIDRequired = errors.New("tenant ID is required")
	// ErrProviderNameRequired is returned when the provider name is empty.
	ErrProviderNameRequired = errors.New("provider name is required")
	// ErrBaseURLRequired is returned when the base URL is empty.
	ErrBaseURLRequired = errors.New("base URL is required")
	// ErrAuthConfigRequired is returned when the auth config is nil.
	ErrAuthConfigRequired = errors.New("auth config is required")
)

// AuthConfig is the interface implemented by all authentication configuration types.
// Implementations store secret references, not raw secret values. The actual secrets
// are resolved at dispatch time via the SecretStore port.
type AuthConfig interface {
	// AuthType returns a string identifying the authentication mechanism.
	AuthType() string
}

// APIKeyAuth authenticates using a static API key passed in a request header.
// SecretRef is a reference resolved at dispatch time via SecretStore.
type APIKeyAuth struct {
	// HeaderName is the HTTP header name to use (e.g., "X-API-Key").
	HeaderName string
	// SecretRef is the reference to the secret containing the API key value.
	SecretRef string
}

// AuthType implements AuthConfig.
func (a *APIKeyAuth) AuthType() string { return "api_key" }

// BasicAuth authenticates using HTTP Basic authentication.
// UsernameRef and PasswordRef are references resolved at dispatch time via SecretStore.
type BasicAuth struct {
	// UsernameRef is the reference to the secret containing the username.
	UsernameRef string
	// PasswordRef is the reference to the secret containing the password.
	PasswordRef string
}

// AuthType implements AuthConfig.
func (a *BasicAuth) AuthType() string { return "basic" }

// OAuth2Auth authenticates using the OAuth 2.0 client credentials flow.
// ClientIDRef and ClientSecretRef are references resolved at dispatch time via SecretStore.
type OAuth2Auth struct {
	// TokenURL is the token endpoint URL.
	TokenURL string
	// ClientIDRef is the reference to the secret containing the OAuth client ID.
	ClientIDRef string
	// ClientSecretRef is the reference to the secret containing the OAuth client secret.
	ClientSecretRef string
	// Scopes is the list of OAuth scopes to request.
	Scopes []string
}

// AuthType implements AuthConfig.
func (a *OAuth2Auth) AuthType() string { return "oauth2" }

// HMACAuth authenticates by signing request payloads with HMAC.
// SecretRef is a reference resolved at dispatch time via SecretStore.
type HMACAuth struct {
	// SecretRef is the reference to the HMAC signing secret.
	SecretRef string
	// Algorithm is the HMAC algorithm to use (e.g., "SHA256", "SHA512").
	Algorithm string
	// HeaderName is the HTTP header used to send the HMAC signature.
	HeaderName string
}

// AuthType implements AuthConfig.
func (a *HMACAuth) AuthType() string { return "hmac" }

// MTLSAuth authenticates using mutual TLS with a client certificate.
// CertRef and KeyRef are references resolved at dispatch time via SecretStore.
type MTLSAuth struct {
	// CertRef is the reference to the secret containing the PEM-encoded client certificate.
	CertRef string
	// KeyRef is the reference to the secret containing the PEM-encoded client private key.
	KeyRef string
}

// AuthType implements AuthConfig.
func (a *MTLSAuth) AuthType() string { return "mtls" }

// RetryPolicy defines how failed requests to a provider should be retried.
type RetryPolicy struct {
	// MaxAttempts is the maximum number of request attempts (including the initial attempt).
	MaxAttempts int
	// InitialBackoff is the wait duration before the first retry.
	InitialBackoff time.Duration
	// MaxBackoff is the maximum wait duration between retries.
	MaxBackoff time.Duration
	// BackoffMultiplier is the factor by which the backoff duration grows per retry.
	BackoffMultiplier time.Duration
}

// RateLimitConfig defines the rate limiting policy for outbound requests to a provider.
type RateLimitConfig struct {
	// RequestsPerSecond is the maximum number of requests allowed per second.
	RequestsPerSecond float64
	// BurstSize is the maximum number of requests allowed in a burst above the steady-state rate.
	BurstSize int
}

// ProviderConnection is the aggregate root representing a configured connection to an
// external provider. It tracks health, circuit breaker state, and authentication
// configuration. Auth configs store secret references that are resolved at dispatch
// time via the SecretStore port — raw secret values are never stored here.
type ProviderConnection struct {
	// TenantID is the owning tenant's identifier.
	TenantID string

	// ConnectionID is the unique identifier for this connection.
	ConnectionID string

	// ProviderName is the human-readable name of the provider (e.g., "acme-bank").
	ProviderName string

	// ProviderType is the category of provider (e.g., "bank", "energy", "compute").
	ProviderType string

	// Protocol is the communication protocol used for this connection.
	Protocol Protocol

	// BaseURL is the root URL for the provider's API.
	BaseURL string

	// AuthConfig holds the authentication configuration for this connection.
	// Implementations store secret references, resolved at dispatch time.
	AuthConfig AuthConfig

	// RetryPolicy defines retry behavior for failed requests.
	RetryPolicy RetryPolicy

	// RateLimitConfig defines rate limiting for outbound requests.
	RateLimitConfig RateLimitConfig

	// HealthStatus is the current observed health of the provider connection.
	HealthStatus HealthStatus

	// LastHealthCheckAt is the time of the most recent health check, or nil if none performed.
	LastHealthCheckAt *time.Time

	// CircuitState is the current state of the circuit breaker.
	CircuitState CircuitState

	// CircuitOpenedAt is the time the circuit was opened, or nil when closed/half-open without prior trip.
	CircuitOpenedAt *time.Time

	// FailureCount is the number of consecutive failures recorded since the last success.
	FailureCount int

	// SuccessCount is the total number of successes recorded on this connection.
	SuccessCount int

	// CreatedAt is the time this connection was created.
	CreatedAt time.Time

	// UpdatedAt is the time this connection was last modified.
	UpdatedAt time.Time
}

// NewProviderConnection creates and validates a new ProviderConnection aggregate.
func NewProviderConnection(
	tenantID string,
	providerName string,
	providerType string,
	protocol Protocol,
	baseURL string,
	authConfig AuthConfig,
	retryPolicy RetryPolicy,
	rateLimitConfig RateLimitConfig,
) (*ProviderConnection, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	if providerName == "" {
		return nil, ErrProviderNameRequired
	}
	if baseURL == "" {
		return nil, ErrBaseURLRequired
	}
	if authConfig == nil {
		return nil, ErrAuthConfigRequired
	}

	now := time.Now().UTC()
	return &ProviderConnection{
		TenantID:        tenantID,
		ConnectionID:    uuid.New().String(),
		ProviderName:    providerName,
		ProviderType:    providerType,
		Protocol:        protocol,
		BaseURL:         baseURL,
		AuthConfig:      authConfig,
		RetryPolicy:     retryPolicy,
		RateLimitConfig: rateLimitConfig,
		HealthStatus:    HealthStatusUnknown,
		CircuitState:    CircuitStateClosed,
		FailureCount:    0,
		SuccessCount:    0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// RecordSuccess records a successful request to the provider.
// When the circuit is half-open, a success closes the circuit and resets failure tracking.
// In all cases the success counter is incremented.
func (c *ProviderConnection) RecordSuccess() {
	c.SuccessCount++
	if c.CircuitState == CircuitStateHalfOpen {
		c.CircuitState = CircuitStateClosed
		c.FailureCount = 0
		c.CircuitOpenedAt = nil
	}
	c.UpdatedAt = time.Now().UTC()
}

// RecordFailure records a failed request to the provider and trips the circuit breaker
// if the failure count reaches the given threshold. In the half-open state any failure
// immediately re-trips the circuit.
func (c *ProviderConnection) RecordFailure(threshold int) {
	c.FailureCount++
	switch c.CircuitState {
	case CircuitStateClosed:
		if c.FailureCount >= threshold {
			c.TripCircuit()
			return
		}
	case CircuitStateHalfOpen:
		c.TripCircuit()
		return
	case CircuitStateOpen:
		// Circuit already open; failure is recorded but no additional state change needed.
	}
	c.UpdatedAt = time.Now().UTC()
}

// TripCircuit transitions the circuit breaker to the open state, blocking further requests.
// Calling TripCircuit when the circuit is already open is a no-op.
func (c *ProviderConnection) TripCircuit() {
	now := time.Now().UTC()
	c.CircuitState = CircuitStateOpen
	c.CircuitOpenedAt = &now
	c.UpdatedAt = now
}

// AttemptReset transitions the circuit breaker from open to half-open, allowing a probe
// request to test whether the provider has recovered. Calling AttemptReset when the
// circuit is closed or already half-open is a no-op.
func (c *ProviderConnection) AttemptReset() {
	if c.CircuitState == CircuitStateOpen {
		c.CircuitState = CircuitStateHalfOpen
		c.UpdatedAt = time.Now().UTC()
	}
}

// IsAvailable returns true when the circuit breaker is in a state that permits sending
// requests to the provider (closed or half-open for a probe attempt).
func (c *ProviderConnection) IsAvailable() bool {
	return c.CircuitState == CircuitStateClosed || c.CircuitState == CircuitStateHalfOpen
}

// UpdateHealthStatus sets the health status and records the current time as the last
// health check timestamp.
func (c *ProviderConnection) UpdateHealthStatus(status HealthStatus) {
	now := time.Now().UTC()
	c.HealthStatus = status
	c.LastHealthCheckAt = &now
	c.UpdatedAt = now
}
