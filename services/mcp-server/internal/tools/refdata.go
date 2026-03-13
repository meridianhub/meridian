// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

// Status filter constants used across tool parameter parsing.
const (
	statusActive     = "ACTIVE"
	statusDraft      = "DRAFT"
	statusDeprecated = "DEPRECATED"
)

// ManifestHistoryClient is the interface used by reference data tools to retrieve manifests.
// Defined as an interface so tests can inject mocks.
type ManifestHistoryClient interface {
	GetCurrentManifest(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error)
}

// ReferenceDataClient is the interface used by reference data tools to query instrument definitions.
type ReferenceDataClient interface {
	ListInstruments(ctx context.Context, req *referencedatav1.ListInstrumentsRequest) (*referencedatav1.ListInstrumentsResponse, error)
	RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error)
}

// SagaRegistryClient is the interface used by reference data tools to query saga definitions.
type SagaRegistryClient interface {
	ListSagas(ctx context.Context, req *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error)
	GetSaga(ctx context.Context, req *sagav1.GetSagaRequest) (*sagav1.GetSagaResponse, error)
}

// MarketInformationClient is the interface used by reference data tools to query market data.
type MarketInformationClient interface {
	ListDataSets(ctx context.Context, req *marketinformationv1.ListDataSetsRequest) (*marketinformationv1.ListDataSetsResponse, error)
	ListObservations(ctx context.Context, req *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error)
}

// ReferenceDataDeps holds all service clients used by reference data tools.
type ReferenceDataDeps struct {
	ManifestHistory   ManifestHistoryClient
	ReferenceData     ReferenceDataClient
	SagaRegistry      SagaRegistryClient
	MarketInformation MarketInformationClient
}

// RegisterReferenceDataTools registers all reference data query tools onto the SDK server.
// All tools are read-only (CategoryRead) and query gRPC services for reference data.
func RegisterReferenceDataTools(srv *mcp.Server, deps ReferenceDataDeps) {
	allTools := []Tool{
		buildEconomyStructureTool(deps.ManifestHistory),
		buildInstrumentsListTool(deps.ReferenceData),
		buildInstrumentDescribeTool(deps.ReferenceData),
		buildSagasListTool(deps.SagaRegistry),
		buildSagaDescribeTool(deps.SagaRegistry),
		buildHandlersDescribeTool(deps.ManifestHistory),
		buildMarketDataQueryTool(deps.MarketInformation),
	}

	for _, t := range allTools {
		addTool(srv, t)
	}
}

// buildEconomyStructureTool creates the meridian_economy_structure tool.
// It retrieves the current manifest and returns a hierarchical summary of the tenant's economy.
func buildEconomyStructureTool(client ManifestHistoryClient) Tool {
	return Tool{
		Name:        "meridian_economy_structure",
		Description: "Returns a hierarchical summary of the tenant's economy: instruments, account types, valuation rules, sagas, and payment rails from the current manifest.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]interface{}{},
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (interface{}, error) {
			if client == nil {
				return formatError("manifest_history client not configured"), nil
			}

			resp, err := client.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
			if err != nil {
				fe := mcperrors.FormatGRPCError(err)
				return fe, nil
			}

			if resp.GetVersion() == nil || resp.GetVersion().GetManifest() == nil {
				return map[string]interface{}{
					"status":  "no_manifest",
					"message": "no manifest has been applied for this tenant",
				}, nil
			}

			m := resp.GetVersion().GetManifest()
			return buildManifestSummary(resp.GetVersion().GetVersion(), m), nil
		},
	}
}

// buildInstrumentsListTool creates the meridian_instruments_list tool.
func buildInstrumentsListTool(client ReferenceDataClient) Tool {
	return Tool{
		Name:        "meridian_instruments_list",
		Description: "Lists instrument definitions registered for the tenant. Supports optional status filter (ACTIVE, DRAFT, DEPRECATED).",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"status_filter": map[string]interface{}{
					"type":        "string",
					"description": "Filter by status: ACTIVE, DRAFT, or DEPRECATED. Omit to return all.",
					"enum":        []interface{}{statusActive, statusDraft, statusDeprecated},
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (1-100).",
					"minimum":     1,
					"maximum":     100,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			if client == nil {
				return formatError("reference_data client not configured"), nil
			}

			var p struct {
				StatusFilter string `json:"status_filter"`
				PageSize     int32  `json:"page_size"`
			}
			unmarshalErr := json.Unmarshal(params, &p)
			if unmarshalErr != nil {
				return formatError("invalid parameters: " + unmarshalErr.Error()), nil //nolint:nilerr // tool errors are returned in the result, not as Go errors
			}

			req := &referencedatav1.ListInstrumentsRequest{
				PageSize: p.PageSize,
			}
			if p.StatusFilter != "" {
				req.StatusFilter = parseInstrumentStatus(p.StatusFilter)
			}

			resp, err := client.ListInstruments(ctx, req)
			if err != nil {
				fe := mcperrors.FormatGRPCError(err)
				return fe, nil
			}

			instruments := make([]map[string]interface{}, 0, len(resp.GetInstruments()))
			for _, inst := range resp.GetInstruments() {
				instruments = append(instruments, summarizeInstrument(inst))
			}

			return map[string]interface{}{
				"instruments":     instruments,
				"count":           len(instruments),
				"next_page_token": resp.GetNextPageToken(),
			}, nil
		},
	}
}

// buildInstrumentDescribeTool creates the meridian_instrument_describe tool.
func buildInstrumentDescribeTool(client ReferenceDataClient) Tool {
	return Tool{
		Name:        "meridian_instrument_describe",
		Description: "Returns full details for a specific instrument definition by code. Optionally specify a version; defaults to latest active.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []interface{}{"code"},
			"properties": map[string]interface{}{
				"code": map[string]interface{}{
					"type":        "string",
					"description": "Instrument code (e.g., USD, KWH, CARBON_CREDIT).",
				},
				"version": map[string]interface{}{
					"type":        "integer",
					"description": "Specific version to retrieve. If 0 or omitted, returns the latest active version.",
					"minimum":     0,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			if client == nil {
				return formatError("reference_data client not configured"), nil
			}

			var p struct {
				Code    string `json:"code"`
				Version int32  `json:"version"`
			}
			unmarshalErr := json.Unmarshal(params, &p)
			if unmarshalErr != nil {
				return formatError("invalid parameters: " + unmarshalErr.Error()), nil //nolint:nilerr // tool errors are returned in the result, not as Go errors
			}
			if p.Code == "" {
				return formatError("code is required"), nil
			}

			resp, err := client.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
				Code:    p.Code,
				Version: p.Version,
			})
			if err != nil {
				fe := mcperrors.FormatGRPCError(err)
				return fe, nil
			}

			return detailInstrument(resp.GetInstrument()), nil
		},
	}
}

// buildSagasListTool creates the meridian_sagas_list tool.
func buildSagasListTool(client SagaRegistryClient) Tool {
	return Tool{
		Name:        "meridian_sagas_list",
		Description: "Lists saga workflow definitions registered for the tenant. Supports optional status filter (ACTIVE, DRAFT, DEPRECATED) and system saga inclusion.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"status_filter": map[string]interface{}{
					"type":        "string",
					"description": "Filter by status: ACTIVE, DRAFT, or DEPRECATED. Omit to return all.",
					"enum":        []interface{}{statusActive, statusDraft, statusDeprecated},
				},
				"exclude_system": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, excludes system sagas from the response.",
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (1-100).",
					"minimum":     1,
					"maximum":     100,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			if client == nil {
				return formatError("saga_registry client not configured"), nil
			}

			var p struct {
				StatusFilter  string `json:"status_filter"`
				ExcludeSystem bool   `json:"exclude_system"`
				PageSize      int32  `json:"page_size"`
			}
			unmarshalErr := json.Unmarshal(params, &p)
			if unmarshalErr != nil {
				return formatError("invalid parameters: " + unmarshalErr.Error()), nil //nolint:nilerr // tool errors are returned in the result, not as Go errors
			}

			req := &sagav1.ListSagasRequest{
				ExcludeSystem: p.ExcludeSystem,
				PageSize:      p.PageSize,
			}
			if p.StatusFilter != "" {
				req.StatusFilter = parseSagaStatus(p.StatusFilter)
			}

			resp, err := client.ListSagas(ctx, req)
			if err != nil {
				fe := mcperrors.FormatGRPCError(err)
				return fe, nil
			}

			sagas := make([]map[string]interface{}, 0, len(resp.GetSagas()))
			for _, s := range resp.GetSagas() {
				sagas = append(sagas, summarizeSaga(s))
			}

			return map[string]interface{}{
				"sagas":           sagas,
				"count":           len(sagas),
				"next_page_token": resp.GetNextPageToken(),
			}, nil
		},
	}
}

// buildSagaDescribeTool creates the meridian_saga_describe tool.
func buildSagaDescribeTool(client SagaRegistryClient) Tool {
	return Tool{
		Name:        "meridian_saga_describe",
		Description: "Returns full details for a specific saga definition including its Starlark script. Lookup by saga ID (UUID) or by name with optional version.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "Saga UUID. If provided, name and version are ignored.",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Saga name (e.g., current_account_withdrawal). Used when id is not provided.",
				},
				"version": map[string]interface{}{
					"type":        "integer",
					"description": "Specific version. If 0 or omitted with name, returns the active version.",
					"minimum":     0,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			if client == nil {
				return formatError("saga_registry client not configured"), nil
			}

			var p struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Version int32  `json:"version"`
			}
			unmarshalErr := json.Unmarshal(params, &p)
			if unmarshalErr != nil {
				return formatError("invalid parameters: " + unmarshalErr.Error()), nil //nolint:nilerr // tool errors are returned in the result, not as Go errors
			}
			if p.ID == "" && p.Name == "" {
				return formatError("either id or name is required"), nil
			}

			req := &sagav1.GetSagaRequest{}
			if p.ID != "" {
				req.Id = p.ID
			} else {
				req.Name = p.Name
				req.Version = p.Version
			}

			resp, err := client.GetSaga(ctx, req)
			if err != nil {
				fe := mcperrors.FormatGRPCError(err)
				return fe, nil
			}

			return detailSaga(resp.GetSaga()), nil
		},
	}
}

// buildHandlersDescribeTool creates the meridian_handlers_describe tool.
// It reads handler definitions from the manifest's saga definitions and account type policies.
func buildHandlersDescribeTool(client ManifestHistoryClient) Tool {
	return Tool{
		Name:        "meridian_handlers_describe",
		Description: "Returns the tenant's available saga triggers and account type policies from the current manifest. Shows which handlers are wired to API endpoints, webhooks, scheduled jobs, and domain events.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"trigger_prefix": map[string]interface{}{
					"type":        "string",
					"description": "Filter saga triggers by prefix: api, webhook, scheduled, or event. Omit to return all.",
					"enum":        []interface{}{"api", "webhook", "scheduled", "event"},
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			if client == nil {
				return formatError("manifest_history client not configured"), nil
			}

			var p struct {
				TriggerPrefix string `json:"trigger_prefix"`
			}
			unmarshalErr := json.Unmarshal(params, &p)
			if unmarshalErr != nil {
				return formatError("invalid parameters: " + unmarshalErr.Error()), nil //nolint:nilerr // tool errors are returned in the result, not as Go errors
			}

			resp, err := client.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
			if err != nil {
				fe := mcperrors.FormatGRPCError(err)
				return fe, nil
			}

			if resp.GetVersion() == nil || resp.GetVersion().GetManifest() == nil {
				return map[string]interface{}{
					"status":  "no_manifest",
					"message": "no manifest has been applied for this tenant",
				}, nil
			}

			m := resp.GetVersion().GetManifest()
			triggers := extractSagaTriggers(m.GetSagas(), p.TriggerPrefix)
			policies := extractAccountTypePolicies(m.GetAccountTypes())

			return map[string]interface{}{
				"saga_triggers":         triggers,
				"saga_trigger_count":    len(triggers),
				"account_type_policies": policies,
				"policy_count":          len(policies),
			}, nil
		},
	}
}

// triggerEntry describes a saga trigger mapping.
type triggerEntry struct {
	SagaName    string `json:"saga_name"`
	TriggerType string `json:"trigger_type"`
	TriggerPath string `json:"trigger_path"`
}

// policyEntry describes CEL policies attached to an account type.
type policyEntry struct {
	AccountTypeCode string `json:"account_type_code"`
	Validation      string `json:"validation,omitempty"`
	Bucketing       string `json:"bucketing,omitempty"`
}

// extractSagaTriggers converts manifest saga definitions into trigger entries.
// If prefix is non-empty, only triggers with that prefix (e.g., "api:") are returned.
func extractSagaTriggers(sagas []*controlplanev1.SagaDefinition, prefix string) []triggerEntry {
	result := make([]triggerEntry, 0, len(sagas))
	for _, saga := range sagas {
		if prefix != "" && !strings.HasPrefix(saga.GetTrigger(), prefix+":") {
			continue
		}
		parts := strings.SplitN(saga.GetTrigger(), ":", 2)
		triggerType := saga.GetTrigger()
		triggerPath := ""
		if len(parts) == 2 {
			triggerType = parts[0]
			triggerPath = parts[1]
		}
		result = append(result, triggerEntry{
			SagaName:    saga.GetName(),
			TriggerType: triggerType,
			TriggerPath: triggerPath,
		})
	}
	return result
}

// extractAccountTypePolicies converts manifest account type definitions into policy entries.
// Account types without CEL policies are excluded.
func extractAccountTypePolicies(accountTypes []*controlplanev1.AccountTypeDefinition) []policyEntry {
	result := make([]policyEntry, 0, len(accountTypes))
	for _, at := range accountTypes {
		if at.GetPolicies() == nil {
			continue
		}
		if at.GetPolicies().GetValidation() == "" && at.GetPolicies().GetBucketing() == "" {
			continue
		}
		result = append(result, policyEntry{
			AccountTypeCode: at.GetCode(),
			Validation:      at.GetPolicies().GetValidation(),
			Bucketing:       at.GetPolicies().GetBucketing(),
		})
	}
	return result
}

// buildMarketDataQueryTool creates the meridian_market_data_query tool.
func buildMarketDataQueryTool(client MarketInformationClient) Tool {
	return Tool{
		Name:        "meridian_market_data_query",
		Description: "Lists market data sets or queries observations for a specific dataset. Use dataset_code to query observations for a specific set.",
		Category:    CategoryRead,
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"dataset_code": map[string]interface{}{
					"type":        "string",
					"description": "Dataset code to retrieve observations for (e.g., USD_EUR_FX). Omit to list all datasets.",
				},
				"status_filter": map[string]interface{}{
					"type":        "string",
					"description": "Filter datasets by status: ACTIVE, DRAFT, or DEPRECATED. Only used when dataset_code is omitted.",
					"enum":        []interface{}{statusActive, statusDraft, statusDeprecated},
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (1-100).",
					"minimum":     1,
					"maximum":     100,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			if client == nil {
				return formatError("market_information client not configured"), nil
			}

			var p struct {
				DatasetCode  string `json:"dataset_code"`
				StatusFilter string `json:"status_filter"`
				PageSize     int32  `json:"page_size"`
			}
			unmarshalErr := json.Unmarshal(params, &p)
			if unmarshalErr != nil {
				return formatError("invalid parameters: " + unmarshalErr.Error()), nil //nolint:nilerr // tool errors are returned in the result, not as Go errors
			}

			if p.DatasetCode != "" {
				return queryObservations(ctx, client, p.DatasetCode, p.PageSize)
			}
			return queryDatasets(ctx, client, p.StatusFilter, p.PageSize)
		},
	}
}

// queryObservations lists observations for a specific dataset code.
func queryObservations(ctx context.Context, client MarketInformationClient, datasetCode string, pageSize int32) (interface{}, error) {
	req := &marketinformationv1.ListObservationsRequest{
		DatasetCode: datasetCode,
		PageSize:    pageSize,
	}
	resp, err := client.ListObservations(ctx, req)
	if err != nil {
		fe := mcperrors.FormatGRPCError(err)
		return fe, nil
	}

	observations := make([]map[string]interface{}, 0, len(resp.GetObservations()))
	for _, obs := range resp.GetObservations() {
		observations = append(observations, summarizeObservation(obs))
	}

	return map[string]interface{}{
		"dataset_code":    datasetCode,
		"observations":    observations,
		"count":           len(observations),
		"next_page_token": resp.GetNextPageToken(),
	}, nil
}

// queryDatasets lists all datasets with an optional status filter.
func queryDatasets(ctx context.Context, client MarketInformationClient, statusFilter string, pageSize int32) (interface{}, error) {
	req := &marketinformationv1.ListDataSetsRequest{
		PageSize: pageSize,
	}
	if statusFilter != "" {
		req.StatusFilter = parseDataSetStatus(statusFilter)
	}

	resp, err := client.ListDataSets(ctx, req)
	if err != nil {
		fe := mcperrors.FormatGRPCError(err)
		return fe, nil
	}

	datasets := make([]map[string]interface{}, 0, len(resp.GetDatasets()))
	for _, ds := range resp.GetDatasets() {
		datasets = append(datasets, summarizeDataSet(ds))
	}

	return map[string]interface{}{
		"datasets":        datasets,
		"count":           len(datasets),
		"next_page_token": resp.GetNextPageToken(),
	}, nil
}

// ---- helpers ----

// formatError returns a structured error result for use in tool responses.
func formatError(msg string) map[string]interface{} {
	return map[string]interface{}{
		"valid":  false,
		"errors": []map[string]interface{}{{"type": "generic", "message": msg}},
	}
}

// buildManifestSummary converts a Manifest into a hierarchical summary map.
func buildManifestSummary(version string, m *controlplanev1.Manifest) map[string]interface{} {
	instruments := make([]map[string]interface{}, 0, len(m.GetInstruments()))
	for _, inst := range m.GetInstruments() {
		instruments = append(instruments, map[string]interface{}{
			"code": inst.GetCode(),
			"name": inst.GetName(),
			"type": inst.GetType().String(),
			"unit": func() string {
				if inst.GetDimensions() != nil {
					return inst.GetDimensions().GetUnit()
				}
				return ""
			}(),
			"precision": func() int32 {
				if inst.GetDimensions() != nil {
					return inst.GetDimensions().GetPrecision()
				}
				return 0
			}(),
		})
	}

	accountTypes := make([]map[string]interface{}, 0, len(m.GetAccountTypes()))
	for _, at := range m.GetAccountTypes() {
		entry := map[string]interface{}{
			"code":           at.GetCode(),
			"name":           at.GetName(),
			"normal_balance": at.GetNormalBalance().String(),
		}
		if len(at.GetAllowedInstruments()) > 0 {
			entry["allowed_instruments"] = at.GetAllowedInstruments()
		}
		accountTypes = append(accountTypes, entry)
	}

	valuationRules := make([]map[string]interface{}, 0, len(m.GetValuationRules()))
	for _, vr := range m.GetValuationRules() {
		valuationRules = append(valuationRules, map[string]interface{}{
			"from_instrument": vr.GetFromInstrument(),
			"to_instrument":   vr.GetToInstrument(),
			"method":          vr.GetMethod().String(),
			"source":          vr.GetSource(),
		})
	}

	sagas := make([]map[string]interface{}, 0, len(m.GetSagas()))
	for _, s := range m.GetSagas() {
		sagas = append(sagas, map[string]interface{}{
			"name":    s.GetName(),
			"trigger": s.GetTrigger(),
		})
	}

	paymentRails := make([]map[string]interface{}, 0, len(m.GetPaymentRails()))
	for _, pr := range m.GetPaymentRails() {
		paymentRails = append(paymentRails, map[string]interface{}{
			"provider": pr.GetProvider(),
			"mode":     pr.GetMode().String(),
		})
	}

	result := map[string]interface{}{
		"version": version,
		"economy": map[string]interface{}{
			"instruments": map[string]interface{}{
				"count": len(instruments),
				"items": instruments,
			},
			"account_types": map[string]interface{}{
				"count": len(accountTypes),
				"items": accountTypes,
			},
			"valuation_rules": map[string]interface{}{
				"count": len(valuationRules),
				"items": valuationRules,
			},
			"sagas": map[string]interface{}{
				"count": len(sagas),
				"items": sagas,
			},
			"payment_rails": map[string]interface{}{
				"count": len(paymentRails),
				"items": paymentRails,
			},
		},
	}

	if m.GetMetadata() != nil {
		result["metadata"] = map[string]interface{}{
			"name":        m.GetMetadata().GetName(),
			"industry":    m.GetMetadata().GetIndustry(),
			"description": m.GetMetadata().GetDescription(),
		}
	}

	return result
}

// summarizeInstrument returns a concise map of key instrument fields for listing.
func summarizeInstrument(inst *referencedatav1.InstrumentDefinition) map[string]interface{} {
	if inst == nil {
		return nil
	}
	return map[string]interface{}{
		"id":           inst.GetId(),
		"code":         inst.GetCode(),
		"version":      inst.GetVersion(),
		"dimension":    inst.GetDimension().String(),
		"precision":    inst.GetPrecision(),
		"status":       inst.GetStatus().String(),
		"display_name": inst.GetDisplayName(),
		"is_system":    inst.GetIsSystem(),
	}
}

// detailInstrument returns a full map of all instrument fields.
func detailInstrument(inst *referencedatav1.InstrumentDefinition) map[string]interface{} {
	if inst == nil {
		return nil
	}
	result := map[string]interface{}{
		"id":                         inst.GetId(),
		"code":                       inst.GetCode(),
		"version":                    inst.GetVersion(),
		"dimension":                  inst.GetDimension().String(),
		"precision":                  inst.GetPrecision(),
		"status":                     inst.GetStatus().String(),
		"display_name":               inst.GetDisplayName(),
		"description":                inst.GetDescription(),
		"is_system":                  inst.GetIsSystem(),
		"validation_expression":      inst.GetValidationExpression(),
		"fungibility_key_expression": inst.GetFungibilityKeyExpression(),
		"error_message_expression":   inst.GetErrorMessageExpression(),
		"attribute_schema":           inst.GetAttributeSchema(),
	}
	if inst.GetSuccessorId() != "" {
		result["successor_id"] = inst.GetSuccessorId()
	}
	if inst.GetCreatedAt() != nil {
		result["created_at"] = inst.GetCreatedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if inst.GetActivatedAt() != nil {
		result["activated_at"] = inst.GetActivatedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if inst.GetDeprecatedAt() != nil {
		result["deprecated_at"] = inst.GetDeprecatedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	return result
}

// summarizeSaga returns a concise map for saga listing.
func summarizeSaga(s *sagav1.SagaDefinition) map[string]interface{} {
	if s == nil {
		return nil
	}
	return map[string]interface{}{
		"id":           s.GetId(),
		"name":         s.GetName(),
		"version":      s.GetVersion(),
		"status":       s.GetStatus().String(),
		"is_system":    s.GetIsSystem(),
		"display_name": s.GetDisplayName(),
		"description":  s.GetDescription(),
	}
}

// detailSaga returns a full map of all saga fields.
func detailSaga(s *sagav1.SagaDefinition) map[string]interface{} {
	if s == nil {
		return nil
	}
	result := map[string]interface{}{
		"id":                       s.GetId(),
		"name":                     s.GetName(),
		"version":                  s.GetVersion(),
		"status":                   s.GetStatus().String(),
		"is_system":                s.GetIsSystem(),
		"display_name":             s.GetDisplayName(),
		"description":              s.GetDescription(),
		"script":                   s.GetScript(),
		"preconditions_expression": s.GetPreconditionsExpression(),
	}
	if s.GetSuccessorId() != "" {
		result["successor_id"] = s.GetSuccessorId()
	}
	if s.GetCreatedAt() != nil {
		result["created_at"] = s.GetCreatedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if s.GetUpdatedAt() != nil {
		result["updated_at"] = s.GetUpdatedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if s.GetActivatedAt() != nil {
		result["activated_at"] = s.GetActivatedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if s.GetDeprecatedAt() != nil {
		result["deprecated_at"] = s.GetDeprecatedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	return result
}

// summarizeDataSet returns a concise map for dataset listing.
func summarizeDataSet(ds *marketinformationv1.DataSetDefinition) map[string]interface{} {
	if ds == nil {
		return nil
	}
	return map[string]interface{}{
		"id":           ds.GetId(),
		"code":         ds.GetCode(),
		"version":      ds.GetVersion(),
		"category":     ds.GetCategory().String(),
		"unit":         ds.GetUnit(),
		"status":       ds.GetStatus().String(),
		"display_name": ds.GetDisplayName(),
		"description":  ds.GetDescription(),
	}
}

// summarizeObservation returns a concise map for observation listing.
func summarizeObservation(obs *marketinformationv1.MarketPriceObservation) map[string]interface{} {
	if obs == nil {
		return nil
	}
	result := map[string]interface{}{
		"id":              obs.GetId(),
		"dataset_code":    obs.GetDatasetCode(),
		"dataset_version": obs.GetDatasetVersion(),
		"resolution_key":  obs.GetResolutionKeyValue(),
		"value":           obs.GetValue(),
		"quality":         obs.GetQuality().String(),
	}
	if obs.GetObservedAt() != nil {
		result["observed_at"] = obs.GetObservedAt().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if obs.GetValidFrom() != nil {
		result["valid_from"] = obs.GetValidFrom().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	if obs.GetValidTo() != nil {
		result["valid_to"] = obs.GetValidTo().AsTime().Format("2006-01-02T15:04:05Z07:00")
	}
	return result
}

// parseInstrumentStatus converts a string to a referencedatav1.InstrumentStatus.
func parseInstrumentStatus(s string) referencedatav1.InstrumentStatus {
	switch strings.ToUpper(s) {
	case statusActive:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE
	case statusDraft:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT
	case statusDeprecated:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED
	default:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED
	}
}

// parseSagaStatus converts a string to a sagav1.SagaStatus.
func parseSagaStatus(s string) sagav1.SagaStatus {
	switch strings.ToUpper(s) {
	case statusActive:
		return sagav1.SagaStatus_SAGA_STATUS_ACTIVE
	case statusDraft:
		return sagav1.SagaStatus_SAGA_STATUS_DRAFT
	case statusDeprecated:
		return sagav1.SagaStatus_SAGA_STATUS_DEPRECATED
	default:
		return sagav1.SagaStatus_SAGA_STATUS_UNSPECIFIED
	}
}

// parseDataSetStatus converts a string to a marketinformationv1.DataSetStatus.
func parseDataSetStatus(s string) marketinformationv1.DataSetStatus {
	switch strings.ToUpper(s) {
	case statusActive:
		return marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE
	case statusDraft:
		return marketinformationv1.DataSetStatus_DATA_SET_STATUS_DRAFT
	case statusDeprecated:
		return marketinformationv1.DataSetStatus_DATA_SET_STATUS_DEPRECATED
	default:
		return marketinformationv1.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED
	}
}
