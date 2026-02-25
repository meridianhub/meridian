// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Visibility validation errors.
var (
	// ErrVisibilityViolation is returned when a saga references parties outside the executing party's scope.
	ErrVisibilityViolation = errors.New("visibility violation: saga references parties outside visible scope")
)

// VisibilityValidator performs pre-flight validation of party visibility.
// It ensures the executing party has visibility over all parties that the saga will access.
type VisibilityValidator struct{}

// NewVisibilityValidator creates a new VisibilityValidator.
func NewVisibilityValidator() *VisibilityValidator {
	return &VisibilityValidator{}
}

// VisibilityManifest declares the parties that a saga will access.
// This is used for pre-flight validation before saga execution begins.
type VisibilityManifest struct {
	// ReferencedParties lists all party IDs that the saga will access.
	// These are typically extracted from script input or declared in metadata.
	ReferencedParties []uuid.UUID

	// AuthorizedLookups lists lookup types that are authorized for cross-party access.
	// Valid values: "resolve_account", "internal_account"
	// These lookups are validated per-step, not during pre-flight.
	AuthorizedLookups []string
}

// isAuthorizedLookup checks if a lookup type is in the authorized list.
// This is a hook for future per-step validation where step handlers can
// declare lookup types (e.g., "exchange_rate", "market_data") that are
// authorized even when referencing external parties. Currently tested but
// not integrated into step execution; integration is planned for future work.
func (m *VisibilityManifest) isAuthorizedLookup(lookupType string) bool {
	for _, auth := range m.AuthorizedLookups {
		if auth == lookupType {
			return true
		}
	}
	return false
}

// ValidateResult contains the result of visibility validation.
type ValidateResult struct {
	// Valid indicates whether all referenced parties are visible.
	Valid bool

	// InvisibleParties lists party IDs that are referenced but not visible.
	InvisibleParties []uuid.UUID
}

// Validate checks if the executing party has visibility over all referenced parties.
// Returns a ValidateResult with details about any visibility violations.
//
// Validation rules:
//   - All parties in manifest.ReferencedParties must be in scope.VisibleParties
//   - Parties accessed through AuthorizedLookups are exempt (validated per-step)
//   - If scope is nil, validation passes (party isolation disabled)
func (v *VisibilityValidator) Validate(scope *PartyScope, manifest *VisibilityManifest) *ValidateResult {
	// If no party scope is configured, allow all access
	if scope == nil {
		return &ValidateResult{Valid: true}
	}

	// If no manifest is provided, allow (nothing to check)
	if manifest == nil {
		return &ValidateResult{Valid: true}
	}

	// Check each referenced party
	var invisibleParties []uuid.UUID
	for _, refParty := range manifest.ReferencedParties {
		if !scope.Contains(refParty) {
			invisibleParties = append(invisibleParties, refParty)
		}
	}

	if len(invisibleParties) > 0 {
		return &ValidateResult{
			Valid:            false,
			InvisibleParties: invisibleParties,
		}
	}

	return &ValidateResult{Valid: true}
}

// ValidateOrError validates visibility and returns an error if validation fails.
// This is a convenience method that wraps Validate() for simpler use cases.
func (v *VisibilityValidator) ValidateOrError(scope *PartyScope, manifest *VisibilityManifest) error {
	result := v.Validate(scope, manifest)
	if !result.Valid {
		return fmt.Errorf("%w: party %s lacks visibility over parties %v",
			ErrVisibilityViolation, scope.PartyID, result.InvisibleParties)
	}
	return nil
}

// partyCollector accumulates unique party IDs from various input formats.
type partyCollector struct {
	seen   map[uuid.UUID]bool
	result []uuid.UUID
}

// newPartyCollector creates a new party collector.
func newPartyCollector() *partyCollector {
	return &partyCollector{
		seen: make(map[uuid.UUID]bool),
	}
}

// add attempts to parse and add a party ID if valid and not already seen.
func (c *partyCollector) add(val interface{}) {
	partyID, ok := c.parsePartyID(val)
	if !ok || c.seen[partyID] {
		return
	}
	c.seen[partyID] = true
	c.result = append(c.result, partyID)
}

// parsePartyID attempts to parse a value as a UUID.
func (c *partyCollector) parsePartyID(val interface{}) (uuid.UUID, bool) {
	switch v := val.(type) {
	case string:
		id, err := uuid.Parse(v)
		return id, err == nil
	case uuid.UUID:
		return v, true
	default:
		return uuid.UUID{}, false
	}
}

// knownPartyFields lists field names that typically contain party IDs.
var knownPartyFields = []string{
	"party_id", "counterparty_id", "from_party", "to_party",
	"source_party_id", "target_party_id", "owner_party_id",
	"beneficiary_party_id", "creditor_party_id", "debtor_party_id",
}

// ExtractPartyReferencesFromInput extracts party IDs from saga input data.
// It looks for common patterns:
//   - Fields named "party_id", "counterparty_id", "from_party", "to_party", etc.
//   - Arrays named "party_ids" or "parties"
//
// Note: This is a basic implementation. Full AST-based extraction
// (parsing Starlark script for party ID literals) can be added later.
func ExtractPartyReferencesFromInput(input map[string]interface{}) []uuid.UUID {
	if input == nil {
		return nil
	}

	collector := newPartyCollector()

	// Check known field patterns
	for _, field := range knownPartyFields {
		if val, ok := input[field]; ok {
			collector.add(val)
		}
	}

	// Check for party_ids array
	collector.extractFromArray(input, "party_ids")

	// Check for parties array (may contain party objects)
	collector.extractFromPartiesArray(input)

	return collector.result
}

// extractFromArray extracts party IDs from a named array field.
// Handles []interface{}, []string, and []uuid.UUID typed slices.
func (c *partyCollector) extractFromArray(input map[string]interface{}, fieldName string) {
	val, ok := input[fieldName]
	if !ok {
		return
	}
	switch arr := val.(type) {
	case []interface{}:
		for _, item := range arr {
			c.add(item)
		}
	case []string:
		for _, item := range arr {
			c.add(item)
		}
	case []uuid.UUID:
		for _, item := range arr {
			c.add(item)
		}
	}
}

// extractFromPartiesArray extracts party IDs from a "parties" array that may contain objects.
// Handles []interface{}, []string, []uuid.UUID, and []map[string]interface{} typed slices.
func (c *partyCollector) extractFromPartiesArray(input map[string]interface{}) {
	val, ok := input["parties"]
	if !ok {
		return
	}
	switch arr := val.(type) {
	case []interface{}:
		for _, item := range arr {
			// Try as direct party ID
			c.add(item)
			// Try as map with party_id field
			if m, ok := item.(map[string]interface{}); ok {
				if id, ok := m["party_id"]; ok {
					c.add(id)
				}
			}
		}
	case []string:
		for _, item := range arr {
			c.add(item)
		}
	case []uuid.UUID:
		for _, item := range arr {
			c.add(item)
		}
	case []map[string]interface{}:
		for _, item := range arr {
			if id, ok := item["party_id"]; ok {
				c.add(id)
			}
		}
	}
}

// NewVisibilityManifestFromInput creates a VisibilityManifest by extracting party references
// from the saga input data. This is a convenience method for pre-flight validation.
func NewVisibilityManifestFromInput(input map[string]interface{}) *VisibilityManifest {
	manifest := &VisibilityManifest{
		ReferencedParties: ExtractPartyReferencesFromInput(input),
	}

	// Check for authorized_lookups declaration in input
	if lookups, ok := input["authorized_lookups"]; ok {
		if arr, ok := lookups.([]interface{}); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					manifest.AuthorizedLookups = append(manifest.AuthorizedLookups, s)
				}
			}
		}
	}

	return manifest
}
