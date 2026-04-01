package differ

import (
	"context"
	"fmt"
	"sort"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// ManifestDiffer compares a last-applied manifest against a new manifest
// to produce a plan of CREATE/UPDATE/DELETE/NO_CHANGE actions.
//
// It follows Kubernetes apply semantics:
//   - Codes are immutable primary keys (like metadata.name in k8s)
//   - Resources present in new but not in last-applied -> CREATE
//   - Resources present in both with field changes -> UPDATE
//   - Resources present in last-applied but not in new -> DELETE (with safety checks)
//   - Resources identical in both -> NO_CHANGE
type ManifestDiffer struct {
	safety    SafetyChecker
	drift     DriftDetector
	liveState LiveStateProvider
}

// New creates a ManifestDiffer with the given safety checker, drift detector,
// and optional live state provider. Pass nil for any parameter to use no-op defaults.
func New(safety SafetyChecker, drift DriftDetector, liveState LiveStateProvider) *ManifestDiffer {
	if safety == nil {
		safety = &NoOpSafetyChecker{}
	}
	if drift == nil {
		drift = &NoOpDriftDetector{}
	}
	return &ManifestDiffer{
		safety:    safety,
		drift:     drift,
		liveState: liveState,
	}
}

// DiffOption configures optional behavior of a Diff call.
type DiffOption func(*diffConfig)

type diffConfig struct {
	skipSafetyChecks bool
}

// WithSkipSafetyChecks skips safety checks (blocked deletions) and breaking
// change flagging. Use when validating a manifest for a new tenant that has
// no existing state.
func WithSkipSafetyChecks() DiffOption {
	return func(c *diffConfig) {
		c.skipSafetyChecks = true
	}
}

// Diff compares lastApplied against newManifest and returns a plan.
// If lastApplied is nil, all resources in newManifest are treated as CREATE.
func (d *ManifestDiffer) Diff(ctx context.Context, lastApplied, newManifest *controlplanev1.Manifest, opts ...DiffOption) (*DiffPlan, error) {
	if newManifest == nil {
		return nil, ErrNilManifest
	}

	cfg := &diffConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	plan := &DiffPlan{}

	d.diffAllResourceTypes(lastApplied, newManifest, plan)

	if err := d.finalizeDiffPlan(ctx, lastApplied, plan, cfg); err != nil {
		return nil, err
	}

	sort.Slice(plan.Actions, func(i, j int) bool {
		if plan.Actions[i].ResourceType != plan.Actions[j].ResourceType {
			return plan.Actions[i].ResourceType < plan.Actions[j].ResourceType
		}
		return plan.Actions[i].ResourceCode < plan.Actions[j].ResourceCode
	})

	return plan, nil
}

// diffAllResourceTypes diffs each resource type independently.
func (d *ManifestDiffer) diffAllResourceTypes(lastApplied, newManifest *controlplanev1.Manifest, plan *DiffPlan) {
	d.diffInstruments(lastApplied, newManifest, plan)
	d.diffAccountTypes(lastApplied, newManifest, plan)
	d.diffValuationRules(lastApplied, newManifest, plan)
	d.diffSagas(lastApplied, newManifest, plan)
	d.diffPartyTypes(lastApplied, newManifest, plan)
	d.diffMappings(lastApplied, newManifest, plan)
	d.diffOperationalGateway(lastApplied, newManifest, plan)
	d.diffMarketDataSources(lastApplied, newManifest, plan)
	d.diffMarketDataSets(lastApplied, newManifest, plan)
	d.diffOrganizations(lastApplied, newManifest, plan)
	d.diffInternalAccounts(lastApplied, newManifest, plan)
}

// finalizeDiffPlan runs safety checks, flags breaking changes, and detects drift.
func (d *ManifestDiffer) finalizeDiffPlan(ctx context.Context, lastApplied *controlplanev1.Manifest, plan *DiffPlan, cfg *diffConfig) error {
	if !cfg.skipSafetyChecks {
		if err := d.runSafetyChecks(ctx, plan); err != nil {
			return fmt.Errorf("safety check failed: %w", err)
		}
		for i := range plan.Actions {
			if plan.Actions[i].Action == ActionDelete {
				plan.Actions[i].Breaking = true
				plan.HasBreakingChanges = true
			}
		}
	}

	if lastApplied != nil {
		warnings, err := d.drift.DetectDrift(ctx, lastApplied)
		if err != nil {
			return fmt.Errorf("drift detection failed: %w", err)
		}
		plan.DriftWarnings = warnings
	}

	return nil
}

func (d *ManifestDiffer) runSafetyChecks(ctx context.Context, plan *DiffPlan) error {
	for _, action := range plan.Actions {
		if action.Action != ActionDelete {
			continue
		}
		var blocked *BlockedDeletion
		var err error

		switch action.ResourceType {
		case ResourceAccountType:
			blocked, err = d.safety.CheckAccountTypeDeletion(ctx, action.ResourceCode)
		case ResourceInstrument:
			blocked, err = d.safety.CheckInstrumentDeletion(ctx, action.ResourceCode)
		case ResourceSaga:
			blocked, err = d.safety.CheckSagaDeletion(ctx, action.ResourceCode)
		case ResourceValuationRule,
			ResourcePartyType,
			ResourceMapping,
			ResourceProviderConnection,
			ResourceInstructionRoute,
			ResourceMarketDataSource,
			ResourceMarketDataSet,
			ResourceOrganization,
			ResourceInternalAccount:
			// No downstream dependency checks for these resource types.
		}
		if err != nil {
			return fmt.Errorf("safety check for %s %s: %w", action.ResourceType, action.ResourceCode, err)
		}
		if blocked != nil {
			plan.BlockedDeletions = append(plan.BlockedDeletions, *blocked)
		}
	}
	return nil
}
