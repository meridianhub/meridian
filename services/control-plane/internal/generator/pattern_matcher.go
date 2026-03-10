// Package generator provides economy configuration generation utilities for the control-plane.
// It assembles a GenerationContext from live services and matches cookbook patterns to
// a tenant's business description.
package generator

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// scoredEntry pairs a PatternMatch with its computed relevance score for internal sorting.
type scoredEntry struct {
	Match PatternMatch
	Score float64
}

// PatternMatch describes a cookbook pattern that was selected by MatchPatterns.
type PatternMatch struct {
	// Name is the kebab-case registry name (e.g. "energy-settlement").
	Name string
	// Title is the human-readable title from the registry.
	Title string
	// Score is the relevance score computed by the multi-factor scoring algorithm.
	Score float64
	// Provides lists the instruments, account types, and sagas contributed by this pattern.
	Provides []string
	// Requires lists the external prerequisites (instruments, market data) needed.
	Requires []string
	// ComposesWith lists patterns that work well alongside this one.
	ComposesWith []string
	// ConflictsWith lists patterns that are incompatible with this one.
	ConflictsWith []string
	// ManifestFragment is the YAML fragment from the pattern's manifest-fragment.yaml file.
	ManifestFragment string
	// SagaScript is the content of the first Starlark saga file found for the pattern.
	SagaScript string
}

// MatchPatterns loads all registry:pattern entries from cookbookFS, scores each against
// the provided description and industry, resolves extends dependencies, applies conflict
// filtering, and returns the top maxResults results ordered by descending score.
//
// cookbookFS must expose registry.json at its root and pattern files under
// patterns/<name>/pattern.json, patterns/<name>/manifest-fragment.yaml, and
// patterns/<name>/*.star.
func MatchPatterns(cookbookFS fs.FS, description string, industry string, maxResults int) ([]PatternMatch, error) {
	// --- Subtask 5.1: Load and parse pattern metadata ---

	reg, err := loadRegistry(cookbookFS)
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}

	// Load all pattern details upfront.
	allDetails := make(map[string]*patternDetail, len(reg.Items))
	for _, item := range reg.Items {
		if item.Type != "registry:pattern" {
			continue
		}
		detail, loadErr := loadPatternDetail(cookbookFS, item.Name)
		if loadErr != nil {
			// Skip patterns that fail to load — non-fatal.
			continue
		}
		allDetails[item.Name] = detail
	}

	// --- Subtask 5.2: Multi-factor scoring ---

	descWords := tokenise(description)
	industryLower := strings.ToLower(industry)

	var scored []scoredEntry

	for _, item := range reg.Items {
		if item.Type != "registry:pattern" {
			continue
		}
		detail := allDetails[item.Name]
		if detail == nil {
			continue
		}

		score := scorePattern(detail, descWords, industryLower)

		match := buildPatternMatch(cookbookFS, item, detail)
		match.Score = score
		scored = append(scored, scoredEntry{Match: match, Score: score})
	}

	// Sort by score descending, then name ascending for determinism.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Match.Name < scored[j].Match.Name
	})

	// --- Subtask 5.3: Conflict filtering, extends resolution, top-N selection ---

	selected := applyConflictFilterAndExtends(scored, allDetails, maxResults)

	return selected, nil
}

// scorePattern computes the relevance score for a pattern against the description tokens and industry.
func scorePattern(detail *patternDetail, descWords []string, industryLower string) float64 {
	if detail.Meta == nil {
		return 0
	}

	score := scoreIndustry(detail.Meta, industryLower)
	if len(descWords) > 0 {
		score += scoreKeywords(detail, descWords)
	}

	return score
}

// scoreIndustry returns +10 when the industry matches meta.industries, 0 otherwise.
func scoreIndustry(meta *patternMeta, industryLower string) float64 {
	if industryLower == "" {
		return 0
	}
	for _, ind := range meta.Industries {
		if strings.ToLower(ind) == industryLower {
			return 10
		}
	}
	return 0
}

// scoreKeywords returns keyword match (+1 per word that appears in provides terms)
// and fuzzy title/description match (+0.5 per word).
func scoreKeywords(detail *patternDetail, descWords []string) float64 {
	providedTerms := collectProvidedTerms(detail.Meta)
	titleLower := strings.ToLower(detail.Title)
	descLower := strings.ToLower(detail.Description)

	var score float64
	for _, word := range descWords {
		if matchesAnyTerm(word, providedTerms) {
			score++
		}
		if strings.Contains(titleLower, word) || strings.Contains(descLower, word) {
			score += 0.5
		}
	}
	return score
}

// collectProvidedTerms returns all lowercase tokens from the provides block.
func collectProvidedTerms(meta *patternMeta) []string {
	if meta == nil || meta.Provides == nil {
		return nil
	}
	p := meta.Provides
	terms := make([]string, 0, len(p.Instruments)+len(p.AccountTypes)+len(p.Sagas))
	for _, s := range p.Instruments {
		terms = append(terms, strings.ToLower(s))
	}
	for _, s := range p.AccountTypes {
		terms = append(terms, strings.ToLower(s))
	}
	for _, s := range p.Sagas {
		terms = append(terms, strings.ToLower(s))
	}
	return terms
}

// matchesAnyTerm returns true when word is a substring of any term in terms.
func matchesAnyTerm(word string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(term, word) {
			return true
		}
	}
	return false
}

// applyConflictFilterAndExtends applies three operations in order:
//  1. Conflict filtering: skip patterns that conflict with already-selected ones.
//  2. Extends resolution: prepend base patterns required by extends declarations.
//  3. Top-N selection: return at most maxResults patterns.
func applyConflictFilterAndExtends(
	scored []scoredEntry,
	allDetails map[string]*patternDetail,
	maxResults int,
) []PatternMatch {
	selectedNames := make(map[string]bool)
	conflictSet := make(map[string]bool) // patterns blocked by conflicts

	var result []PatternMatch

	// Collect patterns with conflict filtering.
	var candidates []PatternMatch
	for _, s := range scored {
		if conflictSet[s.Match.Name] {
			continue
		}
		candidates = append(candidates, s.Match)
		selectedNames[s.Match.Name] = true

		// Register conflicts: any pattern that conflicts_with this one is blocked.
		detail := allDetails[s.Match.Name]
		if detail != nil && detail.Meta != nil {
			for _, conflictName := range detail.Meta.ConflictsWith {
				conflictSet[conflictName] = true
			}
		}
	}

	// Resolve extends: collect all base patterns required by the selected candidates.
	// Use a worklist to handle transitive extends.
	basePatterns := resolveExtends(candidates, allDetails, selectedNames)

	// Prepend base patterns (they have no score on their own but are required).
	result = append(result, basePatterns...)
	result = append(result, candidates...)

	// Apply top-N.
	if maxResults > 0 && len(result) > maxResults {
		result = result[:maxResults]
	}

	return result
}

// resolveExtends returns the set of base patterns required by the extends fields of candidates,
// excluding patterns already in selectedNames, in dependency order (bases first).
func resolveExtends(candidates []PatternMatch, allDetails map[string]*patternDetail, selectedNames map[string]bool) []PatternMatch {
	worklist := collectExtendsWorklist(candidates, allDetails, selectedNames)
	if len(worklist) == 0 {
		return nil
	}
	return buildBaseMatches(worklist, allDetails)
}

// collectExtendsWorklist performs a BFS over the extends graph starting from candidates.
// It returns all base pattern names in BFS discovery order, skipping already-selected names.
func collectExtendsWorklist(candidates []PatternMatch, allDetails map[string]*patternDetail, selectedNames map[string]bool) []string {
	needed := make(map[string]bool)
	var worklist []string

	enqueue := func(name string) {
		if !selectedNames[name] && !needed[name] {
			needed[name] = true
			worklist = append(worklist, name)
		}
	}

	for _, c := range candidates {
		if d := allDetails[c.Name]; d != nil && d.Meta != nil {
			for _, base := range d.Meta.Extends {
				enqueue(base)
			}
		}
	}

	for i := 0; i < len(worklist); i++ {
		if d := allDetails[worklist[i]]; d != nil && d.Meta != nil {
			for _, base := range d.Meta.Extends {
				enqueue(base)
			}
		}
	}

	return worklist
}

// buildBaseMatches converts a BFS-ordered worklist into PatternMatch values, bases first.
func buildBaseMatches(worklist []string, allDetails map[string]*patternDetail) []PatternMatch {
	bases := make([]PatternMatch, 0, len(worklist))
	seen := make(map[string]bool, len(worklist))
	for i := len(worklist) - 1; i >= 0; i-- {
		name := worklist[i]
		if seen[name] {
			continue
		}
		seen[name] = true
		if detail := allDetails[name]; detail != nil {
			bases = append(bases, buildPatternMatchFromDetail(name, detail))
		}
	}
	return bases
}

// buildPatternMatch constructs a PatternMatch from a registry item and its full detail,
// including loading manifest fragment and saga script from the FS.
func buildPatternMatch(cookbookFS fs.FS, item registryItem, detail *patternDetail) PatternMatch {
	m := buildPatternMatchFromDetail(item.Name, detail)

	// Load manifest fragment.
	fragmentPath := fmt.Sprintf("patterns/%s/manifest-fragment.yaml", item.Name)
	if data, err := fs.ReadFile(cookbookFS, fragmentPath); err == nil {
		m.ManifestFragment = string(data)
	}

	// Load the first .star saga file found.
	m.SagaScript = loadFirstStarFile(cookbookFS, item.Name)

	return m
}

// buildPatternMatchFromDetail constructs a PatternMatch from a pattern detail without file loading.
func buildPatternMatchFromDetail(name string, detail *patternDetail) PatternMatch {
	m := PatternMatch{
		Name:  name,
		Title: detail.Title,
	}

	if detail.Meta == nil {
		return m
	}

	// Populate Provides.
	if detail.Meta.Provides != nil {
		p := detail.Meta.Provides
		provides := make([]string, 0, len(p.Instruments)+len(p.AccountTypes)+len(p.Sagas))
		provides = append(provides, p.Instruments...)
		provides = append(provides, p.AccountTypes...)
		provides = append(provides, p.Sagas...)
		m.Provides = provides
	}

	// Populate Requires.
	if detail.Meta.Requires != nil {
		r := detail.Meta.Requires
		requires := make([]string, 0, len(r.Instruments)+len(r.MarketData))
		requires = append(requires, r.Instruments...)
		requires = append(requires, r.MarketData...)
		m.Requires = requires
	}

	m.ComposesWith = detail.Meta.ComposesWith
	m.ConflictsWith = detail.Meta.ConflictsWith

	return m
}

// loadFirstStarFile returns the content of the first *.star file found under patterns/<name>/.
func loadFirstStarFile(cookbookFS fs.FS, name string) string {
	dir := fmt.Sprintf("patterns/%s", name)
	entries, err := fs.ReadDir(cookbookFS, dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".star") {
			path := fmt.Sprintf("%s/%s", dir, entry.Name())
			if data, err := fs.ReadFile(cookbookFS, path); err == nil {
				return string(data)
			}
		}
	}
	return ""
}

// tokenise splits a description into lowercase words, stripping punctuation.
func tokenise(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ToLower(s)
	// Replace punctuation with spaces.
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	parts := strings.Fields(b.String())
	// Deduplicate and filter very short tokens.
	seen := make(map[string]bool)
	result := parts[:0]
	for _, p := range parts {
		if len(p) >= 2 && !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

// --- Internal types for JSON parsing ---

type registryItem struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type registry struct {
	Items []registryItem `json:"items"`
}

type patternDetail struct {
	Name        string       `json:"name"`
	Type        string       `json:"type"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Meta        *patternMeta `json:"meta,omitempty"`
}

type patternMeta struct {
	Industries    []string         `json:"industries,omitempty"`
	Provides      *patternProvides `json:"provides,omitempty"`
	Requires      *patternRequires `json:"requires,omitempty"`
	ComposesWith  []string         `json:"composes_with,omitempty"`
	ConflictsWith []string         `json:"conflicts_with,omitempty"`
	Extends       []string         `json:"extends,omitempty"`
}

type patternProvides struct {
	Instruments  []string `json:"instruments,omitempty"`
	AccountTypes []string `json:"account_types,omitempty"`
	Sagas        []string `json:"sagas,omitempty"`
}

type patternRequires struct {
	Instruments []string `json:"instruments,omitempty"`
	MarketData  []string `json:"market_data,omitempty"`
}

// loadRegistry reads and parses registry.json from the given FS.
func loadRegistry(cookbookFS fs.FS) (*registry, error) {
	data, err := fs.ReadFile(cookbookFS, "registry.json")
	if err != nil {
		return nil, fmt.Errorf("read registry.json: %w", err)
	}
	var reg registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse registry.json: %w", err)
	}
	return &reg, nil
}

// loadPatternDetail reads and parses patterns/<name>/pattern.json from the given FS.
func loadPatternDetail(cookbookFS fs.FS, name string) (*patternDetail, error) {
	path := fmt.Sprintf("patterns/%s/pattern.json", name)
	data, err := fs.ReadFile(cookbookFS, path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var detail patternDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &detail, nil
}
