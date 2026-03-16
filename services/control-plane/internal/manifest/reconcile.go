package manifest

import (
	"context"
	"errors"
	"fmt"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
)

// ReconcileResult holds the output of a reconciliation operation.
type ReconcileResult struct {
	DriftItems        []DriftItem
	Summary           ReconcileSummary
	ReconciledVersion string
	ReconciledAt      time.Time
	Warnings          []string
}

// DriftItem represents a single resource-level discrepancy between the
// stored manifest and live service state.
type DriftItem struct {
	ResourceType string
	ResourceCode string
	DriftType    DriftItemType
	Description  string
}

// DriftItemType classifies the nature of a drift discrepancy.
type DriftItemType string

const (
	// DriftTypeMissing indicates a resource exists in the stored manifest
	// but is absent from live service state.
	DriftTypeMissing DriftItemType = "MISSING"

	// DriftTypeModified indicates a resource exists in both but their
	// definitions differ.
	DriftTypeModified DriftItemType = "MODIFIED"

	// DriftTypeExtra indicates a resource exists in live service state
	// but is absent from the stored manifest.
	DriftTypeExtra DriftItemType = "EXTRA"
)

// ReconcileSummary contains aggregate statistics about a reconciliation run.
type ReconcileSummary struct {
	TotalChecked int
	TotalDrifted int
	Missing      int
	Modified     int
	Extra        int
}

// ErrNilExporter is returned when exporter is nil.
var ErrNilExporter = errors.New("exporter cannot be nil")

// ReconcileService compares stored manifest state against live service state
// to detect drift. This is a read-only operation — no auto-repair is performed.
type ReconcileService struct {
	history  *HistoryService
	exporter *ExportService
	d        *differ.ManifestDiffer
}

// NewReconcileService creates a new ReconcileService.
// history is required for loading stored manifests.
// exporter is required for querying live state.
// d is the differ used for comparison; if nil a no-op differ is used.
func NewReconcileService(history *HistoryService, exporter *ExportService, d *differ.ManifestDiffer) (*ReconcileService, error) {
	if history == nil {
		return nil, ErrNilRepository
	}
	if exporter == nil {
		return nil, ErrNilExporter
	}
	if d == nil {
		d = differ.New(nil, nil)
	}
	return &ReconcileService{
		history:  history,
		exporter: exporter,
		d:        d,
	}, nil
}

// Reconcile compares the stored manifest against live service state and
// reports any drift. version specifies which stored manifest to reconcile;
// empty means the latest applied. includeSections filters which sections
// are compared; empty means all.
func (s *ReconcileService) Reconcile(ctx context.Context, version string, includeSections []string) (*ReconcileResult, error) {
	// Load the stored manifest.
	storedManifest, storedVersion, err := s.loadStoredManifest(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("load stored manifest: %w", err)
	}

	// Export live state using the same section filter.
	exportResult, err := s.exporter.Export(ctx, includeSections, storedVersion)
	if err != nil {
		return nil, fmt.Errorf("export live state: %w", err)
	}

	// If include_sections is specified, zero out sections not in the filter
	// on the stored manifest so the differ only sees requested sections.
	filteredStored := storedManifest
	if len(includeSections) > 0 {
		filteredStored = filterManifestSections(storedManifest, includeSections)
	}

	// Diff stored (as "last-applied") against live (as "new") to detect drift.
	// Stored=old, Live=new:
	//   - DELETE actions mean resource in stored but not live -> MISSING
	//   - CREATE actions mean resource in live but not stored -> EXTRA
	//   - UPDATE actions mean resource differs -> MODIFIED
	plan, err := s.d.Diff(ctx, filteredStored, exportResult.Manifest, differ.WithSkipSafetyChecks())
	if err != nil {
		return nil, fmt.Errorf("diff failed: %w", err)
	}

	result := diffPlanToReconcileResult(plan, storedVersion)
	result.ReconciledAt = time.Now().UTC()
	result.Warnings = exportResult.Warnings

	return result, nil
}

// diffPlanToReconcileResult converts a differ.DiffPlan to a ReconcileResult.
// The mapping from diff actions to drift types:
//   - DELETE (in stored but not in live) -> MISSING
//   - CREATE (in live but not in stored) -> EXTRA
//   - UPDATE (both exist but differ)     -> MODIFIED
//   - NO_CHANGE                          -> no drift (counted in TotalChecked)
func diffPlanToReconcileResult(plan *differ.DiffPlan, version string) *ReconcileResult {
	result := &ReconcileResult{
		ReconciledVersion: version,
	}

	for _, action := range plan.Actions {
		switch action.Action {
		case differ.ActionDelete:
			result.DriftItems = append(result.DriftItems, DriftItem{
				ResourceType: string(action.ResourceType),
				ResourceCode: action.ResourceCode,
				DriftType:    DriftTypeMissing,
				Description:  fmt.Sprintf("Resource %s %s exists in manifest but not in live state", action.ResourceType, action.ResourceCode),
			})
			result.Summary.Missing++
			result.Summary.TotalDrifted++
		case differ.ActionCreate:
			result.DriftItems = append(result.DriftItems, DriftItem{
				ResourceType: string(action.ResourceType),
				ResourceCode: action.ResourceCode,
				DriftType:    DriftTypeExtra,
				Description:  fmt.Sprintf("Resource %s %s exists in live state but not in manifest", action.ResourceType, action.ResourceCode),
			})
			result.Summary.Extra++
			result.Summary.TotalDrifted++
		case differ.ActionUpdate:
			result.DriftItems = append(result.DriftItems, DriftItem{
				ResourceType: string(action.ResourceType),
				ResourceCode: action.ResourceCode,
				DriftType:    DriftTypeModified,
				Description:  action.Description,
			})
			result.Summary.Modified++
			result.Summary.TotalDrifted++
		case differ.ActionNoChange:
			// No drift for this resource.
		}
		result.Summary.TotalChecked++
	}

	return result
}

// loadStoredManifest loads the stored manifest to reconcile against.
func (s *ReconcileService) loadStoredManifest(ctx context.Context, version string) (*controlplanev1.Manifest, string, error) {
	var entity *VersionEntity
	var err error

	if version != "" {
		entity, err = s.history.GetManifestVersion(ctx, version)
	} else {
		entity, err = s.history.GetCurrentManifest(ctx)
	}
	if err != nil {
		return nil, "", err
	}

	manifest, err := unmarshalManifest(entity.ManifestJSON)
	if err != nil {
		return nil, "", fmt.Errorf("unmarshal stored manifest: %w", err)
	}
	return manifest, entity.Version, nil
}

// filterManifestSections returns a copy of the manifest with only the
// specified sections populated. Non-matching sections are left nil.
func filterManifestSections(m *controlplanev1.Manifest, includeSections []string) *controlplanev1.Manifest {
	sections := parseSections(includeSections)

	filtered := &controlplanev1.Manifest{
		Version:  m.Version,
		Metadata: m.Metadata,
	}

	if sections[SectionInstruments] {
		filtered.Instruments = m.Instruments
	}
	if sections[SectionAccountTypes] {
		filtered.AccountTypes = m.AccountTypes
	}
	if sections[SectionValuationRules] {
		filtered.ValuationRules = m.ValuationRules
	}
	if sections[SectionSagas] {
		filtered.Sagas = m.Sagas
	}
	if sections[SectionMarketData] {
		filtered.MarketData = m.MarketData
	}
	if sections[SectionOrganizations] {
		filtered.Organizations = m.Organizations
	}
	if sections[SectionInternalAccounts] {
		filtered.InternalAccounts = m.InternalAccounts
	}
	if sections[SectionOperationalGateway] {
		filtered.OperationalGateway = m.OperationalGateway
	}
	if sections[SectionMappings] {
		filtered.Mappings = m.Mappings
	}
	if sections[SectionPartyTypes] {
		filtered.PartyTypes = m.PartyTypes
	}
	if sections[SectionPaymentRails] {
		filtered.PaymentRails = m.PaymentRails
	}

	return filtered
}

// ToProtoResponse converts a ReconcileResult to the gRPC response.
func (r *ReconcileResult) ToProtoResponse() *controlplanev1.ReconcileManifestResponse {
	resp := &controlplanev1.ReconcileManifestResponse{
		ReconciledVersion: r.ReconciledVersion,
		Warnings:          r.Warnings,
		Summary: &controlplanev1.ReconcileSummary{
			TotalChecked: int32(r.Summary.TotalChecked),
			TotalDrifted: int32(r.Summary.TotalDrifted),
			Missing:      int32(r.Summary.Missing),
			Modified:     int32(r.Summary.Modified),
			Extra:        int32(r.Summary.Extra),
		},
	}

	for _, item := range r.DriftItems {
		resp.DriftItems = append(resp.DriftItems, &controlplanev1.DriftItem{
			ResourceType: item.ResourceType,
			ResourceCode: item.ResourceCode,
			DriftType:    toDriftTypeProto(item.DriftType),
			Description:  item.Description,
		})
	}

	return resp
}

// toDriftTypeProto converts an internal DriftItemType to the proto enum.
func toDriftTypeProto(dt DriftItemType) controlplanev1.DriftType {
	switch dt {
	case DriftTypeMissing:
		return controlplanev1.DriftType_DRIFT_TYPE_MISSING
	case DriftTypeModified:
		return controlplanev1.DriftType_DRIFT_TYPE_MODIFIED
	case DriftTypeExtra:
		return controlplanev1.DriftType_DRIFT_TYPE_EXTRA
	default:
		return controlplanev1.DriftType_DRIFT_TYPE_UNSPECIFIED
	}
}
