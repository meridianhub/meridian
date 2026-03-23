package validator

import (
	"fmt"
	"regexp"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// handlerCallPattern matches service.method( call patterns in Starlark scripts.
// It captures the service name and method name for handler completeness checks.
// NOTE: This scans raw source text so it may match occurrences in comments or string
// literals. A proper Starlark AST walk would eliminate false positives, but this
// approach is acceptable for a warning-level check.
var handlerCallPattern = regexp.MustCompile(`\b([a-z][a-z0-9_]*)\.([a-z][a-z0-9_]*)\s*\(`)

// validateSettlementCompleteness checks that internal accounts use instruments
// that are permitted by their account type's allowed_instruments list.
// When allowed_instruments is empty the account type accepts all instruments,
// so no error is raised in that case.
func (v *ManifestValidator) validateSettlementCompleteness(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Build a map of account_type code -> allowed_instruments set (only for restricted types).
	restricted := make(map[string]map[string]bool)
	for _, at := range manifest.GetAccountTypes() {
		if len(at.GetAllowedInstruments()) == 0 {
			continue
		}
		allowed := make(map[string]bool, len(at.GetAllowedInstruments()))
		for _, code := range at.GetAllowedInstruments() {
			allowed[code] = true
		}
		restricted[at.GetCode()] = allowed
	}

	for i, ia := range manifest.GetInternalAccounts() {
		allowed, ok := restricted[ia.GetAccountType()]
		if !ok {
			continue
		}
		instrCode := ia.GetInstrument()
		if instrCode == "" || allowed[instrCode] {
			continue
		}
		allowedList := mapKeys(allowed)
		ve := ValidationError{
			Severity:        SeverityError,
			Path:            fmt.Sprintf("internal_accounts[%d].instrument", i),
			Code:            "INSTRUMENT_NOT_ALLOWED_FOR_ACCOUNT_TYPE",
			Message:         fmt.Sprintf("internal account %q uses instrument %q but account type %q only permits: %s", ia.GetCode(), instrCode, ia.GetAccountType(), strings.Join(allowedList, ", ")),
			AvailableFields: allowedList,
			ResourceType:    "internal_account",
			ResourceID:      ia.GetCode(),
		}
		if suggestion := findClosestMatch(instrCode, allowedList); suggestion != "" {
			ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
		addError(result, ve)
	}
}

// validateSagaHandlerCompleteness performs a static scan of each saga's Starlark
// script to detect calls to service handlers that are not registered in the schema
// registry. It only runs when the registry has at least one handler loaded, so
// manifests validated without a schema registry are not affected.
func (v *ManifestValidator) validateSagaHandlerCompleteness(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	knownHandlers := v.schemaRegistry.ListHandlers()
	if len(knownHandlers) == 0 {
		return
	}
	handlerSet := make(map[string]bool, len(knownHandlers))
	for _, h := range knownHandlers {
		handlerSet[h] = true
	}

	serviceSet := make(map[string]bool, len(knownServiceBindings))
	for _, svc := range knownServiceBindings {
		serviceSet[svc] = true
	}

	for i, saga := range manifest.GetSagas() {
		v.checkSagaHandlerCalls(saga, i, handlerSet, serviceSet, knownHandlers, result)
	}
}

// checkSagaHandlerCalls scans a single saga script for unknown handler references.
func (v *ManifestValidator) checkSagaHandlerCalls(
	saga *controlplanev1.SagaDefinition,
	sagaIdx int,
	handlerSet map[string]bool,
	serviceSet map[string]bool,
	knownHandlers []string,
	result *ValidationResult,
) {
	script := saga.GetScript()
	if script == "" {
		return
	}
	seen := make(map[string]bool)
	for _, m := range handlerCallPattern.FindAllStringSubmatch(script, -1) {
		service, method := m[1], m[2]
		if !serviceSet[service] {
			continue
		}
		handlerName := service + "." + method
		if handlerSet[handlerName] || seen[handlerName] {
			continue
		}
		seen[handlerName] = true
		ve := ValidationError{
			Severity:        SeverityWarning,
			Path:            fmt.Sprintf("sagas[%d].script", sagaIdx),
			Code:            "UNKNOWN_HANDLER_REFERENCE",
			Message:         fmt.Sprintf("saga %q calls %q which is not defined in the handler schema", saga.GetName(), handlerName),
			AvailableFields: knownHandlers,
			ResourceType:    "saga",
			ResourceID:      saga.GetName(),
		}
		if suggestion := findClosestMatch(handlerName, knownHandlers); suggestion != "" {
			ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
		addError(result, ve)
	}
}

// validateValuationRuleCycles detects cycles in the valuation rule graph.
// A cycle such as GBP->KWH->GBP would mean an instrument can be converted back
// to itself, which indicates a configuration error.
func (v *ManifestValidator) validateValuationRuleCycles(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	adj := buildValuationRuleGraph(manifest)
	if len(adj) == 0 {
		return
	}
	color := make(map[string]int)
	reported := make(map[string]bool)
	for node := range adj {
		if color[node] == colorWhite {
			valuationRuleDFS(node, nil, adj, color, reported, result)
		}
	}
}

// colorWhite/colorGray/colorBlack are DFS traversal states for cycle detection.
const (
	colorWhite = 0
	colorGray  = 1
	colorBlack = 2
)

// buildValuationRuleGraph constructs the adjacency list from valuation rules.
func buildValuationRuleGraph(manifest *controlplanev1.Manifest) map[string][]string {
	adj := make(map[string][]string)
	for _, rule := range manifest.GetValuationRules() {
		from := rule.GetFromInstrument()
		to := rule.GetToInstrument()
		if from == "" || to == "" || from == to {
			continue
		}
		adj[from] = append(adj[from], to)
	}
	return adj
}

// valuationRuleDFS performs a depth-first search on the valuation rule graph,
// reporting any back-edges (cycles) found.
func valuationRuleDFS(
	node string,
	path []string,
	adj map[string][]string,
	color map[string]int,
	reported map[string]bool,
	result *ValidationResult,
) {
	color[node] = colorGray
	path = append(path, node)
	for _, neighbor := range adj[node] {
		switch color[neighbor] {
		case colorGray:
			reportValuationCycle(neighbor, path, reported, result)
		case colorWhite:
			valuationRuleDFS(neighbor, path, adj, color, reported, result)
		}
	}
	color[node] = colorBlack
}

// reportValuationCycle records a cycle error, deduplicating on the cycle entry point.
func reportValuationCycle(
	cycleEntry string,
	path []string,
	reported map[string]bool,
	result *ValidationResult,
) {
	if reported[cycleEntry] {
		return
	}
	reported[cycleEntry] = true
	cycleStart := 0
	for i, n := range path {
		if n == cycleEntry {
			cycleStart = i
			break
		}
	}
	cycleStr := strings.Join(path[cycleStart:], " -> ") + " -> " + cycleEntry
	addError(result, ValidationError{
		Severity:     SeverityError,
		Path:         "valuation_rules",
		Code:         "VALUATION_RULE_CYCLE",
		Message:      fmt.Sprintf("circular dependency detected in valuation rules: %s", cycleStr),
		ResourceType: "valuation_rule",
		ResourceID:   cycleEntry,
	})
}

// validateInstrumentAccountTypeConsistency verifies that the instruments listed
// in account_types.allowed_instruments are actually used by at least one
// internal_account of that type when internal accounts are defined in the manifest.
// An allowed instrument that has no corresponding internal account may indicate
// a misconfiguration or incomplete manifest. This is a warning, not an error,
// because tenants can create runtime accounts for any allowed instrument.
func (v *ManifestValidator) validateInstrumentAccountTypeConsistency(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	if len(manifest.GetInternalAccounts()) == 0 {
		return // no internal accounts to validate against
	}

	// Build a map of (account_type, instrument) pairs actually used.
	usedPairs := make(map[string]bool)
	for _, ia := range manifest.GetInternalAccounts() {
		usedPairs[ia.GetAccountType()+":"+ia.GetInstrument()] = true
	}

	for i, at := range manifest.GetAccountTypes() {
		for j, instrCode := range at.GetAllowedInstruments() {
			if !usedPairs[at.GetCode()+":"+instrCode] {
				addError(result, ValidationError{
					Severity:     SeverityWarning,
					Path:         fmt.Sprintf("account_types[%d].allowed_instruments[%d]", i, j),
					Code:         "INSTRUMENT_UNUSED_IN_ACCOUNT_TYPE",
					Message:      fmt.Sprintf("account type %q permits instrument %q but no internal account uses this combination", at.GetCode(), instrCode),
					Suggestion:   "Add an internal_account entry for this instrument, or remove it from allowed_instruments if not needed",
					ResourceType: "account_type",
					ResourceID:   at.GetCode(),
				})
			}
		}
	}
}

// validateOrphanedInstruments warns when instruments are defined in the manifest
// but never referenced by any account type, valuation rule, or internal account.
// Orphaned instruments increase manifest complexity without providing value.
func (v *ManifestValidator) validateOrphanedInstruments(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	if len(manifest.GetInstruments()) == 0 {
		return
	}

	referenced := collectReferencedInstruments(manifest)

	for i, inst := range manifest.GetInstruments() {
		if !referenced[inst.GetCode()] {
			addError(result, ValidationError{
				Severity:     SeverityWarning,
				Path:         fmt.Sprintf("instruments[%d].code", i),
				Code:         "ORPHANED_INSTRUMENT",
				Message:      fmt.Sprintf("instrument %q is defined but not referenced by any account_type, valuation_rule, or internal_account", inst.GetCode()),
				Suggestion:   "Add this instrument to an account_type's allowed_instruments or a valuation_rule, or remove it if unused",
				ResourceType: "instrument",
				ResourceID:   inst.GetCode(),
			})
		}
	}
}

// collectReferencedInstruments returns the set of instrument codes that appear
// in account_types.allowed_instruments, valuation_rules, or internal_accounts.
func collectReferencedInstruments(manifest *controlplanev1.Manifest) map[string]bool {
	referenced := make(map[string]bool)
	for _, at := range manifest.GetAccountTypes() {
		for _, code := range at.GetAllowedInstruments() {
			referenced[code] = true
		}
	}
	for _, rule := range manifest.GetValuationRules() {
		referenced[rule.GetFromInstrument()] = true
		referenced[rule.GetToInstrument()] = true
	}
	for _, ia := range manifest.GetInternalAccounts() {
		referenced[ia.GetInstrument()] = true
	}
	return referenced
}
