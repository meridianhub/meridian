// Package persistence provides CockroachDB-backed repository implementations for
// the operational-gateway service using GORM.
package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidJSONScan is returned when scanning an incompatible type into a JSON column.
var ErrInvalidJSONScan = errors.New("cannot scan value into JSON column")

// InstructionEntity is the GORM persistence model for the instructions table.
// Column names must match the migration schema exactly.
type InstructionEntity struct {
	ID                   uuid.UUID  `gorm:"type:uuid;primaryKey"`
	TenantID             uuid.UUID  `gorm:"column:tenant_id;type:uuid;not null;index"`
	InstructionType      string     `gorm:"column:instruction_type;type:varchar(255);not null"`
	ProviderConnectionID uuid.UUID  `gorm:"column:provider_connection_id;type:uuid;not null"`
	CorrelationID        *string    `gorm:"column:correlation_id;type:varchar(255)"`
	CausationID          *string    `gorm:"column:causation_id;type:varchar(255)"`
	Payload              JSONB      `gorm:"column:payload;type:jsonb;not null"`
	Metadata             JSONB      `gorm:"column:metadata;type:jsonb"`
	Priority             int16      `gorm:"column:priority;not null;default:2"`
	Status               string     `gorm:"column:status;type:varchar(20);not null;default:'PENDING'"`
	ScheduledAt          *time.Time `gorm:"column:scheduled_at"`
	ExpiresAt            *time.Time `gorm:"column:expires_at"`
	AttemptCount         int        `gorm:"column:attempt_count;not null;default:0"`
	MaxAttempts          int        `gorm:"column:max_attempts;not null;default:3"`
	NextRetryAt          *time.Time `gorm:"column:next_retry_at"`
	IdempotencyKey       string     `gorm:"column:idempotency_key;type:varchar(255);not null;uniqueIndex:idx_instructions_idempotency"`
	DispatchedAt         *time.Time `gorm:"column:dispatched_at"`
	CompletedAt          *time.Time `gorm:"column:completed_at"`
	FailureReason        *string    `gorm:"column:failure_reason;type:text"`
	ErrorCode            *string    `gorm:"column:error_code;type:varchar(64)"`
	Version              int64      `gorm:"column:version;not null;default:1"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null;default:now()"`
}

// TableName returns the table name matching the migration schema.
func (InstructionEntity) TableName() string {
	return "instructions"
}

// InstructionAttemptEntity is the GORM persistence model for the instruction_attempts table.
type InstructionAttemptEntity struct {
	ID                  uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	InstructionID       uuid.UUID  `gorm:"column:instruction_id;type:uuid;not null;index"`
	AttemptNumber       int        `gorm:"column:attempt_number;not null"`
	DispatchedAt        time.Time  `gorm:"column:dispatched_at;not null"`
	CompletedAt         *time.Time `gorm:"column:completed_at"`
	ResponseStatusCode  *int       `gorm:"column:response_status_code"`
	ResponseBodyPreview *string    `gorm:"column:response_body_preview;type:varchar(1024)"`
	ErrorMessage        *string    `gorm:"column:error_message;type:text"`
	DurationMs          *int64     `gorm:"column:duration_ms"`
}

// TableName returns the table name matching the migration schema.
func (InstructionAttemptEntity) TableName() string {
	return "instruction_attempts"
}

// priorityNormal is the default priority string used for NORMAL priority.
const priorityNormal = "NORMAL"

// JSONB is a generic type for persisting arbitrary data as a JSONB column.
// It implements driver.Valuer and sql.Scanner for transparent marshaling.
type JSONB map[string]any

// Value implements driver.Valuer for database writes.
// Returns an empty JSON object ("{}") for nil maps to satisfy NOT NULL JSONB constraints.
func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return "{}", nil
	}
	b, err := json.Marshal(j)
	if err != nil {
		return nil, fmt.Errorf("marshal JSONB: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner for database reads.
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = nil
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
	return json.Unmarshal(b, j)
}

// JSONBString is a map[string]string that round-trips through JSONB.
type JSONBString map[string]string

// Value implements driver.Valuer.
func (j JSONBString) Value() (driver.Value, error) {
	if j == nil {
		return driver.Value(nil), nil
	}
	b, err := json.Marshal(j)
	if err != nil {
		return nil, fmt.Errorf("marshal JSONBString: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner.
func (j *JSONBString) Scan(value interface{}) error {
	if value == nil {
		*j = nil
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
	return json.Unmarshal(b, j)
}

// priorityToInt maps domain Priority strings to DB SMALLINT values.
// LOW=1, NORMAL=2, HIGH=3, CRITICAL=4 (matching migration CHECK constraint).
func priorityToInt(p string) int16 {
	switch p {
	case "LOW":
		return 1
	case priorityNormal:
		return 2
	case "HIGH":
		return 3
	case "CRITICAL":
		return 4
	default:
		return 2 // default NORMAL
	}
}

// intToPriority maps DB SMALLINT values back to domain Priority strings.
func intToPriority(n int16) string {
	switch n {
	case 1:
		return "LOW"
	case 2:
		return priorityNormal
	case 3:
		return "HIGH"
	case 4:
		return "CRITICAL"
	default:
		return priorityNormal
	}
}

// nullableString returns a *string pointer from a string, nil if empty.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// derefString dereferences a *string, returning empty string if nil.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
