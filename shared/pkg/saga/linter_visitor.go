// Package saga AST visitor that backs the semantic linter, walking Starlark
// syntax trees to detect lint issues.
package saga

import (
	"fmt"
	"strings"

	"go.starlark.net/syntax"
)

// lintVisitor walks the AST to detect lint issues.
type lintVisitor struct {
	linter           *SemanticLinter
	issues           []LintIssue
	ifDepth          int
	verifiedHandlers map[string]bool // handlers that have verify_external_state called
	decimalVars      map[string]bool // variables that hold Decimal values
	counterVars      map[string]bool // variables that are loop counters
	inLoopInit       bool            // true when walking loop initialization
}

// walkStmt walks a statement node.
func (v *lintVisitor) walkStmt(stmt syntax.Stmt) {
	switch s := stmt.(type) {
	case *syntax.ExprStmt:
		v.walkExpr(s.X)
	case *syntax.AssignStmt:
		v.walkAssignStmt(s)
	case *syntax.DefStmt:
		v.walkStmtList(s.Body)
	case *syntax.IfStmt:
		v.walkIfStmt(s)
	case *syntax.ForStmt:
		v.walkForStmt(s)
	case *syntax.WhileStmt:
		v.walkExpr(s.Cond)
		v.walkStmtList(s.Body)
	case *syntax.ReturnStmt:
		if s.Result != nil {
			v.walkExpr(s.Result)
		}
	}
}

func (v *lintVisitor) walkStmtList(stmts []syntax.Stmt) {
	for _, stmt := range stmts {
		v.walkStmt(stmt)
	}
}

func (v *lintVisitor) walkAssignStmt(s *syntax.AssignStmt) {
	if ident, ok := s.LHS.(*syntax.Ident); ok {
		if v.isDecimalExpr(s.RHS) {
			v.decimalVars[ident.Name] = true
		}
		if v.isCounterAssignment(ident.Name, s.RHS) {
			v.counterVars[ident.Name] = true
		}
	}
	v.walkExpr(s.LHS)
	v.walkExpr(s.RHS)
}

func (v *lintVisitor) walkIfStmt(s *syntax.IfStmt) {
	v.ifDepth++
	if v.ifDepth > 3 {
		v.addIssue(LintIssueTypeNestedConditional, int(s.If.Line),
			fmt.Sprintf("Nested conditionals exceed 3 levels (found %d)", v.ifDepth),
			"Refactor to flatten conditionals or extract to helper functions")
	}
	v.walkExpr(s.Cond)
	v.walkStmtList(s.True)
	v.walkStmtList(s.False)
	v.ifDepth--
}

func (v *lintVisitor) walkForStmt(s *syntax.ForStmt) {
	if ident, ok := s.Vars.(*syntax.Ident); ok {
		v.counterVars[ident.Name] = true
	}
	v.inLoopInit = true
	v.walkExpr(s.X)
	v.inLoopInit = false
	v.walkStmtList(s.Body)
}

// walkExpr walks an expression node.
func (v *lintVisitor) walkExpr(expr syntax.Expr) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *syntax.CallExpr:
		v.checkCallExpr(e)
		v.walkExpr(e.Fn)
		for _, arg := range e.Args {
			v.walkExpr(arg)
		}
	case *syntax.BinaryExpr:
		v.checkBinaryExpr(e)
		v.walkExpr(e.X)
		v.walkExpr(e.Y)
	case *syntax.UnaryExpr:
		v.walkExpr(e.X)
	case *syntax.CondExpr:
		v.walkExpr(e.Cond)
		v.walkExpr(e.True)
		v.walkExpr(e.False)
	case *syntax.IndexExpr:
		v.walkExpr(e.X)
		v.walkExpr(e.Y)
	case *syntax.SliceExpr:
		v.walkExpr(e.X)
		v.walkExpr(e.Lo)
		v.walkExpr(e.Hi)
		v.walkExpr(e.Step)
	case *syntax.ListExpr:
		v.walkExprList(e.List)
	case *syntax.DictExpr:
		v.walkDictExpr(e)
	case *syntax.TupleExpr:
		v.walkExprList(e.List)
	case *syntax.Comprehension:
		v.walkComprehension(e)
	case *syntax.LambdaExpr:
		v.walkExpr(e.Body)
	case *syntax.DotExpr:
		v.walkExpr(e.X)
	case *syntax.ParenExpr:
		v.walkExpr(e.X)
	case *syntax.Literal:
		v.checkLiteral(e)
	}
}

func (v *lintVisitor) walkExprList(exprs []syntax.Expr) {
	for _, elem := range exprs {
		v.walkExpr(elem)
	}
}

func (v *lintVisitor) walkDictExpr(e *syntax.DictExpr) {
	for _, entry := range e.List {
		if dictEntry, ok := entry.(*syntax.DictEntry); ok {
			v.walkExpr(dictEntry.Key)
			v.walkExpr(dictEntry.Value)
		}
	}
}

func (v *lintVisitor) walkComprehension(e *syntax.Comprehension) {
	for _, clause := range e.Clauses {
		if forClause, ok := clause.(*syntax.ForClause); ok {
			if ident, ok := forClause.Vars.(*syntax.Ident); ok {
				v.counterVars[ident.Name] = true
			}
			v.walkExpr(forClause.X)
		}
		if ifClause, ok := clause.(*syntax.IfClause); ok {
			v.walkExpr(ifClause.Cond)
		}
	}
	v.walkExpr(e.Body)
}

// checkCallExpr checks function calls for lint issues.
func (v *lintVisitor) checkCallExpr(e *syntax.CallExpr) {
	ident, ok := e.Fn.(*syntax.Ident)
	if !ok {
		return
	}

	switch ident.Name {
	case "verify_external_state":
		v.handleVerifyExternalState(e)
	case "step":
		v.handleStepCall(e)
	case "valuate":
		v.handleValuateCall(e)
	case "resolve_account", "resolve_instrument":
		v.handleResolveCall(e)
	}
}

// handleVerifyExternalState tracks verified handlers.
func (v *lintVisitor) handleVerifyExternalState(e *syntax.CallExpr) {
	if handlerName := v.extractHandlerArg(e); handlerName != "" {
		v.verifiedHandlers[handlerName] = true
	}
}

// handleStepCall checks handler calls for pre-check and compensation coverage.
func (v *lintVisitor) handleStepCall(e *syntax.CallExpr) {
	handlerName := v.extractHandlerArg(e)
	if handlerName == "" {
		return
	}

	meta, ok := v.linter.handlerMetadata[handlerName]
	if !ok {
		return
	}

	// Pre-check validation for external handlers
	if meta.IsExternal && meta.RequiresPreCheck && !v.verifiedHandlers[handlerName] {
		stepName := v.extractStepName(e)
		v.addIssue(LintIssueTypeMissingPreCheck, int(e.Lparen.Line),
			fmt.Sprintf("External handler %q requires Pre-Step Check", handlerName),
			fmt.Sprintf("Add verify_external_state before step '%s'", stepName))
	}

	// Compensation coverage validation
	if !meta.HasAutoCompensation && meta.CompensationStrategy == "" {
		v.addIssue(LintIssueTypeMissingCompensationStrategy, int(e.Lparen.Line),
			fmt.Sprintf("Handler %q has no compensation strategy declared", handlerName),
			fmt.Sprintf("Add compensation_strategy to handler metadata for %s", handlerName))
	}
}

// handleValuateCall checks for hardcoded instrument codes.
func (v *lintVisitor) handleValuateCall(e *syntax.CallExpr) {
	if hardcoded := v.extractHardcodedArg(e, "instrument"); hardcoded != "" {
		v.addIssue(LintIssueTypeHardcodedCode, int(e.Lparen.Line),
			fmt.Sprintf("Hardcoded instrument code %q detected", hardcoded),
			"Use parameters or resolve_instrument(reference=...) instead")
	}
}

// handleResolveCall checks for hardcoded reference strings.
func (v *lintVisitor) handleResolveCall(e *syntax.CallExpr) {
	if hardcoded := v.extractHardcodedArg(e, "reference"); hardcoded != "" {
		v.addIssue(LintIssueTypeHardcodedCode, int(e.Lparen.Line),
			fmt.Sprintf("Hardcoded reference %q detected", hardcoded),
			"Use parameters from saga input instead of hardcoded references")
	}
}

// checkBinaryExpr checks binary operations for Decimal arithmetic.
//
//nolint:exhaustive,nolintlint // We only care about arithmetic operators
func (v *lintVisitor) checkBinaryExpr(e *syntax.BinaryExpr) {
	// Only check arithmetic operators
	switch e.Op {
	case syntax.PLUS, syntax.MINUS, syntax.STAR, syntax.SLASH:
		// OK
	default:
		return
	}

	// Check if either operand involves Decimal
	leftIsDecimal := v.isDecimalExpr(e.X)
	rightIsDecimal := v.isDecimalExpr(e.Y)

	if !leftIsDecimal && !rightIsDecimal {
		return
	}

	// Exempt counter arithmetic (i + 1, count - 1)
	if v.isCounterArithmetic(e) {
		return
	}

	// Exempt index expressions (items[i + offset])
	// This is handled by not flagging when we're in an index expression context
	// The parent IndexExpr will contain this, so we check if operands are counters
	if v.isIndexArithmetic(e) {
		return
	}

	v.addIssue(LintIssueTypeDecimalArithmetic, int(e.OpPos.Line),
		"Financial math detected. Move this to a CEL Valuation Strategy in Reference Data.",
		"Extract to CEL expression: cel_eval('qty * rate', {...})")
}

// checkLiteral checks literals for magic numbers.
func (v *lintVisitor) checkLiteral(e *syntax.Literal) {
	// Only check float literals (magic decimal numbers)
	if e.Token != syntax.FLOAT {
		return
	}

	// Skip if we're in loop initialization (range bounds)
	if v.inLoopInit {
		return
	}

	v.addIssue(LintIssueTypeMagicNumber, int(e.TokenPos.Line),
		"Magic number detected. Use a named constant or configuration.",
		"Define as a constant: RATE = Decimal(\"...\") or use config lookup")
}

// isDecimalExpr returns true if the expression produces a Decimal value.
func (v *lintVisitor) isDecimalExpr(expr syntax.Expr) bool {
	switch e := expr.(type) {
	case *syntax.CallExpr:
		// Decimal("...") constructor
		if ident, ok := e.Fn.(*syntax.Ident); ok {
			if ident.Name == "Decimal" {
				return true
			}
			// valuate() returns validated rates - not subject to linting
			if ident.Name == "valuate" {
				return false
			}
		}
		return false

	case *syntax.Ident:
		// Check if variable is known to hold a Decimal
		return v.decimalVars[e.Name]

	case *syntax.BinaryExpr:
		// Arithmetic on Decimals produces Decimal
		return v.isDecimalExpr(e.X) || v.isDecimalExpr(e.Y)

	case *syntax.ParenExpr:
		return v.isDecimalExpr(e.X)

	default:
		return false
	}
}

// isCounterAssignment checks if this is a counter variable assignment.
func (v *lintVisitor) isCounterAssignment(varName string, rhs syntax.Expr) bool {
	// i = 0 pattern
	if lit, ok := rhs.(*syntax.Literal); ok {
		if lit.Token == syntax.INT {
			val := lit.Value
			if val == 0 || val == 1 {
				return true
			}
		}
	}

	// i = i + 1 pattern
	if bin, ok := rhs.(*syntax.BinaryExpr); ok {
		if bin.Op == syntax.PLUS || bin.Op == syntax.MINUS {
			if ident, ok := bin.X.(*syntax.Ident); ok && ident.Name == varName {
				if lit, ok := bin.Y.(*syntax.Literal); ok && lit.Token == syntax.INT {
					return true
				}
			}
		}
	}

	return false
}

// isCounterArithmetic checks if the binary expression is counter arithmetic.
func (v *lintVisitor) isCounterArithmetic(e *syntax.BinaryExpr) bool {
	// Only + or - can be counter arithmetic
	if e.Op != syntax.PLUS && e.Op != syntax.MINUS {
		return false
	}

	// Check if left operand is a counter variable
	if ident, ok := e.X.(*syntax.Ident); ok {
		if v.counterVars[ident.Name] {
			// Check if right operand is a small integer
			if lit, ok := e.Y.(*syntax.Literal); ok && lit.Token == syntax.INT {
				return true
			}
		}
	}

	// Check reverse: 1 + i
	if ident, ok := e.Y.(*syntax.Ident); ok {
		if v.counterVars[ident.Name] {
			if lit, ok := e.X.(*syntax.Literal); ok && lit.Token == syntax.INT {
				return true
			}
		}
	}

	return false
}

// isIndexArithmetic checks if this is index arithmetic (i + offset).
func (v *lintVisitor) isIndexArithmetic(e *syntax.BinaryExpr) bool {
	// Both operands should be counter variables or integers
	leftOK := v.isCounterOrInt(e.X)
	rightOK := v.isCounterOrInt(e.Y)

	return leftOK && rightOK
}

// isCounterOrInt returns true if expr is a counter variable or integer.
func (v *lintVisitor) isCounterOrInt(expr syntax.Expr) bool {
	if ident, ok := expr.(*syntax.Ident); ok {
		return v.counterVars[ident.Name]
	}
	if lit, ok := expr.(*syntax.Literal); ok {
		return lit.Token == syntax.INT
	}
	return false
}

// extractNamedStringArg extracts a named string argument from a function call.
// Returns the string value if found, empty string otherwise.
func (v *lintVisitor) extractNamedStringArg(e *syntax.CallExpr, argName string) string {
	for _, arg := range e.Args {
		binExpr, ok := arg.(*syntax.BinaryExpr)
		if !ok || binExpr.Op != syntax.EQ {
			continue
		}

		ident, ok := binExpr.X.(*syntax.Ident)
		if !ok || ident.Name != argName {
			continue
		}

		lit, ok := binExpr.Y.(*syntax.Literal)
		if !ok || lit.Token != syntax.STRING {
			continue
		}

		if str, ok := lit.Value.(string); ok {
			return strings.Trim(str, "\"")
		}
	}
	return ""
}

// extractHandlerArg extracts the "handler" argument from a function call.
func (v *lintVisitor) extractHandlerArg(e *syntax.CallExpr) string {
	return v.extractNamedStringArg(e, "handler")
}

// extractStepName extracts the "name" argument from a step() call.
func (v *lintVisitor) extractStepName(e *syntax.CallExpr) string {
	return v.extractNamedStringArg(e, "name")
}

// extractHardcodedArg checks if a named argument is a hardcoded string literal.
// Returns the hardcoded value if found, empty string otherwise.
func (v *lintVisitor) extractHardcodedArg(e *syntax.CallExpr, argName string) string {
	return v.extractNamedStringArg(e, argName)
}

// addIssue creates a lint issue with the configured severity.
func (v *lintVisitor) addIssue(issueType LintIssueType, line int, message, suggestedFix string) {
	level := v.linter.enforcementLevels[issueType]
	severity := LintSeverityWarning
	if level == EnforcementLevelError {
		severity = LintSeverityError
	}

	v.issues = append(v.issues, LintIssue{
		Type:         issueType,
		Severity:     severity,
		LineNumber:   line,
		Message:      message,
		SuggestedFix: suggestedFix,
	})
}
