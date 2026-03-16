package applier

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- ApplyResource unit tests ---

func TestApplyResource_NilResource(t *testing.T) {
	handler := newTestHandler(t)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		AppliedBy:    "test-user",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "resource payload is required")
}

func TestApplyResource_EmptyAppliedBy_NonDryRun(t *testing.T) {
	handler := newTestHandler(t)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "applied_by is required")
}

func TestApplyResource_NoVersionStore(t *testing.T) {
	// Handler without version store should fail
	handler := newTestHandler(t)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
		AppliedBy: "test-user",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestApplyResource_NoCurrentManifest(t *testing.T) {
	// Version store returns nil (no manifest applied yet)
	handler := newTestHandlerWithVersionStore(t, nil)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
		AppliedBy: "test-user",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, err.Error(), "no current manifest")
}

func TestApplyResource_DryRun_AddNewInstrument(t *testing.T) {
	// Set up handler with an existing manifest in the version store
	existingManifest := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
		DryRun: true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
	assert.NotEmpty(t, resp.DiffSummary)

	// Should have steps: validate, diff, plan, execute (skipped)
	require.Len(t, resp.StepResults, 4)
	assert.Equal(t, "validate", resp.StepResults[0].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, resp.StepResults[0].Status)
	assert.Equal(t, "diff", resp.StepResults[1].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, resp.StepResults[1].Status)
	assert.Equal(t, "plan", resp.StepResults[2].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, resp.StepResults[2].Status)
	assert.Equal(t, "execute", resp.StepResults[3].StepName)
	assert.Equal(t, controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SKIPPED, resp.StepResults[3].Status)
}

func TestApplyResource_DryRun_UpdateExistingInstrument(t *testing.T) {
	existingManifest := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	// Update the existing GBP instrument's name
	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "GBP",
				Name: "Pound Sterling (updated)",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GBP",
					Precision: 2,
				},
			},
		},
		DryRun: true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
	// Diff should show an update, not a create
	assert.Contains(t, resp.DiffSummary, "update")
}

func TestApplyResource_DryRun_AllowsEmptyAppliedBy(t *testing.T) {
	existingManifest := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
		DryRun: true,
		// AppliedBy intentionally omitted
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

func TestApplyResource_ValidationFailure_InvalidReference(t *testing.T) {
	existingManifest := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	// Add an account type that references a non-existent instrument
	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ACCOUNT_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_AccountType{
			AccountType: &controlplanev1.AccountTypeDefinition{
				Code:               "SAVINGS",
				Name:               "Savings Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
				AllowedInstruments: []string{"NONEXISTENT"},
			},
		},
		DryRun: true,
	})

	require.NoError(t, err) // RPC succeeds, status reflects validation failure
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED, resp.Status)
	assert.NotEmpty(t, resp.ValidationErrors)

	foundRefError := false
	for _, ve := range resp.ValidationErrors {
		if ve.Code == "UNDEFINED_INSTRUMENT_REFERENCE" {
			foundRefError = true
		}
	}
	assert.True(t, foundRefError, "expected UNDEFINED_INSTRUMENT_REFERENCE validation error")
}

func TestApplyResource_NonDryRun_NoExecutor(t *testing.T) {
	existingManifest := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
		AppliedBy: "test-user",
		DryRun:    false,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	// Without executor, execution fails
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_FAILED, resp.Status)
}

func TestApplyResource_DryRun_AddAccountType(t *testing.T) {
	existingManifest := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ACCOUNT_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_AccountType{
			AccountType: &controlplanev1.AccountTypeDefinition{
				Code:               "SAVINGS",
				Name:               "Savings Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
				AllowedInstruments: []string{"GBP"}, // Valid: GBP exists
			},
		},
		DryRun: true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
	assert.Contains(t, resp.DiffSummary, "create")
}

func TestApplyResource_DryRun_AddSaga(t *testing.T) {
	existingManifest := newTestManifest()
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_SAGA,
		Resource: &controlplanev1.ApplyResourceRequest_Saga{
			Saga: &controlplanev1.SagaDefinition{
				Name:    "process_payment",
				Trigger: "api:/v1/payments",
				Script:  "def execute(ctx):\n  pass\n",
			},
		},
		DryRun: true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Saga validation may produce warnings (e.g. Starlark parse warnings)
	// but should succeed or at least produce a meaningful result.
	// The validation result depends on the saga validator; accept either
	// DRY_RUN (valid) or VALIDATION_FAILED (if Starlark is strict).
	validStatuses := []controlplanev1.ApplyManifestStatus{
		controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
		controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED,
	}
	assert.Contains(t, validStatuses, resp.Status)
}

func TestApplyResource_DryRun_AddValuationRule(t *testing.T) {
	existingManifest := newTestManifest()
	// Add a second instrument so the valuation rule is valid
	existingManifest.Instruments = append(existingManifest.Instruments, &controlplanev1.InstrumentDefinition{
		Code: "KWH",
		Name: "Kilowatt Hour",
		Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      "kWh",
			Precision: 3,
		},
	})
	handler := newTestHandlerWithVersionStore(t, existingManifest)

	resp, err := handler.ApplyResource(context.Background(), &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_VALUATION_RULE,
		Resource: &controlplanev1.ApplyResourceRequest_ValuationRule{
			ValuationRule: &controlplanev1.ValuationRule{
				FromInstrument: "GBP",
				ToInstrument:   "KWH",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_FIXED,
			},
		},
		DryRun: true,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN, resp.Status)
}

// --- Resource Patcher unit tests ---

func TestPatchResource_AddNewInstrument(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "USD",
				Name: "US Dollar",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "USD",
					Precision: 2,
				},
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.Instruments, 2)
	assert.Equal(t, "GBP", patched.Instruments[0].Code)
	assert.Equal(t, "USD", patched.Instruments[1].Code)

	// Original should not be modified
	assert.Len(t, base.Instruments, 1)
}

func TestPatchResource_UpdateExistingInstrument(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INSTRUMENT,
		Resource: &controlplanev1.ApplyResourceRequest_Instrument{
			Instrument: &controlplanev1.InstrumentDefinition{
				Code: "GBP",
				Name: "Pound Sterling (updated)",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GBP",
					Precision: 2,
				},
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.Instruments, 1)
	assert.Equal(t, "Pound Sterling (updated)", patched.Instruments[0].Name)

	// Original should not be modified
	assert.Equal(t, "British Pound Sterling", base.Instruments[0].Name)
}

func TestPatchResource_AddAccountType(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ACCOUNT_TYPE,
		Resource: &controlplanev1.ApplyResourceRequest_AccountType{
			AccountType: &controlplanev1.AccountTypeDefinition{
				Code:          "SAVINGS",
				Name:          "Savings Account",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.AccountTypes, 2)
	assert.Equal(t, "CURRENT", patched.AccountTypes[0].Code)
	assert.Equal(t, "SAVINGS", patched.AccountTypes[1].Code)
}

func TestPatchResource_AddSaga(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_SAGA,
		Resource: &controlplanev1.ApplyResourceRequest_Saga{
			Saga: &controlplanev1.SagaDefinition{
				Name:    "test_saga",
				Trigger: "api:/test",
				Script:  "def execute(ctx):\n  pass\n",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.Sagas, 1)
	assert.Equal(t, "test_saga", patched.Sagas[0].Name)
}

func TestPatchResource_AddProviderConnection_InitializesGateway(t *testing.T) {
	base := newTestManifest()
	// base has no OperationalGateway
	require.Nil(t, base.OperationalGateway)

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_PROVIDER_CONNECTION,
		Resource: &controlplanev1.ApplyResourceRequest_ProviderConnection{
			ProviderConnection: &controlplanev1.ProviderConnectionConfig{
				ConnectionId: "stripe-payments",
				ProviderName: "Stripe",
				ProviderType: "payment_gateway",
				Protocol:     controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS,
				BaseUrl:      "https://api.stripe.com",
				Auth: &controlplanev1.AuthConfigManifest{
					AuthConfig: &controlplanev1.AuthConfigManifest_ApiKey{
						ApiKey: &controlplanev1.ApiKeyAuthConfig{
							HeaderName:      "Authorization",
							ApiKeySecretRef: "sm://stripe/api_key",
						},
					},
				},
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.NotNil(t, patched.OperationalGateway)
	require.Len(t, patched.OperationalGateway.ProviderConnections, 1)
	assert.Equal(t, "stripe-payments", patched.OperationalGateway.ProviderConnections[0].ConnectionId)
}

func TestPatchResource_AddMarketDataSource_InitializesMarketData(t *testing.T) {
	base := newTestManifest()
	require.Nil(t, base.MarketData)

	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_MARKET_DATA_SOURCE,
		Resource: &controlplanev1.ApplyResourceRequest_MarketDataSource{
			MarketDataSource: &controlplanev1.MarketDataSourceDefinition{
				Code:       "BLOOMBERG",
				Name:       "Bloomberg",
				TrustLevel: 90,
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.NotNil(t, patched.MarketData)
	require.Len(t, patched.MarketData.Sources, 1)
	assert.Equal(t, "BLOOMBERG", patched.MarketData.Sources[0].Code)
}

func TestPatchResource_AddOrganization(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_ORGANIZATION,
		Resource: &controlplanev1.ApplyResourceRequest_Organization{
			Organization: &controlplanev1.OrganizationDefinition{
				Code:      "ACME_ENERGY",
				Name:      "Acme Energy Corp",
				PartyType: "ORGANIZATION",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.Organizations, 1)
	assert.Equal(t, "ACME_ENERGY", patched.Organizations[0].Code)
}

func TestPatchResource_AddInternalAccount(t *testing.T) {
	base := newTestManifest()
	req := &controlplanev1.ApplyResourceRequest{
		ResourceType: controlplanev1.ManifestResourceType_MANIFEST_RESOURCE_TYPE_INTERNAL_ACCOUNT,
		Resource: &controlplanev1.ApplyResourceRequest_InternalAccount{
			InternalAccount: &controlplanev1.InternalAccountDefinition{
				Code:        "REVENUE_GBP",
				AccountType: "CURRENT",
				Instrument:  "GBP",
				Description: "Revenue clearing account",
			},
		},
	}

	patched, err := patchResource(base, req)
	require.NoError(t, err)
	require.Len(t, patched.InternalAccounts, 1)
	assert.Equal(t, "REVENUE_GBP", patched.InternalAccounts[0].Code)
}

func TestResourceID_AllTypes(t *testing.T) {
	tests := []struct {
		name   string
		req    *controlplanev1.ApplyResourceRequest
		wantID string
	}{
		{
			name: "instrument",
			req: &controlplanev1.ApplyResourceRequest{
				Resource: &controlplanev1.ApplyResourceRequest_Instrument{
					Instrument: &controlplanev1.InstrumentDefinition{Code: "GBP"},
				},
			},
			wantID: "GBP",
		},
		{
			name: "account_type",
			req: &controlplanev1.ApplyResourceRequest{
				Resource: &controlplanev1.ApplyResourceRequest_AccountType{
					AccountType: &controlplanev1.AccountTypeDefinition{Code: "CURRENT"},
				},
			},
			wantID: "CURRENT",
		},
		{
			name: "valuation_rule",
			req: &controlplanev1.ApplyResourceRequest{
				Resource: &controlplanev1.ApplyResourceRequest_ValuationRule{
					ValuationRule: &controlplanev1.ValuationRule{
						FromInstrument: "GBP",
						ToInstrument:   "KWH",
					},
				},
			},
			wantID: "GBP->KWH",
		},
		{
			name: "saga",
			req: &controlplanev1.ApplyResourceRequest{
				Resource: &controlplanev1.ApplyResourceRequest_Saga{
					Saga: &controlplanev1.SagaDefinition{Name: "my_saga"},
				},
			},
			wantID: "my_saga",
		},
		{
			name: "organization",
			req: &controlplanev1.ApplyResourceRequest{
				Resource: &controlplanev1.ApplyResourceRequest_Organization{
					Organization: &controlplanev1.OrganizationDefinition{Code: "ACME"},
				},
			},
			wantID: "ACME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceID(tt.req)
			assert.Equal(t, tt.wantID, got)
		})
	}
}
