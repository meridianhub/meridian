// Package cel re-exports the shared CEL compiler from shared/pkg/cel.
// Kept for backwards compatibility; new code should import shared/pkg/cel directly.
package cel

import sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"

// Re-exported constants.
const (
	CELVersion          = sharedcel.CELVersion
	MaxExpressionLength = sharedcel.MaxExpressionLength
	MaxExpressionDepth  = sharedcel.MaxExpressionDepth
	CostLimit           = sharedcel.CostLimit
)

// Re-exported error variables.
var (
	ErrExpressionTooLong   = sharedcel.ErrExpressionTooLong
	ErrExpressionTooDeep   = sharedcel.ErrExpressionTooDeep
	ErrEnvironmentCreation = sharedcel.ErrEnvironmentCreation
	ErrCompilation         = sharedcel.ErrCompilation
	ErrEligibilityNotBool  = sharedcel.ErrEligibilityNotBool
	ErrBucketKeyNotString  = sharedcel.ErrBucketKeyNotString
	ErrValidationNotBool   = sharedcel.ErrValidationNotBool
)

// Compiler is an alias for the shared CEL compiler.
type Compiler = sharedcel.Compiler

// NewCompiler re-exports the shared CEL compiler constructor.
var NewCompiler = sharedcel.NewCompiler

// SafeParseLib re-exports the shared SafeParseLib function.
// BucketKeyLib re-exports the shared BucketKeyLib function.
var (
	SafeParseLib = sharedcel.SafeParseLib
	BucketKeyLib = sharedcel.BucketKeyLib
)

// EvalEligibility re-exports the shared EvalEligibility function.
var EvalEligibility = sharedcel.EvalEligibility
