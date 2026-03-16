// Package validator provides manifest validation for the control plane.
// It performs structural schema validation, CEL type-checking for policy
// expressions, Starlark compilation for saga scripts, cross-reference
// validation, and immutability checks. All errors are structured with
// location paths and suggestions for AI-friendly feedback loops.
package validator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/google/cel-go/cel"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	"gopkg.in/yaml.v3"
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
	"invoke_handler",
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
	return starlark.String(""), nil
}

// CEL Balance type fields available in account type policy expressions.
var celBalanceFields = []string{
	"quantity",
	"instrument",
	"bucket_id",
	"as_of",
	"amount",
}

// Severity indicates the severity of a validation finding.
type Severity string

const (
	// SeverityError blocks manifest activation.
	SeverityError Severity = "error"
	// SeverityWarning allows activation but should be reviewed.
	SeverityWarning Severity = "warning"
)

// ValidationError represents a single validation finding with structured
// location information and optional suggestions for AI feedback.
type ValidationError struct {
	// Severity indicates whether this blocks activation.
	Severity Severity `json:"severity"`

	// Path is the location within the manifest (e.g., "instruments[0].code").
	Path string `json:"path"`

	// Code is a machine-readable error code (e.g., "CEL_TYPE_ERROR").
	Code string `json:"code"`

	// Message is a human-readable description of the issue.
	Message string `json:"message"`

	// Line is the source line number (for script errors).
	Line int `json:"line,omitempty"`

	// Column is the source column number (for script errors).
	Column int `json:"column,omitempty"`

	// Suggestion is a "Did you mean...?" hint for typos.
	Suggestion string `json:"suggestion,omitempty"`

	// AvailableFields lists valid field names when an unknown field is referenced.
	AvailableFields []string `json:"available_fields,omitempty"`
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	msg := fmt.Sprintf("[%s] %s: %s", e.Severity, e.Path, e.Message)
	if e.Suggestion != "" {
		msg += fmt.Sprintf(" (suggestion: %s)", e.Suggestion)
	}
	return msg
}

// ValidationResult contains the outcome of manifest validation.
type ValidationResult struct {
	// Valid is true when there are no errors (warnings are allowed).
	Valid bool `json:"valid"`

	// Errors contains all error-severity findings.
	Errors []ValidationError `json:"errors"`

	// Warnings contains all warning-severity findings.
	Warnings []ValidationError `json:"warnings"`

	// Graph contains the relationship graph extracted during validation.
	// Only populated when validation succeeds (no errors).
	Graph *RelationshipGraph `json:"graph,omitempty"`
}

// ManifestValidator validates Meridian manifests.
type ManifestValidator struct {
	protoValidator       protovalidate.Validator
	celEnv               *cel.Env
	bucketCelEnv         *cel.Env
	partyTypeCelEnv      *cel.Env
	mappingCelEnv        *cel.Env
	eventFilterEnv       *cel.Env
	channelRegistry      map[string]bool
	schemaRegistry       *schema.Registry
	apiPathRegistry      map[string]bool            // valid API endpoint paths from OpenAPI spec
	asyncAPISchemas      map[string]map[string]bool // topic -> set of payload field names
	apiPathsExplicit     bool                       // true when set via option
	asyncSchemasExplicit bool                       // true when set via option
}

// Option configures a ManifestValidator.
type Option func(*ManifestValidator)

// WithOpenAPIPaths sets the valid API paths for API trigger validation.
// Pass nil to disable API trigger validation (skip filesystem loading).
func WithOpenAPIPaths(paths map[string]bool) Option {
	return func(v *ManifestValidator) {
		v.apiPathRegistry = paths
		v.apiPathsExplicit = true
	}
}

// WithSchemaRegistry injects a pre-built schema registry for Starlark handler validation.
// When not set, the validator defaults to an empty registry: Starlark scripts still compile
// but handler calls (e.g. position_keeping.initiate_log) are treated as undefined names
// rather than being validated against typed service modules. Use WithDerivedSchema for
// production to enable typed parameter validation.
func WithSchemaRegistry(reg *schema.Registry) Option {
	return func(v *ManifestValidator) {
		if reg == nil {
			return // keep default empty registry
		}
		v.schemaRegistry = reg
	}
}

// WithDerivedSchema populates the validator's schema registry from a proto-derived Schema.
// This is the preferred option for production use where handlers are registered with
// proto type annotations.
func WithDerivedSchema(derivedSchema *schema.Schema) Option {
	return func(v *ManifestValidator) {
		v.schemaRegistry = schema.NewRegistryFromSchema(derivedSchema)
	}
}

// WithAsyncAPISchemas sets the event payload schemas for CEL field validation.
// The map keys are topic names; values are sets of valid field names.
// Pass nil to disable AsyncAPI field validation (skip filesystem loading).
func WithAsyncAPISchemas(schemas map[string]map[string]bool) Option {
	return func(v *ManifestValidator) {
		v.asyncAPISchemas = schemas
		v.asyncSchemasExplicit = true
	}
}

// New creates a new ManifestValidator.
func New(opts ...Option) (*ManifestValidator, error) {
	pv, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create proto validator: %w", err)
	}

	// CEL environment for account type validation policies.
	// These expressions operate on balance state.
	// Use DynType for numeric fields to allow both int and double literals
	// (e.g., "amount > 0" and "amount > 0.0" both work).
	celEnv, err := cel.NewEnv(
		cel.Variable("quantity", cel.DynType),
		cel.Variable("instrument", cel.StringType),
		cel.Variable("bucket_id", cel.StringType),
		cel.Variable("as_of", cel.TimestampType),
		cel.Variable("amount", cel.DynType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	// CEL environment for bucketing expressions.
	bucketCelEnv, err := cel.NewEnv(
		cel.Variable("instrument_code", cel.StringType),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket CEL environment: %w", err)
	}

	// CEL environment for party type validation/eligibility expressions.
	// These expressions operate on party attributes.
	partyTypeCelEnv, err := cel.NewEnv(
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("party_type", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create party type CEL environment: %w", err)
	}

	// CEL environment for mapping validation/transformation expressions.
	// These expressions operate on arbitrary payload fields.
	mappingCelEnv, err := cel.NewEnv(
		cel.Variable("payload", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("value", cel.DynType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapping CEL environment: %w", err)
	}

	// CEL environment for event trigger filter expressions.
	// Filters operate on a dynamic event map representing the domain event payload.
	eventFilterEnv, err := cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create event filter CEL environment: %w", err)
	}

	// Build channel registry from the canonical topic list.
	allTopics := topics.All()
	channelRegistry := make(map[string]bool, len(allTopics))
	for _, topic := range allTopics {
		channelRegistry[topic] = true
	}

	mv := &ManifestValidator{
		protoValidator:  pv,
		celEnv:          celEnv,
		bucketCelEnv:    bucketCelEnv,
		partyTypeCelEnv: partyTypeCelEnv,
		mappingCelEnv:   mappingCelEnv,
		eventFilterEnv:  eventFilterEnv,
		channelRegistry: channelRegistry,
		schemaRegistry:  schema.NewRegistry(),
	}

	for _, opt := range opts {
		opt(mv)
	}

	// Best-effort auto-loading from repo checkout. When the spec files are not
	// reachable (e.g. running outside a repo checkout), the corresponding checks
	// degrade gracefully: format and duplicate checks still run, only the
	// endpoint-existence and field-reference checks are skipped. For deterministic
	// behavior in tests, use WithOpenAPIPaths / WithAsyncAPISchemas.
	if !mv.apiPathsExplicit {
		mv.apiPathRegistry = tryLoadOpenAPIPaths()
	}
	if !mv.asyncSchemasExplicit {
		mv.asyncAPISchemas = tryLoadAsyncAPISchemas()
	}

	return mv, nil
}

// ValidateOption configures validation behavior.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	forceDestructiveChanges bool
	skipImmutabilityChecks  bool
}

// WithForceDestructiveChanges converts destructive change errors into warnings,
// allowing removal of in-use resources when explicitly requested.
func WithForceDestructiveChanges() ValidateOption {
	return func(c *validateConfig) {
		c.forceDestructiveChanges = true
	}
}

// WithSkipImmutabilityChecks skips immutability enforcement and destructive
// change detection. Use when validating a manifest intended for a new tenant
// (create mode) that has no existing state to compare against.
func WithSkipImmutabilityChecks() ValidateOption {
	return func(c *validateConfig) {
		c.skipImmutabilityChecks = true
	}
}

// Validate performs full validation of a manifest.
// If previousManifest is non-nil, immutability and destructive change checks are also performed.
func (v *ManifestValidator) Validate(
	manifest *controlplanev1.Manifest,
	previousManifest *controlplanev1.Manifest,
	opts ...ValidateOption,
) *ValidationResult {
	cfg := &validateConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	result := &ValidationResult{Valid: true}

	// 1. Structural validation (protobuf constraints)
	v.validateStructural(manifest, result)

	// 2. Duplicate code detection
	v.validateDuplicates(manifest, result)

	// 3. CEL expression validation
	v.validateCELExpressions(manifest, result)

	// 4. Starlark script compilation
	callLogs := v.validateStarlarkScripts(manifest, result)

	// 5. Cross-reference validation
	v.validateCrossReferences(manifest, result)

	// 6. Payment rails validation
	v.validatePaymentRails(manifest, result)

	// 7. Party types validation
	v.validatePartyTypes(manifest, result)

	// 8. Mappings validation
	v.validateMappings(manifest, result)

	// 9. Event trigger channel and filter validation
	v.validateEventTriggers(manifest, result)

	// 10. Webhook trigger validation against provider connections
	v.validateWebhookTriggers(manifest, result)

	// 11. Scheduled trigger name uniqueness
	v.validateScheduledTriggers(manifest, result)

	// 12. API trigger validation against OpenAPI spec
	v.validateAPITriggers(manifest, result)

	// 13. Operational gateway orphan detection
	v.validateOperationalGatewayOrphans(manifest, result)

	// 14. Immutability checks (skipped when validating for a new tenant)
	if previousManifest != nil && !cfg.skipImmutabilityChecks {
		v.validateImmutability(manifest, previousManifest, result)
	}

	// 15. Destructive change detection (skipped when validating for a new tenant)
	// Use the previous manifest's call logs for graph construction so that
	// dependencies that existed in the previous version are correctly detected,
	// even if a saga was modified or removed in the current manifest.
	if previousManifest != nil && !cfg.skipImmutabilityChecks {
		previousCallLogs := v.validateStarlarkScripts(previousManifest, &ValidationResult{})
		v.validateDestructiveChanges(manifest, previousManifest, previousCallLogs, cfg, result)
	}

	// Set valid flag based on error count
	result.Valid = len(result.Errors) == 0

	// Extract relationship graph when validation succeeds
	if result.Valid {
		result.Graph = ExtractRelationshipGraph(manifest, callLogs)
	}

	return result
}

// validateStructural uses protovalidate to check protobuf constraints.
func (v *ManifestValidator) validateStructural(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	err := v.protoValidator.Validate(manifest)
	if err == nil {
		return
	}

	var valErr *protovalidate.ValidationError
	if errors.As(err, &valErr) {
		for _, violation := range valErr.Violations {
			path := buildFieldPath(violation)
			message := ""
			if violation.Proto != nil {
				message = violation.Proto.GetMessage()
			}
			if message == "" {
				message = violation.String()
			}
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     path,
				Code:     "PROTO_VALIDATION",
				Message:  message,
			})
		}
	} else {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     "",
			Code:     "PROTO_VALIDATION",
			Message:  err.Error(),
		})
	}
}

// validateDuplicates checks for duplicate codes within the manifest.
func (v *ManifestValidator) validateDuplicates(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Check duplicate instrument codes
	instrumentCodes := make(map[string]int)
	for i, inst := range manifest.GetInstruments() {
		if prev, exists := instrumentCodes[inst.GetCode()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("instruments[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate instrument code %q (first defined at instruments[%d])", inst.GetCode(), prev),
			})
		} else {
			instrumentCodes[inst.GetCode()] = i
		}
	}

	// Check duplicate account type codes
	accountTypeCodes := make(map[string]int)
	for i, acct := range manifest.GetAccountTypes() {
		if prev, exists := accountTypeCodes[acct.GetCode()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("account_types[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate account type code %q (first defined at account_types[%d])", acct.GetCode(), prev),
			})
		} else {
			accountTypeCodes[acct.GetCode()] = i
		}
	}

	// Check duplicate saga names and event trigger filter requirements
	sagaNames := make(map[string]int)
	for i, saga := range manifest.GetSagas() {
		if prev, exists := sagaNames[saga.GetName()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("sagas[%d].name", i),
				Code:     "DUPLICATE_NAME",
				Message:  fmt.Sprintf("duplicate saga name %q (first defined at sagas[%d])", saga.GetName(), prev),
			})
		} else {
			sagaNames[saga.GetName()] = i
		}
		// Warn when an event-triggered saga has no filter; all events will trigger the saga
		if strings.HasPrefix(saga.GetTrigger(), "event:") && saga.Filter == nil {
			addError(result, ValidationError{
				Severity:   SeverityWarning,
				Path:       fmt.Sprintf("sagas[%d].filter", i),
				Code:       "MISSING_EVENT_FILTER",
				Message:    fmt.Sprintf("saga %q subscribes to event trigger %q without a filter; the saga will execute for every matching event", saga.GetName(), saga.GetTrigger()),
				Suggestion: `Add a CEL filter expression, e.g. filter: 'event.amount > 0'`,
			})
		}
	}

	// Check duplicate mapping (name, version) pairs
	type mappingKey struct {
		name    string
		version int32
	}
	mappingKeys := make(map[mappingKey]int)
	for i, mp := range manifest.GetMappings() {
		key := mappingKey{name: mp.GetName(), version: mp.GetVersion()}
		if prev, exists := mappingKeys[key]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("mappings[%d]", i),
				Code:     "DUPLICATE_MAPPING",
				Message:  fmt.Sprintf("duplicate mapping name=%q version=%d (first defined at mappings[%d])", mp.GetName(), mp.GetVersion(), prev),
			})
		} else {
			mappingKeys[key] = i
		}
	}

	// Check duplicate operational_gateway connection_ids and instruction_types
	v.validateOperationalGatewayDuplicates(manifest, result)

	// Check duplicate market data and organization codes
	v.validateMarketDataAndOrgDuplicates(manifest, result)
}

// validateMarketDataAndOrgDuplicates checks for duplicate codes in market data and organization sections.
func (v *ManifestValidator) validateMarketDataAndOrgDuplicates(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Check duplicate market data source codes
	mdSourceCodes := make(map[string]int)
	for i, src := range manifest.GetMarketData().GetSources() {
		if prev, exists := mdSourceCodes[src.GetCode()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("market_data.sources[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate market data source code %q (first defined at market_data.sources[%d])", src.GetCode(), prev),
			})
		} else {
			mdSourceCodes[src.GetCode()] = i
		}
	}

	// Check duplicate market data set codes
	mdSetCodes := make(map[string]int)
	for i, ds := range manifest.GetMarketData().GetDatasets() {
		if prev, exists := mdSetCodes[ds.GetCode()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("market_data.datasets[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate market data set code %q (first defined at market_data.datasets[%d])", ds.GetCode(), prev),
			})
		} else {
			mdSetCodes[ds.GetCode()] = i
		}
	}

	// Check duplicate organization codes
	orgCodes := make(map[string]int)
	for i, org := range manifest.GetOrganizations() {
		if prev, exists := orgCodes[org.GetCode()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("organizations[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate organization code %q (first defined at organizations[%d])", org.GetCode(), prev),
			})
		} else {
			orgCodes[org.GetCode()] = i
		}
	}

	// Check duplicate internal account codes
	iaCodes := make(map[string]int)
	for i, ia := range manifest.GetInternalAccounts() {
		if prev, exists := iaCodes[ia.GetCode()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("internal_accounts[%d].code", i),
				Code:     "DUPLICATE_CODE",
				Message:  fmt.Sprintf("duplicate internal account code %q (first defined at internal_accounts[%d])", ia.GetCode(), prev),
			})
		} else {
			iaCodes[ia.GetCode()] = i
		}
	}
}

// validateOperationalGatewayDuplicates checks for duplicate connection_ids and instruction_types.
func (v *ManifestValidator) validateOperationalGatewayDuplicates(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	gw := manifest.GetOperationalGateway()
	if gw == nil {
		return
	}

	connectionIDs := make(map[string]int)
	for i, conn := range gw.GetProviderConnections() {
		if prev, exists := connectionIDs[conn.GetConnectionId()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("operational_gateway.provider_connections[%d].connection_id", i),
				Code:     "DUPLICATE_CONNECTION_ID",
				Message:  fmt.Sprintf("duplicate connection_id %q (first defined at provider_connections[%d])", conn.GetConnectionId(), prev),
			})
		} else {
			connectionIDs[conn.GetConnectionId()] = i
		}
	}

	instructionTypes := make(map[string]int)
	for i, route := range gw.GetInstructionRoutes() {
		if prev, exists := instructionTypes[route.GetInstructionType()]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("operational_gateway.instruction_routes[%d].instruction_type", i),
				Code:     "DUPLICATE_INSTRUCTION_TYPE",
				Message:  fmt.Sprintf("duplicate instruction_type %q (first defined at instruction_routes[%d])", route.GetInstructionType(), prev),
			})
		} else {
			instructionTypes[route.GetInstructionType()] = i
		}
	}
}

// validateCELExpressions type-checks CEL expressions in account type policies.
func (v *ManifestValidator) validateCELExpressions(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, acctType := range manifest.GetAccountTypes() {
		policies := acctType.GetPolicies()
		if policies == nil {
			continue
		}

		// Validate validation expression
		if expr := policies.GetValidation(); expr != "" {
			v.validateCELExpression(
				expr,
				fmt.Sprintf("account_types[%d].policies.validation", i),
				v.celEnv,
				celBalanceFields,
				result,
			)
		}

		// Validate bucketing expression
		if expr := policies.GetBucketing(); expr != "" {
			v.validateCELExpression(
				expr,
				fmt.Sprintf("account_types[%d].policies.bucketing", i),
				v.bucketCelEnv,
				[]string{"instrument_code", "attributes"},
				result,
			)
		}
	}
}

// validateCELExpression compiles a single CEL expression and produces structured errors.
func (v *ManifestValidator) validateCELExpression(
	expression string,
	path string,
	env *cel.Env,
	availableFields []string,
	result *ValidationResult,
) {
	// Check length constraint
	if len(expression) > 4096 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "CEL_EXPRESSION_TOO_LONG",
			Message:  fmt.Sprintf("CEL expression exceeds maximum length of 4096 bytes (got %d)", len(expression)),
		})
		return
	}

	_, issues := env.Compile(expression)
	if issues == nil || issues.Err() == nil {
		return
	}

	errMsg := issues.Err().Error()

	// Check for undeclared reference errors to provide field suggestions
	if strings.Contains(errMsg, "undeclared reference") {
		// Extract the undeclared field name from the error
		undeclaredField := extractUndeclaredReference(errMsg)
		suggestion := ""
		if undeclaredField != "" {
			suggestion = findClosestMatch(undeclaredField, availableFields)
			if suggestion != "" {
				suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
		}

		addError(result, ValidationError{
			Severity:        SeverityError,
			Path:            path,
			Code:            "CEL_UNDECLARED_REFERENCE",
			Message:         errMsg,
			Suggestion:      suggestion,
			AvailableFields: availableFields,
		})
		return
	}

	addError(result, ValidationError{
		Severity: SeverityError,
		Path:     path,
		Code:     "CEL_COMPILATION_ERROR",
		Message:  errMsg,
	})
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
	if len(script) > 65536 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "STARLARK_SCRIPT_TOO_LARGE",
			Message:  fmt.Sprintf("Starlark script exceeds maximum size of 65536 bytes (got %d)", len(script)),
		})
		return nil
	}

	fileOpts := &syntax.FileOptions{}
	_, parseErr := fileOpts.Parse(saga.GetName()+".star", script, 0)
	if parseErr != nil {
		addError(result, parseStarlarkError(parseErr, path))
		return nil
	}

	predeclared, callLog, deprecationWarnings, err := v.buildStarlarkPredeclared()
	if err != nil {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "STARLARK_MODULE_BUILD_ERROR",
			Message:  fmt.Sprintf("failed to build typed service modules: %v", err),
		})
		return nil
	}

	thread := &starlark.Thread{
		Name:  saga.GetName(),
		Print: func(_ *starlark.Thread, _ string) {},
	}

	_, execErr := starlark.ExecFileOptions(fileOpts, thread, saga.GetName()+".star", script, predeclared)
	if execErr != nil {
		ve := parseStarlarkError(execErr, path)

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
				Severity:   SeverityWarning,
				Path:       path,
				Code:       w.Code,
				Message:    w.Message,
				Suggestion: w.Suggestion,
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

// validateCrossReferences checks that all references between manifest sections are valid.
func (v *ManifestValidator) validateCrossReferences(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	instrumentCodes := make(map[string]bool)
	for _, inst := range manifest.GetInstruments() {
		instrumentCodes[inst.GetCode()] = true
	}
	codeList := mapKeys(instrumentCodes)

	for i, acctType := range manifest.GetAccountTypes() {
		for j, instrCode := range acctType.GetAllowedInstruments() {
			checkInstrumentRef(
				instrCode, instrumentCodes, codeList,
				fmt.Sprintf("account_types[%d].allowed_instruments[%d]", i, j),
				result,
			)
		}
	}

	for i, rule := range manifest.GetValuationRules() {
		checkInstrumentRef(
			rule.GetFromInstrument(), instrumentCodes, codeList,
			fmt.Sprintf("valuation_rules[%d].from_instrument", i),
			result,
		)
		checkInstrumentRef(
			rule.GetToInstrument(), instrumentCodes, codeList,
			fmt.Sprintf("valuation_rules[%d].to_instrument", i),
			result,
		)
	}

	// Validate operational_gateway cross-references
	v.validateOperationalGatewayCrossRefs(manifest, result)

	// Validate market data set source_code references valid market data source
	mdSourceCodes := make(map[string]bool)
	for _, src := range manifest.GetMarketData().GetSources() {
		mdSourceCodes[src.GetCode()] = true
	}
	mdSourceCodeList := mapKeys(mdSourceCodes)
	for i, ds := range manifest.GetMarketData().GetDatasets() {
		sourceCode := ds.GetSourceCode()
		if sourceCode != "" && !mdSourceCodes[sourceCode] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            fmt.Sprintf("market_data.datasets[%d].source_code", i),
				Code:            "INVALID_REFERENCE",
				Message:         fmt.Sprintf("market data set %q references unknown source code %q", ds.GetCode(), sourceCode),
				AvailableFields: mdSourceCodeList,
			}
			if suggestion := findClosestMatch(sourceCode, mdSourceCodeList); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}

	// Validate organization party_type references
	v.validateOrganizationCrossRefs(manifest, result)

	// Validate internal account cross-references
	v.validateInternalAccountCrossRefs(manifest, result)
}

// validateOrganizationCrossRefs validates that organizations reference valid party types.
func (v *ManifestValidator) validateOrganizationCrossRefs(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Built-in party types from the PartyType enum plus manifest-defined party types
	validPartyTypes := map[string]bool{
		"PERSON":       true,
		"ORGANIZATION": true,
	}
	for _, pt := range manifest.GetPartyTypes() {
		if ptCode := pt.GetPartyType(); ptCode != "" {
			validPartyTypes[ptCode] = true
		}
	}
	partyTypeList := mapKeys(validPartyTypes)
	for i, org := range manifest.GetOrganizations() {
		partyType := org.GetPartyType()
		if partyType != "" && !validPartyTypes[partyType] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            fmt.Sprintf("organizations[%d].party_type", i),
				Code:            "INVALID_REFERENCE",
				Message:         fmt.Sprintf("organization %q references unknown party type %q", org.GetCode(), partyType),
				AvailableFields: partyTypeList,
			}
			if suggestion := findClosestMatch(partyType, partyTypeList); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// validateInternalAccountCrossRefs validates that internal accounts reference valid
// account types, instruments, and organizations.
func (v *ManifestValidator) validateInternalAccountCrossRefs(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	accountTypeCodes := make(map[string]bool)
	for _, at := range manifest.GetAccountTypes() {
		accountTypeCodes[at.GetCode()] = true
	}

	instrumentCodes := make(map[string]bool)
	for _, inst := range manifest.GetInstruments() {
		instrumentCodes[inst.GetCode()] = true
	}

	orgCodes := make(map[string]bool)
	for _, org := range manifest.GetOrganizations() {
		orgCodes[org.GetCode()] = true
	}

	for i, ia := range manifest.GetInternalAccounts() {
		code := ia.GetCode()
		checkRef(ia.GetAccountType(), accountTypeCodes,
			fmt.Sprintf("internal_accounts[%d].account_type", i),
			fmt.Sprintf("internal account %q references unknown account type", code),
			result)
		checkRef(ia.GetInstrument(), instrumentCodes,
			fmt.Sprintf("internal_accounts[%d].instrument", i),
			fmt.Sprintf("internal account %q references unknown instrument", code),
			result)
		checkRef(ia.GetOwnerOrganization(), orgCodes,
			fmt.Sprintf("internal_accounts[%d].owner_organization", i),
			fmt.Sprintf("internal account %q references unknown organization", code),
			result)
	}
}

// checkRef validates that value exists in validCodes. If value is empty, no check is performed.
func checkRef(value string, validCodes map[string]bool, path, msgPrefix string, result *ValidationResult) {
	if value == "" || validCodes[value] {
		return
	}
	codeList := mapKeys(validCodes)
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "INVALID_REFERENCE",
		Message:         fmt.Sprintf("%s %q", msgPrefix, value),
		AvailableFields: codeList,
	}
	if suggestion := findClosestMatch(value, codeList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// validateOperationalGatewayCrossRefs validates referential integrity for the operational_gateway section.
// It checks that:
// - instruction_route.connection_id references an existing provider_connection
// - instruction_route.fallback_connection_id (if set) references an existing provider_connection
// - instruction_route.outbound_mapping_id (if set) references an existing mapping
// - instruction_route.inbound_mapping_id (if set) references an existing mapping
// - inbound_route.handler_saga references an existing saga
// - inbound_route.mapping_id (if set) references an existing mapping
func (v *ManifestValidator) validateOperationalGatewayCrossRefs(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	gw := manifest.GetOperationalGateway()
	if gw == nil {
		return
	}

	// Build lookup sets for valid connection_ids, mapping names, and saga names
	connectionIDs := make(map[string]bool)
	for _, conn := range gw.GetProviderConnections() {
		if id := conn.GetConnectionId(); id != "" {
			connectionIDs[id] = true
		}
	}

	mappingNames := make(map[string]bool)
	for _, mp := range manifest.GetMappings() {
		if name := mp.GetName(); name != "" {
			mappingNames[name] = true
		}
	}

	sagaNames := make(map[string]bool)
	for _, saga := range manifest.GetSagas() {
		if name := saga.GetName(); name != "" {
			sagaNames[name] = true
		}
	}

	for i, route := range gw.GetInstructionRoutes() {
		v.validateInstructionRouteRefs(route, i, connectionIDs, mappingNames, result)
	}

	for i, route := range gw.GetInboundRoutes() {
		v.validateInboundRouteRefs(route, i, sagaNames, mappingNames, result)
	}
}

// validateInstructionRouteRefs checks connection and mapping references for a single InstructionRouteConfig.
func (v *ManifestValidator) validateInstructionRouteRefs(
	route *controlplanev1.InstructionRouteConfig,
	idx int,
	connectionIDs map[string]bool,
	mappingNames map[string]bool,
	result *ValidationResult,
) {
	basePath := fmt.Sprintf("operational_gateway.instruction_routes[%d]", idx)
	connectionIDList := mapKeys(connectionIDs)
	mappingNameList := mapKeys(mappingNames)

	checkConnectionRef(route.GetConnectionId(), basePath+".connection_id", connectionIDs, connectionIDList, result)
	if fallbackID := route.GetFallbackConnectionId(); fallbackID != "" {
		checkConnectionRef(fallbackID, basePath+".fallback_connection_id", connectionIDs, connectionIDList, result)
	}
	if id := route.GetOutboundMappingId(); id != "" {
		checkMappingRef(id, basePath+".outbound_mapping_id", mappingNames, mappingNameList, result)
	}
	if id := route.GetInboundMappingId(); id != "" {
		checkMappingRef(id, basePath+".inbound_mapping_id", mappingNames, mappingNameList, result)
	}
}

// validateInboundRouteRefs checks saga and mapping references for a single InboundRouteConfig.
func (v *ManifestValidator) validateInboundRouteRefs(
	route *controlplanev1.InboundRouteConfig,
	idx int,
	sagaNames map[string]bool,
	mappingNames map[string]bool,
	result *ValidationResult,
) {
	basePath := fmt.Sprintf("operational_gateway.inbound_routes[%d]", idx)
	sagaNameList := mapKeys(sagaNames)
	mappingNameList := mapKeys(mappingNames)

	if sagaName := route.GetHandlerSaga(); sagaName != "" && !sagaNames[sagaName] {
		ve := ValidationError{
			Severity:        SeverityError,
			Path:            basePath + ".handler_saga",
			Code:            "UNDEFINED_SAGA_REFERENCE",
			Message:         fmt.Sprintf("handler_saga %q is not defined in sagas[]", sagaName),
			AvailableFields: sagaNameList,
		}
		if suggestion := findClosestMatch(sagaName, sagaNameList); suggestion != "" {
			ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
		addError(result, ve)
	}
	if id := route.GetMappingId(); id != "" {
		checkMappingRef(id, basePath+".mapping_id", mappingNames, mappingNameList, result)
	}
}

// checkConnectionRef validates that a connection ID string references an existing provider connection.
func checkConnectionRef(
	connID string,
	path string,
	validIDs map[string]bool,
	idList []string,
	result *ValidationResult,
) {
	if connID == "" || validIDs[connID] {
		return
	}
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "UNDEFINED_CONNECTION_REFERENCE",
		Message:         fmt.Sprintf("connection_id %q is not defined in operational_gateway.provider_connections", connID),
		AvailableFields: idList,
	}
	if suggestion := findClosestMatch(connID, idList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// checkMappingRef validates that a mapping name string references an existing mapping.
func checkMappingRef(
	mappingID string,
	path string,
	validNames map[string]bool,
	nameList []string,
	result *ValidationResult,
) {
	if mappingID == "" || validNames[mappingID] {
		return
	}
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "UNDEFINED_MAPPING_REFERENCE",
		Message:         fmt.Sprintf("mapping %q is not defined in mappings[]", mappingID),
		AvailableFields: nameList,
	}
	if suggestion := findClosestMatch(mappingID, nameList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// validateMappings validates all MappingDefinition entries in the manifest.
// It enforces no duplicate (name, version) pairs, valid CEL expressions,
// and valid status.
func (v *ManifestValidator) validateMappings(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, mp := range manifest.GetMappings() {
		v.validateSingleMapping(mp, fmt.Sprintf("mappings[%d]", i), result)
	}
}

// validateSingleMapping validates one MappingDefinition entry.
func (v *ManifestValidator) validateSingleMapping(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	v.validateMappingCELExpressions(mp, basePath, result)
	v.validateMappingFields(mp, basePath, result)
	v.validateMappingComputedFields(mp, basePath, result)
	v.validateMappingBatch(mp, basePath, result)
	v.validateMappingStatus(mp, basePath, result)
	v.validateMappingIdempotency(mp, basePath, result)
}

// validateMappingCELExpressions validates inbound/outbound CEL validation expressions.
func (v *ManifestValidator) validateMappingCELExpressions(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	if expr := mp.GetInboundValidationCel(); expr != "" {
		v.validateMappingCELExpression(expr, basePath+".inbound_validation_cel", result)
	}
	if expr := mp.GetOutboundValidationCel(); expr != "" {
		v.validateMappingCELExpression(expr, basePath+".outbound_validation_cel", result)
	}
}

// validateMappingFields validates CEL transforms on field correspondences.
func (v *ManifestValidator) validateMappingFields(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	for j, field := range mp.GetFields() {
		ft := field.GetTransform()
		if ft == nil {
			continue
		}
		celT := ft.GetCel()
		if celT == nil {
			continue
		}
		fieldPath := fmt.Sprintf("%s.fields[%d].transform.cel", basePath, j)
		if expr := celT.GetInboundCel(); expr != "" {
			v.validateMappingCELExpression(expr, fieldPath+".inbound_cel", result)
		}
		if expr := celT.GetOutboundCel(); expr != "" {
			v.validateMappingCELExpression(expr, fieldPath+".outbound_cel", result)
		}
	}
}

// validateMappingComputedFields validates CEL expressions on computed fields.
func (v *ManifestValidator) validateMappingComputedFields(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	for j, cf := range mp.GetInboundComputedFields() {
		if expr := cf.GetCelExpression(); expr != "" {
			v.validateMappingCELExpression(expr, fmt.Sprintf("%s.inbound_computed_fields[%d].cel_expression", basePath, j), result)
		}
	}
	for j, cf := range mp.GetOutboundComputedFields() {
		if expr := cf.GetCelExpression(); expr != "" {
			v.validateMappingCELExpression(expr, fmt.Sprintf("%s.outbound_computed_fields[%d].cel_expression", basePath, j), result)
		}
	}
}

// validateMappingBatch checks batch consistency (is_batch requires batch_target_path).
func (v *ManifestValidator) validateMappingBatch(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	if mp.GetIsBatch() && mp.GetBatchTargetPath() == "" {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".batch_target_path",
			Code:     "MAPPING_BATCH_TARGET_REQUIRED",
			Message:  "batch_target_path must be set when is_batch is true",
		})
	}
}

// mappingCELAvailableFields lists the variables available in mapping CEL expressions.
var mappingCELAvailableFields = []string{"payload", "value"}

// validateMappingCELExpression compiles a single CEL expression for mapping contexts.
// Uses a simplified CEL environment with payload and value variables.
func (v *ManifestValidator) validateMappingCELExpression(
	expression string,
	path string,
	result *ValidationResult,
) {
	if len(expression) > 2048 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "CEL_EXPRESSION_TOO_LONG",
			Message:  fmt.Sprintf("CEL expression exceeds maximum length of 2048 bytes (got %d)", len(expression)),
		})
		return
	}

	_, issues := v.mappingCelEnv.Compile(expression)
	if issues == nil || issues.Err() == nil {
		return
	}

	errMsg := issues.Err().Error()

	// Check for undeclared reference errors to provide field suggestions.
	if strings.Contains(errMsg, "undeclared reference") {
		undeclaredField := extractUndeclaredReference(errMsg)
		suggestion := ""
		if undeclaredField != "" {
			if match := findClosestMatch(undeclaredField, mappingCELAvailableFields); match != "" {
				suggestion = fmt.Sprintf("Did you mean %q?", match)
			}
		}
		addError(result, ValidationError{
			Severity:        SeverityError,
			Path:            path,
			Code:            "CEL_UNDECLARED_REFERENCE",
			Message:         errMsg,
			Suggestion:      suggestion,
			AvailableFields: mappingCELAvailableFields,
		})
		return
	}

	addError(result, ValidationError{
		Severity: SeverityError,
		Path:     path,
		Code:     "CEL_COMPILATION_ERROR",
		Message:  errMsg,
	})
}

// validateMappingStatus checks that status is a defined, non-unspecified value.
func (v *ManifestValidator) validateMappingStatus(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	status := mp.GetStatus()
	if status == mappingv1.MappingStatus_MAPPING_STATUS_UNSPECIFIED {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".status",
			Code:     "INVALID_MAPPING_STATUS",
			Message:  "mapping status must be specified (DRAFT, ACTIVE, or DEPRECATED)",
		})
	}
}

// validateMappingIdempotency enforces cross-field constraints on IdempotencyConfig.
// When use_content_hash is false, source_selector must be non-empty.
// When use_content_hash is true, content_hash_fields must have at least one entry.
func (v *ManifestValidator) validateMappingIdempotency(
	mp *mappingv1.MappingDefinition,
	basePath string,
	result *ValidationResult,
) {
	cfg := mp.GetIdempotency()
	if cfg == nil {
		return
	}
	idemPath := basePath + ".idempotency"
	if !cfg.GetUseContentHash() && cfg.GetSourceSelector() == "" {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     idemPath + ".source_selector",
			Code:     "IDEMPOTENCY_SOURCE_REQUIRED",
			Message:  "source_selector is required when use_content_hash is false",
		})
	}
	if cfg.GetUseContentHash() && len(cfg.GetContentHashFields()) == 0 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     idemPath + ".content_hash_fields",
			Code:     "IDEMPOTENCY_HASH_FIELDS_REQUIRED",
			Message:  "content_hash_fields must have at least one entry when use_content_hash is true",
		})
	}
}

// eventFilterAvailableFields lists the variables available in event trigger filter CEL expressions.
var eventFilterAvailableFields = []string{"event"}

// validateEventTriggers validates all event-triggered sagas in the manifest.
// It checks that the channel referenced after "event:" exists in the topic registry,
// and that any filter expression is valid CEL.
func (v *ManifestValidator) validateEventTriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, saga := range manifest.GetSagas() {
		if !strings.HasPrefix(saga.GetTrigger(), "event:") {
			continue
		}
		path := fmt.Sprintf("sagas[%d]", i)
		v.validateEventTrigger(saga, path, result)
	}
}

// validateEventTrigger validates a single event-triggered saga definition.
// It checks channel existence and, if a filter is provided, validates it as CEL.
func (v *ManifestValidator) validateEventTrigger(
	saga *controlplanev1.SagaDefinition,
	path string,
	result *ValidationResult,
) {
	channel := strings.TrimPrefix(saga.GetTrigger(), "event:")

	if !v.channelRegistry[channel] {
		availableChans := v.availableChannels()
		ve := ValidationError{
			Severity:        SeverityError,
			Path:            path + ".trigger",
			Code:            "INVALID_EVENT_CHANNEL",
			Message:         fmt.Sprintf("unknown event channel %q; must be a registered topic", channel),
			AvailableFields: availableChans,
		}
		if suggestion := findClosestMatch(channel, availableChans); suggestion != "" {
			ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
		}
		addError(result, ve)
	}

	if saga.Filter == nil || saga.GetFilter() == "" {
		// Missing filter warning is already emitted in validateDuplicates; skip here.
		return
	}

	v.validateEventFilterCEL(saga.GetFilter(), path+".filter", result)

	// Cross-check CEL field references against AsyncAPI schema
	v.validateEventFilterCELFields(saga.GetFilter(), channel, path+".filter", result)
}

// validateEventFilterCEL compiles a CEL expression in the event filter environment.
func (v *ManifestValidator) validateEventFilterCEL(
	expression string,
	path string,
	result *ValidationResult,
) {
	if len(expression) > 4096 {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "CEL_EXPRESSION_TOO_LONG",
			Message:  fmt.Sprintf("CEL expression exceeds maximum length of 4096 bytes (got %d)", len(expression)),
		})
		return
	}

	_, issues := v.eventFilterEnv.Compile(expression)
	if issues == nil || issues.Err() == nil {
		return
	}

	errMsg := issues.Err().Error()

	if strings.Contains(errMsg, "undeclared reference") {
		undeclaredField := extractUndeclaredReference(errMsg)
		suggestion := ""
		if undeclaredField != "" {
			if match := findClosestMatch(undeclaredField, eventFilterAvailableFields); match != "" {
				suggestion = fmt.Sprintf("Did you mean %q?", match)
			}
		}
		addError(result, ValidationError{
			Severity:        SeverityError,
			Path:            path,
			Code:            "CEL_UNDECLARED_REFERENCE",
			Message:         errMsg,
			Suggestion:      suggestion,
			AvailableFields: eventFilterAvailableFields,
		})
		return
	}

	addError(result, ValidationError{
		Severity: SeverityError,
		Path:     path,
		Code:     "CEL_COMPILATION_ERROR",
		Message:  errMsg,
	})
}

// availableChannels returns a sorted list of all registered event channel names.
func (v *ManifestValidator) availableChannels() []string {
	return mapKeys(v.channelRegistry)
}

// validateWebhookTriggers checks that webhook triggers reference provider connections
// defined in the manifest's operational_gateway.provider_connections section.
func (v *ManifestValidator) validateWebhookTriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Build lookup of available provider connection IDs.
	connectionIDs := make(map[string]bool)
	var availableIDs []string
	if gw := manifest.GetOperationalGateway(); gw != nil {
		for _, pc := range gw.GetProviderConnections() {
			cid := pc.GetConnectionId()
			connectionIDs[cid] = true
			availableIDs = append(availableIDs, cid)
		}
	}
	sort.Strings(availableIDs)

	for i, saga := range manifest.GetSagas() {
		source := extractWebhookSource(saga.GetTrigger())
		if source == "" {
			continue
		}

		if !connectionIDs[source] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            fmt.Sprintf("sagas[%d].trigger", i),
				Code:            "UNKNOWN_WEBHOOK_SOURCE",
				Message:         fmt.Sprintf("webhook source %q does not match any provider connection in operational_gateway.provider_connections", source),
				AvailableFields: availableIDs,
			}
			if suggestion := findClosestMatch(source, availableIDs); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// validateScheduledTriggers enforces that scheduled trigger names are unique across all sagas.
func (v *ManifestValidator) validateScheduledTriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Track seen schedule names → first saga index.
	seen := make(map[string]int)

	for i, saga := range manifest.GetSagas() {
		trigger := saga.GetTrigger()
		if !strings.HasPrefix(trigger, "scheduled:") {
			continue
		}

		name := strings.TrimPrefix(trigger, "scheduled:")
		if firstIdx, exists := seen[name]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("sagas[%d].trigger", i),
				Code:     "DUPLICATE_SCHEDULED_TRIGGER",
				Message:  fmt.Sprintf("scheduled trigger name %q already defined at sagas[%d]", name, firstIdx),
			})
		} else {
			seen[name] = i
		}
	}
}

// accountIDPattern matches valid Stripe Connect account IDs (acct_ followed by 16+ alphanumeric chars).
var accountIDPattern = regexp.MustCompile(`^acct_[A-Za-z0-9]{16,}$`)

// allowedProviders is the set of supported payment rail providers.
var allowedProviders = map[string]bool{
	"stripe_connect": true,
}

// allowedPaymentMethods is the set of supported payment methods.
var allowedPaymentMethods = map[string]bool{
	"card":         true,
	"sepa_debit":   true,
	"bank_account": true,
}

// validatePaymentRails validates all payment rail configurations in the manifest.
func (v *ManifestValidator) validatePaymentRails(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	for i, rail := range manifest.GetPaymentRails() {
		basePath := fmt.Sprintf("payment_rails[%d]", i)

		// Validate provider
		if rail.GetProvider() != "" && !allowedProviders[rail.GetProvider()] {
			providerList := mapKeys(allowedProviders)
			addError(result, ValidationError{
				Severity:        SeverityError,
				Path:            basePath + ".provider",
				Code:            "INVALID_PAYMENT_PROVIDER",
				Message:         fmt.Sprintf("unsupported payment provider %q", rail.GetProvider()),
				AvailableFields: providerList,
			})
		}

		// Validate account_id format
		if rail.GetAccountId() != "" && !accountIDPattern.MatchString(rail.GetAccountId()) {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     basePath + ".account_id",
				Code:     "INVALID_ACCOUNT_ID_FORMAT",
				Message:  fmt.Sprintf("account_id %q does not match expected format acct_[A-Za-z0-9]{16,}", rail.GetAccountId()),
			})
		}

		// Validate platform_fee
		if fee := rail.GetPlatformFee(); fee != nil {
			v.validatePlatformFee(fee, basePath+".platform_fee", result)
		}

		// Validate supported_methods contain only known values
		for j, method := range rail.GetSupportedMethods() {
			if !allowedPaymentMethods[method] {
				methodList := mapKeys(allowedPaymentMethods)
				ve := ValidationError{
					Severity:        SeverityWarning,
					Path:            fmt.Sprintf("%s.supported_methods[%d]", basePath, j),
					Code:            "UNKNOWN_PAYMENT_METHOD",
					Message:         fmt.Sprintf("payment method %q is not a recognized method", method),
					AvailableFields: methodList,
				}
				if suggestion := findClosestMatch(method, methodList); suggestion != "" {
					ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
				}
				addError(result, ve)
			}
		}
	}
}

// validatePlatformFee validates the platform fee value is a valid positive decimal.
func (v *ManifestValidator) validatePlatformFee(
	fee *controlplanev1.PlatformFee,
	basePath string,
	result *ValidationResult,
) {
	if fee.GetValue() == "" {
		return
	}

	d, err := decimal.NewFromString(fee.GetValue())
	if err != nil {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".value",
			Code:     "INVALID_PLATFORM_FEE_VALUE",
			Message:  fmt.Sprintf("platform_fee.value %q is not a valid decimal", fee.GetValue()),
		})
		return
	}

	if d.LessThanOrEqual(decimal.Zero) {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     basePath + ".value",
			Code:     "INVALID_PLATFORM_FEE_VALUE",
			Message:  fmt.Sprintf("platform_fee.value must be greater than 0, got %s", fee.GetValue()),
		})
	}
}

// checkInstrumentRef validates that a referenced instrument code exists in the manifest.
func checkInstrumentRef(
	code string,
	validCodes map[string]bool,
	codeList []string,
	path string,
	result *ValidationResult,
) {
	if validCodes[code] {
		return
	}
	ve := ValidationError{
		Severity:        SeverityError,
		Path:            path,
		Code:            "UNDEFINED_INSTRUMENT_REFERENCE",
		Message:         fmt.Sprintf("instrument code %q is not defined in instruments[]", code),
		AvailableFields: codeList,
	}
	if suggestion := findClosestMatch(code, codeList); suggestion != "" {
		ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
	}
	addError(result, ve)
}

// celPartyTypeFields lists the variables available in party type CEL expressions.
var celPartyTypeFields = []string{"attributes", "party_type"}

// validatePartyTypes validates all party type definitions in the manifest.
func (v *ManifestValidator) validatePartyTypes(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Check duplicate (tenant_id, party_type) pairs
	seen := make(map[string]int)
	for i, pt := range manifest.GetPartyTypes() {
		key := pt.GetTenantId() + ":" + pt.GetPartyType()
		if prev, exists := seen[key]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     fmt.Sprintf("party_types[%d].party_type", i),
				Code:     "DUPLICATE_PARTY_TYPE",
				Message:  fmt.Sprintf("duplicate party_type %q for tenant %q (first defined at party_types[%d])", pt.GetPartyType(), pt.GetTenantId(), prev),
			})
		} else {
			seen[key] = i
		}

		basePath := fmt.Sprintf("party_types[%d]", i)

		// Validate attribute_schema is valid JSON
		if schema := pt.GetAttributeSchema(); schema != "" {
			v.validatePartyTypeSchema(schema, basePath+".attribute_schema", result)
		}

		// Validate CEL expressions
		if expr := pt.GetValidationCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".validation_cel", v.partyTypeCelEnv, celPartyTypeFields, result)
		}
		if expr := pt.GetEligibilityCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".eligibility_cel", v.partyTypeCelEnv, celPartyTypeFields, result)
		}
		if expr := pt.GetErrorMessageCel(); expr != "" {
			v.validateCELExpression(expr, basePath+".error_message_cel", v.partyTypeCelEnv, celPartyTypeFields, result)
		}
	}
}

// validatePartyTypeSchema validates that a party type attribute_schema is valid JSON.
func (v *ManifestValidator) validatePartyTypeSchema(schema, path string, result *ValidationResult) {
	var js json.RawMessage
	if err := json.Unmarshal([]byte(schema), &js); err != nil {
		addError(result, ValidationError{
			Severity: SeverityError,
			Path:     path,
			Code:     "INVALID_JSON_SCHEMA",
			Message:  fmt.Sprintf("attribute_schema is not valid JSON: %s", err.Error()),
		})
	}
}

// validateImmutability checks that immutable code fields have not been changed.
func (v *ManifestValidator) validateImmutability(
	current *controlplanev1.Manifest,
	previous *controlplanev1.Manifest,
	result *ValidationResult,
) {
	// Build maps of previous codes by index position to detect renames
	prevInstrumentsByIdx := make(map[int]string)
	for i, inst := range previous.GetInstruments() {
		prevInstrumentsByIdx[i] = inst.GetCode()
	}

	prevAccountTypesByIdx := make(map[int]string)
	for i, acct := range previous.GetAccountTypes() {
		prevAccountTypesByIdx[i] = acct.GetCode()
	}

	// Build lookup maps for previous codes
	prevInstruments := make(map[string]bool)
	for _, inst := range previous.GetInstruments() {
		prevInstruments[inst.GetCode()] = true
	}
	prevAccountTypes := make(map[string]bool)
	for _, acct := range previous.GetAccountTypes() {
		prevAccountTypes[acct.GetCode()] = true
	}

	// Check instruments: detect code changes at same index position
	for i, inst := range current.GetInstruments() {
		if prevCode, existed := prevInstrumentsByIdx[i]; existed {
			if inst.GetCode() != prevCode {
				addError(result, ValidationError{
					Severity: SeverityError,
					Path:     fmt.Sprintf("instruments[%d].code", i),
					Code:     "IMMUTABLE_FIELD_CHANGED",
					Message:  fmt.Sprintf("instrument code changed from %q to %q; codes are immutable primary keys", prevCode, inst.GetCode()),
				})
			}
		}
	}

	// Check account types: detect code changes at same index position
	for i, acct := range current.GetAccountTypes() {
		if prevCode, existed := prevAccountTypesByIdx[i]; existed {
			if acct.GetCode() != prevCode {
				addError(result, ValidationError{
					Severity: SeverityError,
					Path:     fmt.Sprintf("account_types[%d].code", i),
					Code:     "IMMUTABLE_FIELD_CHANGED",
					Message:  fmt.Sprintf("account type code changed from %q to %q; codes are immutable primary keys", prevCode, acct.GetCode()),
				})
			}
		}
	}
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
	// Build the relationship graph from the previous manifest to check dependencies.
	prevGraph := ExtractRelationshipGraph(previous, callLogs)

	// Build lookup sets for current manifest resources.
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

	// Check removed instruments. Use Impact (all connected edges) because instruments
	// can be both targets (denominated_in from account types) and sources (converts in
	// valuation rules).
	for _, inst := range previous.GetInstruments() {
		code := inst.GetCode()
		if currentInstruments[code] {
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

	// Check removed account types.
	// Note: In the current graph structure, account types are only sources (denominated_in
	// edges to instruments), not targets. Saga-to-account-type dependencies are captured
	// via dynamic edges when call logs are provided (e.g., from a saga execution engine).
	// Without call logs, this check relies on graph edges populated externally.
	for _, acct := range previous.GetAccountTypes() {
		code := acct.GetCode()
		if currentAccountTypes[code] {
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

	// Check removed sagas.
	for _, saga := range previous.GetSagas() {
		name := saga.GetName()
		if currentSagas[name] {
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
	predeclared["invoke_handler"] = starlark.NewBuiltin("invoke_handler",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.None, nil
		})
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

// ─── Task 2: API Trigger Validation Against OpenAPI Spec ────────────────────

// apiPathPattern validates that API trigger paths start with '/'.
var apiPathPattern = regexp.MustCompile(`^/`)

// validateAPITriggers validates sagas with "api:" trigger prefix.
// It checks path format, uniqueness, and (when an OpenAPI spec is available) endpoint existence.
func (v *ManifestValidator) validateAPITriggers(
	manifest *controlplanev1.Manifest,
	result *ValidationResult,
) {
	seenPaths := make(map[string]int)
	var availablePaths []string
	if v.apiPathRegistry != nil {
		availablePaths = mapKeys(v.apiPathRegistry)
	}

	for i, saga := range manifest.GetSagas() {
		trigger := saga.GetTrigger()
		if !strings.HasPrefix(trigger, "api:") {
			continue
		}

		path := strings.TrimPrefix(trigger, "api:")
		sagaPath := fmt.Sprintf("sagas[%d].trigger", i)

		// Validate format: must start with '/'
		if !apiPathPattern.MatchString(path) {
			addError(result, ValidationError{
				Severity:   SeverityError,
				Path:       sagaPath,
				Code:       "INVALID_API_PATH_FORMAT",
				Message:    fmt.Sprintf("API trigger path %q must start with '/'", path),
				Suggestion: "API paths should follow the format '/v1/resource'",
			})
			continue
		}

		// Check uniqueness
		if prevIdx, exists := seenPaths[path]; exists {
			addError(result, ValidationError{
				Severity: SeverityError,
				Path:     sagaPath,
				Code:     "DUPLICATE_API_TRIGGER",
				Message:  fmt.Sprintf("API path %q already bound to saga at sagas[%d]", path, prevIdx),
			})
		} else {
			seenPaths[path] = i
		}

		// Check existence in OpenAPI spec (only when spec is available)
		if v.apiPathRegistry != nil && !v.apiPathRegistry[path] {
			ve := ValidationError{
				Severity:        SeverityError,
				Path:            sagaPath,
				Code:            "UNKNOWN_API_ENDPOINT",
				Message:         fmt.Sprintf("API path %q is not defined in the OpenAPI spec", path),
				AvailableFields: availablePaths,
			}
			if suggestion := findClosestMatch(path, availablePaths); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// tryLoadOpenAPIPaths attempts to load API paths from api/openapi/meridian.swagger.json.
// Returns nil if the file doesn't exist or can't be parsed.
func tryLoadOpenAPIPaths() map[string]bool {
	specPath := findRepoFile("api/openapi/meridian.swagger.json")
	if specPath == "" {
		return nil
	}

	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil
	}

	return parseOpenAPIPaths(data)
}

// parseOpenAPIPaths extracts endpoint paths from an OpenAPI/Swagger JSON spec.
func parseOpenAPIPaths(data []byte) map[string]bool {
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil
	}

	paths := make(map[string]bool, len(spec.Paths))
	for p := range spec.Paths {
		paths[p] = true
	}
	return paths
}

// ─── Task 5: AsyncAPI CEL Field Validation ──────────────────────────────────

// tryLoadAsyncAPISchemas attempts to load event payload schemas from api/asyncapi/*.yaml.
// Returns nil if the directory doesn't exist or no files are found.
func tryLoadAsyncAPISchemas() map[string]map[string]bool {
	dir := findRepoFile("api/asyncapi")
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	schemas := make(map[string]map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		parseAsyncAPIFile(data, schemas)
	}

	if len(schemas) == 0 {
		return nil
	}
	return schemas
}

// asyncAPIDoc represents the minimal structure of an AsyncAPI 3.0 document.
type asyncAPIDoc struct {
	Channels   map[string]asyncAPIChannel `yaml:"channels"`
	Components asyncAPIComponents         `yaml:"components"`
}

type asyncAPIChannel struct {
	Messages map[string]asyncAPIMessageRef `yaml:"messages"`
}

type asyncAPIMessageRef struct {
	Ref string `yaml:"$ref"`
}

type asyncAPIComponents struct {
	Messages map[string]asyncAPIMessage `yaml:"messages"`
	Schemas  map[string]asyncAPISchema  `yaml:"schemas"`
}

type asyncAPIMessage struct {
	Payload asyncAPIPayloadRef `yaml:"payload"`
}

type asyncAPIPayloadRef struct {
	Ref string `yaml:"$ref"`
}

type asyncAPISchema struct {
	Properties map[string]yaml.Node `yaml:"properties"`
}

// parseAsyncAPIFile parses an AsyncAPI YAML file and populates the schemas map.
func parseAsyncAPIFile(data []byte, schemas map[string]map[string]bool) {
	var doc asyncAPIDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return
	}

	for channelName, channel := range doc.Channels {
		for _, msgRef := range channel.Messages {
			fields := resolveMessageFields(msgRef, doc.Components)
			if len(fields) == 0 {
				continue
			}
			existing := schemas[channelName]
			if existing == nil {
				existing = make(map[string]bool, len(fields))
			}
			for _, f := range fields {
				existing[f] = true
			}
			schemas[channelName] = existing
		}
	}
}

// resolveMessageFields follows $ref chains from a message reference to its schema
// and returns the list of top-level property names.
func resolveMessageFields(msgRef asyncAPIMessageRef, components asyncAPIComponents) []string {
	msgName := extractRef(msgRef.Ref)
	if msgName == "" {
		return nil
	}
	msg, ok := components.Messages[msgName]
	if !ok {
		return nil
	}
	schemaName := extractRef(msg.Payload.Ref)
	if schemaName == "" {
		return nil
	}
	s, ok := components.Schemas[schemaName]
	if !ok {
		return nil
	}
	fields := make([]string, 0, len(s.Properties))
	for fieldName := range s.Properties {
		fields = append(fields, fieldName)
	}
	return fields
}

// extractRef extracts the last segment from a JSON/YAML $ref (e.g., "#/components/schemas/Foo" -> "Foo").
func extractRef(ref string) string {
	if ref == "" {
		return ""
	}
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

// extractCELFieldRefs extracts field names accessed on the "event" variable from a CEL expression.
// For example, "event.amount > 0 && event.currency == 'GBP'" returns ["amount", "currency"].
func extractCELFieldRefs(expression string, env *cel.Env) []string {
	celAST, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil
	}

	parsedExpr, err := cel.AstToParsedExpr(celAST)
	if err != nil {
		return nil
	}

	fields := make(map[string]bool)
	extractFieldsFromExpr(parsedExpr.GetExpr(), fields)

	result := make([]string, 0, len(fields))
	for f := range fields {
		result = append(result, f)
	}
	sort.Strings(result)
	return result
}

// extractFieldsFromExpr recursively walks a CEL proto expression to find
// select expressions on the "event" variable (event.field_name).
func extractFieldsFromExpr(expr *exprpb.Expr, fields map[string]bool) {
	if expr == nil {
		return
	}

	switch e := expr.ExprKind.(type) {
	case *exprpb.Expr_SelectExpr:
		extractSelectField(e.SelectExpr, fields)
	case *exprpb.Expr_CallExpr:
		extractCallFields(e.CallExpr, fields)
	case *exprpb.Expr_ListExpr:
		for _, elem := range e.ListExpr.GetElements() {
			extractFieldsFromExpr(elem, fields)
		}
	case *exprpb.Expr_StructExpr:
		for _, entry := range e.StructExpr.GetEntries() {
			extractFieldsFromExpr(entry.GetValue(), fields)
		}
	case *exprpb.Expr_ComprehensionExpr:
		extractComprehensionFields(e.ComprehensionExpr, fields)
	}
}

func extractSelectField(sel *exprpb.Expr_Select, fields map[string]bool) {
	if ident, ok := sel.GetOperand().ExprKind.(*exprpb.Expr_IdentExpr); ok && ident.IdentExpr.GetName() == "event" {
		fields[sel.GetField()] = true
	} else {
		extractFieldsFromExpr(sel.GetOperand(), fields)
	}
}

func extractCallFields(call *exprpb.Expr_Call, fields map[string]bool) {
	// Handle index operator: event["field_name"] is represented as _[_](event, "field_name")
	if call.GetFunction() == "_[_]" && len(call.GetArgs()) == 2 {
		if ident, ok := call.GetArgs()[0].ExprKind.(*exprpb.Expr_IdentExpr); ok && ident.IdentExpr.GetName() == "event" {
			if constExpr, ok := call.GetArgs()[1].ExprKind.(*exprpb.Expr_ConstExpr); ok {
				if sv := constExpr.ConstExpr.GetStringValue(); sv != "" {
					fields[sv] = true
					return
				}
			}
		}
	}
	extractFieldsFromExpr(call.GetTarget(), fields)
	for _, arg := range call.GetArgs() {
		extractFieldsFromExpr(arg, fields)
	}
}

func extractComprehensionFields(comp *exprpb.Expr_Comprehension, fields map[string]bool) {
	extractFieldsFromExpr(comp.GetIterRange(), fields)
	extractFieldsFromExpr(comp.GetAccuInit(), fields)
	extractFieldsFromExpr(comp.GetLoopCondition(), fields)
	extractFieldsFromExpr(comp.GetLoopStep(), fields)
	extractFieldsFromExpr(comp.GetResult(), fields)
}

// validateEventFilterCELFields cross-checks CEL expression field references against
// the AsyncAPI schema for the given event channel. Produces warnings for unknown fields.
func (v *ManifestValidator) validateEventFilterCELFields(
	expression string,
	channel string,
	path string,
	result *ValidationResult,
) {
	if v.asyncAPISchemas == nil {
		return
	}

	schemaFields, ok := v.asyncAPISchemas[channel]
	if !ok {
		return
	}

	celFields := extractCELFieldRefs(expression, v.eventFilterEnv)
	schemaFieldList := mapKeys(schemaFields)

	for _, field := range celFields {
		if !schemaFields[field] {
			ve := ValidationError{
				Severity:        SeverityWarning,
				Path:            path,
				Code:            "CEL_UNKNOWN_EVENT_FIELD",
				Message:         fmt.Sprintf("field %q is not defined in the AsyncAPI schema for channel %q", field, channel),
				AvailableFields: schemaFieldList,
			}
			if suggestion := findClosestMatch(field, schemaFieldList); suggestion != "" {
				ve.Suggestion = fmt.Sprintf("Did you mean %q?", suggestion)
			}
			addError(result, ve)
		}
	}
}

// ─── Task 11: Operational Gateway Orphan Detection ──────────────────────────

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
				Severity: SeverityWarning,
				Path:     fmt.Sprintf("operational_gateway.provider_connections[%d].connection_id", i),
				Code:     "ORPHAN_PROVIDER_CONNECTION",
				Message:  fmt.Sprintf("provider connection %q is not referenced by any instruction route or webhook trigger", cid),
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
				Severity: SeverityWarning,
				Path:     fmt.Sprintf("operational_gateway.instruction_routes[%d].instruction_type", i),
				Code:     "ORPHAN_INSTRUCTION_ROUTE",
				Message:  fmt.Sprintf("instruction type %q is not dispatched by any saga script", instrType),
			})
		}
	}
}

// extractWebhookSource extracts the provider connection source from a webhook trigger string.
// Returns empty string for non-webhook triggers.
func extractWebhookSource(trigger string) string {
	if !strings.HasPrefix(trigger, "webhook:") {
		return ""
	}
	remainder := strings.TrimPrefix(trigger, "webhook:")
	if dotIdx := strings.Index(remainder, "."); dotIdx > 0 {
		return remainder[:dotIdx]
	}
	return remainder
}

// ─── Shared Helpers ─────────────────────────────────────────────────────────

// findRepoFile walks up from the current working directory to find a relative file path.
// Returns the absolute path if found, empty string otherwise.
func findRepoFile(relPath string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// buildFieldPath extracts a dotted field path from a protovalidate Violation.
func buildFieldPath(violation *protovalidate.Violation) string {
	if violation.Proto == nil || violation.Proto.GetField() == nil {
		return ""
	}
	elements := violation.Proto.GetField().GetElements()
	if len(elements) == 0 {
		return ""
	}
	parts := make([]string, 0, len(elements))
	for _, elem := range elements {
		parts = append(parts, elem.GetFieldName())
	}
	return strings.Join(parts, ".")
}

// addError appends a validation error to the result in the appropriate list.
func addError(result *ValidationResult, ve ValidationError) {
	if ve.Severity == SeverityWarning {
		result.Warnings = append(result.Warnings, ve)
	} else {
		result.Errors = append(result.Errors, ve)
	}
}

// parseStarlarkError converts a Starlark error into a structured ValidationError.
func parseStarlarkError(err error, basePath string) ValidationError {
	ve := ValidationError{
		Severity: SeverityError,
		Path:     basePath,
		Code:     "STARLARK_COMPILATION_ERROR",
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
		ve.Code = "STARLARK_SYNTAX_ERROR"
	}

	return ve
}

// extractUndeclaredReference extracts the field name from a CEL undeclared reference error.
// Example: "undeclared reference to 'quanity'" -> "quanity"
func extractUndeclaredReference(errMsg string) string {
	const prefix = "undeclared reference to '"
	idx := strings.Index(errMsg, prefix)
	if idx < 0 {
		return ""
	}
	rest := errMsg[idx+len(prefix):]
	endIdx := strings.Index(rest, "'")
	if endIdx < 0 {
		return ""
	}
	return rest[:endIdx]
}

// extractUndefinedStarlarkName extracts the name from a Starlark "undefined: X" error.
func extractUndefinedStarlarkName(errMsg string) string {
	const marker = "undefined: "
	idx := strings.Index(errMsg, marker)
	if idx < 0 {
		return ""
	}
	rest := errMsg[idx+len(marker):]
	// The name goes until the end of the line or end of string
	endIdx := strings.IndexAny(rest, " \n\t")
	if endIdx < 0 {
		return rest
	}
	return rest[:endIdx]
}

// findClosestMatch finds the closest string in candidates to the target using
// Levenshtein distance. Returns empty string if no candidate is close enough.
func findClosestMatch(target string, candidates []string) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	bestDist := len(target)/2 + 1 // Threshold: must be within half the target length

	for _, candidate := range candidates {
		dist := levenshteinDistance(strings.ToLower(target), strings.ToLower(candidate))
		if dist < bestDist {
			bestDist = dist
			bestMatch = candidate
		}
	}

	return bestMatch
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	la := len(a)
	lb := len(b)

	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, la+1)
	curr := make([]int, la+1)

	for i := 0; i <= la; i++ {
		prev[i] = i
	}

	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			curr[i] = del
			if ins < curr[i] {
				curr[i] = ins
			}
			if sub < curr[i] {
				curr[i] = sub
			}
		}
		prev, curr = curr, prev
	}

	return prev[la]
}

// mapKeys returns the sorted keys of a map[string]bool.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
