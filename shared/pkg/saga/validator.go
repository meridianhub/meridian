package saga

import (
	"errors"
	"fmt"
	"strings"

	"go.starlark.net/syntax"
)

// ValidationResult contains both validation errors and lint warnings.
type ValidationResult struct {
	// Errors contains fatal validation errors that block execution.
	Errors []error

	// LintIssues contains semantic lint issues (warnings and errors).
	LintIssues []LintIssue
}

// HasErrors returns true if there are any fatal errors.
func (r *ValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// HasBlockingLintIssues returns true if any lint issues have ERROR severity.
func (r *ValidationResult) HasBlockingLintIssues() bool {
	for _, issue := range r.LintIssues {
		if issue.Severity == LintSeverityError {
			return true
		}
	}
	return false
}

// IsValid returns true if the script can be activated (no errors and no blocking lint issues).
func (r *ValidationResult) IsValid() bool {
	return !r.HasErrors() && !r.HasBlockingLintIssues()
}

// Summary returns a human-readable summary of all issues.
func (r *ValidationResult) Summary() string {
	totalIssues := len(r.Errors) + len(r.LintIssues)
	if totalIssues == 0 {
		return "No issues found"
	}

	parts := make([]string, 0, totalIssues)

	for _, err := range r.Errors {
		parts = append(parts, fmt.Sprintf("ERROR: %s", err.Error()))
	}

	for _, issue := range r.LintIssues {
		parts = append(parts, fmt.Sprintf("%s [line %d]: %s",
			issue.Severity, issue.LineNumber, issue.Message))
	}

	return strings.Join(parts, "\n")
}

// Validation errors.
var (
	// ErrBlockedFunction is returned when a script uses a blocked function.
	ErrBlockedFunction = errors.New("blocked function")

	// ErrExcessiveLoopNesting is returned when loop nesting exceeds MaxLoopNestingDepth.
	ErrExcessiveLoopNesting = errors.New("excessive loop nesting")
)

// blockedFunctions is the set of function names that are not allowed in saga scripts.
// These functions are security risks as they allow code execution, file access, or imports.
var blockedFunctions = map[string]bool{
	"load":       true,  // File/module loading
	"exec":       true,  // Code execution
	"compile":    true,  // Code compilation
	"open":       true,  // File system access
	"eval":       true,  // Expression evaluation
	"__import__": true,  // Module import
	"getattr":    false, // Allowed - useful for object access
	"setattr":    true,  // Blocked - mutation risk
	"delattr":    true,  // Blocked - mutation risk
}

// ValidateSagaScript performs static validation on a Starlark script.
// It checks for:
// - Script size (max 64KB)
// - Blocked function usage
// - Loop nesting depth (max 3)
// - Syntax errors
//
// This function should be called at script upload time before the script
// is stored in the registry.
func ValidateSagaScript(script string) error {
	// Check script size
	if len(script) > MaxScriptSize {
		return fmt.Errorf("%w: size %d exceeds maximum %d bytes", ErrScriptTooLarge, len(script), MaxScriptSize)
	}

	// Empty script is valid
	if len(script) == 0 {
		return nil
	}

	// Parse without executing
	fileOpts := &syntax.FileOptions{}
	file, err := fileOpts.Parse("script.star", script, 0)
	if err != nil {
		return errors.Join(ErrSyntax, err)
	}

	// Walk the AST to check for violations
	v := &validationVisitor{
		loopDepth: 0,
		maxDepth:  0,
	}

	if err := v.walkFile(file); err != nil {
		return err
	}

	// Check loop nesting
	if v.maxDepth > MaxLoopNestingDepth {
		return fmt.Errorf("%w: depth %d exceeds maximum %d", ErrExcessiveLoopNesting, v.maxDepth, MaxLoopNestingDepth)
	}

	return nil
}

// validationVisitor walks the AST to check for security violations.
type validationVisitor struct {
	loopDepth int
	maxDepth  int
}

// walkFile walks a parsed Starlark file.
func (v *validationVisitor) walkFile(file *syntax.File) error {
	for _, stmt := range file.Stmts {
		if err := v.walkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

// walkStmt walks a statement node.
func (v *validationVisitor) walkStmt(stmt syntax.Stmt) error {
	switch s := stmt.(type) {
	case *syntax.ExprStmt:
		return v.walkExpr(s.X)
	case *syntax.AssignStmt:
		return v.walkAssignStmt(s)
	case *syntax.DefStmt:
		return v.walkDefStmt(s)
	case *syntax.IfStmt:
		return v.walkIfStmt(s)
	case *syntax.ForStmt:
		return v.walkForStmt(s)
	case *syntax.WhileStmt:
		return v.walkWhileStmt(s)
	case *syntax.ReturnStmt:
		if s.Result != nil {
			return v.walkExpr(s.Result)
		}
	case *syntax.LoadStmt:
		return fmt.Errorf("%w: load at line %d", ErrBlockedFunction, s.Load.Line)
	case *syntax.BranchStmt:
		// break, continue, pass - all safe
	}
	return nil
}

func (v *validationVisitor) walkAssignStmt(s *syntax.AssignStmt) error {
	if err := v.walkExpr(s.LHS); err != nil {
		return err
	}
	return v.walkExpr(s.RHS)
}

func (v *validationVisitor) walkDefStmt(s *syntax.DefStmt) error {
	for _, stmt := range s.Body {
		if err := v.walkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (v *validationVisitor) walkIfStmt(s *syntax.IfStmt) error {
	if err := v.walkExpr(s.Cond); err != nil {
		return err
	}
	for _, stmt := range s.True {
		if err := v.walkStmt(stmt); err != nil {
			return err
		}
	}
	for _, stmt := range s.False {
		if err := v.walkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (v *validationVisitor) walkForStmt(s *syntax.ForStmt) error {
	v.loopDepth++
	if v.loopDepth > v.maxDepth {
		v.maxDepth = v.loopDepth
	}

	if err := v.walkExpr(s.X); err != nil {
		v.loopDepth--
		return err
	}
	for _, stmt := range s.Body {
		if err := v.walkStmt(stmt); err != nil {
			v.loopDepth--
			return err
		}
	}
	v.loopDepth--
	return nil
}

func (v *validationVisitor) walkWhileStmt(s *syntax.WhileStmt) error {
	v.loopDepth++
	if v.loopDepth > v.maxDepth {
		v.maxDepth = v.loopDepth
	}

	if err := v.walkExpr(s.Cond); err != nil {
		v.loopDepth--
		return err
	}
	for _, stmt := range s.Body {
		if err := v.walkStmt(stmt); err != nil {
			v.loopDepth--
			return err
		}
	}
	v.loopDepth--
	return nil
}

// walkExpr walks an expression node.
func (v *validationVisitor) walkExpr(expr syntax.Expr) error {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *syntax.CallExpr:
		return v.walkCallExpr(e)
	case *syntax.BinaryExpr:
		if err := v.walkExpr(e.X); err != nil {
			return err
		}
		return v.walkExpr(e.Y)
	case *syntax.UnaryExpr:
		return v.walkExpr(e.X)
	case *syntax.CondExpr:
		return v.walkCondExpr(e)
	case *syntax.IndexExpr:
		if err := v.walkExpr(e.X); err != nil {
			return err
		}
		return v.walkExpr(e.Y)
	case *syntax.SliceExpr:
		return v.walkSliceExpr(e)
	case *syntax.ListExpr:
		return v.walkExprList(e.List)
	case *syntax.DictExpr:
		return v.walkDictExpr(e)
	case *syntax.TupleExpr:
		return v.walkExprList(e.List)
	case *syntax.Comprehension:
		return v.walkComprehension(e)
	case *syntax.LambdaExpr:
		return v.walkExpr(e.Body)
	case *syntax.DotExpr:
		return v.walkExpr(e.X)
	case *syntax.ParenExpr:
		return v.walkExpr(e.X)
	case *syntax.Ident, *syntax.Literal:
		// Safe, no nested expressions
	}

	return nil
}

func (v *validationVisitor) walkCallExpr(e *syntax.CallExpr) error {
	if ident, ok := e.Fn.(*syntax.Ident); ok {
		if blockedFunctions[ident.Name] {
			return fmt.Errorf("%w: %s at line %d", ErrBlockedFunction, ident.Name, ident.NamePos.Line)
		}
	}
	if err := v.walkExpr(e.Fn); err != nil {
		return err
	}
	for _, arg := range e.Args {
		if err := v.walkExpr(arg); err != nil {
			return err
		}
	}
	return nil
}

func (v *validationVisitor) walkCondExpr(e *syntax.CondExpr) error {
	if err := v.walkExpr(e.Cond); err != nil {
		return err
	}
	if err := v.walkExpr(e.True); err != nil {
		return err
	}
	return v.walkExpr(e.False)
}

func (v *validationVisitor) walkSliceExpr(e *syntax.SliceExpr) error {
	if err := v.walkExpr(e.X); err != nil {
		return err
	}
	if err := v.walkExpr(e.Lo); err != nil {
		return err
	}
	if err := v.walkExpr(e.Hi); err != nil {
		return err
	}
	return v.walkExpr(e.Step)
}

func (v *validationVisitor) walkExprList(exprs []syntax.Expr) error {
	for _, elem := range exprs {
		if err := v.walkExpr(elem); err != nil {
			return err
		}
	}
	return nil
}

func (v *validationVisitor) walkDictExpr(e *syntax.DictExpr) error {
	for _, entry := range e.List {
		if dictEntry, ok := entry.(*syntax.DictEntry); ok {
			if err := v.walkExpr(dictEntry.Key); err != nil {
				return err
			}
			if err := v.walkExpr(dictEntry.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *validationVisitor) walkComprehension(e *syntax.Comprehension) error {
	savedDepth := v.loopDepth
	for _, clause := range e.Clauses {
		if forClause, ok := clause.(*syntax.ForClause); ok {
			v.loopDepth++
			if v.loopDepth > v.maxDepth {
				v.maxDepth = v.loopDepth
			}
			if err := v.walkExpr(forClause.X); err != nil {
				v.loopDepth = savedDepth
				return err
			}
		}
		if ifClause, ok := clause.(*syntax.IfClause); ok {
			if err := v.walkExpr(ifClause.Cond); err != nil {
				v.loopDepth = savedDepth
				return err
			}
		}
	}

	if err := v.walkExpr(e.Body); err != nil {
		v.loopDepth = savedDepth
		return err
	}
	v.loopDepth = savedDepth
	return nil
}

// ValidateDraft performs full validation including semantic linting for draft scripts.
// This is used during script development and returns warnings that may be addressed.
// If handlerMetadata is provided, it will be configured for pre-check validation.
func ValidateDraft(script string, handlerMetadata map[string]HandlerMetadata) (*ValidationResult, error) {
	linter := NewSemanticLinter()

	if len(handlerMetadata) > 0 {
		linter.SetHandlerMetadata(handlerMetadata)
	}

	return ValidateWithLinter(script, linter)
}

// ValidateActivation performs strict validation for scripts being activated.
// Returns an error if any blocking issues are found.
// If handlerMetadata is provided, it will be configured for pre-check validation.
func ValidateActivation(script string, handlerMetadata map[string]HandlerMetadata) error {
	linter := NewSemanticLinter()

	if len(handlerMetadata) > 0 {
		linter.SetHandlerMetadata(handlerMetadata)
	}

	// Enforce ERROR level for Decimal arithmetic and compensation coverage in activation
	linter.SetEnforcementLevel(LintIssueTypeDecimalArithmetic, EnforcementLevelError)
	linter.SetEnforcementLevel(LintIssueTypeMissingCompensationStrategy, EnforcementLevelError)

	result, err := ValidateWithLinter(script, linter)
	if err != nil {
		return err
	}

	if !result.IsValid() {
		return fmt.Errorf("%w: %s", ErrValidationFailed, result.Summary())
	}

	return nil
}

// ValidateWithLinter performs validation using a custom linter configuration.
func ValidateWithLinter(script string, linter *SemanticLinter) (*ValidationResult, error) {
	result := &ValidationResult{}

	// First, run basic validation
	if err := ValidateSagaScript(script); err != nil {
		result.Errors = append(result.Errors, err)
	}

	// If basic validation passed, run semantic linting
	if len(result.Errors) == 0 && linter != nil {
		lintIssues, err := linter.Analyze(script)
		if err != nil {
			// Lint errors are also added to the result
			result.Errors = append(result.Errors, fmt.Errorf("lint error: %w", err))
		}
		result.LintIssues = lintIssues
	}

	return result, nil
}
