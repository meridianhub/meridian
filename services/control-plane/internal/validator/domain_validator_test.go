package validator

import (
	"fmt"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Payment Rails ───────────────────────────────────────────────────────────

func TestValidatePaymentRails_ValidRail(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PaymentRails: []*controlplanev1.PaymentRails{
			{
				Provider:         "stripe_connect",
				AccountId:        "acct_1234567890123456",
				SupportedMethods: []string{"card"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePaymentRails(manifest, result)
	assert.Empty(t, result.Errors)
	assert.Empty(t, result.Warnings)
}

func TestValidatePaymentRails_InvalidProvider_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PaymentRails: []*controlplanev1.PaymentRails{
			{Provider: "paypal"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePaymentRails(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_PAYMENT_PROVIDER", result.Errors[0].Code)
	assert.Equal(t, "payment_rails[0].provider", result.Errors[0].Path)
}

func TestValidatePaymentRails_InvalidAccountIDFormat_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PaymentRails: []*controlplanev1.PaymentRails{
			{
				Provider:  "stripe_connect",
				AccountId: "not_a_stripe_account_id",
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePaymentRails(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_ACCOUNT_ID_FORMAT", result.Errors[0].Code)
}

func TestValidatePaymentRails_ValidAccountIDFormat(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PaymentRails: []*controlplanev1.PaymentRails{
			{
				Provider:  "stripe_connect",
				AccountId: "acct_AbCdEf1234567890",
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePaymentRails(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidatePaymentRails_UnknownPaymentMethod_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PaymentRails: []*controlplanev1.PaymentRails{
			{
				Provider:         "stripe_connect",
				SupportedMethods: []string{"crypto"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePaymentRails(manifest, result)
	assert.Empty(t, result.Errors) // unknown method is a warning only
	require.Len(t, result.Warnings, 1)
	assert.Equal(t, "UNKNOWN_PAYMENT_METHOD", result.Warnings[0].Code)
}

func TestValidatePaymentRails_EmptyProvider(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Empty provider is not validated (skip check)
	manifest := &controlplanev1.Manifest{
		PaymentRails: []*controlplanev1.PaymentRails{
			{Provider: ""},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePaymentRails(manifest, result)
	assert.Empty(t, result.Errors)
}

// ─── Platform Fee ────────────────────────────────────────────────────────────

func TestValidatePlatformFee_ValidDecimal(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	fee := &controlplanev1.PlatformFee{Value: "0.025"}
	result := &ValidationResult{Valid: true}
	v.validatePlatformFee(fee, "payment_rails[0].platform_fee", result)
	assert.Empty(t, result.Errors)
}

func TestValidatePlatformFee_Zero(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	fee := &controlplanev1.PlatformFee{Value: "0"}
	result := &ValidationResult{Valid: true}
	v.validatePlatformFee(fee, "payment_rails[0].platform_fee", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_PLATFORM_FEE_VALUE", result.Errors[0].Code)
	assert.Contains(t, result.Errors[0].Message, "greater than 0")
}

func TestValidatePlatformFee_Negative(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	fee := &controlplanev1.PlatformFee{Value: "-0.01"}
	result := &ValidationResult{Valid: true}
	v.validatePlatformFee(fee, "payment_rails[0].platform_fee", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_PLATFORM_FEE_VALUE", result.Errors[0].Code)
}

func TestValidatePlatformFee_InvalidDecimal(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	fee := &controlplanev1.PlatformFee{Value: "not-a-number"}
	result := &ValidationResult{Valid: true}
	v.validatePlatformFee(fee, "payment_rails[0].platform_fee", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_PLATFORM_FEE_VALUE", result.Errors[0].Code)
}

func TestValidatePlatformFee_EmptyValue(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	fee := &controlplanev1.PlatformFee{Value: ""}
	result := &ValidationResult{Valid: true}
	v.validatePlatformFee(fee, "payment_rails[0].platform_fee", result)
	assert.Empty(t, result.Errors)
}

// ─── Party Types ──────────────────────────────────────────────────────────────

func TestValidatePartyTypes_ValidDefinition_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PartyTypes: []*partyv1.PartyTypeDefinition{
			{
				TenantId:        "tenant-1",
				PartyType:       "CUSTOMER",
				AttributeSchema: `{"name": "string"}`,
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePartyTypes(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidatePartyTypes_InvalidJSON(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PartyTypes: []*partyv1.PartyTypeDefinition{
			{
				TenantId:        "tenant-1",
				PartyType:       "CUSTOMER",
				AttributeSchema: `{not valid json`,
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePartyTypes(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_JSON_SCHEMA", result.Errors[0].Code)
}

func TestValidatePartyTypes_DuplicateType(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PartyTypes: []*partyv1.PartyTypeDefinition{
			{TenantId: "tenant-1", PartyType: "CUSTOMER"},
			{TenantId: "tenant-1", PartyType: "CUSTOMER"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePartyTypes(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "DUPLICATE_PARTY_TYPE", result.Errors[0].Code)
}

func TestValidatePartyTypes_DifferentTenantsSameType(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PartyTypes: []*partyv1.PartyTypeDefinition{
			{TenantId: "tenant-1", PartyType: "CUSTOMER"},
			{TenantId: "tenant-2", PartyType: "CUSTOMER"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePartyTypes(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidatePartyTypes_InvalidValidationCEL_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		PartyTypes: []*partyv1.PartyTypeDefinition{
			{
				TenantId:      "tenant-1",
				PartyType:     "CUSTOMER",
				ValidationCel: "undeclared_var > 0",
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validatePartyTypes(manifest, result)
	assert.NotEmpty(t, result.Errors)
}

// ─── Mapping validation ──────────────────────────────────────────────────────

func TestValidateMappingBatch_RequiresBatchTargetPath(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "",
	}

	result := &ValidationResult{Valid: true}
	v.validateMappingBatch(mp, "mappings[0]", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "MAPPING_BATCH_TARGET_REQUIRED", result.Errors[0].Code)
}

func TestValidateMappingBatch_WithBatchTargetPath(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "items",
	}

	result := &ValidationResult{Valid: true}
	v.validateMappingBatch(mp, "mappings[0]", result)
	assert.Empty(t, result.Errors)
}

func TestValidateMappingStatus_Unspecified(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{
		Status: mappingv1.MappingStatus_MAPPING_STATUS_UNSPECIFIED,
	}

	result := &ValidationResult{Valid: true}
	v.validateMappingStatus(mp, "mappings[0]", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_MAPPING_STATUS", result.Errors[0].Code)
}

func TestValidateMappingStatus_Active(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{
		Status: mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
	}

	result := &ValidationResult{Valid: true}
	v.validateMappingStatus(mp, "mappings[0]", result)
	assert.Empty(t, result.Errors)
}

func TestValidateMappingIdempotency_NilConfig(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{}
	result := &ValidationResult{Valid: true}
	v.validateMappingIdempotency(mp, "mappings[0]", result)
	assert.Empty(t, result.Errors)
}

func TestValidateMappingIdempotency_MissingSourceSelector(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash: false,
			SourceSelector: "",
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateMappingIdempotency(mp, "mappings[0]", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "IDEMPOTENCY_SOURCE_REQUIRED", result.Errors[0].Code)
}

func TestValidateMappingIdempotency_MissingContentHashFields(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: nil,
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateMappingIdempotency(mp, "mappings[0]", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "IDEMPOTENCY_HASH_FIELDS_REQUIRED", result.Errors[0].Code)
}

func TestValidateMappingIdempotency_ValidContentHash(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	mp := &mappingv1.MappingDefinition{
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"amount", "currency"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateMappingIdempotency(mp, "mappings[0]", result)
	assert.Empty(t, result.Errors)
}

// ─── Scheduled Trigger Cron Validation ───────────────────────────────────────

func TestValidateScheduledTriggers_ValidCronExpression(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "hourly_billing", Trigger: "scheduled:hourly_billing:0 * * * *"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	assert.Empty(t, result.Errors)
	assert.Empty(t, result.Warnings)
}

func TestValidateScheduledTriggers_EmptyCronSuffix_Errors(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// scheduled:<name>: with trailing colon but empty cron is invalid
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "billing", Trigger: "scheduled:billing:"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_CRON_EXPRESSION", result.Errors[0].Code)
}

func TestValidateScheduledTriggers_NoCronExpression_Valid(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// scheduled:<name> with no cron is valid (cron comes from DB)
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "billing", Trigger: "scheduled:billing"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	assert.Empty(t, result.Errors)
	assert.Empty(t, result.Warnings)
}

func TestValidateScheduledTriggers_InvalidCronSyntax(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "billing", Trigger: "scheduled:billing:not-a-cron"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_CRON_EXPRESSION", result.Errors[0].Code)
	assert.Equal(t, "sagas[0].trigger", result.Errors[0].Path)
}

func TestValidateScheduledTriggers_IntervalTooShort(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Every minute - interval is 1 min, below 15 min minimum
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "too_frequent", Trigger: "scheduled:too_frequent:* * * * *"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "CRON_INTERVAL_TOO_SHORT", result.Errors[0].Code)
}

func TestValidateScheduledTriggers_IntervalExactly15Minutes_Valid(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Every 15 minutes - exactly at minimum
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "quarter_hourly", Trigger: "scheduled:quarter_hourly:0,15,30,45 * * * *"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidateScheduledTriggers_TooManySchedules(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	sagas := make([]*controlplanev1.SagaDefinition, maxScheduledTriggersPerTenant+1)
	for i := range sagas {
		sagas[i] = &controlplanev1.SagaDefinition{
			Name:    fmt.Sprintf("saga_%d", i),
			Trigger: fmt.Sprintf("scheduled:schedule_%d", i),
		}
	}

	manifest := &controlplanev1.Manifest{Sagas: sagas}
	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "TOO_MANY_SCHEDULED_TRIGGERS", result.Errors[0].Code)
}

func TestValidateScheduledTriggers_InfrequentScheduleWarns(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Every year on Jan 1 at midnight
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "annual", Trigger: "scheduled:annual:0 0 1 1 *"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	assert.Empty(t, result.Errors)
	require.Len(t, result.Warnings, 1)
	assert.Equal(t, "CRON_VERY_INFREQUENT", result.Warnings[0].Code)
}
