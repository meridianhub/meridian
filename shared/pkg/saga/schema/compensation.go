package schema

import (
	"go.starlark.net/starlark"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// trackStepResult records the step result (success or failure) for saga compensation tracking.
func trackStepResult(thread *starlark.Thread, fullName string, result any, err error, params map[string]any, handlerDef *HandlerDef) {
	stepResultsVal := thread.Local("saga.StepResults")
	if stepResultsVal == nil {
		return
	}
	stepResults, ok := stepResultsVal.(*[]saga.StepResult)
	if !ok {
		return
	}

	stepResult := saga.StepResult{
		StepName: fullName,
		Success:  err == nil,
		Output:   result,
	}

	if err != nil {
		stepResult.Error = err.Error()
	} else if handlerDef.Compensate != "" {
		stepResult.CompensateHandler = handlerDef.Compensate
		stepResult.CompensateParams = buildCompensateParams(result, params, handlerDef.Compensate)
	}

	*stepResults = append(*stepResults, stepResult)
}

// buildCompensateParams derives compensation parameters from the forward step's output and input.
func buildCompensateParams(result any, params map[string]any, compensateHandler string) map[string]any {
	compensateParams := make(map[string]any)

	// Copy ALL fields from output (compensation handlers need version, status, etc.)
	if output, ok := result.(map[string]interface{}); ok {
		for key, value := range output {
			compensateParams[key] = value
		}
	}

	// Copy commonly-needed input fields for compensation context
	for _, field := range []string{
		"transaction_id", "account_id", "position_id", "direction",
		"amount", "instrument_code", "currency", "booking_log_id", "posting_id", "posting_type",
	} {
		if value, ok := params[field]; ok {
			compensateParams[field] = value
		}
	}

	// Handle field aliases: position_id and account_id are often interchangeable
	if posID, ok := compensateParams["position_id"]; ok {
		if _, hasAcctID := compensateParams["account_id"]; !hasAcctID {
			compensateParams["account_id"] = posID
		}
	}
	if acctID, ok := compensateParams["account_id"]; ok {
		if _, hasPosID := compensateParams["position_id"]; !hasPosID {
			compensateParams["position_id"] = acctID
		}
	}

	// Invert direction for financial posting compensations
	if compensateHandler == "financial_accounting.compensate_posting" {
		if direction, ok := compensateParams["direction"].(string); ok {
			switch direction {
			case "DEBIT":
				compensateParams["direction"] = "CREDIT"
			case "CREDIT":
				compensateParams["direction"] = "DEBIT"
			}
		}
	}

	return compensateParams
}
