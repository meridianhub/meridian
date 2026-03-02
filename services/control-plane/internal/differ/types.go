// Package differ implements the manifest diff engine that compares
// a last-applied manifest against a new manifest to produce a plan
// of CREATE/UPDATE/DELETE/NO_CHANGE actions with safety checks.
package differ

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNilManifest is returned when the new manifest is nil.
var ErrNilManifest = errors.New("new manifest cannot be nil")

// ActionType classifies how a resource should change during manifest application.
type ActionType string

// Action type constants for diff plan entries.
const (
	ActionCreate   ActionType = "CREATE"
	ActionUpdate   ActionType = "UPDATE"
	ActionDelete   ActionType = "DELETE"
	ActionNoChange ActionType = "NO_CHANGE"
)

// ResourceType identifies the category of resource being planned.
type ResourceType string

// Resource type constants matching manifest sections.
const (
	ResourceInstrument         ResourceType = "instrument"
	ResourceAccountType        ResourceType = "account_type"
	ResourceValuationRule      ResourceType = "valuation_rule"
	ResourceSaga               ResourceType = "saga"
	ResourcePartyType          ResourceType = "party_type"
	ResourceMapping            ResourceType = "mapping"
	ResourceProviderConnection ResourceType = "provider_connection"
	ResourceInstructionRoute   ResourceType = "instruction_route"
)

// PlannedAction represents a single action in the diff plan.
type PlannedAction struct {
	ResourceType ResourceType
	ResourceCode string
	Action       ActionType
	Description  string
	Breaking     bool
	Warnings     []string
}

// BlockedDeletion holds context about why a deletion was rejected.
type BlockedDeletion struct {
	ResourceType ResourceType
	ResourceCode string
	Reason       string
}

// DriftWarning indicates where database state differs from the last-applied manifest.
type DriftWarning struct {
	ResourceType ResourceType
	ResourceCode string
	Description  string
}

// DiffPlan is the complete result of comparing last-applied vs new manifest.
type DiffPlan struct {
	Actions            []PlannedAction
	BlockedDeletions   []BlockedDeletion
	DriftWarnings      []DriftWarning
	HasBreakingChanges bool
}

// Summary returns a human-readable summary of the plan.
func (p *DiffPlan) Summary() string {
	counts := map[ActionType]int{}
	for _, a := range p.Actions {
		counts[a.Action]++
	}
	return fmt.Sprintf(
		"%d to create, %d to update, %d to delete, %d no-change",
		counts[ActionCreate],
		counts[ActionUpdate],
		counts[ActionDelete],
		counts[ActionNoChange],
	)
}

// HasBlockedDeletions returns true if any deletions were rejected by safety checks.
func (p *DiffPlan) HasBlockedDeletions() bool {
	return len(p.BlockedDeletions) > 0
}

// BlockedDeletionErrors formats blocked deletions as actionable error strings.
func (p *DiffPlan) BlockedDeletionErrors() []string {
	msgs := make([]string, len(p.BlockedDeletions))
	for i, bd := range p.BlockedDeletions {
		msgs[i] = fmt.Sprintf("Cannot delete %s %s: %s", bd.ResourceType, bd.ResourceCode, bd.Reason)
	}
	return msgs
}

// valRuleKey produces a stable identifier for a valuation rule (from->to pair).
func valRuleKey(from, to string) string {
	return strings.ToUpper(from) + "->" + strings.ToUpper(to)
}
