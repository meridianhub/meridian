package accounttype

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func (r *PostgresRegistry) scanDefinition(row pgx.Row) (*Definition, error) {
	var def Definition
	var normalBalance, behaviorClass, status string
	var description, defaultSagaPrefix sql.NullString
	var validationCEL, bucketingCEL, eligibilityCEL sql.NullString
	var attributeSchema []byte
	var attrsJSON []byte
	var successorID *uuid.UUID

	err := row.Scan(
		&def.ID, &def.Code, &def.Version, &def.DisplayName, &description,
		&normalBalance, &behaviorClass, &def.InstrumentCode,
		&defaultSagaPrefix, &def.DefaultConversionMethodID, &def.DefaultConversionMethodVersion,
		&validationCEL, &bucketingCEL, &eligibilityCEL,
		&attributeSchema, &attrsJSON,
		&status, &def.IsSystem, &successorID,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to scan account type definition: %w", err)
	}

	def.NormalBalance = NormalBalance(normalBalance)
	def.BehaviorClass = BehaviorClass(behaviorClass)
	def.Status = Status(status)
	def.SuccessorID = successorID

	if description.Valid {
		def.Description = description.String
	}
	if defaultSagaPrefix.Valid {
		def.DefaultSagaPrefix = defaultSagaPrefix.String
	}
	if validationCEL.Valid {
		def.ValidationCEL = validationCEL.String
	}
	if bucketingCEL.Valid {
		def.BucketingCEL = bucketingCEL.String
	}
	if eligibilityCEL.Valid {
		def.EligibilityCEL = eligibilityCEL.String
	}

	def.AttributeSchema = attributeSchema
	if attrsJSON != nil {
		if err := json.Unmarshal(attrsJSON, &def.Attributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
		}
	}

	return &def, nil
}

func (r *PostgresRegistry) scanDefinitionFromRows(rows pgx.Rows) (*Definition, error) {
	var def Definition
	var normalBalance, behaviorClass, status string
	var description, defaultSagaPrefix sql.NullString
	var validationCEL, bucketingCEL, eligibilityCEL sql.NullString
	var attributeSchema []byte
	var attrsJSON []byte
	var successorID *uuid.UUID

	err := rows.Scan(
		&def.ID, &def.Code, &def.Version, &def.DisplayName, &description,
		&normalBalance, &behaviorClass, &def.InstrumentCode,
		&defaultSagaPrefix, &def.DefaultConversionMethodID, &def.DefaultConversionMethodVersion,
		&validationCEL, &bucketingCEL, &eligibilityCEL,
		&attributeSchema, &attrsJSON,
		&status, &def.IsSystem, &successorID,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan account type definition: %w", err)
	}

	def.NormalBalance = NormalBalance(normalBalance)
	def.BehaviorClass = BehaviorClass(behaviorClass)
	def.Status = Status(status)
	def.SuccessorID = successorID

	if description.Valid {
		def.Description = description.String
	}
	if defaultSagaPrefix.Valid {
		def.DefaultSagaPrefix = defaultSagaPrefix.String
	}
	if validationCEL.Valid {
		def.ValidationCEL = validationCEL.String
	}
	if bucketingCEL.Valid {
		def.BucketingCEL = bucketingCEL.String
	}
	if eligibilityCEL.Valid {
		def.EligibilityCEL = eligibilityCEL.String
	}

	def.AttributeSchema = attributeSchema
	if attrsJSON != nil {
		if err := json.Unmarshal(attrsJSON, &def.Attributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
		}
	}

	return &def, nil
}

// validateSchemaIfPresent validates the JSON Schema if it's non-empty.
func validateSchemaIfPresent(schema json.RawMessage) error {
	if !hasNonEmptySchema(schema) {
		return nil
	}
	if err := validateJSONSchema(schema); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAttributeSchema, err)
	}
	return nil
}

// marshalAttributes marshals a map to JSON bytes. Returns nil for nil maps.
func marshalAttributes(attrs map[string]any) ([]byte, error) {
	if attrs == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal attributes: %w", err)
	}
	return b, nil
}

// hasNonEmptySchema checks whether the schema is non-nil and not just an empty JSON object.
func hasNonEmptySchema(schema json.RawMessage) bool {
	if len(schema) == 0 {
		return false
	}
	trimmed := strings.TrimSpace(string(schema))
	return trimmed != "" && trimmed != "{}" && trimmed != "null"
}

// nullString converts a string to sql.NullString, treating empty strings as NULL.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// validateJSONSchema validates that the given bytes are a valid JSON Schema.
func validateJSONSchema(schema json.RawMessage) error {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", strings.NewReader(string(schema))); err != nil {
		return fmt.Errorf("invalid JSON Schema: %w", err)
	}
	if _, err := c.Compile("schema.json"); err != nil {
		return fmt.Errorf("JSON Schema compilation failed: %w", err)
	}
	return nil
}

// validateAttributesAgainstSchema validates attributes against a JSON Schema.
func validateAttributesAgainstSchema(schema json.RawMessage, attributes map[string]any) error {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", strings.NewReader(string(schema))); err != nil {
		return fmt.Errorf("invalid JSON Schema: %w", err)
	}
	compiled, err := c.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("JSON Schema compilation failed: %w", err)
	}

	if err := compiled.Validate(attributes); err != nil {
		return fmt.Errorf("%w: %w", ErrAttributesInvalid, err)
	}

	return nil
}
