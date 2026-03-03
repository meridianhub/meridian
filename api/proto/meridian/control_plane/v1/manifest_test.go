package controlplanev1_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
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

// validManifest returns a fully-populated valid manifest for testing.
func validManifest() *controlplanev1.Manifest {
	seedData, _ := structpb.NewStruct(map[string]interface{}{
		"default_market": "nordpool",
	})
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:        "Test Manifest",
			Industry:    "energy",
			Description: "A test manifest for energy trading",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "GBP",
				Name: "British Pound Sterling",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GBP",
					Precision: 2,
				},
			},
			{
				Code: "KWH",
				Name: "Kilowatt Hour",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "kWh",
					Precision: 3,
				},
			},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:               "SETTLEMENT",
				Name:               "Settlement Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"GBP"},
				Policies: &controlplanev1.AccountTypePolicies{
					Validation: "amount > 0",
					Bucketing:  "",
				},
			},
		},
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
				Source:         "nordpool_spot",
			},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "process_settlement",
				Trigger: "api:/v1/settlements",
				Script:  "def execute(ctx):\n    return {\"status\": \"ok\"}\n",
			},
		},
		SeedData: seedData,
	}
}

// TestManifestValidation verifies Manifest validation constraints.
func TestManifestValidation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(m *controlplanev1.Manifest)
		wantErr bool
	}{
		{
			name:    "valid full manifest",
			modify:  func(_ *controlplanev1.Manifest) {},
			wantErr: false,
		},
		{
			name: "valid minimal manifest (no optional fields)",
			modify: func(m *controlplanev1.Manifest) {
				m.Instruments = nil
				m.AccountTypes = nil
				m.ValuationRules = nil
				m.Sagas = nil
				m.SeedData = nil
			},
			wantErr: false,
		},
		{
			name: "invalid: missing version",
			modify: func(m *controlplanev1.Manifest) {
				m.Version = ""
			},
			wantErr: true,
		},
		{
			name: "invalid: bad version format",
			modify: func(m *controlplanev1.Manifest) {
				m.Version = "v1.0"
			},
			wantErr: true,
		},
		{
			name: "invalid: version without minor",
			modify: func(m *controlplanev1.Manifest) {
				m.Version = "1"
			},
			wantErr: true,
		},
		{
			name: "invalid: missing metadata",
			modify: func(m *controlplanev1.Manifest) {
				m.Metadata = nil
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.modify(m)
			err := testValidator.Validate(m)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestManifestMetadataValidation verifies ManifestMetadata constraints.
func TestManifestMetadataValidation(t *testing.T) {
	tests := []struct {
		name    string
		meta    *controlplanev1.ManifestMetadata
		wantErr bool
	}{
		{
			name: "valid metadata",
			meta: &controlplanev1.ManifestMetadata{
				Name:        "Nordic Energy Trading",
				Industry:    "energy",
				Description: "Energy trading manifest",
			},
			wantErr: false,
		},
		{
			name: "valid: minimal (only name required)",
			meta: &controlplanev1.ManifestMetadata{
				Name: "Minimal",
			},
			wantErr: false,
		},
		{
			name: "invalid: empty name",
			meta: &controlplanev1.ManifestMetadata{
				Name: "",
			},
			wantErr: true,
		},
		{
			name: "invalid: name exceeds max length",
			meta: &controlplanev1.ManifestMetadata{
				Name: strings.Repeat("x", 256),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.meta)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestInstrumentDefinitionValidation verifies InstrumentDefinition constraints in the manifest context.
func TestInstrumentDefinitionValidation(t *testing.T) {
	tests := []struct {
		name    string
		inst    *controlplanev1.InstrumentDefinition
		wantErr bool
	}{
		{
			name: "valid fiat instrument",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "United States Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
			wantErr: false,
		},
		{
			name: "valid commodity instrument",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "TONNE_CO2E",
				Name: "Tonne of CO2 Equivalent",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "tCO2e",
					Precision: 3,
				},
			},
			wantErr: false,
		},
		{
			name: "valid voucher instrument",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "AID_VOUCHER",
				Name: "Humanitarian Aid Voucher",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_VOUCHER,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "vouchers",
					Precision: 0,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid: empty code",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "",
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 2,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid: lowercase code",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "usd",
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 2,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid: code with hyphens",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "CO2-E",
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "tCO2e",
					Precision: 3,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid: code exceeds 50 chars",
			inst: &controlplanev1.InstrumentDefinition{
				Code: strings.Repeat("A", 51),
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 2,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid: unspecified type",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "TEST",
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 2,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid: missing dimensions",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "TEST",
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing name",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "TEST",
				Name: "",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 2,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid: precision exceeds 18",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "TEST",
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 19,
				},
			},
			wantErr: true,
		},
		{
			name: "valid: code with numbers",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "H2O",
				Name: "Water",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "liters",
					Precision: 0,
				},
			},
			wantErr: false,
		},
		{
			name: "valid: code with underscores",
			inst: &controlplanev1.InstrumentDefinition{
				Code: "GPU_HOUR",
				Name: "GPU Compute Hour",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GPU-hr",
					Precision: 6,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.inst)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestAccountTypeDefinitionValidation verifies AccountTypeDefinition constraints.
func TestAccountTypeDefinitionValidation(t *testing.T) {
	tests := []struct {
		name    string
		acct    *controlplanev1.AccountTypeDefinition
		wantErr bool
	}{
		{
			name: "valid debit account type",
			acct: &controlplanev1.AccountTypeDefinition{
				Code:               "CURRENT",
				Name:               "Current Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"GBP", "USD"},
				Policies: &controlplanev1.AccountTypePolicies{
					Validation: "amount > 0",
					Bucketing:  "",
				},
			},
			wantErr: false,
		},
		{
			name: "valid credit account type",
			acct: &controlplanev1.AccountTypeDefinition{
				Code:          "REVENUE",
				Name:          "Revenue Account",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
			},
			wantErr: false,
		},
		{
			name: "invalid: empty code",
			acct: &controlplanev1.AccountTypeDefinition{
				Code:          "",
				Name:          "Test",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
			},
			wantErr: true,
		},
		{
			name: "invalid: lowercase code",
			acct: &controlplanev1.AccountTypeDefinition{
				Code:          "current",
				Name:          "Test",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
			},
			wantErr: true,
		},
		{
			name: "invalid: unspecified normal balance",
			acct: &controlplanev1.AccountTypeDefinition{
				Code:          "TEST",
				Name:          "Test",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_UNSPECIFIED,
			},
			wantErr: true,
		},
		{
			name: "invalid: allowed_instruments with lowercase code",
			acct: &controlplanev1.AccountTypeDefinition{
				Code:               "TEST",
				Name:               "Test",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"gbp"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.acct)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValuationRuleValidation verifies ValuationRule constraints.
func TestValuationRuleValidation(t *testing.T) {
	tests := []struct {
		name    string
		rule    *controlplanev1.ValuationRule
		wantErr bool
	}{
		{
			name: "valid spot rate rule",
			rule: &controlplanev1.ValuationRule{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
				Source:         "nordpool_spot",
			},
			wantErr: false,
		},
		{
			name: "valid fixed rate rule",
			rule: &controlplanev1.ValuationRule{
				FromInstrument: "CREDIT",
				ToInstrument:   "USD",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
				Source:         "admin_override",
			},
			wantErr: false,
		},
		{
			name: "invalid: empty from_instrument",
			rule: &controlplanev1.ValuationRule{
				FromInstrument: "",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty to_instrument",
			rule: &controlplanev1.ValuationRule{
				FromInstrument: "KWH",
				ToInstrument:   "",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
			},
			wantErr: true,
		},
		{
			name: "invalid: unspecified method",
			rule: &controlplanev1.ValuationRule{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_UNSPECIFIED,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.rule)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestSagaDefinitionValidation verifies SagaDefinition constraints.
func TestSagaDefinitionValidation(t *testing.T) {
	tests := []struct {
		name    string
		saga    *controlplanev1.SagaDefinition
		wantErr bool
	}{
		{
			name: "valid API trigger",
			saga: &controlplanev1.SagaDefinition{
				Name:    "process_payment",
				Trigger: "api:/v1/payments",
				Script:  "def execute(ctx):\n    return {}\n",
			},
			wantErr: false,
		},
		{
			name: "valid webhook trigger",
			saga: &controlplanev1.SagaDefinition{
				Name:    "handle_stripe_event",
				Trigger: "webhook:stripe_payment_confirmed",
				Script:  "def execute(ctx):\n    return {}\n",
			},
			wantErr: false,
		},
		{
			name: "valid scheduled trigger",
			saga: &controlplanev1.SagaDefinition{
				Name:    "daily_reconciliation",
				Trigger: "scheduled:daily_at_0200",
				Script:  "def execute(ctx):\n    return {}\n",
			},
			wantErr: false,
		},
		{
			name: "valid multi-line script",
			saga: &controlplanev1.SagaDefinition{
				Name:    "complex_workflow",
				Trigger: "api:/v1/workflows",
				Script: `def execute(ctx):
    order = ctx.input

    # Create lien
    lien = current_account.initiate_lien(
        account_id=order.account_id,
        amount=order.amount,
        instrument_code="GBP",
    )

    # Process payment
    current_account.execute_lien(lien_id=lien.lien_id)

    return {"status": "completed"}
`,
			},
			wantErr: false,
		},
		{
			name: "invalid: empty name",
			saga: &controlplanev1.SagaDefinition{
				Name:    "",
				Trigger: "api:/v1/test",
				Script:  "def execute(ctx):\n    pass\n",
			},
			wantErr: true,
		},
		{
			name: "invalid: uppercase name",
			saga: &controlplanev1.SagaDefinition{
				Name:    "ProcessPayment",
				Trigger: "api:/v1/test",
				Script:  "def execute(ctx):\n    pass\n",
			},
			wantErr: true,
		},
		{
			name: "invalid: name starting with number",
			saga: &controlplanev1.SagaDefinition{
				Name:    "1_invalid_name",
				Trigger: "api:/v1/test",
				Script:  "def execute(ctx):\n    pass\n",
			},
			wantErr: true,
		},
		{
			name: "invalid: trigger without prefix",
			saga: &controlplanev1.SagaDefinition{
				Name:    "test_saga",
				Trigger: "no_prefix",
				Script:  "def execute(ctx):\n    pass\n",
			},
			wantErr: true,
		},
		{
			name: "invalid: empty trigger",
			saga: &controlplanev1.SagaDefinition{
				Name:    "test_saga",
				Trigger: "",
				Script:  "def execute(ctx):\n    pass\n",
			},
			wantErr: true,
		},
		{
			name: "invalid: empty script",
			saga: &controlplanev1.SagaDefinition{
				Name:    "test_saga",
				Trigger: "api:/v1/test",
				Script:  "",
			},
			wantErr: true,
		},
		{
			name: "valid event trigger with filter",
			saga: &controlplanev1.SagaDefinition{
				Name:    "on_transaction_captured",
				Trigger: "event:position-keeping.transaction-captured.v1",
				Script:  "def execute(ctx):\n    return {}\n",
				Filter:  proto.String(`event.amount > 0 && event.currency == "GBP"`),
			},
			wantErr: false,
		},
		{
			name: "valid event trigger without filter",
			saga: &controlplanev1.SagaDefinition{
				Name:    "on_transaction_captured",
				Trigger: "event:position-keeping.transaction-captured.v1",
				Script:  "def execute(ctx):\n    return {}\n",
			},
			wantErr: false,
		},
		{
			name: "invalid: filter exceeds max length",
			saga: &controlplanev1.SagaDefinition{
				Name:    "test_saga",
				Trigger: "event:some.event.v1",
				Script:  "def execute(ctx):\n    return {}\n",
				Filter:  proto.String(strings.Repeat("x", 4097)),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.saga)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestSagaDefinitionEventTrigger verifies event trigger round-trip serialization.
func TestSagaDefinitionEventTrigger(t *testing.T) {
	filter := `event.amount > 0 && event.currency == "GBP"`
	original := &controlplanev1.SagaDefinition{
		Name:    "on_transaction_captured",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  proto.String(filter),
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &controlplanev1.SagaDefinition{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Error("round-trip proto serialization produced different message")
	}
	if decoded.GetFilter() != filter {
		t.Errorf("expected filter %q, got %q", filter, decoded.GetFilter())
	}
}

// TestManifestRoundtrip verifies proto -> marshal -> unmarshal preserves all fields.
func TestManifestRoundtrip(t *testing.T) {
	original := validManifest()

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &controlplanev1.Manifest{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Error("round-trip proto serialization produced different message")
	}

	// Verify specific fields survived the roundtrip
	if decoded.Version != "1.0" {
		t.Errorf("version mismatch: got %v, want 1.0", decoded.Version)
	}
	if decoded.Metadata.Name != "Test Manifest" {
		t.Errorf("metadata.name mismatch: got %v", decoded.Metadata.Name)
	}
	if len(decoded.Instruments) != 2 {
		t.Errorf("instruments count mismatch: got %d, want 2", len(decoded.Instruments))
	}
	if len(decoded.AccountTypes) != 1 {
		t.Errorf("account_types count mismatch: got %d, want 1", len(decoded.AccountTypes))
	}
	if len(decoded.ValuationRules) != 1 {
		t.Errorf("valuation_rules count mismatch: got %d, want 1", len(decoded.ValuationRules))
	}
	if len(decoded.Sagas) != 1 {
		t.Errorf("sagas count mismatch: got %d, want 1", len(decoded.Sagas))
	}
	if decoded.Sagas[0].Script != original.Sagas[0].Script {
		t.Error("saga script not preserved after roundtrip")
	}
}

// TestManifestJSONRoundtrip verifies proto -> JSON -> proto preserves all fields.
func TestManifestJSONRoundtrip(t *testing.T) {
	original := validManifest()

	// Marshal to JSON
	marshaler := protojson.MarshalOptions{
		Multiline: true,
		Indent:    "  ",
	}
	jsonData, err := marshaler.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal to JSON: %v", err)
	}

	// Verify it's valid JSON
	if !json.Valid(jsonData) {
		t.Fatal("marshaled data is not valid JSON")
	}

	// Unmarshal from JSON
	decoded := &controlplanev1.Manifest{}
	if err := protojson.Unmarshal(jsonData, decoded); err != nil {
		t.Fatalf("failed to unmarshal from JSON: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Error("round-trip JSON serialization produced different message")
	}

	// Verify multi-line script survived JSON encoding
	if decoded.Sagas[0].Script != original.Sagas[0].Script {
		t.Error("multi-line Starlark script not preserved through JSON roundtrip")
	}
}

// TestEnumValues verifies enum values are correctly defined.
func TestEnumValues(t *testing.T) {
	t.Run("InstrumentType", func(t *testing.T) {
		expected := map[int32]string{
			0: "INSTRUMENT_TYPE_UNSPECIFIED",
			1: "INSTRUMENT_TYPE_FIAT",
			2: "INSTRUMENT_TYPE_COMMODITY",
			3: "INSTRUMENT_TYPE_VOUCHER",
		}
		for val, name := range expected {
			if controlplanev1.InstrumentType_name[val] != name {
				t.Errorf("InstrumentType %d: got %s, want %s",
					val, controlplanev1.InstrumentType_name[val], name)
			}
		}
	})

	t.Run("NormalBalance", func(t *testing.T) {
		expected := map[int32]string{
			0: "NORMAL_BALANCE_UNSPECIFIED",
			1: "NORMAL_BALANCE_DEBIT",
			2: "NORMAL_BALANCE_CREDIT",
		}
		for val, name := range expected {
			if controlplanev1.NormalBalance_name[val] != name {
				t.Errorf("NormalBalance %d: got %s, want %s",
					val, controlplanev1.NormalBalance_name[val], name)
			}
		}
	})

	t.Run("ValuationMethod", func(t *testing.T) {
		expected := map[int32]string{
			0: "VALUATION_METHOD_UNSPECIFIED",
			1: "VALUATION_METHOD_SPOT_RATE",
			2: "VALUATION_METHOD_FIXED",
		}
		for val, name := range expected {
			if controlplanev1.ValuationMethod_name[val] != name {
				t.Errorf("ValuationMethod %d: got %s, want %s",
					val, controlplanev1.ValuationMethod_name[val], name)
			}
		}
	})
}

// TestInstrumentCodeRegex verifies the ^[A-Z0-9_]{1,50}$ pattern works correctly.
func TestInstrumentCodeRegex(t *testing.T) {
	validCodes := []string{
		"GBP", "USD", "EUR", "KWH", "MWH",
		"TONNE_CO2E", "GPU_HOUR", "CARBON_CREDIT",
		"A", "ABC123", "A_B_C",
		strings.Repeat("A", 50), // max length
	}

	invalidCodes := []string{
		"", "gbp", "usd", // empty/lowercase
		"CO2-E", "test.code", // special chars
		"abc", "a_b", // lowercase
		strings.Repeat("A", 51), // exceeds max
	}

	for _, code := range validCodes {
		t.Run("valid:"+code, func(t *testing.T) {
			inst := &controlplanev1.InstrumentDefinition{
				Code: code,
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 2,
				},
			}
			if err := testValidator.Validate(inst); err != nil {
				t.Errorf("code %q should be valid: %v", code, err)
			}
		})
	}

	for _, code := range invalidCodes {
		t.Run("invalid:"+code, func(t *testing.T) {
			inst := &controlplanev1.InstrumentDefinition{
				Code: code,
				Name: "Test",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "TST",
					Precision: 2,
				},
			}
			if err := testValidator.Validate(inst); err == nil {
				t.Errorf("code %q should be invalid", code)
			}
		})
	}
}
