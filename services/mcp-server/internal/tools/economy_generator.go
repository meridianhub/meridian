// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

// generatorUnavailableMessage is returned when the economy generator service is not deployed.
const generatorUnavailableMessage = "Economy generator is not available on this instance. " +
	"Use meridian_manifest_validate (mode=create) to check manually composed manifests, " +
	"or meridian_cookbook_list/describe for pattern examples."

// IsServiceUnavailable returns true when err is a transport-generated gRPC Unimplemented
// error indicating that the entire service is not registered on the server (i.e. not deployed).
// It deliberately excludes method-level Unimplemented errors that signal unsupported operations,
// by requiring the message to contain the transport phrase "unknown service".
func IsServiceUnavailable(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.Unimplemented && strings.Contains(st.Message(), "unknown service")
}

// EconomyGeneratorClient is the minimal interface for the EconomyGeneratorService RPCs.
type EconomyGeneratorClient interface {
	GenerateManifest(ctx context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error)
	GetGenerationContext(ctx context.Context, req *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error)
}

// RegisterEconomyGeneratorTools registers the economy generator MCP tools onto the SDK server.
// Tools are silently skipped if the client is nil.
func RegisterEconomyGeneratorTools(srv *mcp.Server, client EconomyGeneratorClient) {
	if client == nil {
		return
	}
	candidates := []Tool{
		buildEconomyGenerateContextTool(client),
		buildEconomyGenerateTool(client),
	}
	for _, t := range candidates {
		addTool(srv, t)
	}
}

// buildEconomyGenerateContextTool returns the meridian_economy_generate_context tool.
func buildEconomyGenerateContextTool(client EconomyGeneratorClient) Tool {
	return Tool{
		Name:     "meridian_economy_generate_context",
		Category: CategoryRead,
		Description: "Retrieve the context that would be used to generate a manifest from a natural language description, " +
			"without performing the actual generation. " +
			"Returns the handler reference card, available topics, manifest schema summary, and matched cookbook patterns. " +
			"Use this to understand which handlers and patterns are relevant before calling meridian_economy_generate.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Natural language description of the economy to inspect context for.",
				},
				"include_patterns": map[string]interface{}{
					"type":        "boolean",
					"description": "When false, excludes matched pattern fragments from the response. Defaults to true.",
				},
				"include_current_economy": map[string]interface{}{
					"type":        "boolean",
					"description": "When true, includes the tenant's current manifest YAML in the response. Requires tenant_id.",
				},
				"tenant_id": map[string]interface{}{
					"type":        "string",
					"description": "Tenant identifier. Required when include_current_economy is true.",
				},
			},
			"required": []interface{}{"description"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleEconomyGenerateContext(ctx, client, params)
		},
	}
}

// economyGenerateContextParams holds parsed parameters for meridian_economy_generate_context.
type economyGenerateContextParams struct {
	Description           string `json:"description"`
	IncludePatterns       *bool  `json:"include_patterns"`
	IncludeCurrentEconomy bool   `json:"include_current_economy"`
	TenantID              string `json:"tenant_id"`
}

// handleEconomyGenerateContext implements the meridian_economy_generate_context handler.
func handleEconomyGenerateContext(ctx context.Context, client EconomyGeneratorClient, params json.RawMessage) (interface{}, error) {
	var p economyGenerateContextParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if p.IncludeCurrentEconomy && p.TenantID == "" {
		return map[string]interface{}{
			"error":   "tenant_id is required when include_current_economy is true",
			"message": "Provide a tenant_id to include the current economy in the context.",
		}, nil
	}

	// include_patterns defaults to true; exclude_patterns is the inverse field in the proto.
	excludePatterns := p.IncludePatterns != nil && !*p.IncludePatterns

	req := &controlplanev1.GetGenerationContextRequest{
		Description:           p.Description,
		ExcludePatterns:       excludePatterns,
		IncludeCurrentEconomy: p.IncludeCurrentEconomy,
		TenantId:              p.TenantID,
	}

	resp, err := client.GetGenerationContext(ctx, req)
	if err != nil {
		if IsServiceUnavailable(err) {
			return map[string]interface{}{"message": generatorUnavailableMessage}, nil
		}
		return mcperrors.FormatGRPCError(err), nil
	}

	result := map[string]interface{}{
		"handler_reference_card":  resp.HandlerReferenceCard,
		"topic_list":              resp.TopicList,
		"manifest_schema_summary": resp.ManifestSchemaSummary,
	}

	if len(resp.MatchedPatterns) > 0 {
		patterns := make([]map[string]interface{}, 0, len(resp.MatchedPatterns))
		for _, pc := range resp.MatchedPatterns {
			entry := map[string]interface{}{
				"name":  pc.Name,
				"title": pc.Title,
				"score": pc.Score,
			}
			if pc.ManifestFragment != "" {
				entry["manifest_fragment"] = pc.ManifestFragment
			}
			if pc.SagaScript != "" {
				entry["saga_script"] = pc.SagaScript
			}
			patterns = append(patterns, entry)
		}
		result["matched_patterns"] = patterns
	}

	if resp.CurrentEconomyYaml != "" {
		result["current_economy_yaml"] = resp.CurrentEconomyYaml
	}

	return result, nil
}

// buildEconomyGenerateTool returns the meridian_economy_generate tool.
func buildEconomyGenerateTool(client EconomyGeneratorClient) Tool {
	return Tool{
		Name:     "meridian_economy_generate",
		Category: CategorySimulate,
		Description: "Generate a tenant manifest from a natural language description using AI assistance. " +
			"Produces a manifest YAML, validates it, and iterates to fix any validation errors. " +
			"Does not apply the manifest — use meridian_manifest_plan and meridian_manifest_apply to deploy it. " +
			"Use mode=amend with a tenant_id to incorporate changes into an existing economy.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Natural language description of the economy to generate.",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"description": "Generation mode: 'create' (default) generates a fresh manifest; 'amend' incorporates changes into the existing manifest (requires tenant_id).",
					"enum":        []interface{}{"create", "amend"},
				},
				"tenant_id": map[string]interface{}{
					"type":        "string",
					"description": "Tenant identifier. Required when mode is 'amend'.",
				},
				"preferences": map[string]interface{}{
					"type":        "object",
					"description": "Optional hints to guide generation.",
					"properties": map[string]interface{}{
						"industry": map[string]interface{}{
							"type":        "string",
							"description": "The tenant's industry (e.g., 'energy', 'fintech', 'healthcare').",
						},
						"instruments": map[string]interface{}{
							"type":        "array",
							"description": "Asset types the tenant works with (e.g., 'GBP', 'kWh', 'TONNE_CO2E').",
							"items":       map[string]interface{}{"type": "string"},
						},
						"patterns": map[string]interface{}{
							"type":        "array",
							"description": "Saga or workflow patterns to include (e.g., 'payment', 'settlement').",
							"items":       map[string]interface{}{"type": "string"},
						},
					},
				},
				"max_fix_iterations": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum validation-fix cycles (0–5). Defaults to 3 when unset.",
					"minimum":     0,
					"maximum":     5,
				},
			},
			"required": []interface{}{"description"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleEconomyGenerate(ctx, client, params)
		},
	}
}

// economyGeneratePreferences holds nested preferences for meridian_economy_generate.
type economyGeneratePreferences struct {
	Industry    string   `json:"industry"`
	Instruments []string `json:"instruments"`
	Patterns    []string `json:"patterns"`
}

// economyGenerateParams holds parsed parameters for meridian_economy_generate.
type economyGenerateParams struct {
	Description      string                      `json:"description"`
	Mode             string                      `json:"mode"`
	TenantID         string                      `json:"tenant_id"`
	Preferences      *economyGeneratePreferences `json:"preferences"`
	MaxFixIterations int32                       `json:"max_fix_iterations"`
}

// resolveGenerationMode converts the string mode parameter to a proto GenerationMode,
// returning an error response map if validation fails, or nil on success.
func resolveGenerationMode(mode, tenantID string) (controlplanev1.GenerationMode, map[string]interface{}) {
	switch mode {
	case "", "create":
		return controlplanev1.GenerationMode_GENERATION_MODE_CREATE, nil
	case "amend":
		if tenantID == "" {
			return 0, map[string]interface{}{
				"error":   "tenant_id is required when mode is 'amend'",
				"message": "Provide a tenant_id to amend the tenant's existing manifest.",
			}
		}
		return controlplanev1.GenerationMode_GENERATION_MODE_AMEND, nil
	default:
		return 0, map[string]interface{}{
			"error":   "invalid mode: " + mode,
			"message": "mode must be 'create' or 'amend'",
		}
	}
}

// buildGenerationMetadata converts proto GenerationMetadata to a map for JSON serialization.
func buildGenerationMetadata(m *controlplanev1.GenerationMetadata) map[string]interface{} {
	meta := map[string]interface{}{"fix_iterations": m.FixIterations}
	if len(m.PatternsUsed) > 0 {
		meta["patterns_used"] = m.PatternsUsed
	}
	if len(m.InstrumentsCreated) > 0 {
		meta["instruments_created"] = m.InstrumentsCreated
	}
	if len(m.AccountTypesCreated) > 0 {
		meta["account_types_created"] = m.AccountTypesCreated
	}
	if len(m.SagasCreated) > 0 {
		meta["sagas_created"] = m.SagasCreated
	}
	if len(m.Decisions) > 0 {
		meta["decisions"] = m.Decisions
	}
	return meta
}

// handleEconomyGenerate implements the meridian_economy_generate handler.
func handleEconomyGenerate(ctx context.Context, client EconomyGeneratorClient, params json.RawMessage) (interface{}, error) {
	var p economyGenerateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	mode, errResp := resolveGenerationMode(p.Mode, p.TenantID)
	if errResp != nil {
		return errResp, nil
	}

	req := &controlplanev1.GenerateManifestRequest{
		Description:      p.Description,
		Mode:             mode,
		TenantId:         p.TenantID,
		MaxFixIterations: p.MaxFixIterations,
	}

	if p.Preferences != nil {
		req.Preferences = &controlplanev1.GenerationPreferences{
			Industry:    p.Preferences.Industry,
			Instruments: p.Preferences.Instruments,
			Patterns:    p.Preferences.Patterns,
		}
	}

	resp, err := client.GenerateManifest(ctx, req)
	if err != nil {
		if IsServiceUnavailable(err) {
			return map[string]interface{}{"message": generatorUnavailableMessage}, nil
		}
		return mcperrors.FormatGRPCError(err), nil
	}

	result := map[string]interface{}{
		"manifest_yaml": resp.ManifestYaml,
		"valid":         resp.Valid,
	}

	if len(resp.ValidationErrors) > 0 {
		result["validation_errors"] = formatProtoValidationErrors(resp.ValidationErrors)
	}
	if len(resp.ValidationWarnings) > 0 {
		result["validation_warnings"] = formatProtoValidationErrors(resp.ValidationWarnings)
	}
	if resp.GenerationMetadata != nil {
		result["generation_metadata"] = buildGenerationMetadata(resp.GenerationMetadata)
	}

	return result, nil
}
