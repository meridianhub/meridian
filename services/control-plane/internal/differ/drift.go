package differ

import (
	"context"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// DriftDetector compares the current database state against a manifest
// to surface manual changes made outside the manifest apply workflow.
type DriftDetector interface {
	// DetectDrift compares the given manifest against current DB state
	// and returns warnings for any discrepancies found.
	DetectDrift(ctx context.Context, lastApplied *controlplanev1.Manifest) ([]DriftWarning, error)
}

// NoOpDriftDetector returns no drift warnings. Used when drift detection
// is not configured or during testing.
type NoOpDriftDetector struct{}

// DetectDrift returns no warnings (drift detection disabled).
func (n *NoOpDriftDetector) DetectDrift(_ context.Context, _ *controlplanev1.Manifest) ([]DriftWarning, error) {
	return nil, nil
}
