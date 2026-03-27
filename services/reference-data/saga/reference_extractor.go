package saga

import (
	"strings"

	"go.starlark.net/syntax"
)

// referenceExtractor walks the Starlark AST to extract references.
type referenceExtractor struct {
	references []Reference
}

// walkStmt walks a statement node.
func (e *referenceExtractor) walkStmt(stmt syntax.Stmt) {
	switch s := stmt.(type) {
	case *syntax.ExprStmt:
		e.walkExpr(s.X)

	case *syntax.AssignStmt:
		e.walkExpr(s.LHS)
		e.walkExpr(s.RHS)

	case *syntax.DefStmt:
		for _, stmt := range s.Body {
			e.walkStmt(stmt)
		}

	case *syntax.IfStmt:
		e.walkExpr(s.Cond)
		for _, stmt := range s.True {
			e.walkStmt(stmt)
		}
		for _, stmt := range s.False {
			e.walkStmt(stmt)
		}

	case *syntax.ForStmt:
		e.walkExpr(s.X)
		for _, stmt := range s.Body {
			e.walkStmt(stmt)
		}

	case *syntax.WhileStmt:
		e.walkExpr(s.Cond)
		for _, stmt := range s.Body {
			e.walkStmt(stmt)
		}

	case *syntax.ReturnStmt:
		if s.Result != nil {
			e.walkExpr(s.Result)
		}
	}
}

// walkExpr walks an expression node and extracts references.
func (e *referenceExtractor) walkExpr(expr syntax.Expr) {
	if expr == nil {
		return
	}

	switch ex := expr.(type) {
	case *syntax.CallExpr:
		e.walkCallExpr(ex)
	case *syntax.IndexExpr:
		e.walkIndexExpr(ex)
	case *syntax.BinaryExpr:
		e.walkExpr(ex.X)
		e.walkExpr(ex.Y)
	case *syntax.UnaryExpr:
		e.walkExpr(ex.X)
	case *syntax.CondExpr:
		e.walkExpr(ex.Cond)
		e.walkExpr(ex.True)
		e.walkExpr(ex.False)
	case *syntax.SliceExpr:
		e.walkExpr(ex.X)
		e.walkExpr(ex.Lo)
		e.walkExpr(ex.Hi)
		e.walkExpr(ex.Step)
	case *syntax.ListExpr:
		e.walkExprList(ex.List)
	case *syntax.DictExpr:
		e.walkDictExpr(ex)
	case *syntax.TupleExpr:
		e.walkExprList(ex.List)
	case *syntax.Comprehension:
		e.walkComprehension(ex)
	case *syntax.LambdaExpr:
		e.walkExpr(ex.Body)
	case *syntax.DotExpr:
		e.walkExpr(ex.X)
	case *syntax.ParenExpr:
		e.walkExpr(ex.X)
	}
}

func (e *referenceExtractor) walkExprList(exprs []syntax.Expr) {
	for _, elem := range exprs {
		e.walkExpr(elem)
	}
}

func (e *referenceExtractor) walkDictExpr(ex *syntax.DictExpr) {
	for _, entry := range ex.List {
		if dictEntry, ok := entry.(*syntax.DictEntry); ok {
			e.walkExpr(dictEntry.Key)
			e.walkExpr(dictEntry.Value)
		}
	}
}

func (e *referenceExtractor) walkComprehension(ex *syntax.Comprehension) {
	for _, clause := range ex.Clauses {
		if forClause, ok := clause.(*syntax.ForClause); ok {
			e.walkExpr(forClause.X)
		}
		if ifClause, ok := clause.(*syntax.IfClause); ok {
			e.walkExpr(ifClause.Cond)
		}
	}
	e.walkExpr(ex.Body)
}

func (e *referenceExtractor) walkCallExpr(ex *syntax.CallExpr) {
	if ident, ok := ex.Fn.(*syntax.Ident); ok {
		switch ident.Name {
		case "resolve_instrument":
			e.extractResolveInstrument(ex, ident)
		case "resolve_account":
			e.extractResolveAccount(ex, ident)
		case "invoke_saga":
			e.extractInvokeSaga(ex, ident)
		case "step":
			e.extractStep(ex, ident)
		}
	}
	e.walkExpr(ex.Fn)
	for _, arg := range ex.Args {
		e.walkExpr(arg)
	}
}

func (e *referenceExtractor) extractResolveInstrument(ex *syntax.CallExpr, ident *syntax.Ident) {
	code := extractStringArg(ex, "reference")
	if code != "" {
		e.references = append(e.references, Reference{
			Type:       ReferenceTypeInstrument,
			Key:        code,
			LineNumber: int(ident.NamePos.Line),
		})
	}
}

func (e *referenceExtractor) extractResolveAccount(ex *syntax.CallExpr, ident *syntax.Ident) {
	code := extractStringArg(ex, "reference")
	if code != "" {
		e.references = append(e.references, Reference{
			Type:       ReferenceTypeAccount,
			Key:        code,
			LineNumber: int(ident.NamePos.Line),
		})
	}
}

// extractStringArg extracts a string value from either the first positional argument
// or a named keyword argument in a call expression.
func extractStringArg(call *syntax.CallExpr, kwargName string) string {
	// Check first positional argument
	if len(call.Args) > 0 {
		if lit, ok := call.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
			return strings.Trim(lit.Raw, `"'`)
		}
	}
	// Check keyword arguments
	for _, arg := range call.Args {
		if binExpr, ok := arg.(*syntax.BinaryExpr); ok && binExpr.Op == syntax.EQ {
			if nameIdent, ok := binExpr.X.(*syntax.Ident); ok && nameIdent.Name == kwargName {
				if lit, ok := binExpr.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
					return strings.Trim(lit.Raw, `"'`)
				}
			}
		}
	}
	return ""
}

func (e *referenceExtractor) extractInvokeSaga(ex *syntax.CallExpr, ident *syntax.Ident) {
	var sagaName string
	lineNum := int(ident.NamePos.Line)

	if len(ex.Args) > 0 {
		if lit, ok := ex.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
			sagaName = strings.Trim(lit.Raw, `"'`)
		}
	}

	if sagaName == "" {
		for _, kwarg := range ex.Args {
			if binExpr, ok := kwarg.(*syntax.BinaryExpr); ok && binExpr.Op == syntax.EQ {
				if nameIdent, ok := binExpr.X.(*syntax.Ident); ok && nameIdent.Name == "saga_name" {
					if lit, ok := binExpr.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
						sagaName = strings.Trim(lit.Raw, `"'`)
					}
				}
			}
		}
	}

	if sagaName != "" {
		e.references = append(e.references, Reference{
			Type:       ReferenceTypeSaga,
			Key:        sagaName,
			LineNumber: lineNum,
		})
	}
}

func (e *referenceExtractor) extractStep(ex *syntax.CallExpr, ident *syntax.Ident) {
	var handler string
	var lineNum int
	paramNames := make(map[string]bool)
	paramsKnown := true

	for _, kwarg := range ex.Args {
		if binExpr, ok := kwarg.(*syntax.BinaryExpr); ok && binExpr.Op == syntax.EQ {
			if nameIdent, ok := binExpr.X.(*syntax.Ident); ok {
				switch nameIdent.Name {
				case "action", "handler":
					if lit, ok := binExpr.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
						handler = strings.Trim(lit.Raw, `"'`)
						lineNum = int(ident.NamePos.Line)
					}
				case "params":
					paramsKnown = e.extractStepParams(binExpr.Y, paramNames)
				}
			}
		}
	}

	if handler != "" {
		e.references = append(e.references, Reference{
			Type:        ReferenceTypeStepHandler,
			Key:         handler,
			LineNumber:  lineNum,
			Params:      paramNames,
			ParamsKnown: paramsKnown,
		})
	}
}

func (e *referenceExtractor) extractStepParams(expr syntax.Expr, paramNames map[string]bool) bool {
	dictExpr, ok := expr.(*syntax.DictExpr)
	if !ok {
		return false
	}
	paramsKnown := true
	for _, entry := range dictExpr.List {
		if dictEntry, ok := entry.(*syntax.DictEntry); ok {
			keyLit, ok := dictEntry.Key.(*syntax.Literal)
			if !ok || keyLit.Token != syntax.STRING {
				paramsKnown = false
				continue
			}
			paramName := strings.Trim(keyLit.Raw, `"'`)
			paramNames[paramName] = true
		}
	}
	return paramsKnown
}

func (e *referenceExtractor) walkIndexExpr(ex *syntax.IndexExpr) {
	e.walkExpr(ex.X)
	e.walkExpr(ex.Y)

	if e.isAttributeAccess(ex) {
		if lit, ok := ex.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
			attrKey := strings.Trim(lit.Raw, `"'`)
			instrumentCode := e.extractInstrumentCode(ex.X)
			refKey := attrKey
			if instrumentCode != "" {
				refKey = instrumentCode + ":" + attrKey
			}
			e.references = append(e.references, Reference{
				Type:           ReferenceTypeAttribute,
				Key:            refKey,
				AttributeKey:   attrKey,
				InstrumentCode: instrumentCode,
				LineNumber:     int(lit.TokenPos.Line),
			})
		}
	}
}

// isAttributeAccess checks if an index expression is accessing .attributes[...]
func (e *referenceExtractor) isAttributeAccess(expr *syntax.IndexExpr) bool {
	if dotExpr, ok := expr.X.(*syntax.DotExpr); ok {
		return dotExpr.Name.Name == "attributes"
	}
	return false
}

// extractInstrumentCode tries to extract instrument code from attribute access context.
func (e *referenceExtractor) extractInstrumentCode(expr syntax.Expr) string {
	// Look for patterns like:
	// - instrument.attributes["key"] where instrument is a variable
	// - ctx.instrument.attributes["key"]
	// - resolve_instrument("CODE").attributes["key"]

	if dotExpr, ok := expr.(*syntax.DotExpr); ok {
		if dotExpr.Name.Name == "attributes" {
			// Check if the base is a call to resolve_instrument
			if callExpr, ok := dotExpr.X.(*syntax.CallExpr); ok {
				if ident, ok := callExpr.Fn.(*syntax.Ident); ok && ident.Name == "resolve_instrument" {
					if len(callExpr.Args) > 0 {
						if lit, ok := callExpr.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
							return strings.Trim(lit.Raw, `"'`)
						}
					}
				}
			}
		}
	}

	return ""
}
