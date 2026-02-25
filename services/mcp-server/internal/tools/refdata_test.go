package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ---- mock clients ----

type mockManifestHistoryClient struct {
	resp *controlplanev1.GetCurrentManifestResponse
	err  error
}

func (m *mockManifestHistoryClient) GetCurrentManifest(_ context.Context, _ *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
	return m.resp, m.err
}

type mockReferenceDataClient struct {
	listResp     *referencedatav1.ListInstrumentsResponse
	listErr      error
	retrieveResp *referencedatav1.RetrieveInstrumentResponse
	retrieveErr  error
}

func (m *mockReferenceDataClient) ListInstruments(_ context.Context, _ *referencedatav1.ListInstrumentsRequest) (*referencedatav1.ListInstrumentsResponse, error) {
	return m.listResp, m.listErr
}

func (m *mockReferenceDataClient) RetrieveInstrument(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	return m.retrieveResp, m.retrieveErr
}

type mockSagaRegistryClient struct {
	listResp *sagav1.ListSagasResponse
	listErr  error
	getResp  *sagav1.GetSagaResponse
	getErr   error
}

func (m *mockSagaRegistryClient) ListSagas(_ context.Context, _ *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
	return m.listResp, m.listErr
}

func (m *mockSagaRegistryClient) GetSaga(_ context.Context, _ *sagav1.GetSagaRequest) (*sagav1.GetSagaResponse, error) {
	return m.getResp, m.getErr
}

type mockMarketInformationClient struct {
	listDataSetsResp     *marketinformationv1.ListDataSetsResponse
	listDataSetsErr      error
	listObservationsResp *marketinformationv1.ListObservationsResponse
	listObservationsErr  error
}

func (m *mockMarketInformationClient) ListDataSets(_ context.Context, _ *marketinformationv1.ListDataSetsRequest) (*marketinformationv1.ListDataSetsResponse, error) {
	return m.listDataSetsResp, m.listDataSetsErr
}

func (m *mockMarketInformationClient) ListObservations(_ context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
	return m.listObservationsResp, m.listObservationsErr
}

// ---- helpers ----

func mustRegisterRefdata(t *testing.T, deps tools.ReferenceDataDeps) *tools.Registry {
	t.Helper()
	r := tools.NewRegistry()
	if err := tools.RegisterReferenceDataTools(r, deps); err != nil {
		t.Fatalf("RegisterReferenceDataTools: %v", err)
	}
	return r
}

func callTool(t *testing.T, r *tools.Registry, name string, params string) interface{} {
	t.Helper()
	result, err := r.Call(context.Background(), name, json.RawMessage(params))
	if err != nil {
		t.Fatalf("Call(%q): %v", name, err)
	}
	return result
}

func resultMap(t *testing.T, result interface{}) map[string]interface{} {
	t.Helper()
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	return m
}

// assertErrorResult asserts that the result represents an error response.
// It accepts either a map[string]interface{} with valid=false, or a mcperrors.FormattedError.
func assertErrorResult(t *testing.T, result interface{}) {
	t.Helper()
	switch v := result.(type) {
	case map[string]interface{}:
		validRaw, exists := v["valid"]
		if !exists {
			t.Error("expected error map to include 'valid' key")
			return
		}
		valid, ok := validRaw.(bool)
		if !ok || valid {
			t.Error("expected valid=false in map result")
		}
		if _, ok := v["errors"]; !ok {
			t.Error("expected error map to include 'errors' key")
		}
	case mcperrors.FormattedError:
		if v.Valid {
			t.Error("expected Valid=false, got Valid=true in FormattedError")
		}
	default:
		t.Errorf("expected error result (map or FormattedError), got %T", result)
	}
}

func buildTestManifest() *controlplanev1.Manifest {
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:        "Test Economy",
			Industry:    "energy",
			Description: "Test manifest",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "GBP",
				Name: "British Pound",
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
				Code:          "CURRENT",
				Name:          "Current Account",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT,
				Policies: &controlplanev1.AccountTypePolicies{
					Validation: "amount > 0",
					Bucketing:  "instrument_code",
				},
			},
		},
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
				Source:         "nordpool",
			},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "process_payment",
				Trigger: "api:/v1/payments",
				Script:  "# saga script",
			},
			{
				Name:    "daily_reconcile",
				Trigger: "scheduled:daily_reconciliation",
				Script:  "# reconcile script",
			},
		},
	}
}

// ---- RegisterReferenceDataTools ----

func TestRegisterReferenceDataTools_RegistersAllTools(t *testing.T) {
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{})
	listed := r.List()

	expectedNames := []string{
		"meridian_economy_structure",
		"meridian_instrument_describe",
		"meridian_instruments_list",
		"meridian_market_data_query",
		"meridian_saga_describe",
		"meridian_sagas_list",
		"meridian_handlers_describe",
	}
	nameSet := make(map[string]bool, len(listed))
	for _, t := range listed {
		nameSet[t.Name] = true
	}
	for _, name := range expectedNames {
		if !nameSet[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestRegisterReferenceDataTools_AllCategoryRead(t *testing.T) {
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{})
	for _, tool := range r.List() {
		if tool.Category != tools.CategoryRead {
			t.Errorf("tool %q has category %v, want CategoryRead", tool.Name, tool.Category)
		}
	}
}

// ---- meridian_economy_structure ----

func TestEconomyStructure_ValidManifest(t *testing.T) {
	manifest := buildTestManifest()
	client := &mockManifestHistoryClient{
		resp: &controlplanev1.GetCurrentManifestResponse{
			Version: &controlplanev1.ManifestVersion{
				Version:  "1.0",
				Manifest: manifest,
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ManifestHistory: client})

	result := callTool(t, r, "meridian_economy_structure", `{}`)
	m := resultMap(t, result)

	if m["version"] != "1.0" {
		t.Errorf("expected version 1.0, got %v", m["version"])
	}

	economy, ok := m["economy"].(map[string]interface{})
	if !ok {
		t.Fatal("expected economy map")
	}

	instruments, ok := economy["instruments"].(map[string]interface{})
	if !ok {
		t.Fatal("expected instruments map in economy")
	}
	if instruments["count"] != 2 {
		t.Errorf("expected 2 instruments, got %v", instruments["count"])
	}

	sagas, ok := economy["sagas"].(map[string]interface{})
	if !ok {
		t.Fatal("expected sagas map in economy")
	}
	if sagas["count"] != 2 {
		t.Errorf("expected 2 sagas, got %v", sagas["count"])
	}
}

func TestEconomyStructure_NoManifest(t *testing.T) {
	client := &mockManifestHistoryClient{
		resp: &controlplanev1.GetCurrentManifestResponse{},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ManifestHistory: client})

	result := callTool(t, r, "meridian_economy_structure", `{}`)
	m := resultMap(t, result)

	if m["status"] != "no_manifest" {
		t.Errorf("expected status no_manifest, got %v", m["status"])
	}
}

func TestEconomyStructure_GRPCError(t *testing.T) {
	client := &mockManifestHistoryClient{
		err: errors.New("rpc error: connection refused"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ManifestHistory: client})

	result := callTool(t, r, "meridian_economy_structure", `{}`)
	assertErrorResult(t, result)
}

func TestEconomyStructure_NilClient(t *testing.T) {
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ManifestHistory: nil})

	result := callTool(t, r, "meridian_economy_structure", `{}`)
	assertErrorResult(t, result)
}

// ---- meridian_instruments_list ----

func TestInstrumentsList_ValidQuery(t *testing.T) {
	client := &mockReferenceDataClient{
		listResp: &referencedatav1.ListInstrumentsResponse{
			Instruments: []*referencedatav1.InstrumentDefinition{
				{
					Id:          "uuid-1",
					Code:        "USD",
					Version:     1,
					Dimension:   referencedatav1.Dimension_DIMENSION_CURRENCY,
					Precision:   2,
					Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					DisplayName: "US Dollar",
					IsSystem:    true,
				},
				{
					Id:          "uuid-2",
					Code:        "KWH",
					Version:     1,
					Dimension:   referencedatav1.Dimension_DIMENSION_ENERGY,
					Precision:   3,
					Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					DisplayName: "Kilowatt Hour",
				},
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ReferenceData: client})

	result := callTool(t, r, "meridian_instruments_list", `{}`)
	m := resultMap(t, result)

	if m["count"] != 2 {
		t.Fatalf("expected count=2, got %v", m["count"])
	}

	instruments, ok := m["instruments"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected instruments slice")
	}
	if len(instruments) == 0 {
		t.Fatal("expected at least one instrument")
	}
	if instruments[0]["code"] != "USD" {
		t.Errorf("expected first instrument code USD, got %v", instruments[0]["code"])
	}
}

func TestInstrumentsList_WithStatusFilter(t *testing.T) {
	client := &mockReferenceDataClient{
		listResp: &referencedatav1.ListInstrumentsResponse{
			Instruments: []*referencedatav1.InstrumentDefinition{},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ReferenceData: client})

	// Valid status filter does not error
	result := callTool(t, r, "meridian_instruments_list", `{"status_filter":"ACTIVE"}`)
	m := resultMap(t, result)
	if m["count"] != 0 {
		t.Errorf("expected count=0, got %v", m["count"])
	}
}

func TestInstrumentsList_InvalidParams(t *testing.T) {
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{})

	// Invalid JSON params should return validation error from registry (not reach handler)
	_, err := r.Call(context.Background(), "meridian_instruments_list", json.RawMessage(`{invalid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON params")
	}
}

func TestInstrumentsList_GRPCError(t *testing.T) {
	client := &mockReferenceDataClient{
		listErr: errors.New("rpc error: unavailable"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ReferenceData: client})

	result := callTool(t, r, "meridian_instruments_list", `{}`)
	assertErrorResult(t, result)
}

// ---- meridian_instrument_describe ----

func TestInstrumentDescribe_ValidRequest(t *testing.T) {
	now := timestamppb.Now()
	client := &mockReferenceDataClient{
		retrieveResp: &referencedatav1.RetrieveInstrumentResponse{
			Instrument: &referencedatav1.InstrumentDefinition{
				Id:                       "uuid-123",
				Code:                     "USD",
				Version:                  1,
				Dimension:                referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision:                2,
				Status:                   referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				DisplayName:              "US Dollar",
				Description:              "United States Dollar",
				ValidationExpression:     "amount > 0",
				FungibilityKeyExpression: "instrument_code",
				ErrorMessageExpression:   "'invalid amount'",
				AttributeSchema:          `{"type":"object"}`,
				IsSystem:                 true,
				CreatedAt:                now,
				ActivatedAt:              now,
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ReferenceData: client})

	result := callTool(t, r, "meridian_instrument_describe", `{"code":"USD"}`)
	m := resultMap(t, result)

	if m["code"] != "USD" {
		t.Errorf("expected code=USD, got %v", m["code"])
	}
	if m["validation_expression"] != "amount > 0" {
		t.Errorf("expected validation_expression set, got %v", m["validation_expression"])
	}
	if m["created_at"] == "" || m["created_at"] == nil {
		t.Error("expected created_at to be set")
	}
}

func TestInstrumentDescribe_MissingCode(t *testing.T) {
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{})

	result := callTool(t, r, "meridian_instrument_describe", `{}`)
	assertErrorResult(t, result)
}

func TestInstrumentDescribe_GRPCError(t *testing.T) {
	client := &mockReferenceDataClient{
		retrieveErr: errors.New("rpc error: not found"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ReferenceData: client})

	result := callTool(t, r, "meridian_instrument_describe", `{"code":"NONEXISTENT"}`)
	assertErrorResult(t, result)
}

// ---- meridian_sagas_list ----

func TestSagasList_ValidQuery(t *testing.T) {
	client := &mockSagaRegistryClient{
		listResp: &sagav1.ListSagasResponse{
			Sagas: []*sagav1.SagaDefinition{
				{
					Id:          "saga-uuid-1",
					Name:        "process_payment",
					Version:     1,
					Status:      sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
					IsSystem:    false,
					DisplayName: "Process Payment",
					Description: "Handles payment processing",
				},
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{SagaRegistry: client})

	result := callTool(t, r, "meridian_sagas_list", `{}`)
	m := resultMap(t, result)

	if m["count"] != 1 {
		t.Errorf("expected count=1, got %v", m["count"])
	}

	sagas, ok := m["sagas"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected sagas slice")
	}
	if sagas[0]["name"] != "process_payment" {
		t.Errorf("expected saga name process_payment, got %v", sagas[0]["name"])
	}
}

func TestSagasList_GRPCError(t *testing.T) {
	client := &mockSagaRegistryClient{
		listErr: errors.New("rpc error: unavailable"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{SagaRegistry: client})

	result := callTool(t, r, "meridian_sagas_list", `{}`)
	assertErrorResult(t, result)
}

// ---- meridian_saga_describe ----

func TestSagaDescribe_ByID(t *testing.T) {
	now := timestamppb.Now()
	client := &mockSagaRegistryClient{
		getResp: &sagav1.GetSagaResponse{
			Saga: &sagav1.SagaDefinition{
				Id:          "saga-uuid-1",
				Name:        "process_payment",
				Version:     1,
				Status:      sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
				Script:      "def run():\n  pass",
				DisplayName: "Process Payment",
				CreatedAt:   now,
				ActivatedAt: now,
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{SagaRegistry: client})

	result := callTool(t, r, "meridian_saga_describe", `{"id":"saga-uuid-1"}`)
	m := resultMap(t, result)

	if m["name"] != "process_payment" {
		t.Errorf("expected name process_payment, got %v", m["name"])
	}
	if m["script"] != "def run():\n  pass" {
		t.Errorf("expected script to be set, got %v", m["script"])
	}
}

func TestSagaDescribe_ByName(t *testing.T) {
	client := &mockSagaRegistryClient{
		getResp: &sagav1.GetSagaResponse{
			Saga: &sagav1.SagaDefinition{
				Id:     "saga-uuid-1",
				Name:   "process_payment",
				Status: sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{SagaRegistry: client})

	result := callTool(t, r, "meridian_saga_describe", `{"name":"process_payment"}`)
	m := resultMap(t, result)

	if m["name"] != "process_payment" {
		t.Errorf("expected saga name, got %v", m["name"])
	}
}

func TestSagaDescribe_MissingIDAndName(t *testing.T) {
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{})

	result := callTool(t, r, "meridian_saga_describe", `{}`)
	assertErrorResult(t, result)
}

func TestSagaDescribe_GRPCError(t *testing.T) {
	client := &mockSagaRegistryClient{
		getErr: errors.New("rpc error: not found"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{SagaRegistry: client})

	result := callTool(t, r, "meridian_saga_describe", `{"name":"nonexistent"}`)
	assertErrorResult(t, result)
}

// ---- meridian_handlers_describe ----

func TestHandlersDescribe_ValidManifest(t *testing.T) {
	manifest := buildTestManifest()
	client := &mockManifestHistoryClient{
		resp: &controlplanev1.GetCurrentManifestResponse{
			Version: &controlplanev1.ManifestVersion{
				Version:  "1.0",
				Manifest: manifest,
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ManifestHistory: client})

	result := callTool(t, r, "meridian_handlers_describe", `{}`)
	m := resultMap(t, result)

	triggerCount, _ := m["saga_trigger_count"].(int)
	if triggerCount != 2 {
		t.Errorf("expected 2 saga triggers, got %v", m["saga_trigger_count"])
	}

	policyCount, _ := m["policy_count"].(int)
	if policyCount != 1 {
		t.Errorf("expected 1 account type policy, got %v", m["policy_count"])
	}
}

func TestHandlersDescribe_TriggerFilter(t *testing.T) {
	manifest := buildTestManifest()
	client := &mockManifestHistoryClient{
		resp: &controlplanev1.GetCurrentManifestResponse{
			Version: &controlplanev1.ManifestVersion{
				Version:  "1.0",
				Manifest: manifest,
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ManifestHistory: client})

	// Filter by "api" trigger only - should return 1 of the 2 sagas
	result := callTool(t, r, "meridian_handlers_describe", `{"trigger_prefix":"api"}`)
	m := resultMap(t, result)

	triggerCount, _ := m["saga_trigger_count"].(int)
	if triggerCount != 1 {
		t.Errorf("expected 1 api saga trigger, got %v", m["saga_trigger_count"])
	}
}

func TestHandlersDescribe_GRPCError(t *testing.T) {
	client := &mockManifestHistoryClient{
		err: errors.New("rpc error: unavailable"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{ManifestHistory: client})

	result := callTool(t, r, "meridian_handlers_describe", `{}`)
	assertErrorResult(t, result)
}

// ---- meridian_market_data_query ----

func TestMarketDataQuery_ListDatasets(t *testing.T) {
	client := &mockMarketInformationClient{
		listDataSetsResp: &marketinformationv1.ListDataSetsResponse{
			Datasets: []*marketinformationv1.DataSetDefinition{
				{
					Id:          "ds-uuid-1",
					Code:        "USD_EUR_FX",
					Version:     1,
					Category:    marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
					Unit:        "USD/EUR",
					Status:      marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
					DisplayName: "USD to EUR FX Rate",
				},
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{MarketInformation: client})

	result := callTool(t, r, "meridian_market_data_query", `{}`)
	m := resultMap(t, result)

	if m["count"] != 1 {
		t.Errorf("expected count=1, got %v", m["count"])
	}

	datasets, ok := m["datasets"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected datasets slice")
	}
	if datasets[0]["code"] != "USD_EUR_FX" {
		t.Errorf("expected dataset code USD_EUR_FX, got %v", datasets[0]["code"])
	}
}

func TestMarketDataQuery_ListObservations(t *testing.T) {
	now := timestamppb.Now()
	client := &mockMarketInformationClient{
		listObservationsResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{
					Id:                 "obs-uuid-1",
					DatasetCode:        "USD_EUR_FX",
					DatasetVersion:     1,
					ResolutionKeyValue: "spot",
					Value:              "1.0823",
					Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
					ObservedAt:         now,
					ValidFrom:          now,
				},
			},
		},
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{MarketInformation: client})

	result := callTool(t, r, "meridian_market_data_query", `{"dataset_code":"USD_EUR_FX"}`)
	m := resultMap(t, result)

	if m["dataset_code"] != "USD_EUR_FX" {
		t.Errorf("expected dataset_code=USD_EUR_FX, got %v", m["dataset_code"])
	}
	if m["count"] != 1 {
		t.Errorf("expected count=1, got %v", m["count"])
	}

	observations, ok := m["observations"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected observations slice")
	}
	if observations[0]["value"] != "1.0823" {
		t.Errorf("expected value=1.0823, got %v", observations[0]["value"])
	}
}

func TestMarketDataQuery_GRPCErrorListDatasets(t *testing.T) {
	client := &mockMarketInformationClient{
		listDataSetsErr: errors.New("rpc error: unavailable"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{MarketInformation: client})

	result := callTool(t, r, "meridian_market_data_query", `{}`)
	assertErrorResult(t, result)
}

func TestMarketDataQuery_GRPCErrorListObservations(t *testing.T) {
	client := &mockMarketInformationClient{
		listObservationsErr: errors.New("rpc error: not found"),
	}
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{MarketInformation: client})

	result := callTool(t, r, "meridian_market_data_query", `{"dataset_code":"USD_EUR_FX"}`)
	assertErrorResult(t, result)
}

func TestMarketDataQuery_NilClient(t *testing.T) {
	r := mustRegisterRefdata(t, tools.ReferenceDataDeps{MarketInformation: nil})

	result := callTool(t, r, "meridian_market_data_query", `{}`)
	assertErrorResult(t, result)
}
