package referencedatav1_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// testValidator is the shared validator instance for all validation tests.
var testValidator protovalidate.Validator

func TestMain(m *testing.M) {
	var err error
	testValidator, err = protovalidate.New()
	if err != nil {
		panic(fmt.Sprintf("failed to create validator: %v", err))
	}
	os.Exit(m.Run())
}

// TestInstrumentDefinitionValidation verifies InstrumentDefinition validation constraints.
func TestInstrumentDefinitionValidation(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	tests := []struct {
		name    string
		def     *referencedatav1.InstrumentDefinition
		wantErr bool
	}{
		{
			name: "valid minimal definition",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: false,
		},
		{
			name: "valid full definition with CEL expressions",
			def: &referencedatav1.InstrumentDefinition{
				Id:                       validUUID,
				Code:                     "CARBON_CREDIT",
				Version:                  1,
				Dimension:                referencedatav1.Dimension_DIMENSION_CARBON,
				Precision:                4,
				Status:                   referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				ValidationExpression:     "amount > 0 && attributes.vintage_year != ''",
				FungibilityKeyExpression: "instrument_code + ':' + attributes.vintage_year + ':' + attributes.registry",
				ErrorMessageExpression:   "'Invalid amount: ' + string(amount)",
				AttributeSchema:          `{"type":"object","properties":{"vintage_year":{"type":"string"},"registry":{"type":"string"}}}`,
				DisplayName:              "Verified Carbon Credit",
				Description:              "Carbon credit from verified emission reduction projects",
				CreatedAt:                now,
				ActivatedAt:              now,
			},
			wantErr: false,
		},
		{
			name: "valid: DRAFT status",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "NEW_COIN",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_COUNT,
				Precision: 0,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT,
				CreatedAt: now,
			},
			wantErr: false,
		},
		{
			name: "valid: DEPRECATED status with deprecated_at",
			def: &referencedatav1.InstrumentDefinition{
				Id:           validUUID,
				Code:         "OLD_COIN",
				Version:      5,
				Dimension:    referencedatav1.Dimension_DIMENSION_COUNT,
				Precision:    0,
				Status:       referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,
				CreatedAt:    now,
				ActivatedAt:  now,
				DeprecatedAt: now,
			},
			wantErr: false,
		},
		{
			name: "invalid: missing id",
			def: &referencedatav1.InstrumentDefinition{
				Id:        "",
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: malformed UUID",
			def: &referencedatav1.InstrumentDefinition{
				Id:        "not-a-uuid",
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty code",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: lowercase code",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "usd",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: code with special chars",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "US-D",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: code exceeds max length",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "ABCDEFGHIJKLMNOPQRSTUVWXYZ12345678",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: version is zero",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "USD",
				Version:   0,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: DIMENSION_UNSPECIFIED",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_UNSPECIFIED,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: negative precision",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: -1,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: precision exceeds 18",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 19,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: INSTRUMENT_STATUS_UNSPECIFIED",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing created_at",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "USD",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				// CreatedAt: nil - missing required field
			},
			wantErr: true,
		},
		{
			name: "invalid: validation_expression exceeds max length",
			def: &referencedatav1.InstrumentDefinition{
				Id:                   validUUID,
				Code:                 "USD",
				Version:              1,
				Dimension:            referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision:            2,
				Status:               referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				ValidationExpression: string(make([]byte, 2049)),
				CreatedAt:            now,
			},
			wantErr: true,
		},
		{
			name: "invalid: display_name exceeds max length",
			def: &referencedatav1.InstrumentDefinition{
				Id:          validUUID,
				Code:        "USD",
				Version:     1,
				Dimension:   referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision:   2,
				Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				DisplayName: string(make([]byte, 129)),
				CreatedAt:   now,
			},
			wantErr: true,
		},
		{
			name: "invalid: description exceeds max length",
			def: &referencedatav1.InstrumentDefinition{
				Id:          validUUID,
				Code:        "USD",
				Version:     1,
				Dimension:   referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision:   2,
				Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				Description: string(make([]byte, 1025)),
				CreatedAt:   now,
			},
			wantErr: true,
		},
		{
			name: "invalid: attribute_schema exceeds max length",
			def: &referencedatav1.InstrumentDefinition{
				Id:              validUUID,
				Code:            "USD",
				Version:         1,
				Dimension:       referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision:       2,
				Status:          referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				AttributeSchema: string(make([]byte, 16385)),
				CreatedAt:       now,
			},
			wantErr: true,
		},
		{
			name: "valid: all dimension types",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "GPU_HOUR",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_COMPUTE,
				Precision: 6,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: false,
		},
		{
			name: "valid: maximum precision 18",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "CRYPTO",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_COUNT,
				Precision: 18,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: false,
		},
		{
			name: "valid: zero precision",
			def: &referencedatav1.InstrumentDefinition{
				Id:        validUUID,
				Code:      "TICKET",
				Version:   1,
				Dimension: referencedatav1.Dimension_DIMENSION_COUNT,
				Precision: 0,
				Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				CreatedAt: now,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.def)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestDimensionEnum verifies all Dimension enum values are defined.
func TestDimensionEnum(t *testing.T) {
	expected := []referencedatav1.Dimension{
		referencedatav1.Dimension_DIMENSION_UNSPECIFIED,
		referencedatav1.Dimension_DIMENSION_CURRENCY,
		referencedatav1.Dimension_DIMENSION_ENERGY,
		referencedatav1.Dimension_DIMENSION_MASS,
		referencedatav1.Dimension_DIMENSION_VOLUME,
		referencedatav1.Dimension_DIMENSION_TIME,
		referencedatav1.Dimension_DIMENSION_COMPUTE,
		referencedatav1.Dimension_DIMENSION_CARBON,
		referencedatav1.Dimension_DIMENSION_DATA,
		referencedatav1.Dimension_DIMENSION_COUNT,
	}

	for i, dim := range expected {
		if int32(dim) != int32(i) {
			t.Errorf("Dimension %s has unexpected value %d, expected %d", dim, int32(dim), i)
		}
	}

	// Verify total count matches
	if len(referencedatav1.Dimension_name) != len(expected) {
		t.Errorf("expected %d dimensions, got %d", len(expected), len(referencedatav1.Dimension_name))
	}
}

// TestInstrumentStatusEnum verifies all InstrumentStatus enum values.
func TestInstrumentStatusEnum(t *testing.T) {
	expected := []referencedatav1.InstrumentStatus{
		referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED, // 0
		referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT,       // 1
		referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,      // 2
		referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,  // 3
	}

	for i, status := range expected {
		if int32(status) != int32(i) {
			t.Errorf("InstrumentStatus %s has unexpected value %d, expected %d", status, int32(status), i)
		}
	}

	// Verify total count
	if len(referencedatav1.InstrumentStatus_name) != len(expected) {
		t.Errorf("expected %d statuses, got %d", len(expected), len(referencedatav1.InstrumentStatus_name))
	}
}

// TestInstrumentDefinitionSerialization verifies round-trip serialization.
func TestInstrumentDefinitionSerialization(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	original := &referencedatav1.InstrumentDefinition{
		Id:                       validUUID,
		Code:                     "KWH",
		Version:                  2,
		Dimension:                referencedatav1.Dimension_DIMENSION_ENERGY,
		Precision:                6,
		Status:                   referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		ValidationExpression:     "amount >= 0",
		FungibilityKeyExpression: "instrument_code",
		ErrorMessageExpression:   "'Energy amount must be non-negative'",
		AttributeSchema:          `{"type":"object","properties":{"meter_id":{"type":"string"}}}`,
		DisplayName:              "Kilowatt Hour",
		Description:              "Standard unit of electrical energy",
		CreatedAt:                now,
		ActivatedAt:              now,
		IsSystem:                 true,
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &referencedatav1.InstrumentDefinition{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Error("round-trip serialization produced different message")
	}

	// Verify specific fields
	if decoded.Id != original.Id {
		t.Errorf("id mismatch: got %v, want %v", decoded.Id, original.Id)
	}
	if decoded.Code != original.Code {
		t.Errorf("code mismatch: got %v, want %v", decoded.Code, original.Code)
	}
	if decoded.Dimension != original.Dimension {
		t.Errorf("dimension mismatch: got %v, want %v", decoded.Dimension, original.Dimension)
	}
	if decoded.Status != original.Status {
		t.Errorf("status mismatch: got %v, want %v", decoded.Status, original.Status)
	}
	if decoded.ValidationExpression != original.ValidationExpression {
		t.Errorf("validation_expression mismatch")
	}
	if decoded.FungibilityKeyExpression != original.FungibilityKeyExpression {
		t.Errorf("fungibility_key_expression mismatch")
	}
	if decoded.IsSystem != original.IsSystem {
		t.Errorf("is_system mismatch: got %v, want %v", decoded.IsSystem, original.IsSystem)
	}
}

// TestCELExpressionFields verifies CEL expression fields can hold valid expressions.
func TestCELExpressionFields(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	// Test various CEL expression patterns
	tests := []struct {
		name               string
		validationExpr     string
		fungibilityKeyExpr string
		errorMessageExpr   string
	}{
		{
			name:               "simple validation",
			validationExpr:     "amount > 0",
			fungibilityKeyExpr: "instrument_code",
			errorMessageExpr:   "'Invalid'",
		},
		{
			name:               "complex validation with attributes",
			validationExpr:     "amount > 0 && (attributes.batch_id != '' || dimension == 'CURRENCY')",
			fungibilityKeyExpr: "instrument_code + ':' + string(version)",
			errorMessageExpr:   "'Invalid amount: ' + string(amount) + ' for ' + instrument_code",
		},
		{
			name:               "batch segregation pattern",
			validationExpr:     "has(attributes.batch_id) && attributes.batch_id.matches('^BATCH-[0-9]+$')",
			fungibilityKeyExpr: "instrument_code + ':' + attributes.batch_id",
			errorMessageExpr:   "'Batch ID required for inventory items'",
		},
		{
			name:               "time-bound validation",
			validationExpr:     "valid_from != null && valid_to != null && valid_from < valid_to",
			fungibilityKeyExpr: "instrument_code + ':' + string(valid_from.getFullYear())",
			errorMessageExpr:   "'Time-bound assets require valid_from and valid_to'",
		},
		{
			name:               "empty fungibility key (fully fungible)",
			validationExpr:     "amount >= 0",
			fungibilityKeyExpr: "",
			errorMessageExpr:   "",
		},
		{
			name:               "long CEL expression (near limit)",
			validationExpr:     strings.Repeat("amount > 0 && ", 100) + "true",
			fungibilityKeyExpr: "instrument_code",
			errorMessageExpr:   "'Error'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := &referencedatav1.InstrumentDefinition{
				Id:                       validUUID,
				Code:                     "TEST",
				Version:                  1,
				Dimension:                referencedatav1.Dimension_DIMENSION_COUNT,
				Precision:                2,
				Status:                   referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				ValidationExpression:     tt.validationExpr,
				FungibilityKeyExpression: tt.fungibilityKeyExpr,
				ErrorMessageExpression:   tt.errorMessageExpr,
				CreatedAt:                now,
			}

			// Verify validation passes (CEL syntax is not validated at proto level)
			if err := testValidator.Validate(def); err != nil {
				t.Errorf("should pass validation: %v", err)
			}

			// Verify round-trip preserves expressions
			data, err := proto.Marshal(def)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			decoded := &referencedatav1.InstrumentDefinition{}
			if err := proto.Unmarshal(data, decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.ValidationExpression != tt.validationExpr {
				t.Error("validation_expression not preserved")
			}
			if decoded.FungibilityKeyExpression != tt.fungibilityKeyExpr {
				t.Error("fungibility_key_expression not preserved")
			}
			if decoded.ErrorMessageExpression != tt.errorMessageExpr {
				t.Error("error_message_expression not preserved")
			}
		})
	}
}

// TestAttributeSchemaField verifies JSON Schema can be stored.
func TestAttributeSchemaField(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	schemas := []string{
		// Simple schema
		`{"type":"object"}`,
		// Schema with properties
		`{"type":"object","properties":{"batch_id":{"type":"string"},"quantity":{"type":"number"}}}`,
		// Schema with required fields
		`{"type":"object","required":["batch_id"],"properties":{"batch_id":{"type":"string","minLength":1}}}`,
		// Complex schema with nested objects
		`{"type":"object","properties":{"metadata":{"type":"object","properties":{"source":{"type":"string"},"timestamp":{"type":"string","format":"date-time"}}}}}`,
		// Empty (no schema)
		"",
	}

	for i, schema := range schemas {
		t.Run(fmt.Sprintf("schema_%d", i), func(t *testing.T) {
			def := &referencedatav1.InstrumentDefinition{
				Id:              validUUID,
				Code:            "TEST",
				Version:         1,
				Dimension:       referencedatav1.Dimension_DIMENSION_COUNT,
				Precision:       0,
				Status:          referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				AttributeSchema: schema,
				CreatedAt:       now,
			}

			if err := testValidator.Validate(def); err != nil {
				t.Errorf("should pass validation: %v", err)
			}

			data, err := proto.Marshal(def)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			decoded := &referencedatav1.InstrumentDefinition{}
			if err := proto.Unmarshal(data, decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.AttributeSchema != schema {
				t.Error("attribute_schema not preserved after round-trip")
			}
		})
	}
}

// TestIsSystemField verifies system instrument flag.
func TestIsSystemField(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	// System instrument (is_system = true)
	systemDef := &referencedatav1.InstrumentDefinition{
		Id:          validUUID,
		Code:        "USD",
		Version:     1,
		Dimension:   referencedatav1.Dimension_DIMENSION_CURRENCY,
		Precision:   2,
		Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		DisplayName: "US Dollar",
		Description: "United States Dollar - platform-wide system instrument",
		CreatedAt:   now,
		IsSystem:    true,
	}

	if err := testValidator.Validate(systemDef); err != nil {
		t.Errorf("system instrument should be valid: %v", err)
	}

	if !systemDef.IsSystem {
		t.Error("is_system should be true")
	}

	// Tenant instrument (is_system = false, default)
	tenantDef := &referencedatav1.InstrumentDefinition{
		Id:        validUUID,
		Code:      "LOYALTY_POINT",
		Version:   1,
		Dimension: referencedatav1.Dimension_DIMENSION_COUNT,
		Precision: 0,
		Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		CreatedAt: now,
		// IsSystem defaults to false
	}

	if err := testValidator.Validate(tenantDef); err != nil {
		t.Errorf("tenant instrument should be valid: %v", err)
	}

	if tenantDef.IsSystem {
		t.Error("is_system should default to false")
	}
}
