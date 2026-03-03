// Package cel provides CEL (Common Expression Language) compilation and evaluation
// for instrument validation and bucket key generation.
package cel

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// CELVersion is the pinned version of cel-go used in this package.
// This version must match the version in go.mod to ensure consistent behavior.
// See https://github.com/google/cel-go/releases for release notes.
const CELVersion = "0.26.1"

// Security constraints for CEL expressions.
const (
	// MaxExpressionLength is the maximum allowed length of a CEL expression in bytes.
	MaxExpressionLength = 4096 // 4KB

	// MaxExpressionDepth is the maximum allowed nesting depth of a CEL expression.
	MaxExpressionDepth = 10

	// CostLimit is the maximum evaluation cost for a CEL program.
	// This prevents expensive expressions from consuming excessive resources.
	CostLimit = 10000
)

// Error types for CEL compilation.
var (
	// ErrExpressionTooLong is returned when an expression exceeds MaxExpressionLength.
	ErrExpressionTooLong = errors.New("expression exceeds maximum length")

	// ErrExpressionTooDeep is returned when an expression exceeds MaxExpressionDepth.
	ErrExpressionTooDeep = errors.New("expression exceeds maximum nesting depth")

	// ErrEnvironmentCreation is returned when a CEL environment cannot be created.
	ErrEnvironmentCreation = errors.New("failed to create CEL environment")

	// ErrCompilation is returned when CEL compilation fails.
	ErrCompilation = errors.New("CEL compilation failed")

	// ErrEligibilityNotBool is returned when an eligibility expression does not return a boolean.
	ErrEligibilityNotBool = errors.New("eligibility expression did not return boolean")

	// ErrBucketKeyNotString is returned when a bucket key expression does not return a string.
	ErrBucketKeyNotString = errors.New("bucket key expression must return string")

	// ErrValidationNotBool is returned when a validation expression does not return a boolean.
	ErrValidationNotBool = errors.New("validation expression must return boolean")

	// ErrEventFilterNotBool is returned when an event filter expression does not return a boolean.
	ErrEventFilterNotBool = errors.New("event filter expression must return boolean")
)

// Compiler provides CEL expression compilation with security constraints.
type Compiler struct {
	validationEnv  *cel.Env
	bucketKeyEnv   *cel.Env
	eligibilityEnv *cel.Env
	eventFilterEnv *cel.Env
}

// NewCompiler creates a new CEL Compiler with configured environments.
func NewCompiler() (*Compiler, error) {
	validationEnv, err := createValidationEnv()
	if err != nil {
		return nil, errors.Join(ErrEnvironmentCreation, fmt.Errorf("validation env: %w", err))
	}

	bucketKeyEnv, err := createBucketKeyEnv()
	if err != nil {
		return nil, errors.Join(ErrEnvironmentCreation, fmt.Errorf("bucket key env: %w", err))
	}

	eligibilityEnv, err := createEligibilityEnv()
	if err != nil {
		return nil, errors.Join(ErrEnvironmentCreation, fmt.Errorf("eligibility env: %w", err))
	}

	eventFilterEnv, err := createEventFilterEnv()
	if err != nil {
		return nil, errors.Join(ErrEnvironmentCreation, fmt.Errorf("event filter env: %w", err))
	}

	return &Compiler{
		validationEnv:  validationEnv,
		bucketKeyEnv:   bucketKeyEnv,
		eligibilityEnv: eligibilityEnv,
		eventFilterEnv: eventFilterEnv,
	}, nil
}

// createValidationEnv creates the CEL environment for validation expressions.
// Variables available:
//   - attributes: map[string]string - key-value attributes from the quantity
//   - amount: string - decimal amount as a string for arbitrary precision
//   - valid_from: timestamp - optional validity start time
//   - valid_to: timestamp - optional validity end time
//   - source: string - origin identifier for the quantity
func createValidationEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("amount", cel.StringType),
		cel.Variable("valid_from", cel.TimestampType),
		cel.Variable("valid_to", cel.TimestampType),
		cel.Variable("source", cel.StringType),
		SafeParseLib(),
	)
}

// createBucketKeyEnv creates the CEL environment for bucket key expressions.
// Variables available:
//   - attributes: map[string]string - key-value attributes from the quantity
//
// The expression must return a string representing the bucket/fungibility key.
func createBucketKeyEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
		SafeParseLib(),
		BucketKeyLib(),
	)
}

// createEligibilityEnv creates the CEL environment for eligibility expressions.
// Variables available:
//   - party: map[string]string - party context with keys: type, status, external_reference_type
//   - attributes: map[string]string - key-value attributes for the account type
//
// The expression must return a boolean indicating eligibility.
func createEligibilityEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("party", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
	)
}

// createEventFilterEnv creates the CEL environment for event filter expressions.
// Variables available:
//   - event: dyn - the full event payload as a dynamic map (any event proto or JSON object)
//   - metadata: map[string]string - Kafka message headers and other event metadata
//
// The expression must return a boolean indicating whether the saga should trigger.
func createEventFilterEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("event", cel.DynType),
		cel.Variable("metadata", cel.MapType(cel.StringType, cel.StringType)),
	)
}

// CompileValidation compiles a boolean validation expression against the validation environment.
// Returns a cel.Program that can be evaluated with the appropriate input values.
// The expression must return a boolean indicating validity.
func (c *Compiler) CompileValidation(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	ast, issues := c.validationEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	if ast.OutputType() != cel.BoolType {
		return nil, errors.Join(ErrCompilation, ErrValidationNotBool)
	}

	prg, err := c.validationEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	return prg, nil
}

// CompileValueExpression compiles a value-returning expression against the validation environment.
// Unlike CompileValidation, this method does not require a boolean output type and is intended
// for pricing formulas and other expressions that return numeric or string values.
// The validation environment provides: attributes, amount, valid_from, valid_to, source.
func (c *Compiler) CompileValueExpression(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	ast, issues := c.validationEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	prg, err := c.validationEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	return prg, nil
}

// CompileBucketKey compiles a bucket key expression against the bucket key environment.
// Returns a cel.Program that can be evaluated to generate a fungibility key.
// The expression should return a string representing the bucket key.
func (c *Compiler) CompileBucketKey(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	ast, issues := c.bucketKeyEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	if ast.OutputType() != cel.StringType {
		return nil, errors.Join(ErrCompilation, ErrBucketKeyNotString)
	}

	prg, err := c.bucketKeyEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	return prg, nil
}

// CompileEligibility compiles an eligibility expression against the eligibility environment.
// Returns a cel.Program that can be evaluated with party and attributes input values.
// The expression must return a boolean indicating whether a party is eligible.
func (c *Compiler) CompileEligibility(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	ast, issues := c.eligibilityEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	if ast.OutputType() != cel.BoolType {
		return nil, errors.Join(ErrCompilation, ErrEligibilityNotBool)
	}

	prg, err := c.eligibilityEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	return prg, nil
}

// CompileEventFilter compiles a boolean event filter expression against the event filter environment.
// Returns a cel.Program that can be evaluated with event and metadata input values.
// The expression must return a boolean indicating whether the saga should be triggered.
func (c *Compiler) CompileEventFilter(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	ast, issues := c.eventFilterEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	if ast.OutputType() != cel.BoolType {
		return nil, errors.Join(ErrCompilation, ErrEventFilterNotBool)
	}

	prg, err := c.eventFilterEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	return prg, nil
}

// EvalEligibility evaluates a compiled eligibility program with the given party context.
// Only non-empty party fields are included in the evaluation map so that CEL has() checks
// behave correctly — empty strings indicate absent fields rather than empty values.
func EvalEligibility(prg cel.Program, partyType, partyStatus, partyExtRefType string, attributes map[string]string) (bool, error) {
	if attributes == nil {
		attributes = make(map[string]string)
	}

	party := make(map[string]string)
	if partyType != "" {
		party["type"] = partyType
	}
	if partyStatus != "" {
		party["status"] = partyStatus
	}
	if partyExtRefType != "" {
		party["external_reference_type"] = partyExtRefType
	}

	out, _, err := prg.Eval(map[string]any{
		"party":      party,
		"attributes": attributes,
	})
	if err != nil {
		return false, fmt.Errorf("eligibility evaluation error: %w", err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return false, ErrEligibilityNotBool
	}

	return result, nil
}

// ValidateValidationCEL compiles a validation CEL expression and discards the program.
// Implements the accounttype.DefinitionCompiler interface.
func (c *Compiler) ValidateValidationCEL(expression string) error {
	_, err := c.CompileValidation(expression)
	return err
}

// ValidateBucketingCEL compiles a bucketing CEL expression and discards the program.
// Implements the accounttype.DefinitionCompiler interface.
func (c *Compiler) ValidateBucketingCEL(expression string) error {
	_, err := c.CompileBucketKey(expression)
	return err
}

// ValidateEligibilityCEL compiles an eligibility CEL expression and discards the program.
// Implements the accounttype.DefinitionCompiler interface.
func (c *Compiler) ValidateEligibilityCEL(expression string) error {
	_, err := c.CompileEligibility(expression)
	return err
}

// validateExpressionConstraints checks that an expression meets security constraints.
func validateExpressionConstraints(expression string) error {
	if len(expression) > MaxExpressionLength {
		return fmt.Errorf("%w: length %d exceeds %d", ErrExpressionTooLong, len(expression), MaxExpressionLength)
	}

	depth := measureExpressionDepth(expression)
	if depth > MaxExpressionDepth {
		return fmt.Errorf("%w: depth %d exceeds %d", ErrExpressionTooDeep, depth, MaxExpressionDepth)
	}

	return nil
}

// measureExpressionDepth estimates the nesting depth of an expression.
// This is a heuristic based on parentheses and bracket nesting.
func measureExpressionDepth(expression string) int {
	maxDepth := 0
	currentDepth := 0

	for _, ch := range expression {
		switch ch {
		case '(', '[', '{':
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
		case ')', ']', '}':
			currentDepth--
			if currentDepth < 0 {
				currentDepth = 0
			}
		}
	}

	return maxDepth
}

// SafeParseLib creates a CEL function library for safe type parsing.
// All functions handle invalid inputs gracefully by returning errors.
//
// Functions:
//   - parse_iso_date(string) -> timestamp: Parses RFC3339 date string to timestamp
//   - parse_int(string) -> int: Parses string to integer
//   - parse_decimal(string) -> double: Parses string to double
//   - parse_bool(string) -> bool: Parses string to boolean
func SafeParseLib() cel.EnvOption {
	return cel.Lib(&safeParseLibrary{})
}

type safeParseLibrary struct{}

func (*safeParseLibrary) LibraryName() string {
	return "meridian.SafeParse"
}

func (*safeParseLibrary) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("parse_iso_date",
			cel.Overload("parse_iso_date_string",
				[]*cel.Type{cel.StringType},
				cel.TimestampType,
				cel.UnaryBinding(parseISODate),
			),
		),
		cel.Function("parse_int",
			cel.Overload("parse_int_string",
				[]*cel.Type{cel.StringType},
				cel.IntType,
				cel.UnaryBinding(parseInt),
			),
		),
		cel.Function("parse_decimal",
			cel.Overload("parse_decimal_string",
				[]*cel.Type{cel.StringType},
				cel.DoubleType,
				cel.UnaryBinding(parseDecimal),
			),
		),
		cel.Function("parse_bool",
			cel.Overload("parse_bool_string",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
				cel.UnaryBinding(parseBool),
			),
		),
	}
}

func (*safeParseLibrary) ProgramOptions() []cel.ProgramOption {
	return nil
}

func parseISODate(val ref.Val) ref.Val {
	s, ok := val.Value().(string)
	if !ok {
		return types.NewErr("parse_iso_date: expected string, got %T", val.Value())
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return types.NewErr("parse_iso_date: invalid ISO date: %v", err)
	}

	return types.Timestamp{Time: t}
}

func parseInt(val ref.Val) ref.Val {
	s, ok := val.Value().(string)
	if !ok {
		return types.NewErr("parse_int: expected string, got %T", val.Value())
	}

	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return types.NewErr("parse_int: invalid integer: %v", err)
	}

	return types.Int(i)
}

func parseDecimal(val ref.Val) ref.Val {
	s, ok := val.Value().(string)
	if !ok {
		return types.NewErr("parse_decimal: expected string, got %T", val.Value())
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return types.NewErr("parse_decimal: invalid decimal: %v", err)
	}

	return types.Double(f)
}

func parseBool(val ref.Val) ref.Val {
	s, ok := val.Value().(string)
	if !ok {
		return types.NewErr("parse_bool: expected string, got %T", val.Value())
	}

	// Accept common boolean representations
	lower := strings.ToLower(s)
	switch lower {
	case "true", "1", "yes", "on":
		return types.True
	case "false", "0", "no", "off":
		return types.False
	default:
		return types.NewErr("parse_bool: invalid boolean: %q", s)
	}
}

// BucketKeyLib creates a CEL function library for bucket key generation.
//
// Functions:
//   - bucket_key([]string) -> string: Generates a deterministic SHA256 hash from a list of keys.
//     Uses length-prefixed concatenation to prevent delimiter injection attacks.
func BucketKeyLib() cel.EnvOption {
	return cel.Lib(&bucketKeyLibrary{})
}

type bucketKeyLibrary struct{}

func (*bucketKeyLibrary) LibraryName() string {
	return "meridian.BucketKey"
}

func (*bucketKeyLibrary) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("bucket_key",
			cel.Overload("bucket_key_list",
				[]*cel.Type{cel.ListType(cel.StringType)},
				cel.StringType,
				cel.UnaryBinding(bucketKey),
			),
		),
	}
}

func (*bucketKeyLibrary) ProgramOptions() []cel.ProgramOption {
	return nil
}

// bucketKey generates a deterministic SHA256 hash from a list of keys.
// Uses length-prefixed concatenation format:
//
//	[4-byte length][key bytes][4-byte length][key bytes]...
//
// This prevents delimiter injection attacks where a key containing a delimiter
// could be confused with multiple keys.
func bucketKey(val ref.Val) ref.Val {
	list, ok := val.(traits.Lister)
	if !ok {
		return types.NewErr("bucket_key: expected list, got %T", val)
	}

	hasher := sha256.New()

	it := list.Iterator()
	for it.HasNext() == types.True {
		elem := it.Next()
		s, ok := elem.Value().(string)
		if !ok {
			return types.NewErr("bucket_key: list elements must be strings, got %T", elem.Value())
		}

		// Write 4-byte length prefix (big-endian)
		lenBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBytes, uint32(len(s)))
		hasher.Write(lenBytes)

		// Write the key bytes
		hasher.Write([]byte(s))
	}

	return types.String(hex.EncodeToString(hasher.Sum(nil)))
}
