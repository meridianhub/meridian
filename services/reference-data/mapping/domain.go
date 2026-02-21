// Package mapping provides domain types and persistence for MappingDefinition CRUD.
package mapping

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Status represents the lifecycle state of a MappingDefinition.
type Status string

// Status constants for the MappingDefinition lifecycle.
const (
	StatusDraft      Status = "DRAFT"
	StatusActive     Status = "ACTIVE"
	StatusDeprecated Status = "DEPRECATED"
)

// CelTransform holds bidirectional CEL expressions for a field.
type CelTransform struct {
	InboundCEL  string `json:"inbound_cel,omitempty"`
	OutboundCEL string `json:"outbound_cel,omitempty"`
}

// EnumMapping maps external enum values to internal enum values bidirectionally.
type EnumMapping struct {
	Values           map[string]string `json:"values,omitempty"`
	Fallback         string            `json:"fallback,omitempty"`
	OutboundFallback string            `json:"outbound_fallback,omitempty"`
}

// AttributeFlatten merges multiple source keys into a single target map field.
type AttributeFlatten struct {
	SourceKeys  []string `json:"source_keys"`
	TargetField string   `json:"target_field"`
}

// FieldTransform defines a single field transformation variant.
// Exactly one of the variant fields should be set.
type FieldTransform struct {
	CEL              *CelTransform     `json:"cel,omitempty"`
	EnumMapping      *EnumMapping      `json:"enum_mapping,omitempty"`
	DateFormat       string            `json:"date_format,omitempty"`
	DefaultValue     string            `json:"default_value,omitempty"`
	AttributeFlatten *AttributeFlatten `json:"attribute_flatten,omitempty"`
}

// FieldCorrespondence maps a single external field to an internal proto field.
type FieldCorrespondence struct {
	ExternalPath string          `json:"external_path"`
	InternalPath string          `json:"internal_path"`
	Transform    *FieldTransform `json:"transform,omitempty"`
}

// ComputedField derives a field value from a CEL expression.
type ComputedField struct {
	TargetPath    string `json:"target_path"`
	CELExpression string `json:"cel_expression"`
}

// IdempotencyConfig controls how idempotency keys are derived.
type IdempotencyConfig struct {
	SourceSelector    string   `json:"source_selector,omitempty"`
	UseContentHash    bool     `json:"use_content_hash,omitempty"`
	ContentHashFields []string `json:"content_hash_fields,omitempty"`
}

// Definition represents a MappingDefinition in the domain layer.
type Definition struct {
	ID                    uuid.UUID
	TenantID              string
	Name                  string
	TargetService         string
	TargetRPC             string
	Version               int
	Status                Status
	ExternalSchema        string
	Fields                []FieldCorrespondence
	InboundComputed       []ComputedField
	OutboundComputed      []ComputedField
	InboundValidationCEL  string
	OutboundValidationCEL string
	IsBatch               bool
	BatchTargetPath       string
	Idempotency           *IdempotencyConfig
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// MarshalFields serializes the fields slice to JSON for storage.
func MarshalFields(fields []FieldCorrespondence) (json.RawMessage, error) {
	if fields == nil {
		return json.RawMessage("[]"), nil
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// UnmarshalFields deserializes the fields JSON from storage.
func UnmarshalFields(data []byte) ([]FieldCorrespondence, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var fields []FieldCorrespondence
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// MarshalComputedFields serializes a computed fields slice to JSON for storage.
func MarshalComputedFields(fields []ComputedField) (json.RawMessage, error) {
	if fields == nil {
		return json.RawMessage("[]"), nil
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// UnmarshalComputedFields deserializes a computed fields JSON from storage.
func UnmarshalComputedFields(data []byte) ([]ComputedField, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var fields []ComputedField
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// MarshalIdempotency serializes the IdempotencyConfig to JSON for storage.
func MarshalIdempotency(cfg *IdempotencyConfig) (json.RawMessage, error) {
	if cfg == nil {
		return json.RawMessage("null"), nil
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// UnmarshalIdempotency deserializes the IdempotencyConfig JSON from storage.
func UnmarshalIdempotency(data []byte) (*IdempotencyConfig, error) {
	if len(data) == 0 {
		return nil, nil //nolint:nilnil // nil config with nil error signals "no idempotency config stored"
	}
	var cfg IdempotencyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
