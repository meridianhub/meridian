package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/applier"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
)

// EnsurePlatformSaga upserts the apply_manifest saga definition into
// public.platform_saga_definition. Idempotent — safe to call multiple times.
func EnsurePlatformSaga(ctx context.Context, pool *pgxpool.Pool) error {
	return applier.NewBootstrap(pool).EnsurePlatformSaga(ctx)
}

// ManifestValidationResult holds the outcome of manifest validation.
type ManifestValidationResult struct {
	Valid    bool
	Errors   []ManifestValidationError
	Warnings []ManifestValidationError
}

// ManifestValidationError represents a single validation finding.
type ManifestValidationError struct {
	Path         string
	Code         string
	Message      string
	ResourceType string
	ResourceID   string
}

// ValidateManifest validates a manifest against the schema and business rules.
func ValidateManifest(mf *controlplanev1.Manifest, prev *controlplanev1.Manifest) (*ManifestValidationResult, error) {
	v, err := validator.New()
	if err != nil {
		return nil, fmt.Errorf("create validator: %w", err)
	}

	internal := v.Validate(mf, prev)
	result := &ManifestValidationResult{Valid: internal.Valid}
	for _, e := range internal.Errors {
		result.Errors = append(result.Errors, ManifestValidationError{
			Path: e.Path, Code: e.Code, Message: e.Message,
			ResourceType: e.ResourceType, ResourceID: e.ResourceID,
		})
	}
	for _, w := range internal.Warnings {
		result.Warnings = append(result.Warnings, ManifestValidationError{
			Path: w.Path, Code: w.Code, Message: w.Message,
			ResourceType: w.ResourceType, ResourceID: w.ResourceID,
		})
	}
	return result, nil
}
