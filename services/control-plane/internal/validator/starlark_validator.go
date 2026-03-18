package validator

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/sandbox"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Known service bindings available on the saga context object.
// These are the service modules that Starlark scripts can call via ctx.<service>.
var knownServiceBindings = []string{
	"position_keeping",
	"financial_accounting",
	"current_account",
	"valuation_engine",
	"repository",
	"notification",
	"payment_order",
	"reconciliation",
	"reference_data",
}

// Known top-level Starlark builtins provided by the saga runtime.
var knownStarlarkBuiltins = []string{
	"input_data",
	"party_scope",
	"Decimal",
	"print",
	"len",
	"range",
	"str",
	"int",
	"float",
	"bool",
	"list",
	"dict",
	"tuple",
	"type",
	"True",
	"False",
	"None",
	"hasattr",
	"getattr",
	"enumerate",
	"zip",
	"sorted",
	"reversed",
	"min",
	"max",
	"any",
	"all",
	"hash",
	"repr",
	"fail",
}

// ErrUnhashable is returned when hashing a starlark module is attempted.
var ErrUnhashable = errors.New("unhashable: module")

// Starlark validation error codes.
const (
	// CodeStarlarkSyntaxError is used when a Starlark script has a syntax error.
	CodeStarlarkSyntaxError = "STARLARK_SYNTAX_ERROR"
	// CodeStarlarkCompilationError is used when a Starlark script fails to compile.
	CodeStarlarkCompilationError = "STARLARK_COMPILATION_ERROR"
)

// permissiveServiceStub is a Starlark value that accepts any attribute access and
// returns a permissive callable. Used as a fallback when no schema registry is
// configured, so scripts compile without typed handler validation.
type permissiveServiceStub struct {
	name string
}

var _ starlark.HasAttrs = (*permissiveServiceStub)(nil)

func newPermissiveServiceStub(name string) *permissiveServiceStub {
	return &permissiveServiceStub{name: name}
}

func (s *permissiveServiceStub) String() string        { return fmt.Sprintf("<service %s>", s.name) }
func (s *permissiveServiceStub) Type() string          { return "service" }
func (s *permissiveServiceStub) Freeze()               {}
func (s *permissiveServiceStub) Truth() starlark.Bool  { return starlark.True }
func (s *permissiveServiceStub) Hash() (uint32, error) { return 0, ErrUnhashable }
func (s *permissiveServiceStub) AttrNames() []string   { return nil }

func (s *permissiveServiceStub) Attr(name string) (starlark.Value, error) {
	// Return a permissive builtin that accepts any kwargs and returns a permissive result
	fullName := s.name + "." + name
	return starlark.NewBuiltin(fullName, func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return &permissiveResult{}, nil
	}), nil
}

// permissiveResult is returned by permissive handler stubs. It accepts any
// attribute access, returning an empty string for each, so that scripts can
// reference result fields (e.g. result.log_id) without errors.
type permissiveResult struct{}

var _ starlark.HasAttrs = (*permissiveResult)(nil)

func (r *permissiveResult) String() string        { return "<result>" }
func (r *permissiveResult) Type() string          { return "result" }
func (r *permissiveResult) Freeze()               {}
func (r *permissiveResult) Truth() starlark.Bool  { return starlark.True }
func (r *permissiveResult) Hash() (uint32, error) { return 0, ErrUnhashable }
func (r *permissiveResult) AttrNames() []string   { return nil }

func (r *permissiveResult) Attr(_ string) (starlark.Value, error) {
	return r, nil
}

// validateStarlarkScripts compiles each saga's Starlark script.
// Returns a map of saga name -> handler call info for relationship graph extraction.
func (v *ManifestValidator) validateStarlarkScripts(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) map[string][]schema.HandlerCallInfo {
	callLogs := make(map[string][]schema.HandlerCallInfo)
	for i, saga := range manifest.GetSagas() {
		script := saga.GetScript()
		if script == "" {
			continue
		}
		calls := v.validateSingleStarlarkScript(saga, script, fmt.Sprintf("sagas[%d].script", i), result)
		if calls != nil {
			callLogs[saga.GetName()] = calls
		}
	}
	return callLogs
}

// validateSingleStarlarkScript compiles and validates one Starlark script.
// Returns the handler call log for relationship graph extraction, or nil on error.
func (v *ManifestValidator) validateSingleStarlarkScript(
	saga *controlplanev1.SagaDefinition,
	script string,
	path string,
	result *ValidationResult,
) []schema.HandlerCallInfo {
	sagaName := saga.GetName()
	if len(script) > 65536 {
		addError(result, ValidationError{
			Severity:     SeverityError,
			Path:         path,
			Code:         "STARLARK_SCRIPT_TOO_LARGE",
			Message:      fmt.Sprintf("Starlark script exceeds maximum size of 65536 bytes (got %d)", len(script)),
			ResourceType: "saga",
			ResourceID:   sagaName,
		})
		return nil
	}

	fileOpts := &syntax.FileOptions{}
	_, parseErr := fileOpts.Parse(sagaName+".star", script, 0)
	if parseErr != nil {
		ve := parseStarlarkError(parseErr, path)
		ve.ResourceType = "saga"
		ve.ResourceID = sagaName
		addError(result, ve)
		return nil
	}

	predeclared, callLog, deprecationWarnings, err := v.buildStarlarkPredeclared()
	if err != nil {
		addError(result, ValidationError{
			Severity:     SeverityError,
			Path:         path,
			Code:         "STARLARK_MODULE_BUILD_ERROR",
			Message:      fmt.Sprintf("failed to build typed service modules: %v", err),
			ResourceType: "saga",
			ResourceID:   sagaName,
		})
		return nil
	}

	thread := &starlark.Thread{
		Name:  sagaName,
		Print: func(_ *starlark.Thread, _ string) {},
	}
	sandbox.HardenThread(thread, sandbox.DefaultConfig())

	_, execErr := starlark.ExecFileOptions(fileOpts, thread, sagaName+".star", script, predeclared)
	if execErr != nil {
		ve := parseStarlarkError(execErr, path)
		ve.ResourceType = "saga"
		ve.ResourceID = sagaName

		// Enrich with structured error codes from handler validation failures
		if v.enrichHandlerValidationError(execErr, &ve) {
			addError(result, ve)
			return nil
		}

		addStarlarkUndefinedSuggestion(execErr, &ve)
		addError(result, ve)
		return nil
	}

	// Propagate deprecation warnings from handler evolution
	if deprecationWarnings != nil {
		for _, w := range *deprecationWarnings {
			addError(result, ValidationError{
				Severity:     SeverityWarning,
				Path:         path,
				Code:         w.Code,
				Message:      w.Message,
				Suggestion:   w.Suggestion,
				ResourceType: "saga",
				ResourceID:   sagaName,
			})
		}
	}

	return *callLog
}

// addStarlarkUndefinedSuggestion enriches a validation error with a "Did you mean?" suggestion
// when the Starlark error is about an undefined name.
func addStarlarkUndefinedSuggestion(execErr error, ve *ValidationError) {
	if !strings.Contains(execErr.Error(), "undefined") {
		return
	}
	undefinedName := extractUndefinedStarlarkName(execErr.Error())
	if undefinedName == "" {
		return
	}
	allNames := make([]string, 0, len(knownServiceBindings)+len(knownStarlarkBuiltins))
	allNames = append(allNames, knownServiceBindings...)
	allNames = append(allNames, knownStarlarkBuiltins...)
	if suggestion := findClosestMatch(undefinedName, allNames); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
}

// buildStarlarkPredeclared creates the predeclared dictionary for Starlark compilation.
// It uses typed service modules from the schema registry for handler parameter validation.
// When the schema registry has no handlers for a known service, a permissive stub module
// is added so scripts compile without typed validation rather than failing with
// "undefined" errors.
// Returns the predeclared dict, handler call log, and any error.
func (v *ManifestValidator) buildStarlarkPredeclared() (starlark.StringDict, *[]schema.HandlerCallInfo, *[]schema.ValidationWarning, error) {
	predeclared := make(starlark.StringDict)

	// Build typed service modules from schema registry with deprecation warning collection
	var callLog []schema.HandlerCallInfo
	var deprecationWarnings []schema.ValidationWarning
	modules, err := schema.BuildValidationModulesWithWarnings(v.schemaRegistry, &callLog, &deprecationWarnings)
	if err != nil {
		return nil, nil, nil, err
	}
	for name, module := range modules {
		predeclared[name] = module
	}

	// Only fall back to permissive stubs when no schema data is available at all.
	// When a partial schema is loaded, missing services should surface as errors
	// so coverage gaps in derived schemas are visible.
	if len(modules) == 0 {
		for _, svc := range knownServiceBindings {
			if _, exists := predeclared[svc]; !exists {
				predeclared[svc] = newPermissiveServiceStub(svc)
			}
		}
	}

	// Add common builtins
	predeclared["input_data"] = starlark.NewDict(0)
	predeclared["party_scope"] = starlark.NewDict(0)
	predeclared["Decimal"] = starlark.NewBuiltin("Decimal",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.String("0"), nil
		})

	return predeclared, &callLog, &deprecationWarnings, nil
}

// enrichHandlerValidationError checks if a Starlark execution error is a handler
// validation failure and enriches the ValidationError with structured codes and suggestions.
// Returns true if the error was a handler validation failure.
func (v *ManifestValidator) enrichHandlerValidationError(execErr error, ve *ValidationError) bool {
	errStr := execErr.Error()

	// Check for our structured validation failure codes from ValidationFailure errors
	for _, code := range []string{
		schema.ValidationCodeUnknownHandler,
		schema.ValidationCodeUnknownParam,
		schema.ValidationCodeMissingRequiredParam,
		schema.ValidationCodeWrongParamType,
	} {
		if strings.Contains(errStr, "["+code+"]") {
			ve.Code = code

			var vf *schema.ValidationFailure
			if errors.As(execErr, &vf) {
				ve.Message = vf.Message
				ve.Suggestion = vf.Suggestion
				ve.AvailableFields = vf.AvailableValues
			}

			return true
		}
	}

	// Check for starlarkstruct "has no .X attribute" errors, which indicate
	// unknown handler calls on a typed service module.
	if serviceName, methodName, ok := extractStructAttrError(errStr); ok {
		ve.Code = schema.ValidationCodeUnknownHandler
		ve.Message = fmt.Sprintf("unknown handler %q", serviceName+"."+methodName)

		knownHandlers := v.listServiceHandlers(serviceName)
		if len(knownHandlers) > 0 {
			ve.AvailableFields = knownHandlers
			if suggestion := findClosestMatch(methodName, knownHandlers); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", serviceName+"."+suggestion)
			}
		}

		return true
	}

	return false
}

// structAttrErrorPattern matches starlark struct attribute errors:
// "\"service_name\" struct has no .method_name attribute"
var structAttrErrorPattern = regexp.MustCompile(`"(\w+)" struct has no \.(\w+)`)

// extractStructAttrError extracts service and method names from a starlarkstruct
// "has no .X attribute" error message.
func extractStructAttrError(errStr string) (serviceName, methodName string, ok bool) {
	matches := structAttrErrorPattern.FindStringSubmatch(errStr)
	if len(matches) != 3 {
		return "", "", false
	}
	// Only match if the struct name is a known service binding
	for _, svc := range knownServiceBindings {
		if matches[1] == svc {
			return matches[1], matches[2], true
		}
	}
	return "", "", false
}

// listServiceHandlers returns the handler method names (last segment) for a given service.
func (v *ManifestValidator) listServiceHandlers(serviceName string) []string {
	var methods []string
	prefix := serviceName + "."
	for _, h := range v.schemaRegistry.ListHandlers() {
		if strings.HasPrefix(h, prefix) {
			method := strings.TrimPrefix(h, prefix)
			if !strings.Contains(method, ".") {
				methods = append(methods, method)
			}
		}
	}
	sort.Strings(methods)
	return methods
}

// parseStarlarkError converts a Starlark error into a structured ValidationError.
func parseStarlarkError(err error, basePath string) ValidationError {
	ve := ValidationError{
		Severity: SeverityError,
		Path:     basePath,
		Code:     CodeStarlarkCompilationError,
		Message:  err.Error(),
	}

	// Try to extract line/column from Starlark error format "file:line:col: message"
	errStr := err.Error()
	parts := strings.SplitN(errStr, ":", 4)
	if len(parts) >= 3 {
		var line, col int
		if _, scanErr := fmt.Sscanf(parts[1], "%d", &line); scanErr == nil {
			ve.Line = line
		}
		if _, scanErr := fmt.Sscanf(parts[2], "%d", &col); scanErr == nil {
			ve.Column = col
		}
	}

	// Detect syntax errors vs execution errors
	if strings.Contains(errStr, "syntax") || strings.Contains(errStr, "got ") {
		ve.Code = CodeStarlarkSyntaxError
	}

	return ve
}
