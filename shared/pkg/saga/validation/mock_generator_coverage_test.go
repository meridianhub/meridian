package validation

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestGenerateByType(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		fieldDef  *schema.FieldDef
		expected  any
	}{
		{
			name:      "string type",
			fieldName: "name",
			fieldDef:  &schema.FieldDef{Type: schema.TypeString},
			expected:  "mock_name",
		},
		{
			name:      "decimal type",
			fieldName: "amount",
			fieldDef:  &schema.FieldDef{Type: schema.TypeDecimal},
			expected:  decimal.NewFromFloat(100.00),
		},
		{
			name:      "int32 type",
			fieldName: "count",
			fieldDef:  &schema.FieldDef{Type: schema.TypeInt32},
			expected:  int32(1000),
		},
		{
			name:      "int64 type",
			fieldName: "count",
			fieldDef:  &schema.FieldDef{Type: schema.TypeInt64},
			expected:  int64(1000),
		},
		{
			name:      "uint32 type",
			fieldName: "count",
			fieldDef:  &schema.FieldDef{Type: schema.TypeUint32},
			expected:  uint32(1000),
		},
		{
			name:      "bool type",
			fieldName: "active",
			fieldDef:  &schema.FieldDef{Type: schema.TypeBool},
			expected:  true,
		},
		{
			name:      "array type",
			fieldName: "items",
			fieldDef:  &schema.FieldDef{Type: schema.TypeArray},
			expected:  []any{},
		},
		{
			name:      "map type",
			fieldName: "metadata",
			fieldDef:  &schema.FieldDef{Type: schema.TypeMap},
			expected:  map[string]any{},
		},
		{
			name:      "uuid type",
			fieldName: "id",
			fieldDef:  &schema.FieldDef{Type: schema.TypeUUID},
			expected:  uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		},
		{
			name:      "unknown type returns nil",
			fieldName: "unknown",
			fieldDef:  &schema.FieldDef{Type: "unknown_type"},
			expected:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateByType(tt.fieldName, tt.fieldDef)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateEnumValue(t *testing.T) {
	t.Run("returns first value when values exist", func(t *testing.T) {
		fieldDef := &schema.FieldDef{
			Type:   schema.TypeEnum,
			Values: []string{"ACTIVE", "INACTIVE", "SUSPENDED"},
		}
		result := generateEnumValue(fieldDef)
		assert.Equal(t, "ACTIVE", result)
	})

	t.Run("returns UNKNOWN when no values", func(t *testing.T) {
		fieldDef := &schema.FieldDef{
			Type:   schema.TypeEnum,
			Values: []string{},
		}
		result := generateEnumValue(fieldDef)
		assert.Equal(t, "UNKNOWN", result)
	})
}

func TestGenerateByType_EnumWithValues(t *testing.T) {
	fieldDef := &schema.FieldDef{
		Type:   schema.TypeEnum,
		Values: []string{"CREDIT", "DEBIT"},
	}
	result := generateByType("direction", fieldDef)
	assert.Equal(t, "CREDIT", result)
}
