package tools

import (
	"context"
	"encoding/json"
	"strings"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

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
