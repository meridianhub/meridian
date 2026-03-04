// Package domain contains the operational-gateway domain model.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/dispatch"
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

// validProtocols is the set of accepted Protocol values for constructor validation.
var validProtocols = map[Protocol]struct{}{
	ProtocolHTTPS:   {},
	ProtocolGRPC:    {},
	ProtocolWebhook: {},
	ProtocolMQTT:    {},
	ProtocolAMQP:    {},
}

// CircuitState represents the current state of the circuit breaker.
// It is an alias for dispatch.CircuitState from the shared dispatch package.
type CircuitState = dispatch.CircuitState

const (
	// CircuitStateClosed means the circuit is closed and requests flow normally.
	CircuitStateClosed = dispatch.CircuitStateClosed
	// CircuitStateOpen means the circuit is open and requests are blocked.
	CircuitStateOpen = dispatch.CircuitStateOpen
	// CircuitStateHalfOpen means the circuit is allowing a probe request to test recovery.
	CircuitStateHalfOpen = dispatch.CircuitStateHalfOpen
)

// HealthStatus represents the observed health of a provider connection.
// It is an alias for dispatch.HealthStatus from the shared dispatch package.
type HealthStatus = dispatch.HealthStatus

const (
	// HealthStatusUnknown means no health check has been performed yet.
	HealthStatusUnknown = dispatch.HealthStatusUnknown
	// HealthStatusHealthy means the provider is responding normally.
	HealthStatusHealthy = dispatch.HealthStatusHealthy
	// HealthStatusDegraded means the provider is responding but with elevated latency or errors.
	HealthStatusDegraded = dispatch.HealthStatusDegraded
	// HealthStatusUnhealthy means the provider is not responding or returning errors.
	HealthStatusUnhealthy = dispatch.HealthStatusUnhealthy
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
	// ErrInvalidProtocol is returned when an unsupported protocol value is provided.
	ErrInvalidProtocol = errors.New("invalid protocol")
	// ErrInvalidThreshold is returned when a failure threshold of zero or less is used.
	ErrInvalidThreshold = errors.New("threshold must be greater than zero")
)

// AuthConfig is the interface implemented by all authentication configuration types.
// Secret-valued fields store references (resolved at dispatch time via SecretStore).
// Non-secret fields (e.g., usernames, client IDs) are stored as plain values.
type AuthConfig interface {
	// AuthType returns a string identifying the authentication mechanism.
	AuthType() string
}

// APIKeyAuth authenticates using a static API key passed in a request header.
// SecretRef is resolved at dispatch time via SecretStore.
type APIKeyAuth struct {
	// HeaderName is the HTTP header name to use (e.g., "X-API-Key").
	HeaderName string
	// SecretRef is the reference to the secret containing the API key value.
	SecretRef string
}

// AuthType implements AuthConfig.
func (a *APIKeyAuth) AuthType() string { return "api_key" }

// BasicAuth authenticates using HTTP Basic authentication.
// Username is a plain value; PasswordRef is a secret reference resolved via SecretStore.
type BasicAuth struct {
	// Username is the Basic auth username (not a secret).
	Username string
	// PasswordRef is the reference to the secret containing the password.
	PasswordRef string
}

// AuthType implements AuthConfig.
func (a *BasicAuth) AuthType() string { return "basic" }

// OAuth2Auth authenticates using the OAuth 2.0 client credentials flow.
// ClientID is a plain value; ClientSecretRef is a secret reference resolved via SecretStore.
type OAuth2Auth struct {
	// TokenURL is the token endpoint URL.
	TokenURL string
	// ClientID is the OAuth 2.0 client identifier (not a secret).
	ClientID string
	// ClientSecretRef is the reference to the secret containing the OAuth client secret.
	ClientSecretRef string
	// Scopes is the list of OAuth scopes to request.
	Scopes []string
}

// AuthType implements AuthConfig.
func (a *OAuth2Auth) AuthType() string { return "oauth2" }

// HMACAuth authenticates by signing request payloads with HMAC.
// SecretRef is resolved at dispatch time via SecretStore.
type HMACAuth struct {
	// SecretRef is the reference to the HMAC signing secret.
	SecretRef string
	// Algorithm is the HMAC algorithm to use (e.g., "sha256", "sha512").
	Algorithm string
	// SignatureHeader is the HTTP header where the computed signature is placed.
	SignatureHeader string
}

// AuthType implements AuthConfig.
func (a *HMACAuth) AuthType() string { return "hmac" }

// MTLSAuth authenticates using mutual TLS with a client certificate.
// All three fields are secret references resolved at dispatch time via SecretStore.
type MTLSAuth struct {
	// ClientCertRef is the reference to the secret containing the PEM-encoded client certificate.
	ClientCertRef string
	// ClientKeyRef is the reference to the secret containing the PEM-encoded client private key.
	ClientKeyRef string
	// CACertRef is an optional reference to the secret containing the CA certificate used to
	// verify the provider's server certificate. Empty string means the system CA pool is used.
	CACertRef string
}

// AuthType implements AuthConfig.
func (a *MTLSAuth) AuthType() string { return "mtls" }

// RetryPolicy defines how failed requests to a provider should be retried.
// It is an alias for dispatch.RetryPolicy from the shared dispatch package.
type RetryPolicy = dispatch.RetryPolicy

// RateLimitConfig defines the rate limiting policy for outbound requests to a provider.
type RateLimitConfig struct {
	// RequestsPerSecond is the maximum number of requests allowed per second.
	RequestsPerSecond float64
	// BurstSize is the maximum number of requests allowed in a burst above the steady-state rate.
	BurstSize int
}

// ProviderConnection is the aggregate root representing a configured connection to an
// external provider. It tracks health, circuit breaker state, and authentication
// configuration. Secret-valued auth config fields store references resolved at dispatch
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

	// CircuitOpenedAt is the time the circuit was opened, or nil when the circuit has not been tripped.
	CircuitOpenedAt *time.Time

	// FailureCount is the count of consecutive failures since the last RecordSuccess call.
	FailureCount int

	// SuccessCount is the total number of successes recorded on this connection.
	SuccessCount int

	// CreatedAt is the time this connection was created.
	CreatedAt time.Time

	// UpdatedAt is the time this connection was last modified.
	UpdatedAt time.Time
}

// NewProviderConnection creates and validates a new ProviderConnection aggregate.
// Returns ErrInvalidProtocol if the protocol is not one of the known values.
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
	if _, ok := validProtocols[protocol]; !ok {
		return nil, ErrInvalidProtocol
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

// RecordSuccess records a successful request to the provider and increments SuccessCount.
// When the circuit is closed or half-open, it also resets FailureCount and closes the circuit
// (the half-open → closed transition confirms recovery).
func (c *ProviderConnection) RecordSuccess() {
	c.SuccessCount++
	switch c.CircuitState {
	case CircuitStateHalfOpen:
		c.CircuitState = CircuitStateClosed
		c.FailureCount = 0
		c.CircuitOpenedAt = nil
	case CircuitStateClosed:
		c.FailureCount = 0
	case CircuitStateOpen:
		// Success during open state is unexpected (IsAvailable returns false).
		// Record it but do not change circuit state automatically; use AttemptReset first.
	}
	c.UpdatedAt = time.Now().UTC()
}

// RecordFailure records a failed request to the provider and trips the circuit breaker
// if the failure count reaches the given threshold. In the half-open state any failure
// immediately re-trips the circuit. Returns ErrInvalidThreshold if threshold <= 0.
func (c *ProviderConnection) RecordFailure(threshold int) error {
	if threshold <= 0 {
		return ErrInvalidThreshold
	}
	c.FailureCount++
	switch c.CircuitState {
	case CircuitStateClosed:
		if c.FailureCount >= threshold {
			c.TripCircuit()
			return nil
		}
	case CircuitStateHalfOpen:
		c.TripCircuit()
		return nil
	case CircuitStateOpen:
		// Circuit already open; failure is recorded but no additional state change needed.
	}
	c.UpdatedAt = time.Now().UTC()
	return nil
}

// TripCircuit transitions the circuit breaker to the open state, blocking further requests.
// If the circuit is already open, CircuitOpenedAt is preserved so that the open duration
// is measured from the original trip time, not a subsequent re-evaluation.
func (c *ProviderConnection) TripCircuit() {
	now := time.Now().UTC()
	c.CircuitState = CircuitStateOpen
	if c.CircuitOpenedAt == nil {
		c.CircuitOpenedAt = &now
	}
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
