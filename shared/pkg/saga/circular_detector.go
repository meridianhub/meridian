// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"fmt"
	"strings"

	"go.starlark.net/syntax"
)

// Note: The fmt import is used for error formatting in AnalyzeDraft and CheckRuntimeCircular.

// CircularDetector provides circular reference detection at DRAFT, ACTIVATION, and RUNTIME phases.
type CircularDetector struct {
	// sagaGraph maps saga names to their invoke_saga targets.
	sagaGraph map[string][]string
}

// NewCircularDetector creates a new circular detector.
func NewCircularDetector() *CircularDetector {
	return &CircularDetector{
		sagaGraph: make(map[string][]string),
	}
}

// SetSagaGraph sets the complete saga dependency graph for activation-time analysis.
func (d *CircularDetector) SetSagaGraph(graph map[string][]string) {
	d.sagaGraph = graph
}

// AnalyzeDraft performs static AST analysis to detect direct self-references.
// This is Phase 1 (DRAFT) detection - runs when saving a saga definition.
func (d *CircularDetector) AnalyzeDraft(sagaName string, script string) ([][]string, error) {
	if script == "" {
		return nil, nil
	}

	// Parse to check for syntax errors
	fileOpts := &syntax.FileOptions{}
	_, err := fileOpts.Parse("script.star", script, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse script: %w", err)
	}

	refs := d.ExtractInvokeSagaCalls(script)

	var cycles [][]string
	for _, ref := range refs {
		if ref == sagaName {
			// Direct self-reference detected
			cycles = append(cycles, []string{sagaName, sagaName})
		}
	}

	return cycles, nil
}

// ExtractInvokeSagaCalls parses a Starlark script and extracts all invoke_saga call targets.
func (d *CircularDetector) ExtractInvokeSagaCalls(script string) []string {
	if script == "" {
		return nil
	}

	fileOpts := &syntax.FileOptions{}
	file, err := fileOpts.Parse("script.star", script, 0)
	if err != nil {
		return nil
	}

	var refs []string
	for _, stmt := range file.Stmts {
		refs = append(refs, d.extractFromStmt(stmt)...)
	}

	return refs
}

// extractFromStmt extracts invoke_saga calls from a statement.
func (d *CircularDetector) extractFromStmt(stmt syntax.Stmt) []string {
	var refs []string

	switch s := stmt.(type) {
	case *syntax.ExprStmt:
		refs = append(refs, d.extractFromExpr(s.X)...)
	case *syntax.AssignStmt:
		refs = append(refs, d.extractFromExpr(s.RHS)...)
	case *syntax.DefStmt:
		for _, bodyStmt := range s.Body {
			refs = append(refs, d.extractFromStmt(bodyStmt)...)
		}
	case *syntax.IfStmt:
		refs = append(refs, d.extractFromExpr(s.Cond)...)
		for _, bodyStmt := range s.True {
			refs = append(refs, d.extractFromStmt(bodyStmt)...)
		}
		for _, bodyStmt := range s.False {
			refs = append(refs, d.extractFromStmt(bodyStmt)...)
		}
	case *syntax.ForStmt:
		refs = append(refs, d.extractFromExpr(s.X)...)
		for _, bodyStmt := range s.Body {
			refs = append(refs, d.extractFromStmt(bodyStmt)...)
		}
	case *syntax.ReturnStmt:
		if s.Result != nil {
			refs = append(refs, d.extractFromExpr(s.Result)...)
		}
	}

	return refs
}

// extractFromExpr extracts invoke_saga calls from an expression.
func (d *CircularDetector) extractFromExpr(expr syntax.Expr) []string {
	if expr == nil {
		return nil
	}

	var refs []string

	switch e := expr.(type) {
	case *syntax.CallExpr:
		if ident, ok := e.Fn.(*syntax.Ident); ok && ident.Name == "invoke_saga" {
			if sagaName := d.extractSagaNameArg(e); sagaName != "" {
				refs = append(refs, sagaName)
			}
		}
		refs = append(refs, d.extractFromExpr(e.Fn)...)
		refs = d.extractFromExprList(refs, e.Args)
	case *syntax.BinaryExpr:
		refs = append(refs, d.extractFromExpr(e.X)...)
		refs = append(refs, d.extractFromExpr(e.Y)...)
	case *syntax.UnaryExpr:
		refs = append(refs, d.extractFromExpr(e.X)...)
	case *syntax.ListExpr:
		refs = d.extractFromExprList(refs, e.List)
	case *syntax.DictExpr:
		refs = d.extractFromDictExpr(refs, e)
	case *syntax.TupleExpr:
		refs = d.extractFromExprList(refs, e.List)
	case *syntax.ParenExpr:
		refs = append(refs, d.extractFromExpr(e.X)...)
	case *syntax.CondExpr:
		refs = append(refs, d.extractFromExpr(e.Cond)...)
		refs = append(refs, d.extractFromExpr(e.True)...)
		refs = append(refs, d.extractFromExpr(e.False)...)
	case *syntax.IndexExpr:
		refs = append(refs, d.extractFromExpr(e.X)...)
		refs = append(refs, d.extractFromExpr(e.Y)...)
	case *syntax.SliceExpr:
		refs = d.extractFromSliceExpr(refs, e)
	case *syntax.DotExpr:
		refs = append(refs, d.extractFromExpr(e.X)...)
	case *syntax.Comprehension:
		refs = d.extractFromComprehension(refs, e)
	case *syntax.LambdaExpr:
		refs = append(refs, d.extractFromExpr(e.Body)...)
	}

	return refs
}

func (d *CircularDetector) extractFromExprList(refs []string, exprs []syntax.Expr) []string {
	for _, elem := range exprs {
		refs = append(refs, d.extractFromExpr(elem)...)
	}
	return refs
}

func (d *CircularDetector) extractFromDictExpr(refs []string, e *syntax.DictExpr) []string {
	for _, entry := range e.List {
		if dictEntry, ok := entry.(*syntax.DictEntry); ok {
			refs = append(refs, d.extractFromExpr(dictEntry.Key)...)
			refs = append(refs, d.extractFromExpr(dictEntry.Value)...)
		}
	}
	return refs
}

func (d *CircularDetector) extractFromSliceExpr(refs []string, e *syntax.SliceExpr) []string {
	refs = append(refs, d.extractFromExpr(e.X)...)
	refs = append(refs, d.extractFromExpr(e.Lo)...)
	refs = append(refs, d.extractFromExpr(e.Hi)...)
	refs = append(refs, d.extractFromExpr(e.Step)...)
	return refs
}

func (d *CircularDetector) extractFromComprehension(refs []string, e *syntax.Comprehension) []string {
	refs = append(refs, d.extractFromExpr(e.Body)...)
	for _, clause := range e.Clauses {
		if forClause, ok := clause.(*syntax.ForClause); ok {
			refs = append(refs, d.extractFromExpr(forClause.X)...)
		}
		if ifClause, ok := clause.(*syntax.IfClause); ok {
			refs = append(refs, d.extractFromExpr(ifClause.Cond)...)
		}
	}
	return refs
}

// extractSagaNameArg extracts the saga_name argument from an invoke_saga call.
func (d *CircularDetector) extractSagaNameArg(call *syntax.CallExpr) string {
	// Check positional first argument
	if len(call.Args) > 0 {
		if lit, ok := call.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
			if str, ok := lit.Value.(string); ok {
				return str
			}
		}
	}

	// Check keyword arguments
	for _, arg := range call.Args {
		if binExpr, ok := arg.(*syntax.BinaryExpr); ok && binExpr.Op == syntax.EQ {
			if ident, ok := binExpr.X.(*syntax.Ident); ok && ident.Name == "saga_name" {
				if lit, ok := binExpr.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
					if str, ok := lit.Value.(string); ok {
						return str
					}
				}
			}
		}
	}

	return ""
}

// FindCyclesAtActivation performs graph traversal to find cycles.
// This is Phase 2 (ACTIVATION) detection - runs when activating a saga.
func (d *CircularDetector) FindCyclesAtActivation(startSaga string) [][]string {
	visited := make(map[string]bool)
	path := make(map[string]bool)
	var cycles [][]string

	var dfs func(saga string, currentPath []string)
	dfs = func(saga string, currentPath []string) {
		if path[saga] {
			// Found a cycle - extract the cycle portion
			cycleStart := -1
			for i, s := range currentPath {
				if s == saga {
					cycleStart = i
					break
				}
			}
			if cycleStart >= 0 {
				cycle := append([]string{}, currentPath[cycleStart:]...)
				cycle = append(cycle, saga)
				cycles = append(cycles, cycle)
			}
			return
		}

		if visited[saga] {
			return
		}

		visited[saga] = true
		path[saga] = true
		currentPath = append(currentPath, saga)

		for _, dep := range d.sagaGraph[saga] {
			dfs(dep, currentPath)
		}

		path[saga] = false
	}

	dfs(startSaga, nil)
	return cycles
}

// CheckRuntimeCircular performs call stack based circular detection.
// This is Phase 3 (RUNTIME) detection - defense in depth during execution.
func (d *CircularDetector) CheckRuntimeCircular(sagaName string, stack *CallStack) error {
	if stack == nil {
		return nil
	}

	if stack.Contains(sagaName) {
		names := stack.GetSagaNames()
		return fmt.Errorf("%w: call chain %s -> %s",
			ErrCircularSagaReference,
			strings.Join(names, " -> "),
			sagaName,
		)
	}

	return nil
}

// FormatCycle formats a cycle path for human-readable output.
func (d *CircularDetector) FormatCycle(cycle []string) string {
	return strings.Join(cycle, " -> ")
}
