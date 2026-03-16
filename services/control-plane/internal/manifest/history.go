package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	// ErrNilRepository is returned when repository is nil.
	ErrNilRepository = errors.New("repository cannot be nil")
	// ErrNilManifest is returned when manifest is nil.
	ErrNilManifest = errors.New("manifest cannot be nil")
	// ErrEmptyAppliedBy is returned when applied_by is empty.
	ErrEmptyAppliedBy = errors.New("applied_by cannot be empty")
)

// HistoryService provides manifest version history operations including
// storage, retrieval, diff generation, and rollback.
type HistoryService struct {
	repo *Repository
}

// NewHistoryService creates a new history service.
func NewHistoryService(repo *Repository) (*HistoryService, error) {
	if repo == nil {
		return nil, ErrNilRepository
	}
	return &HistoryService{repo: repo}, nil
}

// StoreManifestVersion saves a manifest snapshot with audit metadata.
// It generates a diff summary by comparing to the previous applied version.
// If graph is non-nil, it is serialized as JSON into the relationship_graph column.
// expectedSeq controls optimistic locking: non-zero values are checked
// atomically against the current sequence number; 0 skips the check.
func (s *HistoryService) StoreManifestVersion(
	ctx context.Context,
	manifest *controlplanev1.Manifest,
	appliedBy string,
	applyJobID *uuid.UUID,
	status ApplyStatus,
	graph *validator.RelationshipGraph,
	expectedSeq int64,
) (*VersionEntity, error) {
	if manifest == nil {
		return nil, ErrNilManifest
	}
	if appliedBy == "" {
		return nil, ErrEmptyAppliedBy
	}

	// Serialize manifest to JSON
	marshaler := protojson.MarshalOptions{
		UseProtoNames: true,
	}
	jsonBytes, err := marshaler.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest to JSON: %w", err)
	}

	// Generate diff summary against previous applied version
	var diffSummary *string
	if status == ApplyStatusApplied {
		summary, diffErr := s.generateDiffSummary(ctx, manifest)
		if diffErr != nil && !errors.Is(diffErr, ErrVersionNotFound) {
			return nil, fmt.Errorf("failed to generate diff summary: %w", diffErr)
		}
		if summary != "" {
			diffSummary = &summary
		}
	}

	// Serialize relationship graph if provided.
	// Serialization failure is non-blocking: the graph is informational.
	var graphJSON *string
	if graph != nil {
		if graphBytes, graphErr := json.Marshal(graph); graphErr == nil {
			s := string(graphBytes)
			graphJSON = &s
		}
	}

	now := time.Now().UTC()
	entity := &VersionEntity{
		ID:                uuid.New(),
		Version:           manifest.Version,
		ManifestJSON:      string(jsonBytes),
		AppliedAt:         now,
		AppliedBy:         appliedBy,
		ApplyStatus:       status,
		ApplyJobID:        applyJobID,
		DiffSummary:       diffSummary,
		RelationshipGraph: graphJSON,
		CreatedAt:         now,
	}

	if err := s.repo.Store(ctx, entity, expectedSeq); err != nil {
		return nil, err
	}

	return entity, nil
}

// GetCurrentManifest retrieves the latest applied manifest version.
func (s *HistoryService) GetCurrentManifest(ctx context.Context) (*VersionEntity, error) {
	return s.repo.GetLatestApplied(ctx)
}

// GetManifestVersion retrieves a specific manifest version by version string.
func (s *HistoryService) GetManifestVersion(ctx context.Context, version string) (*VersionEntity, error) {
	return s.repo.GetByVersion(ctx, version)
}

// ListManifestVersions returns a paginated list of manifest versions.
func (s *HistoryService) ListManifestVersions(ctx context.Context, limit, offset int) ([]VersionEntity, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.List(ctx, limit, offset)
}

// CompareVersions generates a human-readable diff between two manifest versions.
func (s *HistoryService) CompareVersions(ctx context.Context, v1, v2 string) (string, error) {
	entity1, err := s.repo.GetByVersion(ctx, v1)
	if err != nil {
		return "", fmt.Errorf("version %s: %w", v1, err)
	}

	entity2, err := s.repo.GetByVersion(ctx, v2)
	if err != nil {
		return "", fmt.Errorf("version %s: %w", v2, err)
	}

	manifest1, err := unmarshalManifest(entity1.ManifestJSON)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal version %s: %w", v1, err)
	}

	manifest2, err := unmarshalManifest(entity2.ManifestJSON)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal version %s: %w", v2, err)
	}

	return diffManifests(manifest1, manifest2), nil
}

// RollbackToVersion creates a new manifest version with the content from a previous version.
// This maintains the forward-only audit trail: rollback from v1.2 to v1.1 creates
// a new record with v1.1's content, not an in-place revert.
func (s *HistoryService) RollbackToVersion(
	ctx context.Context,
	version string,
	appliedBy string,
	applyJobID *uuid.UUID,
) (*VersionEntity, error) {
	if appliedBy == "" {
		return nil, ErrEmptyAppliedBy
	}

	// Retrieve the target version
	target, err := s.repo.GetByVersion(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("target version %s: %w", version, err)
	}

	// Parse the target manifest
	manifest, err := unmarshalManifest(target.ManifestJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal target version: %w", err)
	}

	// Store as a new version (forward-only audit trail)
	// Rollbacks don't re-validate, so no graph is available.
	// expectedSeq=0: rollbacks always overwrite (no optimistic lock check).
	return s.StoreManifestVersion(ctx, manifest, appliedBy, applyJobID, ApplyStatusApplied, nil, 0)
}

// EntityToProto converts a VersionEntity to its protobuf representation.
func EntityToProto(entity *VersionEntity) (*controlplanev1.ManifestVersion, error) {
	manifest, err := unmarshalManifest(entity.ManifestJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest JSON: %w", err)
	}

	mv := &controlplanev1.ManifestVersion{
		Id:             entity.ID.String(),
		Version:        entity.Version,
		Manifest:       manifest,
		AppliedAt:      timestamppb.New(entity.AppliedAt),
		AppliedBy:      entity.AppliedBy,
		ApplyStatus:    toProtoApplyStatus(entity.ApplyStatus),
		CreatedAt:      timestamppb.New(entity.CreatedAt),
		SequenceNumber: entity.SequenceNumber,
	}

	if entity.ApplyJobID != nil {
		jobIDStr := entity.ApplyJobID.String()
		mv.ApplyJobId = &jobIDStr
	}
	if entity.DiffSummary != nil {
		mv.DiffSummary = entity.DiffSummary
	}
	if entity.RelationshipGraph != nil {
		mv.RelationshipGraph = entity.RelationshipGraph
	}
	if entity.Checksum != nil {
		mv.Checksum = entity.Checksum
	}
	if entity.Source != nil {
		mv.Source = entity.Source
	}
	if entity.ResourcePath != nil {
		mv.ResourcePath = entity.ResourcePath
	}

	return mv, nil
}

// generateDiffSummary creates a diff summary comparing the given manifest
// against the previous applied version.
func (s *HistoryService) generateDiffSummary(ctx context.Context, newManifest *controlplanev1.Manifest) (string, error) {
	previous, err := s.repo.GetLatestApplied(ctx)
	if err != nil {
		return "", err
	}

	oldManifest, err := unmarshalManifest(previous.ManifestJSON)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal previous manifest: %w", err)
	}

	return diffManifests(oldManifest, newManifest), nil
}

// unmarshalManifest deserializes a Manifest from its JSON representation.
func unmarshalManifest(jsonStr string) (*controlplanev1.Manifest, error) {
	manifest := &controlplanev1.Manifest{}
	if err := protojson.Unmarshal([]byte(jsonStr), manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// toProtoApplyStatus converts internal ApplyStatus to protobuf ApplyStatus.
func toProtoApplyStatus(status ApplyStatus) controlplanev1.ApplyStatus {
	switch status {
	case ApplyStatusApplied:
		return controlplanev1.ApplyStatus_APPLY_STATUS_APPLIED
	case ApplyStatusFailed:
		return controlplanev1.ApplyStatus_APPLY_STATUS_FAILED
	case ApplyStatusRolledBack:
		return controlplanev1.ApplyStatus_APPLY_STATUS_ROLLED_BACK
	default:
		return controlplanev1.ApplyStatus_APPLY_STATUS_UNSPECIFIED
	}
}

// diffManifests generates a human-readable diff between two manifests.
func diffManifests(prev, next *controlplanev1.Manifest) string {
	var changes []string

	// Compare metadata
	if prev.GetMetadata().GetName() != next.GetMetadata().GetName() {
		changes = append(changes, fmt.Sprintf("Metadata name changed: %q -> %q",
			prev.GetMetadata().GetName(), next.GetMetadata().GetName()))
	}
	if prev.GetMetadata().GetIndustry() != next.GetMetadata().GetIndustry() {
		changes = append(changes, fmt.Sprintf("Metadata industry changed: %q -> %q",
			prev.GetMetadata().GetIndustry(), next.GetMetadata().GetIndustry()))
	}

	// Compare instruments
	prevInstruments := instrumentMap(prev.GetInstruments())
	nextInstruments := instrumentMap(next.GetInstruments())
	diffItems("Instrument", prevInstruments, nextInstruments, &changes)

	// Compare account types
	prevAccounts := accountTypeMap(prev.GetAccountTypes())
	nextAccounts := accountTypeMap(next.GetAccountTypes())
	diffItems("Account type", prevAccounts, nextAccounts, &changes)

	// Compare valuation rules
	prevRuleCount := len(prev.GetValuationRules())
	nextRuleCount := len(next.GetValuationRules())
	if prevRuleCount != nextRuleCount {
		changes = append(changes, fmt.Sprintf("Valuation rules: %d -> %d", prevRuleCount, nextRuleCount))
	}

	// Compare sagas
	prevSagas := sagaMap(prev.GetSagas())
	nextSagas := sagaMap(next.GetSagas())
	diffItems("Saga", prevSagas, nextSagas, &changes)

	// Compare version
	if prev.GetVersion() != next.GetVersion() {
		changes = append(changes, fmt.Sprintf("Schema version changed: %s -> %s",
			prev.GetVersion(), next.GetVersion()))
	}

	if len(changes) == 0 {
		return "No changes detected"
	}

	return strings.Join(changes, "; ")
}

func instrumentMap(instruments []*controlplanev1.InstrumentDefinition) map[string]string {
	m := make(map[string]string, len(instruments))
	for _, inst := range instruments {
		m[inst.GetCode()] = inst.GetName()
	}
	return m
}

func accountTypeMap(accounts []*controlplanev1.AccountTypeDefinition) map[string]string {
	m := make(map[string]string, len(accounts))
	for _, acct := range accounts {
		m[acct.GetCode()] = acct.GetName()
	}
	return m
}

func sagaMap(sagas []*controlplanev1.SagaDefinition) map[string]string {
	m := make(map[string]string, len(sagas))
	for _, s := range sagas {
		m[s.GetName()] = s.GetTrigger()
	}
	return m
}

func diffItems(itemType string, prev, next map[string]string, changes *[]string) {
	var added []string
	for key := range next {
		if _, exists := prev[key]; !exists {
			added = append(added, key)
		}
	}
	sort.Strings(added)
	for _, key := range added {
		*changes = append(*changes, fmt.Sprintf("%s added: %s", itemType, key))
	}

	var removed []string
	for key := range prev {
		if _, exists := next[key]; !exists {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	for _, key := range removed {
		*changes = append(*changes, fmt.Sprintf("%s removed: %s", itemType, key))
	}
}
