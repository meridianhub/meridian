package validator

import (
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validPaymentRails returns a valid PaymentRails for testing.
func validPaymentRails() *controlplanev1.PaymentRails {
	return &controlplanev1.PaymentRails{
		Provider:              "stripe_connect",
		Mode:                  controlplanev1.ConnectMode_CONNECT_MODE_STANDARD,
		AccountId:             "acct_1234567890abcdef",
		WebhookEndpointSecret: "sm://stripe/webhook_secret",
		PlatformFee: &controlplanev1.PlatformFee{
			Type:  controlplanev1.PlatformFeeType_PLATFORM_FEE_TYPE_PERCENTAGE,
			Value: "2.5",
		},
		PayoutSchedule:   controlplanev1.PayoutSchedule_PAYOUT_SCHEDULE_DAILY,
		SupportedMethods: []string{"card", "sepa_debit"},
	}
}

func TestValidatePaymentRails_Valid(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	m.PaymentRails = []*controlplanev1.PaymentRails{validPaymentRails()}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with payment_rails, got errors: %v", result.Errors)
	}
}

func TestValidatePaymentRails_InvalidProvider(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	rail := validPaymentRails()
	rail.Provider = "paypal"
	m.PaymentRails = []*controlplanev1.PaymentRails{rail}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for unsupported provider")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_PAYMENT_PROVIDER" {
			found = true
			if !strings.Contains(e.Message, "paypal") {
				t.Errorf("expected error message to contain 'paypal', got: %s", e.Message)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected INVALID_PAYMENT_PROVIDER error, got: %v", result.Errors)
	}
}

func TestValidatePaymentRails_InvalidAccountIDFormat(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	tests := []struct {
		name      string
		accountID string
	}{
		{"missing_prefix", "1234567890abcdef12"},
		{"wrong_prefix", "cust_1234567890abcdef"},
		{"too_short", "acct_abc"},
		{"special_chars", "acct_1234567890abcdef!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			rail := validPaymentRails()
			rail.AccountId = tt.accountID
			m.PaymentRails = []*controlplanev1.PaymentRails{rail}

			result := v.Validate(m, nil)

			found := false
			for _, e := range result.Errors {
				if e.Code == "INVALID_ACCOUNT_ID_FORMAT" {
					found = true
					break
				}
			}
			// Proto validation or our custom validation should catch this
			if !found {
				// Check for proto validation catching it instead
				protoFound := false
				for _, e := range result.Errors {
					if e.Code == "PROTO_VALIDATION" && strings.Contains(e.Path, "account_id") {
						protoFound = true
						break
					}
				}
				if !protoFound {
					t.Errorf("expected INVALID_ACCOUNT_ID_FORMAT or PROTO_VALIDATION error for account_id %q, got: %v", tt.accountID, result.Errors)
				}
			}
		})
	}
}

func TestValidatePaymentRails_InvalidPlatformFeeValue_NonDecimal(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	rail := validPaymentRails()
	rail.PlatformFee.Value = "not-a-number"
	m.PaymentRails = []*controlplanev1.PaymentRails{rail}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for non-decimal platform fee")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_PLATFORM_FEE_VALUE" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected INVALID_PLATFORM_FEE_VALUE error, got: %v", result.Errors)
	}
}

func TestValidatePaymentRails_InvalidPlatformFeeValue_Negative(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	rail := validPaymentRails()
	rail.PlatformFee.Value = "-1.5"
	m.PaymentRails = []*controlplanev1.PaymentRails{rail}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for negative platform fee")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_PLATFORM_FEE_VALUE" {
			found = true
			if !strings.Contains(e.Message, "greater than 0") {
				t.Errorf("expected message about positive value, got: %s", e.Message)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected INVALID_PLATFORM_FEE_VALUE error, got: %v", result.Errors)
	}
}

func TestValidatePaymentRails_InvalidPlatformFeeValue_Zero(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	rail := validPaymentRails()
	rail.PlatformFee.Value = "0"
	m.PaymentRails = []*controlplanev1.PaymentRails{rail}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for zero platform fee")
	}

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_PLATFORM_FEE_VALUE" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected INVALID_PLATFORM_FEE_VALUE error, got: %v", result.Errors)
	}
}

func TestValidatePaymentRails_UnknownPaymentMethod(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	rail := validPaymentRails()
	rail.SupportedMethods = []string{"card", "crypto_wallet"}
	m.PaymentRails = []*controlplanev1.PaymentRails{rail}

	result := v.Validate(m, nil)
	// Unknown methods produce warnings, not errors
	if !result.Valid {
		t.Errorf("expected valid manifest (unknown methods are warnings), got errors: %v", result.Errors)
	}

	found := false
	for _, w := range result.Warnings {
		if w.Code == "UNKNOWN_PAYMENT_METHOD" && strings.Contains(w.Message, "crypto_wallet") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected UNKNOWN_PAYMENT_METHOD warning for 'crypto_wallet', got warnings: %v", result.Warnings)
	}
}

func TestValidatePaymentRails_MissingRequiredFields(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// Empty PaymentRails should fail proto validation
	m.PaymentRails = []*controlplanev1.PaymentRails{{}}

	result := v.Validate(m, nil)
	if result.Valid {
		t.Error("expected invalid manifest for empty payment_rails entry")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error for missing required fields")
	}
}

func TestValidatePaymentRails_ValidFlatFee(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	rail := validPaymentRails()
	rail.PlatformFee = &controlplanev1.PlatformFee{
		Type:  controlplanev1.PlatformFeeType_PLATFORM_FEE_TYPE_FLAT,
		Value: "0.30",
	}
	m.PaymentRails = []*controlplanev1.PaymentRails{rail}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with flat fee, got errors: %v", result.Errors)
	}
}

func TestValidatePaymentRails_MultipleRails(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	rail1 := validPaymentRails()
	rail2 := validPaymentRails()
	rail2.AccountId = "acct_abcdefghijklmnop"
	rail2.Mode = controlplanev1.ConnectMode_CONNECT_MODE_EXPRESS
	m.PaymentRails = []*controlplanev1.PaymentRails{rail1, rail2}

	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest with multiple payment rails, got errors: %v", result.Errors)
	}
}

func TestValidatePaymentRails_NoPaymentRails(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	m := validManifest()
	// No payment_rails field set - should be valid
	result := v.Validate(m, nil)
	if !result.Valid {
		t.Errorf("expected valid manifest without payment_rails, got errors: %v", result.Errors)
	}
}

func TestValidateImmutability_AddNewInstrument(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	prev := validManifest()
	curr := validManifest()
	curr.Instruments = append(curr.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "EUR",
		Name: "Euro",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "EUR",
			Precision: 2,
		},
	})

	result := v.Validate(curr, prev)
	if !result.Valid {
		t.Errorf("expected valid manifest when adding new instrument, got errors: %v", result.Errors)
	}
}

// --- Party type validator tests ---

func TestValidatePartyTypes_ValidDefinition(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-person-001",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object", "properties": {"name": {"type": "string"}}}`,
		},
	}

	result := v.Validate(manifest, nil)
	assert.True(t, result.Valid, "valid party type should pass validation, errors: %v", result.Errors)
}

func TestValidatePartyTypes_InvalidJSON_Schema(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-person-001",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{not valid json`,
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)
	// Find the JSON schema error (there may be other errors)
	var jsonSchemaErr *ValidationError
	for i := range result.Errors {
		if result.Errors[i].Code == "INVALID_JSON_SCHEMA" {
			jsonSchemaErr = &result.Errors[i]
			break
		}
	}
	require.NotNil(t, jsonSchemaErr, "expected INVALID_JSON_SCHEMA error, got: %v", result.Errors)
	assert.Contains(t, jsonSchemaErr.Path, "party_types[0].attribute_schema")
}

func TestValidatePartyTypes_DuplicatePartyType(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-person-001",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object"}`,
		},
		{
			Id:              "ptd-person-002",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object", "properties": {}}`,
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)
	var dupErr *ValidationError
	for i := range result.Errors {
		if result.Errors[i].Code == "DUPLICATE_PARTY_TYPE" {
			dupErr = &result.Errors[i]
			break
		}
	}
	require.NotNil(t, dupErr, "expected DUPLICATE_PARTY_TYPE error, got: %v", result.Errors)
	assert.Contains(t, dupErr.Path, "party_types[1].party_type")
}

func TestValidatePartyTypes_DifferentTenants_SamePartyType_Valid(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-t1-person",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object"}`,
		},
		{
			Id:              "ptd-t2-person",
			TenantId:        "tenant-2",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object"}`,
		},
	}

	result := v.Validate(manifest, nil)
	assert.True(t, result.Valid, "same party type for different tenants should be valid, errors: %v", result.Errors)
}

func TestValidatePartyTypes_ValidCELExpressions(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-person-001",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object"}`,
			ValidationCel:   "party_type == \"PERSON\"",
			EligibilityCel:  "party_type != \"\"",
		},
	}

	result := v.Validate(manifest, nil)
	assert.True(t, result.Valid, "valid CEL expressions should pass, errors: %v", result.Errors)
}

func TestValidatePartyTypes_InvalidValidationCEL(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-person-001",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object"}`,
			ValidationCel:   "undeclared_var > 0",
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)
	var celErr *ValidationError
	for i := range result.Errors {
		if result.Errors[i].Code == "CEL_UNDECLARED_REFERENCE" {
			celErr = &result.Errors[i]
			break
		}
	}
	require.NotNil(t, celErr, "expected CEL_UNDECLARED_REFERENCE error, got: %v", result.Errors)
	assert.Contains(t, celErr.Path, "party_types[0].validation_cel")
}

func TestValidatePartyTypes_InvalidEligibilityCEL(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-person-001",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{"type": "object"}`,
			EligibilityCel:  "invalid_field_name + 1",
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)
	var celErr *ValidationError
	for i := range result.Errors {
		if result.Errors[i].Code == "CEL_UNDECLARED_REFERENCE" {
			celErr = &result.Errors[i]
			break
		}
	}
	require.NotNil(t, celErr, "expected CEL_UNDECLARED_REFERENCE error, got: %v", result.Errors)
	assert.Contains(t, celErr.Path, "party_types[0].eligibility_cel")
}

func TestValidatePartyTypes_EmptyPartyTypes_Valid(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	// No party_types field - should be valid
	result := v.Validate(manifest, nil)
	assert.True(t, result.Valid, "manifest with no party types should be valid")
}

func TestValidatePartyTypes_MultipleErrors_ReportedAll(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	manifest.PartyTypes = []*partyv1.PartyTypeDefinition{
		{
			Id:              "ptd-person-001",
			TenantId:        "tenant-1",
			PartyType:       "PERSON",
			AttributeSchema: `{bad json`,
			ValidationCel:   "unknown_var > 0",
		},
	}

	result := v.Validate(manifest, nil)
	assert.False(t, result.Valid)
	// Should have at least: INVALID_JSON_SCHEMA + CEL_UNDECLARED_REFERENCE
	codes := make([]string, 0, len(result.Errors))
	for _, e := range result.Errors {
		codes = append(codes, e.Code)
	}
	assert.Contains(t, codes, "INVALID_JSON_SCHEMA")
	assert.Contains(t, codes, "CEL_UNDECLARED_REFERENCE")
}

// --- Webhook trigger validation tests (Task 3) ---

func TestValidate_WebhookTrigger_UnknownSource_Error(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_payment_webhook",
		Trigger: "webhook:nonexistent.payment.succeeded",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNKNOWN_WEBHOOK_SOURCE" {
			found = true
			assert.Contains(t, e.Message, "nonexistent")
			assert.Equal(t, "sagas[1].trigger", e.Path)
			break
		}
	}
	assert.True(t, found, "expected UNKNOWN_WEBHOOK_SOURCE error, got errors: %v", result.Errors)
}

func TestValidate_WebhookTrigger_ValidSource_Passes(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{
				ConnectionId: "stripe-payments",
				ProviderName: "Stripe",
				ProviderType: "payment_gateway",
				Protocol:     controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS,
			},
		},
	}
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_stripe_webhook",
		Trigger: "webhook:stripe-payments.payment.succeeded",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "UNKNOWN_WEBHOOK_SOURCE", e.Code,
			"unexpected UNKNOWN_WEBHOOK_SOURCE error: %v", e)
	}
}

func TestValidate_WebhookTrigger_SuggestsCloseMatch(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{
				ConnectionId: "stripe-payments",
				ProviderName: "Stripe",
				ProviderType: "payment_gateway",
				Protocol:     controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS,
			},
		},
	}
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_stripe_webhook",
		Trigger: "webhook:stripe-payment.payment.succeeded",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := v.Validate(m, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "UNKNOWN_WEBHOOK_SOURCE" {
			found = true
			assert.Contains(t, e.Suggestion, "stripe-payments")
			assert.Contains(t, e.AvailableFields, "stripe-payments")
			break
		}
	}
	assert.True(t, found, "expected UNKNOWN_WEBHOOK_SOURCE error with suggestion")
}

func TestValidate_WebhookTrigger_NoOperationalGateway_Error(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.OperationalGateway = nil
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_webhook",
		Trigger: "webhook:some-provider.event.received",
		Script:  "def execute(ctx):\n    return {}\n",
	})

	result := v.Validate(m, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "UNKNOWN_WEBHOOK_SOURCE" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected UNKNOWN_WEBHOOK_SOURCE error when no operational_gateway defined")
}

// --- Scheduled trigger uniqueness tests (Task 4) ---

func TestValidate_ScheduledTrigger_DuplicateName_Error(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "billing_saga_1",
			Trigger: "scheduled:monthly_billing",
			Script:  "def execute(ctx):\n    return {}\n",
		},
		{
			Name:    "another_saga",
			Trigger: "api:/v1/tenants",
			Script:  "def execute(ctx):\n    return {}\n",
		},
		{
			Name:    "billing_saga_2",
			Trigger: "scheduled:monthly_billing",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_SCHEDULED_TRIGGER" {
			found = true
			assert.Equal(t, "sagas[2].trigger", e.Path)
			assert.Contains(t, e.Message, "monthly_billing")
			assert.Contains(t, e.Message, "sagas[0]")
			break
		}
	}
	assert.True(t, found, "expected DUPLICATE_SCHEDULED_TRIGGER error, got errors: %v", result.Errors)
}

func TestValidate_ScheduledTrigger_UniqueNames_Passes(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "daily_report",
			Trigger: "scheduled:daily_report",
			Script:  "def execute(ctx):\n    return {}\n",
		},
		{
			Name:    "monthly_billing",
			Trigger: "scheduled:monthly_billing",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "DUPLICATE_SCHEDULED_TRIGGER", e.Code,
			"unexpected DUPLICATE_SCHEDULED_TRIGGER error: %v", e)
	}
}

func TestValidate_ScheduledTrigger_SameNameDifferentTriggerType_NoConflict(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "monthly_billing_scheduled",
			Trigger: "scheduled:monthly_billing",
			Script:  "def execute(ctx):\n    return {}\n",
		},
		{
			Name:    "monthly_billing_api",
			Trigger: "api:/v1/postings",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "DUPLICATE_SCHEDULED_TRIGGER", e.Code,
			"unexpected DUPLICATE_SCHEDULED_TRIGGER error: %v", e)
	}
}

// ─── Task 2: API Trigger Validation Tests ───────────────────────────────────

func TestValidateAPITriggers_UnknownEndpoint(t *testing.T) {
	v, err := New(WithOpenAPIPaths(map[string]bool{
		"/v1/deposits":    true,
		"/v1/withdrawals": true,
	}))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "handle_transfers",
			Trigger: "api:/v1/transfers",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "UNKNOWN_API_ENDPOINT" {
			found = true
			assert.Contains(t, e.Message, "/v1/transfers")
			assert.NotEmpty(t, e.AvailableFields)
			break
		}
	}
	assert.True(t, found, "expected UNKNOWN_API_ENDPOINT error, got: %v", result.Errors)
}

func TestValidateAPITriggers_UnknownEndpoint_WithSuggestion(t *testing.T) {
	v, err := New(WithOpenAPIPaths(map[string]bool{
		"/v1/deposits":    true,
		"/v1/withdrawals": true,
	}))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "handle_deposit_typo",
			Trigger: "api:/v1/depositz",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	for _, e := range result.Errors {
		if e.Code == "UNKNOWN_API_ENDPOINT" {
			assert.Contains(t, e.Suggestion, "/v1/deposits")
			return
		}
	}
	t.Error("expected UNKNOWN_API_ENDPOINT error with suggestion")
}

func TestValidateAPITriggers_DuplicatePath(t *testing.T) {
	v, err := New(WithOpenAPIPaths(map[string]bool{
		"/v1/deposits": true,
	}))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "deposit_handler",
			Trigger: "api:/v1/deposits",
			Script:  "def execute(ctx):\n    return {}\n",
		},
		{
			Name:    "deposit_handler_v2",
			Trigger: "api:/v1/deposits",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_API_TRIGGER" {
			found = true
			assert.Contains(t, e.Message, "/v1/deposits")
			break
		}
	}
	assert.True(t, found, "expected DUPLICATE_API_TRIGGER error, got: %v", result.Errors)
}

func TestValidateAPITriggers_InvalidPathFormat(t *testing.T) {
	v, err := New(WithOpenAPIPaths(map[string]bool{
		"/v1/deposits": true,
	}))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "bad_path",
			Trigger: "api:v1/deposits",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	assert.False(t, result.Valid)

	found := false
	for _, e := range result.Errors {
		if e.Code == "INVALID_API_PATH_FORMAT" {
			found = true
			assert.Contains(t, e.Message, "must start with '/'")
			break
		}
	}
	assert.True(t, found, "expected INVALID_API_PATH_FORMAT error, got: %v", result.Errors)
}

func TestValidateAPITriggers_ValidPath(t *testing.T) {
	v, err := New(WithOpenAPIPaths(map[string]bool{
		"/v1/deposits": true,
	}))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "deposit_handler",
			Trigger: "api:/v1/deposits",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "UNKNOWN_API_ENDPOINT", e.Code)
		assert.NotEqual(t, "INVALID_API_PATH_FORMAT", e.Code)
		assert.NotEqual(t, "DUPLICATE_API_TRIGGER", e.Code)
	}
}

func TestValidateAPITriggers_SkippedWhenNoSpec(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "any_path",
			Trigger: "api:/v1/anything",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)
	for _, e := range result.Errors {
		assert.NotEqual(t, "UNKNOWN_API_ENDPOINT", e.Code)
	}
}

func TestValidateAPITriggers_FormatAndDuplicateChecksWithoutSpec(t *testing.T) {
	v, err := New(WithOpenAPIPaths(nil))
	require.NoError(t, err)

	m := validManifest()
	m.Sagas = []*controlplanev1.SagaDefinition{
		{
			Name:    "bad_format",
			Trigger: "api:no-leading-slash",
			Script:  "def execute(ctx):\n    return {}\n",
		},
		{
			Name:    "dup_1",
			Trigger: "api:/v1/duped",
			Script:  "def execute(ctx):\n    return {}\n",
		},
		{
			Name:    "dup_2",
			Trigger: "api:/v1/duped",
			Script:  "def execute(ctx):\n    return {}\n",
		},
	}

	result := v.Validate(m, nil)

	foundFormat, foundDup, foundUnknown := false, false, false
	for _, e := range result.Errors {
		switch e.Code {
		case "INVALID_API_PATH_FORMAT":
			foundFormat = true
		case "DUPLICATE_API_TRIGGER":
			foundDup = true
		case "UNKNOWN_API_ENDPOINT":
			foundUnknown = true
		}
	}
	assert.True(t, foundFormat, "format check should fire without spec")
	assert.True(t, foundDup, "duplicate check should fire without spec")
	assert.False(t, foundUnknown, "endpoint existence check should be skipped without spec")
}

func TestParseOpenAPIPaths(t *testing.T) {
	spec := `{
		"swagger": "2.0",
		"paths": {
			"/v1/deposits": {},
			"/v1/withdrawals": {},
			"/v1/accounts/{id}": {}
		}
	}`

	paths := parseOpenAPIPaths([]byte(spec))
	assert.Len(t, paths, 3)
	assert.True(t, paths["/v1/deposits"])
	assert.True(t, paths["/v1/withdrawals"])
	assert.True(t, paths["/v1/accounts/{id}"])
}

func TestParseOpenAPIPaths_InvalidJSON(t *testing.T) {
	paths := parseOpenAPIPaths([]byte("not json"))
	assert.Nil(t, paths)
}

// ─── Task 5: AsyncAPI CEL Field Validation Tests ────────────────────────────

func TestValidateEventFilterCELFields_UnknownField_Warning(t *testing.T) {
	asyncSchemas := map[string]map[string]bool{
		"position-keeping.transaction-captured.v1": {
			"log_id":          true,
			"account_id":      true,
			"transaction_id":  true,
			"amount_cents":    true,
			"instrument_code": true,
			"direction":       true,
		},
	}

	v, err := New(WithAsyncAPISchemas(asyncSchemas))
	require.NoError(t, err)

	filter := `event.typo_field == "X"`
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_captured",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	result := v.Validate(m, nil)
	assert.True(t, result.Valid, "warnings should not block validation, errors: %v", result.Errors)

	found := false
	for _, w := range result.Warnings {
		if w.Code == "CEL_UNKNOWN_EVENT_FIELD" {
			found = true
			assert.Contains(t, w.Message, "typo_field")
			assert.NotEmpty(t, w.AvailableFields)
			break
		}
	}
	assert.True(t, found, "expected CEL_UNKNOWN_EVENT_FIELD warning, got warnings: %v", result.Warnings)
}

func TestValidateEventFilterCELFields_UnknownField_WithSuggestion(t *testing.T) {
	asyncSchemas := map[string]map[string]bool{
		"position-keeping.transaction-captured.v1": {
			"amount_cents":    true,
			"instrument_code": true,
		},
	}

	v, err := New(WithAsyncAPISchemas(asyncSchemas))
	require.NoError(t, err)

	filter := `event.amount_cent > 0`
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_captured_typo",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	result := v.Validate(m, nil)

	for _, w := range result.Warnings {
		if w.Code == "CEL_UNKNOWN_EVENT_FIELD" {
			assert.Contains(t, w.Suggestion, "amount_cents")
			return
		}
	}
	t.Error("expected CEL_UNKNOWN_EVENT_FIELD warning with suggestion")
}

func TestValidateEventFilterCELFields_KnownFields_NoWarning(t *testing.T) {
	asyncSchemas := map[string]map[string]bool{
		"position-keeping.transaction-captured.v1": {
			"amount_cents":    true,
			"instrument_code": true,
			"direction":       true,
		},
	}

	v, err := New(WithAsyncAPISchemas(asyncSchemas))
	require.NoError(t, err)

	filter := `event.amount_cents > 0 && event.direction == "CREDIT"`
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_captured_valid",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "CEL_UNKNOWN_EVENT_FIELD", w.Code,
			"unexpected CEL_UNKNOWN_EVENT_FIELD warning: %v", w)
	}
}

func TestValidateEventFilterCELFields_NoSchema_NoWarning(t *testing.T) {
	asyncSchemas := map[string]map[string]bool{
		"some.other.topic.v1": {"field_a": true},
	}

	v, err := New(WithAsyncAPISchemas(asyncSchemas))
	require.NoError(t, err)

	filter := `event.any_field > 0`
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_captured_no_schema",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "CEL_UNKNOWN_EVENT_FIELD", w.Code)
	}
}

func TestValidateEventFilterCELFields_NilSchemas_NoWarning(t *testing.T) {
	v, err := New(WithAsyncAPISchemas(nil))
	require.NoError(t, err)

	filter := `event.any_field > 0`
	m := validManifest()
	m.Sagas = append(m.Sagas, &controlplanev1.SagaDefinition{
		Name:    "on_captured_nil_schemas",
		Trigger: "event:position-keeping.transaction-captured.v1",
		Script:  "def execute(ctx):\n    return {}\n",
		Filter:  &filter,
	})

	result := v.Validate(m, nil)
	for _, w := range result.Warnings {
		assert.NotEqual(t, "CEL_UNKNOWN_EVENT_FIELD", w.Code)
	}
}

func TestExtractCELFieldRefs(t *testing.T) {
	env, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
	)
	require.NoError(t, err)

	tests := []struct {
		name     string
		expr     string
		expected []string
	}{
		{
			name:     "single field",
			expr:     `event.amount > 0`,
			expected: []string{"amount"},
		},
		{
			name:     "multiple fields",
			expr:     `event.amount > 0 && event.currency == "GBP"`,
			expected: []string{"amount", "currency"},
		},
		{
			name:     "bracket notation",
			expr:     `event["amount_cents"] > 0`,
			expected: []string{"amount_cents"},
		},
		{
			name:     "mixed dot and bracket",
			expr:     `event.currency == "GBP" && event["amount"] > 0`,
			expected: []string{"amount", "currency"},
		},
		{
			name:     "no event fields",
			expr:     `true`,
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := extractCELFieldRefs(tt.expr, env)
			assert.Equal(t, tt.expected, fields)
		})
	}
}

func TestParseAsyncAPIFile(t *testing.T) {
	data := []byte(`
asyncapi: 3.0.0
channels:
  test.topic.v1:
    messages:
      TestEvent:
        $ref: '#/components/messages/TestEvent'
components:
  messages:
    TestEvent:
      payload:
        $ref: '#/components/schemas/TestEvent'
  schemas:
    TestEvent:
      type: object
      properties:
        field_a:
          type: string
        field_b:
          type: integer
`)

	schemas := make(map[string]map[string]bool)
	parseAsyncAPIFile(data, schemas)

	assert.Contains(t, schemas, "test.topic.v1")
	assert.True(t, schemas["test.topic.v1"]["field_a"])
	assert.True(t, schemas["test.topic.v1"]["field_b"])
}

func TestParseAsyncAPIFile_MergesFieldsAcrossMessages(t *testing.T) {
	data := []byte(`
asyncapi: 3.0.0
channels:
  orders.topic.v1:
    messages:
      OrderCreated:
        $ref: '#/components/messages/OrderCreated'
      OrderUpdated:
        $ref: '#/components/messages/OrderUpdated'
components:
  messages:
    OrderCreated:
      payload:
        $ref: '#/components/schemas/OrderCreatedPayload'
    OrderUpdated:
      payload:
        $ref: '#/components/schemas/OrderUpdatedPayload'
  schemas:
    OrderCreatedPayload:
      type: object
      properties:
        order_id:
          type: string
        amount:
          type: number
    OrderUpdatedPayload:
      type: object
      properties:
        order_id:
          type: string
        status:
          type: string
`)

	schemas := make(map[string]map[string]bool)
	parseAsyncAPIFile(data, schemas)

	require.Contains(t, schemas, "orders.topic.v1")
	fields := schemas["orders.topic.v1"]
	assert.True(t, fields["order_id"], "order_id should be present from both messages")
	assert.True(t, fields["amount"], "amount should be present from OrderCreated")
	assert.True(t, fields["status"], "status should be present from OrderUpdated")
}
