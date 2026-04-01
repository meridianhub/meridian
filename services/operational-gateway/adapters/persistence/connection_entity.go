package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ConnectionEntity is the GORM persistence model for the provider_connections table.
// Column names must match the migration schema exactly.
// The composite primary key is (tenant_id, connection_id).
type ConnectionEntity struct {
	TenantID          uuid.UUID       `gorm:"column:tenant_id;type:uuid;not null;primaryKey"`
	ConnectionID      uuid.UUID       `gorm:"column:connection_id;type:uuid;not null;primaryKey"`
	ProviderName      string          `gorm:"column:provider_name;type:varchar(255);not null"`
	ProviderType      string          `gorm:"column:provider_type;type:varchar(128);not null"`
	Protocol          string          `gorm:"column:protocol;type:varchar(20);not null"`
	BaseURL           string          `gorm:"column:base_url;type:varchar(2048);not null"`
	AuthConfig        AuthConfigJSON  `gorm:"column:auth_config;type:jsonb;not null"`
	RetryPolicy       RetryPolicyJSON `gorm:"column:retry_policy;type:jsonb"`
	RateLimitConfig   RateLimitJSON   `gorm:"column:rate_limit_config;type:jsonb"`
	HealthStatus      string          `gorm:"column:health_status;type:varchar(20);not null;default:'UNKNOWN'"`
	LastHealthCheckAt *time.Time      `gorm:"column:last_health_check_at"`
	CircuitState      string          `gorm:"column:circuit_state;type:varchar(20);not null;default:'CLOSED'"`
	CircuitOpenedAt   *time.Time      `gorm:"column:circuit_opened_at"`
	FailureCount      int             `gorm:"column:failure_count;not null;default:0"`
	SuccessCount      int             `gorm:"column:success_count;not null;default:0"`
	Status            string          `gorm:"column:status;type:varchar(20);not null;default:'ACTIVE'"`
	DeprecatedAt      *time.Time      `gorm:"column:deprecated_at"`
	CreatedAt         time.Time       `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt         time.Time       `gorm:"column:updated_at;not null;default:now()"`
}

// TableName returns the table name matching the migration schema.
func (ConnectionEntity) TableName() string {
	return "provider_connections"
}

// AuthConfigJSON is the persisted representation of an AuthConfig discriminated union.
// The auth_type field identifies which variant's fields are populated.
type AuthConfigJSON struct {
	AuthType string `json:"auth_type"`

	// api_key fields
	HeaderName string `json:"header_name,omitempty"`
	SecretRef  string `json:"secret_ref,omitempty"`

	// basic fields
	Username    string `json:"username,omitempty"`
	PasswordRef string `json:"password_ref,omitempty"`

	// oauth2 fields
	TokenURL        string   `json:"token_url,omitempty"`
	ClientID        string   `json:"client_id,omitempty"`
	ClientSecretRef string   `json:"client_secret_ref,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`

	// hmac fields
	Algorithm       string `json:"algorithm,omitempty"`
	SignatureHeader string `json:"signature_header,omitempty"`

	// mtls fields
	ClientCertRef string `json:"client_cert_ref,omitempty"`
	ClientKeyRef  string `json:"client_key_ref,omitempty"`
	CACertRef     string `json:"ca_cert_ref,omitempty"`
}

// Value implements driver.Valuer for database writes.
func (a AuthConfigJSON) Value() (driver.Value, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("marshal AuthConfigJSON: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner for database reads.
func (a *AuthConfigJSON) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("%w: unsupported type %T", ErrInvalidJSONScan, value)
	}
	return json.Unmarshal(b, a)
}

// RetryPolicyJSON is the persisted representation of a RetryPolicy.
type RetryPolicyJSON struct {
	MaxAttempts           int     `json:"max_attempts"`
	InitialBackoffSeconds float64 `json:"initial_backoff_seconds"`
	MaxBackoffSeconds     float64 `json:"max_backoff_seconds"`
	BackoffMultiplier     float64 `json:"backoff_multiplier"`
}

// Value implements driver.Valuer.
func (r RetryPolicyJSON) Value() (driver.Value, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("marshal RetryPolicyJSON: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner.
func (r *RetryPolicyJSON) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("%w: unsupported type %T", ErrInvalidJSONScan, value)
	}
	return json.Unmarshal(b, r)
}

// RateLimitJSON is the persisted representation of a RateLimitConfig.
type RateLimitJSON struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	BurstSize         int     `json:"burst_size"`
}

// Value implements driver.Valuer.
func (r RateLimitJSON) Value() (driver.Value, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("marshal RateLimitJSON: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner.
func (r *RateLimitJSON) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("%w: unsupported type %T", ErrInvalidJSONScan, value)
	}
	return json.Unmarshal(b, r)
}
