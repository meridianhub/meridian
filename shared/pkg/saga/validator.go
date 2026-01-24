package saga

import (
	"errors"
	"fmt"

	"go.starlark.net/syntax"
)

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
//
//nolint:gocognit,gocyclo // AST walking requires handling many statement types; complexity is inherent
func (v *validationVisitor) walkStmt(stmt syntax.Stmt) error {
	switch s := stmt.(type) {
	case *syntax.ExprStmt:
		return v.walkExpr(s.X)

	case *syntax.AssignStmt:
		if err := v.walkExpr(s.LHS); err != nil {
			return err
		}
		return v.walkExpr(s.RHS)

	case *syntax.DefStmt:
		for _, stmt := range s.Body {
			if err := v.walkStmt(stmt); err != nil {
				return err
			}
		}

	case *syntax.IfStmt:
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

	case *syntax.ForStmt:
		// Track loop nesting
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

	case *syntax.WhileStmt:
		// Track loop nesting (though Starlark doesn't support while by default)
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

	case *syntax.ReturnStmt:
		if s.Result != nil {
			return v.walkExpr(s.Result)
		}

	case *syntax.LoadStmt:
		// load() is blocked
		return fmt.Errorf("%w: load at line %d", ErrBlockedFunction, s.Load.Line)

	case *syntax.BranchStmt:
		// break, continue, pass - all safe

	}

	return nil
}

// walkExpr walks an expression node.
//
//nolint:gocognit,gocyclo // AST walking requires handling many expression types; complexity is inherent
func (v *validationVisitor) walkExpr(expr syntax.Expr) error {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *syntax.CallExpr:
		// Check for blocked function calls
		if ident, ok := e.Fn.(*syntax.Ident); ok {
			if blockedFunctions[ident.Name] {
				return fmt.Errorf("%w: %s at line %d", ErrBlockedFunction, ident.Name, ident.NamePos.Line)
			}
		}

		// Walk function and arguments
		if err := v.walkExpr(e.Fn); err != nil {
			return err
		}
		for _, arg := range e.Args {
			if err := v.walkExpr(arg); err != nil {
				return err
			}
		}

	case *syntax.BinaryExpr:
		if err := v.walkExpr(e.X); err != nil {
			return err
		}
		return v.walkExpr(e.Y)

	case *syntax.UnaryExpr:
		return v.walkExpr(e.X)

	case *syntax.CondExpr:
		if err := v.walkExpr(e.Cond); err != nil {
			return err
		}
		if err := v.walkExpr(e.True); err != nil {
			return err
		}
		return v.walkExpr(e.False)

	case *syntax.IndexExpr:
		if err := v.walkExpr(e.X); err != nil {
			return err
		}
		return v.walkExpr(e.Y)

	case *syntax.SliceExpr:
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

	case *syntax.ListExpr:
		for _, elem := range e.List {
			if err := v.walkExpr(elem); err != nil {
				return err
			}
		}

	case *syntax.DictExpr:
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

	case *syntax.TupleExpr:
		for _, elem := range e.List {
			if err := v.walkExpr(elem); err != nil {
				return err
			}
		}

	case *syntax.Comprehension:
		// List/Dict comprehension - walk clauses
		if err := v.walkExpr(e.Body); err != nil {
			return err
		}
		for _, clause := range e.Clauses {
			if forClause, ok := clause.(*syntax.ForClause); ok {
				v.loopDepth++
				if v.loopDepth > v.maxDepth {
					v.maxDepth = v.loopDepth
				}
				if err := v.walkExpr(forClause.X); err != nil {
					v.loopDepth--
					return err
				}
				v.loopDepth--
			}
			if ifClause, ok := clause.(*syntax.IfClause); ok {
				if err := v.walkExpr(ifClause.Cond); err != nil {
					return err
				}
			}
		}

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
