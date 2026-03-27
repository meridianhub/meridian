package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// timeFmt is the standard time format used for manifest version timestamps.
const timeFmt = time.RFC3339

// errUnsupportedManifestType is returned by parseManifestInput when the value is not
// a string, json.RawMessage, or map.
var errUnsupportedManifestType = errors.New("manifest must be a YAML/JSON string or object")

// PlanStore abstracts the session plan cache to avoid an import cycle
// between tools and session packages. The session.Session type satisfies
// this interface.
type PlanStore interface {
	// StorePlan hashes the manifest bytes and stores the result. Returns the hash.
	StorePlan(manifest []byte) string
	// ValidatePlan returns true when a plan with the given hash exists and has not expired.
	ValidatePlan(hash string) bool
}

// ManifestApplier is the minimal interface for validating, planning, and applying manifests.
type ManifestApplier interface {
	ApplyManifest(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error)
}

// ManifestHistorian is the minimal interface for querying manifest version history.
type ManifestHistorian interface {
	ListManifestVersions(ctx context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error)
	GetCurrentManifest(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error)
	RollbackManifest(ctx context.Context, req *controlplanev1.RollbackManifestRequest) (*controlplanev1.RollbackManifestResponse, error)
}

// EconomyDeps holds all service clients used by economy design tools.
type EconomyDeps struct {
	Applier   ManifestApplier
	Historian ManifestHistorian
}

// RegisterEconomyTools registers the manifest lifecycle tools onto the SDK server.
// Tools whose required client is nil are silently skipped.
func RegisterEconomyTools(srv *mcp.Server, sess PlanStore, deps EconomyDeps) {
	var candidates []Tool

	if deps.Applier != nil {
		candidates = append(candidates, buildManifestValidateTool(deps.Applier))
		if sess != nil {
			candidates = append(candidates, buildManifestPlanTool(deps.Applier, sess))
			candidates = append(candidates, buildManifestApplyTool(deps.Applier, sess))
		}
	}
	if deps.Historian != nil {
		candidates = append(candidates, buildManifestHistoryTool(deps.Historian))
		candidates = append(candidates, buildEconomyGraphTool(deps.Historian))
		candidates = append(candidates, buildManifestRollbackTool(deps.Historian))
	}

	for _, t := range candidates {
		addTool(srv, t)
	}
}

// manifestJSONToProto converts a JSON manifest object into a controlplanev1.Manifest proto.
func manifestJSONToProto(manifestJSON json.RawMessage) (*controlplanev1.Manifest, error) {
	m := &controlplanev1.Manifest{}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(manifestJSON, m); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}
	return m, nil
}

// parseManifestInput converts the raw manifest field value into JSON suitable for
// manifestJSONToProto. When Manifest is typed as interface{} in the param struct,
// json.Unmarshal decodes JSON strings as string and JSON objects as map[string]interface{}.
// It accepts:
//   - string: parsed as YAML (which is a superset of JSON), then marshaled to JSON
//   - map[string]interface{}: marshaled to JSON
//
// Any other type returns an error.
func parseManifestInput(v interface{}) (json.RawMessage, error) {
	switch val := v.(type) {
	case string:
		// Parse YAML (superset of JSON) into a generic map, then re-encode as JSON.
		var parsed interface{}
		if err := yaml.Unmarshal([]byte(val), &parsed); err != nil {
			return nil, fmt.Errorf("invalid YAML/JSON string: %w", err)
		}
		b, err := json.Marshal(parsed)
		if err != nil {
			return nil, fmt.Errorf("failed to convert YAML to JSON: %w", err)
		}
		return json.RawMessage(b), nil
	case map[string]interface{}:
		b, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal manifest object: %w", err)
		}
		return json.RawMessage(b), nil
	default:
		return nil, errUnsupportedManifestType
	}
}

// canonicalManifestBytes returns deterministic proto-encoded bytes for a manifest.
// This ensures semantically equivalent manifests (differing only in JSON key
// order or whitespace) produce identical hashes.
func canonicalManifestBytes(m *controlplanev1.Manifest) ([]byte, error) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize manifest: %w", err)
	}
	return b, nil
}

// sha256Hex returns the hex-encoded SHA256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
