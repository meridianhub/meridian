package differ

import (
	"context"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// ManifestVersion represents a stored snapshot of a previously applied manifest.
type ManifestVersion struct {
	ID        string
	Version   string
	Manifest  *controlplanev1.Manifest
	AppliedAt time.Time
	AppliedBy string
}

// ManifestVersionStore persists and retrieves applied manifest snapshots.
// Each successful apply stores a new version, following Kubernetes
// last-applied-configuration semantics.
type ManifestVersionStore interface {
	// GetLatestApplied returns the most recently applied manifest version.
	// Returns nil, nil if no manifest has been applied yet (first apply).
	GetLatestApplied(ctx context.Context) (*ManifestVersion, error)

	// Save stores a new manifest version after successful application.
	Save(ctx context.Context, manifest *controlplanev1.Manifest, appliedBy string) error
}
