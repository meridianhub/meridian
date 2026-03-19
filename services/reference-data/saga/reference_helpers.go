package saga

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// findSimilar finds the most similar string in candidates using Levenshtein distance.
func findSimilar(target string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}

	target = strings.ToLower(target)
	var bestMatch string
	bestScore := -1

	for _, candidate := range candidates {
		score := similarity(target, strings.ToLower(candidate))
		if score > bestScore {
			bestScore = score
			bestMatch = candidate
		}
	}

	// Only suggest if similarity is above threshold (at least 50% similar)
	if bestScore >= len(target)/2 {
		return bestMatch
	}
	return ""
}

// similarity calculates the similarity between two strings.
// Returns higher scores for more similar strings.
func similarity(a, b string) int {
	if a == b {
		return len(a) * 2
	}

	// Simple prefix/suffix matching
	score := 0

	// Common prefix
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen && a[i] == b[i]; i++ {
		score++
	}

	// Common suffix (only if different from prefix)
	for i := 1; i <= minLen && a[len(a)-i] == b[len(b)-i]; i++ {
		if len(a)-i >= score || len(b)-i >= score { // Don't double-count
			score++
		}
	}

	// Substring matching
	if strings.Contains(a, b) || strings.Contains(b, a) {
		score += minLen / 2
	}

	return score
}

// persistReferences saves extracted references to the saga_reference table.
func (v *ReferenceValidator) persistReferences(ctx context.Context, sagaID uuid.UUID, refs []Reference) error {
	if v.pool == nil {
		return nil // No database configured, skip persistence
	}

	// Start a transaction
	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Delete existing references for this saga
	_, err = tx.Exec(ctx, "DELETE FROM saga_reference WHERE saga_definition_id = $1", sagaID)
	if err != nil {
		return fmt.Errorf("failed to delete existing references: %w", err)
	}

	// Insert new references
	for _, ref := range refs {
		_, err = tx.Exec(ctx, `
			INSERT INTO saga_reference (saga_definition_id, reference_type, reference_key, instrument_code, attribute_key, line_number)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (saga_definition_id, reference_type, reference_key) DO UPDATE
			SET instrument_code = EXCLUDED.instrument_code,
				attribute_key = EXCLUDED.attribute_key,
				line_number = EXCLUDED.line_number,
				extracted_at = now()`,
			sagaID, string(ref.Type), ref.Key, nullString(ref.InstrumentCode), nullString(ref.AttributeKey), ref.LineNumber)
		if err != nil {
			return fmt.Errorf("failed to insert reference: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// suggestHandler finds a similar handler name for suggestions.
func (v *ReferenceValidator) suggestHandler(name string) string {
	handlers := v.handlerRegistry.List()
	if match := findSimilar(name, handlers); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}

// suggestInstrument finds a similar instrument code for suggestions.
func (v *ReferenceValidator) suggestInstrument(ctx context.Context, code string) string {
	if v.instrumentChecker == nil {
		return ""
	}
	codes, err := v.instrumentChecker.ListActiveInstrumentCodes(ctx)
	if err != nil {
		return ""
	}
	if match := findSimilar(code, codes); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}

// suggestSaga finds a similar saga name for suggestions.
func (v *ReferenceValidator) suggestSaga(ctx context.Context, name string) string {
	if v.definitionChecker == nil {
		return ""
	}
	names, err := v.definitionChecker.ListActiveSagaNames(ctx)
	if err != nil {
		return ""
	}
	if match := findSimilar(name, names); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}

// suggestAttribute finds a similar attribute key for suggestions.
func (v *ReferenceValidator) suggestAttribute(schema map[string]interface{}, key string) string {
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	if match := findSimilar(key, keys); match != "" {
		return fmt.Sprintf("Did you mean '%s'?", match)
	}
	return ""
}
