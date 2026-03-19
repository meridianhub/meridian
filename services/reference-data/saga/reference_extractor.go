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
		// Check for specific function calls
		if ident, ok := ex.Fn.(*syntax.Ident); ok {
			switch ident.Name {
			case "resolve_instrument":
				// Extract instrument code from first argument
				if len(ex.Args) > 0 {
					if lit, ok := ex.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
						// Remove quotes from string literal
						code := strings.Trim(lit.Raw, `"'`)
						e.references = append(e.references, Reference{
							Type:       ReferenceTypeInstrument,
							Key:        code,
							LineNumber: int(ident.NamePos.Line),
						})
					}
				}

			case "resolve_account":
				// Extract account reference from first argument
				if len(ex.Args) > 0 {
					if lit, ok := ex.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
						code := strings.Trim(lit.Raw, `"'`)
						e.references = append(e.references, Reference{
							Type:       ReferenceTypeAccount,
							Key:        code,
							LineNumber: int(ident.NamePos.Line),
						})
					}
				}

			case "invoke_saga":
				// Extract saga name from first argument (positional or keyword "saga_name")
				var sagaName string
				lineNum := int(ident.NamePos.Line)

				// Check positional arguments first
				if len(ex.Args) > 0 {
					if lit, ok := ex.Args[0].(*syntax.Literal); ok && lit.Token == syntax.STRING {
						sagaName = strings.Trim(lit.Raw, `"'`)
					}
				}

				// Fall back to keyword argument saga_name=
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

			case "step":
				// Extract step handler and params from keyword arguments
				var handler string
				var lineNum int
				paramNames := make(map[string]bool)
				paramsKnown := true // Assume known unless we encounter non-literal params

				for _, kwarg := range ex.Args {
					if binExpr, ok := kwarg.(*syntax.BinaryExpr); ok && binExpr.Op == syntax.EQ {
						if nameIdent, ok := binExpr.X.(*syntax.Ident); ok {
							switch nameIdent.Name {
							case "action":
								if lit, ok := binExpr.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
									handler = strings.Trim(lit.Raw, `"'`)
									lineNum = int(ident.NamePos.Line)
								}
							case "params":
								// Extract param names from the params dict
								if dictExpr, ok := binExpr.Y.(*syntax.DictExpr); ok {
									for _, entry := range dictExpr.List {
										if dictEntry, ok := entry.(*syntax.DictEntry); ok {
											keyLit, ok := dictEntry.Key.(*syntax.Literal)
											if !ok || keyLit.Token != syntax.STRING {
												// Non-literal key (e.g., variable) - can't extract
												paramsKnown = false
												continue
											}
											paramName := strings.Trim(keyLit.Raw, `"'`)
											paramNames[paramName] = true
										}
									}
								} else {
									// params is not a literal dict (e.g., a variable)
									paramsKnown = false
								}
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
		}

		// Walk function expression and arguments
		e.walkExpr(ex.Fn)
		for _, arg := range ex.Args {
			e.walkExpr(arg)
		}

	case *syntax.IndexExpr:
		// Check for attribute access pattern: ctx.position.attributes["key"]
		// or instrument.attributes["key"]
		e.walkExpr(ex.X)
		e.walkExpr(ex.Y)

		// Try to extract attribute reference
		if e.isAttributeAccess(ex) {
			if lit, ok := ex.Y.(*syntax.Literal); ok && lit.Token == syntax.STRING {
				attrKey := strings.Trim(lit.Raw, `"'`)
				// Try to extract instrument code from the expression
				instrumentCode := e.extractInstrumentCode(ex.X)
				// Compose a collision-safe key: include instrument code when known
				// so that USD.attributes["status"] and EUR.attributes["status"]
				// produce distinct reference keys.
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
		for _, elem := range ex.List {
			e.walkExpr(elem)
		}

	case *syntax.DictExpr:
		for _, entry := range ex.List {
			if dictEntry, ok := entry.(*syntax.DictEntry); ok {
				e.walkExpr(dictEntry.Key)
				e.walkExpr(dictEntry.Value)
			}
		}

	case *syntax.TupleExpr:
		for _, elem := range ex.List {
			e.walkExpr(elem)
		}

	case *syntax.Comprehension:
		for _, clause := range ex.Clauses {
			if forClause, ok := clause.(*syntax.ForClause); ok {
				e.walkExpr(forClause.X)
			}
			if ifClause, ok := clause.(*syntax.IfClause); ok {
				e.walkExpr(ifClause.Cond)
			}
		}
		e.walkExpr(ex.Body)

	case *syntax.LambdaExpr:
		e.walkExpr(ex.Body)

	case *syntax.DotExpr:
		e.walkExpr(ex.X)

	case *syntax.ParenExpr:
		e.walkExpr(ex.X)
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
