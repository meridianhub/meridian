package tools

import (
	"context"
	"encoding/json"

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
	instruments := buildInstrumentSummaries(m.GetInstruments())
	accountTypes := buildAccountTypeSummaries(m.GetAccountTypes())
	valuationRules := buildValuationRuleSummaries(m.GetValuationRules())
	sagas := buildSagaSummaries(m.GetSagas())
	paymentRails := buildPaymentRailSummaries(m.GetPaymentRails())

	result := map[string]interface{}{
		"version": version,
		"economy": map[string]interface{}{
			"instruments":     map[string]interface{}{"count": len(instruments), "items": instruments},
			"account_types":   map[string]interface{}{"count": len(accountTypes), "items": accountTypes},
			"valuation_rules": map[string]interface{}{"count": len(valuationRules), "items": valuationRules},
			"sagas":           map[string]interface{}{"count": len(sagas), "items": sagas},
			"payment_rails":   map[string]interface{}{"count": len(paymentRails), "items": paymentRails},
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

// buildInstrumentSummaries converts manifest instruments to summary maps.
func buildInstrumentSummaries(instruments []*controlplanev1.InstrumentDefinition) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(instruments))
	for _, inst := range instruments {
		unit := ""
		var precision int32
		if inst.GetDimensions() != nil {
			unit = inst.GetDimensions().GetUnit()
			precision = inst.GetDimensions().GetPrecision()
		}
		result = append(result, map[string]interface{}{
			"code":      inst.GetCode(),
			"name":      inst.GetName(),
			"type":      inst.GetType().String(),
			"unit":      unit,
			"precision": precision,
		})
	}
	return result
}

// buildAccountTypeSummaries converts manifest account types to summary maps.
func buildAccountTypeSummaries(accountTypes []*controlplanev1.AccountTypeDefinition) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(accountTypes))
	for _, at := range accountTypes {
		entry := map[string]interface{}{
			"code":           at.GetCode(),
			"name":           at.GetName(),
			"normal_balance": at.GetNormalBalance().String(),
		}
		if len(at.GetAllowedInstruments()) > 0 {
			entry["allowed_instruments"] = at.GetAllowedInstruments()
		}
		result = append(result, entry)
	}
	return result
}

// buildValuationRuleSummaries converts manifest valuation rules to summary maps.
func buildValuationRuleSummaries(rules []*controlplanev1.ValuationRule) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(rules))
	for _, vr := range rules {
		result = append(result, map[string]interface{}{
			"from_instrument": vr.GetFromInstrument(),
			"to_instrument":   vr.GetToInstrument(),
			"method":          vr.GetMethod().String(),
			"source":          vr.GetSource(),
		})
	}
	return result
}

// buildSagaSummaries converts manifest sagas to summary maps.
func buildSagaSummaries(sagaList []*controlplanev1.SagaDefinition) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(sagaList))
	for _, s := range sagaList {
		result = append(result, map[string]interface{}{
			"name":    s.GetName(),
			"trigger": s.GetTrigger(),
		})
	}
	return result
}

// buildPaymentRailSummaries converts manifest payment rails to summary maps.
func buildPaymentRailSummaries(rails []*controlplanev1.PaymentRails) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(rails))
	for _, pr := range rails {
		result = append(result, map[string]interface{}{
			"provider": pr.GetProvider(),
			"mode":     pr.GetMode().String(),
		})
	}
	return result
}
