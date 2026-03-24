package validator

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Duplicate detection ────────────────────────────────────────────────────

func TestValidateDuplicates_EmptyManifest(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	v.validateDuplicates(&controlplanev1.Manifest{}, result)
	assert.Empty(t, result.Errors)
}

func TestValidateDuplicates_NoDuplicates(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := validManifest()
	result := &ValidationResult{Valid: true}
	v.validateDuplicates(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidateDuplicates_DuplicateInstrumentCodes(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Instruments: []*controlplanev1.InstrumentDefinition{
			{Code: "GBP", Name: "British Pound", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
			{Code: "GBP", Name: "Duplicate", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateDuplicates(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "DUPLICATE_CODE", result.Errors[0].Code)
	assert.Equal(t, "instruments[1].code", result.Errors[0].Path)
}

func TestValidateDuplicates_DuplicateAccountTypeCodes(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{Code: "SETTLEMENT", Name: "Settlement"},
			{Code: "SETTLEMENT", Name: "Duplicate Settlement"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateDuplicates(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "DUPLICATE_CODE", result.Errors[0].Code)
	assert.Equal(t, "account_types[1].code", result.Errors[0].Path)
}

func TestValidateDuplicates_DuplicateSagaNames(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "process_settlement", Trigger: "api:/v1/settle"},
			{Name: "process_settlement", Trigger: "api:/v1/settle2"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateDuplicates(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "DUPLICATE_NAME", result.Errors[0].Code)
}

func TestValidateDuplicates_EventTriggerWithoutFilterWarning(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "my_saga", Trigger: "event:market_data_received"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateDuplicates(manifest, result)

	require.Len(t, result.Warnings, 1)
	assert.Equal(t, "MISSING_EVENT_FILTER", result.Warnings[0].Code)
}

// ─── Webhook trigger validation ─────────────────────────────────────────────

func TestValidateWebhookTriggers_ValidConnection(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "on_payment", Trigger: "webhook:stripe_connect"},
		},
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe_connect"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateWebhookTriggers(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidateWebhookTriggers_UnknownConnection(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "on_payment", Trigger: "webhook:unknown_provider"},
		},
		OperationalGateway: &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe_connect"},
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateWebhookTriggers(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "UNKNOWN_WEBHOOK_SOURCE", result.Errors[0].Code)
}

func TestValidateWebhookTriggers_NoWebhookTriggers(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "process", Trigger: "api:/v1/process"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateWebhookTriggers(manifest, result)
	assert.Empty(t, result.Errors)
}

// ─── Scheduled trigger validation ───────────────────────────────────────────

func TestValidateScheduledTriggers_Unique(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "daily_billing", Trigger: "scheduled:daily_billing"},
			{Name: "monthly_report", Trigger: "scheduled:monthly_report"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidateScheduledTriggers_Duplicate(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "billing_a", Trigger: "scheduled:daily_billing"},
			{Name: "billing_b", Trigger: "scheduled:daily_billing"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateScheduledTriggers(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "DUPLICATE_SCHEDULED_TRIGGER", result.Errors[0].Code)
}

// ─── API trigger validation ──────────────────────────────────────────────────

func TestValidateAPITriggers_ValidPath_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "create_order", Trigger: "api:/v1/orders"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateAPITriggers(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidateAPITriggers_InvalidPathFormat_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "bad_path", Trigger: "api:no-leading-slash"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateAPITriggers(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "INVALID_API_PATH_FORMAT", result.Errors[0].Code)
}

func TestValidateAPITriggers_DuplicatePath_Direct(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "saga_a", Trigger: "api:/v1/orders"},
			{Name: "saga_b", Trigger: "api:/v1/orders"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateAPITriggers(manifest, result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "DUPLICATE_API_TRIGGER", result.Errors[0].Code)
}

func TestValidateAPITriggers_NonAPITriggerSkipped(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{Name: "event_saga", Trigger: "event:some_channel"},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateAPITriggers(manifest, result)
	assert.Empty(t, result.Errors)
}
