package quantityv1_test

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"buf.build/go/protovalidate"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
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

// TestAttributeEntryValidation verifies AttributeEntry validation constraints.
func TestAttributeEntryValidation(t *testing.T) {
	tests := []struct {
		name    string
		entry   *quantityv1.AttributeEntry
		wantErr bool
	}{
		{
			name:    "valid snake_case key",
			entry:   &quantityv1.AttributeEntry{Key: "batch_id", Value: "BATCH-001"},
			wantErr: false,
		},
		{
			name:    "valid single letter key",
			entry:   &quantityv1.AttributeEntry{Key: "x", Value: "value"},
			wantErr: false,
		},
		{
			name:    "valid key with numbers",
			entry:   &quantityv1.AttributeEntry{Key: "batch_id_2", Value: "value"},
			wantErr: false,
		},
		{
			name:    "invalid: empty key",
			entry:   &quantityv1.AttributeEntry{Key: "", Value: "value"},
			wantErr: true,
		},
		{
			name:    "invalid: key starts with number",
			entry:   &quantityv1.AttributeEntry{Key: "1batch", Value: "value"},
			wantErr: true,
		},
		{
			name:    "invalid: key with uppercase",
			entry:   &quantityv1.AttributeEntry{Key: "BatchId", Value: "value"},
			wantErr: true,
		},
		{
			name:    "invalid: key with hyphen",
			entry:   &quantityv1.AttributeEntry{Key: "batch-id", Value: "value"},
			wantErr: true,
		},
		{
			name:    "invalid: key exceeds max length",
			entry:   &quantityv1.AttributeEntry{Key: string(make([]byte, 65)), Value: "value"},
			wantErr: true,
		},
		{
			name:    "invalid: value exceeds max length",
			entry:   &quantityv1.AttributeEntry{Key: "key", Value: string(make([]byte, 257))},
			wantErr: true,
		},
		{
			name:    "valid: empty value allowed",
			entry:   &quantityv1.AttributeEntry{Key: "key", Value: ""},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.entry)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestInstrumentAmountValidation verifies InstrumentAmount validation constraints.
func TestInstrumentAmountValidation(t *testing.T) {
	now := timestamppb.Now()

	tests := []struct {
		name    string
		amount  *quantityv1.InstrumentAmount
		wantErr bool
	}{
		{
			name: "valid minimal amount",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100.50",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: false,
		},
		{
			name: "valid with all fields",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "1234.567890",
				InstrumentCode: "KWH",
				Version:        2,
				Attributes: []*quantityv1.AttributeEntry{
					{Key: "batch_id", Value: "BATCH-001"},
					{Key: "location", Value: "SITE-A"},
				},
				ValidFrom: now,
				ValidTo:   timestamppb.New(now.AsTime().AddDate(0, 1, 0)),
				Source:    "meter_reading",
			},
			wantErr: false,
		},
		{
			name: "valid negative amount",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "-50.00",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: false,
		},
		{
			name: "valid integer amount (no decimal)",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: false,
		},
		{
			name: "valid zero amount",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "0",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: false,
		},
		{
			name: "invalid: empty amount",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: amount with letters",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100USD",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: amount with comma",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "1,000.00",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: amount exceeds max length",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "123456789012345678901234567890123456789012345",
				InstrumentCode: "USD",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty instrument_code",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: instrument_code with lowercase",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "usd",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: instrument_code with special chars",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "US-D",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: instrument_code starts with number",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "1USD",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: instrument_code exceeds max length",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "ABCDEFGHIJKLMNOPQRSTUVWXYZ12345678",
				Version:        1,
			},
			wantErr: true,
		},
		{
			name: "invalid: version is zero",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "USD",
				Version:        0,
			},
			wantErr: true,
		},
		{
			name: "invalid: version is negative",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "USD",
				Version:        -1,
			},
			wantErr: true,
		},
		{
			name: "invalid: source exceeds max length",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "USD",
				Version:        1,
				Source:         string(make([]byte, 65)),
			},
			wantErr: true,
		},
		{
			name: "valid: CARBON_CREDIT instrument",
			amount: &quantityv1.InstrumentAmount{
				Amount:         "10.5",
				InstrumentCode: "CARBON_CREDIT",
				Version:        1,
				Attributes: []*quantityv1.AttributeEntry{
					{Key: "vintage_year", Value: "2024"},
					{Key: "registry", Value: "VERRA"},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.amount)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestInstrumentAmountSerialization verifies round-trip serialization.
func TestInstrumentAmountSerialization(t *testing.T) {
	now := timestamppb.Now()
	later := timestamppb.New(now.AsTime().AddDate(0, 0, 30))

	original := &quantityv1.InstrumentAmount{
		Amount:         "1234.567890",
		InstrumentCode: "KWH",
		Version:        3,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "batch_id", Value: "BATCH-001"},
			{Key: "quality_grade", Value: "A"},
			{Key: "location", Value: "SITE-ALPHA"},
		},
		ValidFrom: now,
		ValidTo:   later,
		Source:    "smart_meter",
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &quantityv1.InstrumentAmount{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Error("round-trip serialization produced different message")
	}

	// Verify specific fields
	if decoded.Amount != original.Amount {
		t.Errorf("amount mismatch: got %v, want %v", decoded.Amount, original.Amount)
	}
	if decoded.InstrumentCode != original.InstrumentCode {
		t.Errorf("instrument_code mismatch: got %v, want %v", decoded.InstrumentCode, original.InstrumentCode)
	}
	if len(decoded.Attributes) != len(original.Attributes) {
		t.Errorf("attributes length mismatch: got %d, want %d", len(decoded.Attributes), len(original.Attributes))
	}
	for i, attr := range decoded.Attributes {
		if attr.Key != original.Attributes[i].Key || attr.Value != original.Attributes[i].Value {
			t.Errorf("attribute[%d] mismatch", i)
		}
	}
}

// TestAttributesIsSliceNotMap verifies that attributes field is a slice (for poolability).
func TestAttributesIsSliceNotMap(t *testing.T) {
	// This test verifies the critical design decision: attributes uses repeated
	// (slice) instead of map for deterministic poolability key generation.
	amount := &quantityv1.InstrumentAmount{
		Amount:         "100",
		InstrumentCode: "USD",
		Version:        1,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "a", Value: "1"},
			{Key: "b", Value: "2"},
		},
	}

	// Verify type is slice
	attrs := amount.GetAttributes()
	if attrs == nil {
		t.Fatal("GetAttributes() returned nil")
	}

	// Slice should maintain insertion order (deterministic for poolability)
	if len(attrs) != 2 {
		t.Fatalf("expected 2 attributes, got %d", len(attrs))
	}
	if attrs[0].Key != "a" || attrs[1].Key != "b" {
		t.Error("attribute order not preserved")
	}

	// Verify we can pool/reuse slices across instances
	pool := make([]*quantityv1.AttributeEntry, 0, 10)
	pool = append(pool, &quantityv1.AttributeEntry{Key: "pooled_key", Value: "pooled_value"})

	amount2 := &quantityv1.InstrumentAmount{
		Amount:         "200",
		InstrumentCode: "EUR",
		Version:        1,
		Attributes:     pool,
	}

	if len(amount2.GetAttributes()) != 1 || amount2.GetAttributes()[0].Key != "pooled_key" {
		t.Error("pooled attributes not correctly assigned")
	}
}

// TestDeterministicSerialization verifies deterministic marshaling for attributes.
func TestDeterministicSerialization(t *testing.T) {
	amount := &quantityv1.InstrumentAmount{
		Amount:         "100.00",
		InstrumentCode: "USD",
		Version:        1,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "batch_id", Value: "B001"},
			{Key: "location", Value: "NYC"},
		},
	}

	opts := proto.MarshalOptions{Deterministic: true}

	data1, err := opts.Marshal(amount)
	if err != nil {
		t.Fatalf("first marshal failed: %v", err)
	}

	data2, err := opts.Marshal(amount)
	if err != nil {
		t.Fatalf("second marshal failed: %v", err)
	}

	if !bytes.Equal(data1, data2) {
		t.Error("deterministic marshaling produced different results")
	}
}

// TestInstrumentAmountWithManyAttributes tests handling of many attributes.
func TestInstrumentAmountWithManyAttributes(t *testing.T) {
	// Create amount with 10 attributes
	attrs := make([]*quantityv1.AttributeEntry, 10)
	for i := 0; i < 10; i++ {
		attrs[i] = &quantityv1.AttributeEntry{
			Key:   fmt.Sprintf("attr_%d", i),
			Value: fmt.Sprintf("value_%d", i),
		}
	}

	amount := &quantityv1.InstrumentAmount{
		Amount:         "1000.00",
		InstrumentCode: "KWH",
		Version:        1,
		Attributes:     attrs,
	}

	if err := testValidator.Validate(amount); err != nil {
		t.Errorf("amount with 10 attributes should be valid: %v", err)
	}

	// Verify serialization works
	data, err := proto.Marshal(amount)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &quantityv1.InstrumentAmount{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(decoded.Attributes) != 10 {
		t.Errorf("expected 10 attributes after round-trip, got %d", len(decoded.Attributes))
	}
}

// TestInstrumentAmountWithNoAttributes tests handling of zero attributes.
func TestInstrumentAmountWithNoAttributes(t *testing.T) {
	amount := &quantityv1.InstrumentAmount{
		Amount:         "100.00",
		InstrumentCode: "USD",
		Version:        1,
		// No attributes
	}

	if err := testValidator.Validate(amount); err != nil {
		t.Errorf("amount with no attributes should be valid: %v", err)
	}

	attrs := amount.GetAttributes()
	if len(attrs) != 0 {
		t.Errorf("expected 0 attributes, got %d", len(attrs))
	}
}

// TestProtoSizeReasonable verifies proto size is reasonable for typical use.
func TestProtoSizeReasonable(t *testing.T) {
	// Typical instrument with 5 attributes
	amount := &quantityv1.InstrumentAmount{
		Amount:         "12345.67",
		InstrumentCode: "CARBON_CREDIT",
		Version:        1,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "vintage_year", Value: "2024"},
			{Key: "registry", Value: "VERRA"},
			{Key: "project_id", Value: "VCS-1234"},
			{Key: "serial_start", Value: "100000"},
			{Key: "serial_end", Value: "100099"},
		},
		Source: "registry_import",
	}

	data, err := proto.Marshal(amount)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Typical instrument should be < 1KB
	if len(data) > 1024 {
		t.Errorf("proto size %d exceeds expected <1KB for typical instrument", len(data))
	}
}
