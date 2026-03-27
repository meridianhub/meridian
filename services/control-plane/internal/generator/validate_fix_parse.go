package generator

import (
	"sort"
	"strings"
)

// findHandlerCall finds the index of oldName followed by optional whitespace and '(' in s.
// Returns -1 if not found. Only matches at word boundaries outside string literals and comments.
func findHandlerCall(s, oldName string) int {
	for i := 0; i < len(s); {
		// Skip string literals and line comments to avoid false matches.
		if next, skip := advancePastToken(s, i); skip {
			i = next
			continue
		}

		// Check for oldName at position i (outside string/comment).
		if isHandlerCallAt(s, i, oldName) {
			return i
		}

		i++
	}
	return -1
}

// advancePastToken advances past a string literal or line comment starting at i.
// Returns (next position, true) when a token was skipped, or (i, false) otherwise.
func advancePastToken(s string, i int) (int, bool) {
	if s[i] == '"' || s[i] == '\'' {
		return advancePastString(s, i), true
	}
	if s[i] == '#' {
		j := i
		for j < len(s) && s[j] != '\n' {
			j++
		}
		return j, true
	}
	return i, false
}

// isHandlerCallAt returns true when oldName appears at position i in s followed by
// optional whitespace and '(', at a word boundary (not preceded by an ident char).
func isHandlerCallAt(s string, i int, oldName string) bool {
	if !strings.HasPrefix(s[i:], oldName) {
		return false
	}
	if i > 0 && isScriptIdentChar(s[i-1]) {
		return false
	}
	rest := s[i+len(oldName):]
	wsEnd := 0
	for wsEnd < len(rest) && (rest[wsEnd] == ' ' || rest[wsEnd] == '\t') {
		wsEnd++
	}
	return wsEnd < len(rest) && rest[wsEnd] == '('
}

func isScriptIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

// splitAtMatchingParen splits s at the matching closing paren for an already-opened call.
// Returns (bodyIncludingCloseParen, rest).
func splitAtMatchingParen(s string) (string, string) {
	depth := 1
	i := 0
	for i < len(s) {
		switch s[i] {
		case '(':
			depth++
			i++
		case ')':
			depth--
			if depth == 0 {
				return s[:i+1], s[i+1:]
			}
			i++
		case '"', '\'':
			i = advancePastString(s, i)
		case '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		default:
			i++
		}
	}
	return s, ""
}

// advancePastString advances past a string literal starting at i.
func advancePastString(s string, i int) int {
	q := s[i]
	if i+2 < len(s) && s[i+1] == q && s[i+2] == q {
		// Triple-quoted
		triple := string([]byte{q, q, q})
		end := strings.Index(s[i+3:], triple)
		if end >= 0 {
			return i + 3 + end + 3
		}
		return len(s)
	}
	i++
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == q {
			return i + 1
		}
		i++
	}
	return len(s)
}

// renameKwargs renames keyword arguments in a call body string.
// Only renames top-level (depth=0) identifier=value patterns.
func renameKwargs(callBody string, reverseMapping map[string]string) string {
	var result strings.Builder
	i := 0
	for i < len(callBody) {
		i = renameKwargsStep(&result, callBody, i, reverseMapping)
	}
	return result.String()
}

// renameKwargsStep processes one token at position i and returns the next position.
func renameKwargsStep(result *strings.Builder, s string, i int, reverseMapping map[string]string) int {
	ch := s[i]
	if ch == '"' || ch == '\'' {
		start := i
		i = advancePastString(s, i)
		result.WriteString(s[start:i])
		return i
	}
	if ch == '#' {
		start := i
		for i < len(s) && s[i] != '\n' {
			i++
		}
		result.WriteString(s[start:i])
		return i
	}
	if isIdentStart(ch) {
		return renameKwargIdent(result, s, i, reverseMapping)
	}
	result.WriteByte(ch)
	return i + 1
}

// renameKwargIdent handles an identifier at position i, renaming it if it is a kwarg.
func renameKwargIdent(result *strings.Builder, s string, i int, reverseMapping map[string]string) int {
	start := i
	for i < len(s) && isScriptIdentChar(s[i]) {
		i++
	}
	ident := s[start:i]

	// Check for "ident=" (not "ident==")
	eqIdx := i
	for eqIdx < len(s) && (s[eqIdx] == ' ' || s[eqIdx] == '\t') {
		eqIdx++
	}
	isKwarg := eqIdx < len(s) && s[eqIdx] == '=' && (eqIdx+1 >= len(s) || s[eqIdx+1] != '=')
	if isKwarg {
		if newName, ok := reverseMapping[ident]; ok {
			result.WriteString(newName)
			return i
		}
	}
	result.WriteString(ident)
	return i
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// injectMissingDefaults appends keyword arguments from defaults that are not already
// present at the top level of callBody (the content inside parentheses, including the
// closing ')').  Only top-level kwargs are checked; nested calls are ignored.
//
// callBody is expected to end with ')'.  When defaults must be injected the result
// is something like "existing_arg=v, new_param="default_value")".
func injectMissingDefaults(callBody string, defaults map[string]string) string {
	// Collect kwarg names already present at the top level (depth == 0).
	present := collectTopLevelKwargNames(callBody)

	// Determine which defaults are missing and need to be injected.
	type kv struct{ k, v string }
	var missing []kv
	for param, val := range defaults {
		if !present[param] {
			missing = append(missing, kv{param, val})
		}
	}
	if len(missing) == 0 {
		return callBody
	}

	// Sort for deterministic output.
	sort.Slice(missing, func(i, j int) bool { return missing[i].k < missing[j].k })

	// Strip the trailing ')' and inject the missing args.
	// callBody ends with ')'; content before it is the existing args.
	closeParen := strings.LastIndex(callBody, ")")
	if closeParen < 0 {
		return callBody
	}
	before := strings.TrimRight(callBody[:closeParen], " \t\n")
	var sb strings.Builder
	sb.WriteString(before)
	for _, entry := range missing {
		if len(before) > 0 && !strings.HasSuffix(before, "(") {
			sb.WriteString(", ")
		}
		sb.WriteString(entry.k)
		sb.WriteString("=")
		sb.WriteString(entry.v)
		before = sb.String() // update before for subsequent iterations
	}
	sb.WriteString(callBody[closeParen:]) // append rest from closing paren onwards
	return sb.String()
}

// collectTopLevelKwargNames scans callBody and returns the set of keyword argument
// names found at depth 0 (i.e. not inside nested calls or string literals).
func collectTopLevelKwargNames(callBody string) map[string]bool {
	present := make(map[string]bool)
	i := 0
	depth := 0
	for i < len(callBody) {
		i, depth = collectKwargStep(callBody, i, depth, present)
	}
	return present
}

// collectKwargStep processes one position in a call body, updating depth and the present set.
// Returns the next position and new depth.
func collectKwargStep(s string, i int, depth int, present map[string]bool) (int, int) {
	if s[i] == '"' || s[i] == '\'' {
		return advancePastString(s, i), depth
	}
	if next, skip := advancePastToken(s, i); skip {
		return next, depth
	}
	if s[i] == '(' {
		return i + 1, depth + 1
	}
	if s[i] == ')' {
		if depth > 0 {
			depth--
		}
		return i + 1, depth
	}
	if depth == 0 && isIdentStart(s[i]) {
		return collectKwargIdent(s, i, present), depth
	}
	return i + 1, depth
}

// collectKwargIdent scans an identifier at position i and records it in present
// when it is a keyword argument (followed by '=' but not '==').
// Returns the position after the identifier.
func collectKwargIdent(s string, i int, present map[string]bool) int {
	start := i
	for i < len(s) && isScriptIdentChar(s[i]) {
		i++
	}
	ident := s[start:i]
	j := i
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j < len(s) && s[j] == '=' && (j+1 >= len(s) || s[j+1] != '=') {
		present[ident] = true
	}
	return i
}

// extractHandlerName extracts the handler name from an error path like
// "sagas[0].script:position_keeping.initiate_log" or "sagas[0]:position_keeping.initiate_log#amount".
// Returns empty string when no handler name can be determined.
func extractHandlerName(path string) string {
	// Strip any #param suffix before extracting the handler name.
	if hash := strings.LastIndex(path, "#"); hash >= 0 {
		path = path[:hash]
	}

	// Look for a pattern like "service_name.handler_name" in the path
	colon := strings.LastIndex(path, ":")
	if colon >= 0 && colon < len(path)-1 {
		candidate := path[colon+1:]
		if strings.Contains(candidate, ".") {
			return candidate
		}
	}
	// Fallback: find the last dotted segment if no colon delimiter
	parts := strings.Split(path, ".")
	if len(parts) >= 2 {
		// Return "service.method" from the last two path components
		last := parts[len(parts)-1]
		prev := parts[len(parts)-2]
		if !strings.Contains(prev, "[") {
			return prev + "." + last
		}
	}
	return ""
}

// extractParamName extracts the parameter name from an error path like
// "sagas[0]:position_keeping.initiate_log#amount".
// Returns empty string when no parameter name can be determined.
func extractParamName(path string) string {
	hash := strings.LastIndex(path, "#")
	if hash >= 0 && hash < len(path)-1 {
		return path[hash+1:]
	}
	return ""
}
