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
)

// timeBasedAttributePatterns contains attribute names that suggest time-based bucketing.
// Using these in fungibility expressions can cause bucket cardinality explosion.
// For example, half-hourly periods create 17,520 buckets per year per meter.
//
// Time should be stored as position metadata for valuation, not as part of the bucket key.
// See ADR-0013 for guidance on separating fungibility from valuation dimensions.
var timeBasedAttributePatterns = []string{
	"time",
	"timestamp",
	"date",
	"period",
	"hour",
	"minute",
	"second",
	"day",
	"week",
	"month",
	"year",
	"settlement_period",
	"trading_period",
	"half_hour",
	"halfhour",
	"interval",
	"slot",
	"epoch",
}

// BucketKeyLintWarning represents a lint warning for bucket key expressions.
type BucketKeyLintWarning struct {
	// AttributeName is the detected time-based attribute name.
	AttributeName string
	// Message describes why this attribute may cause scalability issues.
	Message string
}

// BucketKeyResult contains the compiled program and any lint warnings.
type BucketKeyResult struct {
	// Program is the compiled CEL program.
	Program cel.Program
	// Warnings contains lint warnings about potential scalability issues.
	Warnings []BucketKeyLintWarning
}

// Compiler provides CEL expression compilation with security constraints.
type Compiler struct {
	validationEnv *cel.Env
	bucketKeyEnv  *cel.Env
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

	return &Compiler{
		validationEnv: validationEnv,
		bucketKeyEnv:  bucketKeyEnv,
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

// CompileValidation compiles a validation expression against the validation environment.
// Returns a cel.Program that can be evaluated with the appropriate input values.
// The expression should return a boolean indicating validity.
func (c *Compiler) CompileValidation(expression string) (cel.Program, error) {
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
//
// Note: Use CompileBucketKeyWithLint to also receive warnings about potential
// scalability issues such as time-based attributes in the expression.
func (c *Compiler) CompileBucketKey(expression string) (cel.Program, error) {
	result, err := c.CompileBucketKeyWithLint(expression)
	if err != nil {
		return nil, err
	}
	return result.Program, nil
}

// CompileBucketKeyWithLint compiles a bucket key expression and returns lint warnings.
// This method detects potential scalability issues such as time-based attributes
// that could cause bucket cardinality explosion.
//
// Time-based attributes (e.g., settlement_period, hour, timestamp) should typically
// be stored as position metadata for valuation purposes, not as part of the
// fungibility key. Including them in the bucket key can create thousands of buckets
// per day, leading to:
//   - Storage bloat (17,520 buckets/year for half-hourly data)
//   - Query performance degradation
//   - Hitting the 10,000 bucket cardinality limit
//
// Example of problematic expression:
//
//	bucket_key([attributes["region"], attributes["settlement_period"]])  // WARNING!
//
// Recommended pattern:
//
//	bucket_key([attributes["region"], attributes["supplier"]])  // Fungibility only
//	// Store settlement_period as position metadata for valuation
func (c *Compiler) CompileBucketKeyWithLint(expression string) (*BucketKeyResult, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	ast, issues := c.bucketKeyEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	prg, err := c.bucketKeyEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	// Lint the expression for time-based attributes
	warnings := lintBucketKeyExpression(expression)

	return &BucketKeyResult{
		Program:  prg,
		Warnings: warnings,
	}, nil
}

// lintBucketKeyExpression checks a bucket key expression for time-based attributes
// that could cause bucket cardinality explosion.
//
// Returns warnings for any detected time-based attribute patterns.
// This is a heuristic check that looks for common time-related attribute names
// in the expression string.
func lintBucketKeyExpression(expression string) []BucketKeyLintWarning {
	var warnings []BucketKeyLintWarning
	lowerExpr := strings.ToLower(expression)

	for _, pattern := range timeBasedAttributePatterns {
		// Check for attribute access patterns like attributes["period"] or attributes['period']
		// Also check for the pattern appearing as a standalone identifier
		accessPatterns := []string{
			fmt.Sprintf(`attributes["%s"]`, pattern),
			fmt.Sprintf(`attributes['%s']`, pattern),
			fmt.Sprintf(`attributes["%s`, pattern),  // partial match for composite names
			fmt.Sprintf(`attributes['%s`, pattern),  // partial match for composite names
			fmt.Sprintf(`.%s`, pattern),             // dot access
		}

		for _, accessPattern := range accessPatterns {
			if strings.Contains(lowerExpr, accessPattern) {
				warnings = append(warnings, BucketKeyLintWarning{
					AttributeName: pattern,
					Message: fmt.Sprintf(
						"time-based attribute %q detected in bucket key expression; "+
							"this may cause bucket cardinality explosion (e.g., 17,520 buckets/year for half-hourly data); "+
							"consider storing time as position metadata for valuation instead of including it in the fungibility key",
						pattern,
					),
				})
				break // Only warn once per pattern
			}
		}
	}

	return warnings
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
