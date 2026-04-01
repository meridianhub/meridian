package validator

import (
	"fmt"
	"regexp"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// dispatchInstructionRegex matches dispatch_instruction calls in Starlark scripts
// to extract the instruction type, supporting both positional and keyword argument styles.
// NOTE: This scans raw source text so it may match occurrences in comments or string
// literals, producing false negatives (suppressing orphan warnings). A proper Starlark
// AST walk would eliminate this, but the current approach is acceptable for a warning-level check.
var dispatchInstructionRegex = regexp.MustCompile(
	`dispatch_instruction\s*\(\s*(?:instruction_type\s*=\s*)?["']([^"']+)["']`,
)

// validateOperationalGatewayOrphans detects unused provider connections and instruction routes.
func (v *ManifestValidator) validateOperationalGatewayOrphans(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	gw := manifest.GetOperationalGateway()
	if gw == nil {
		return
	}

	v.detectOrphanProviderConnections(manifest, gw, result)
	v.detectOrphanInstructionRoutes(manifest, gw, result)
}

// detectOrphanProviderConnections warns on provider connections not referenced by
// any instruction route or webhook trigger.
func (v *ManifestValidator) detectOrphanProviderConnections(
	manifest *controlplanev1.Manifest,
	gw *controlplanev1.OperationalGatewayConfig,
	result *ValidationResult,
) {
	usedConnections := make(map[string]bool)
	for _, route := range gw.GetInstructionRoutes() {
		usedConnections[route.GetConnectionId()] = true
		if fb := route.GetFallbackConnectionId(); fb != "" {
			usedConnections[fb] = true
		}
	}

	// Also count connections used as webhook sources in saga triggers
	for _, saga := range manifest.GetSagas() {
		if source := extractWebhookSource(saga.GetTrigger()); source != "" {
			usedConnections[source] = true
		}
	}

	for i, conn := range gw.GetProviderConnections() {
		cid := conn.GetConnectionId()
		if !usedConnections[cid] {
			addError(result, ValidationError{
				Severity:     SeverityWarning,
				Path:         fmt.Sprintf("operational_gateway.provider_connections[%d].connection_id", i),
				Code:         "ORPHAN_PROVIDER_CONNECTION",
				Message:      fmt.Sprintf("provider connection %q is not referenced by any instruction route or webhook trigger", cid),
				ResourceType: "provider_connection",
				ResourceID:   cid,
			})
		}
	}
}

// detectOrphanInstructionRoutes warns on instruction routes not dispatched by any saga.
func (v *ManifestValidator) detectOrphanInstructionRoutes(
	manifest *controlplanev1.Manifest,
	gw *controlplanev1.OperationalGatewayConfig,
	result *ValidationResult,
) {
	usedInstructionTypes := make(map[string]bool)
	for _, saga := range manifest.GetSagas() {
		for _, m := range dispatchInstructionRegex.FindAllStringSubmatch(saga.GetScript(), -1) {
			usedInstructionTypes[m[1]] = true
		}
	}

	for i, route := range gw.GetInstructionRoutes() {
		instrType := route.GetInstructionType()
		if !usedInstructionTypes[instrType] {
			addError(result, ValidationError{
				Severity:     SeverityWarning,
				Path:         fmt.Sprintf("operational_gateway.instruction_routes[%d].instruction_type", i),
				Code:         "ORPHAN_INSTRUCTION_ROUTE",
				Message:      fmt.Sprintf("instruction type %q is not dispatched by any saga script", instrType),
				ResourceType: "instruction_route",
				ResourceID:   instrType,
			})
		}
	}
}

// validateImmutability is intentionally a no-op. The previous implementation matched
// instruments and account types by array index position, which produced false positives
// whenever manifests had different compositions (e.g. applying an energy manifest after
// a banking manifest). Codes are primary keys and identity is code-based, so a "rename"
// is actually a removal + addition - already caught by validateDestructiveChanges.
func (v *ManifestValidator) validateImmutability(
	_ *controlplanev1.Manifest,
	_ *controlplanev1.Manifest,
	_ *ValidationResult,
) {
}

// validateDestructiveChanges detects removal of resources that have dependencies in the
// previous manifest. Removing an instrument used by account types or valuation rules,
// an account type referenced in sagas, or a saga with dependents produces an error.
// When force is set via WithForceDestructiveChanges, these errors become warnings.
func (v *ManifestValidator) validateDestructiveChanges(
	current *controlplanev1.Manifest,
	previous *controlplanev1.Manifest,
	callLogs map[string][]schema.HandlerCallInfo,
	cfg *validateConfig,
	result *ValidationResult,
) {
	prevGraph := ExtractRelationshipGraph(previous, callLogs)

	currentInstruments := make(map[string]bool)
	for _, inst := range current.GetInstruments() {
		currentInstruments[inst.GetCode()] = true
	}
	currentAccountTypes := make(map[string]bool)
	for _, acct := range current.GetAccountTypes() {
		currentAccountTypes[acct.GetCode()] = true
	}
	currentSagas := make(map[string]bool)
	for _, saga := range current.GetSagas() {
		currentSagas[saga.GetName()] = true
	}

	severity := SeverityError
	if cfg.forceDestructiveChanges {
		severity = SeverityWarning
	}

	validateRemovedInstruments(previous, currentInstruments, prevGraph, severity, result)
	validateRemovedAccountTypes(previous, currentAccountTypes, prevGraph, severity, result)
	validateRemovedSagas(previous, currentSagas, prevGraph, severity, result)
}

// validateRemovedInstruments checks for destructive instrument removals with dependencies.
func validateRemovedInstruments(previous *controlplanev1.Manifest, current map[string]bool, prevGraph *RelationshipGraph, severity Severity, result *ValidationResult) {
	for _, inst := range previous.GetInstruments() {
		code := inst.GetCode()
		if current[code] {
			continue
		}
		nodeID := "instrument:" + code
		impact := prevGraph.Impact(nodeID)
		if len(impact.AffectedNodes) > 0 {
			addError(result, ValidationError{
				Severity: severity,
				Path:     "instruments",
				Code:     "DESTRUCTIVE_INSTRUMENT_REMOVAL",
				Message:  fmt.Sprintf("cannot remove instrument %q: it is referenced by %s", code, strings.Join(impact.AffectedNodes, ", ")),
			})
		}
	}
}

// validateRemovedAccountTypes checks for destructive account type removals with dependents.
func validateRemovedAccountTypes(previous *controlplanev1.Manifest, current map[string]bool, prevGraph *RelationshipGraph, severity Severity, result *ValidationResult) {
	for _, acct := range previous.GetAccountTypes() {
		code := acct.GetCode()
		if current[code] {
			continue
		}
		nodeID := "account_type:" + code
		dependents := prevGraph.Dependents(nodeID)
		if len(dependents) > 0 {
			addError(result, ValidationError{
				Severity: severity,
				Path:     "account_types",
				Code:     "DESTRUCTIVE_ACCOUNT_TYPE_REMOVAL",
				Message:  fmt.Sprintf("cannot remove account type %q: it is referenced by %s", code, strings.Join(dependents, ", ")),
			})
		}
	}
}

// validateRemovedSagas checks for destructive saga removals with dependents.
func validateRemovedSagas(previous *controlplanev1.Manifest, current map[string]bool, prevGraph *RelationshipGraph, severity Severity, result *ValidationResult) {
	for _, saga := range previous.GetSagas() {
		name := saga.GetName()
		if current[name] {
			continue
		}
		nodeID := "saga:" + name
		dependents := prevGraph.Dependents(nodeID)
		if len(dependents) > 0 {
			addError(result, ValidationError{
				Severity: severity,
				Path:     "sagas",
				Code:     "DESTRUCTIVE_SAGA_REMOVAL",
				Message:  fmt.Sprintf("cannot remove saga %q: it is referenced by %s", name, strings.Join(dependents, ", ")),
			})
		}
	}
}
