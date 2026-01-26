// Package saga provides bi-temporal version pinning for saga instances.
//
// When a saga instance starts from a definition that uses platform_ref,
// the platform saga version is pinned so that replay always uses the exact
// same script, even if the platform definition is updated in the meantime.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	pkgsaga "github.com/meridianhub/meridian/shared/pkg/saga"
)

// VersionPinning provides methods to pin and verify platform saga versions
// for bi-temporal replay determinism.
type VersionPinning struct {
	registry *PostgresRegistry
	logger   *slog.Logger
}

// NewVersionPinning creates a new VersionPinning service.
func NewVersionPinning(registry *PostgresRegistry) *VersionPinning {
	return &VersionPinning{
		registry: registry,
		logger:   slog.Default().With("component", "saga_version_pinning"),
	}
}

// PinVersion resolves the saga definition and pins the platform version on the saga instance.
// This should be called when starting a new saga instance.
//
// The pinning logic:
//  1. Resolve the saga definition using GetActive (with platform fallback)
//  2. Compute the SHA-256 hash of the resolved script
//  3. If the definition uses platform_ref, record the platform version ID
//  4. Set PlatformSagaVersionID and ScriptHashAtStart on the instance
//
// Returns the resolved definition with ResolvedScript populated.
func (vp *VersionPinning) PinVersion(ctx context.Context, instance *pkgsaga.SagaInstance, sagaName string) (*Definition, error) {
	// Resolve the active definition with platform fallback
	def, err := vp.registry.GetActive(ctx, sagaName)
	if err != nil {
		return nil, fmt.Errorf("resolve active saga %q: %w", sagaName, err)
	}

	// Compute script hash from the resolved script (either tenant override or platform fallback)
	resolvedScript := def.ResolvedScript
	if resolvedScript == "" {
		return nil, fmt.Errorf("%w: saga %q resolved to empty script", ErrNoScriptSource, sagaName)
	}

	scriptHash := ComputeScriptHash(resolvedScript)

	// Pin version on the instance
	instance.SagaDefinitionID = def.ID
	instance.SagaName = def.Name
	instance.SagaVersion = def.Version
	instance.ScriptHashAtStart = scriptHash

	// If the definition uses platform fallback, pin the platform version
	if def.PlatformRef != nil {
		instance.PlatformSagaVersionID = def.PlatformRef
		vp.logger.Info("pinned platform saga version for new instance",
			"saga_name", sagaName,
			"saga_definition_id", def.ID,
			"platform_ref", def.PlatformRef,
			"script_hash", scriptHash)
	} else {
		vp.logger.Info("pinned tenant saga version for new instance",
			"saga_name", sagaName,
			"saga_definition_id", def.ID,
			"script_hash", scriptHash)
	}

	return def, nil
}

// ResolveForReplay resolves the exact script that should be used for saga replay.
// This ensures replay determinism by using the pinned version.
//
// Resolution logic:
//  1. If PlatformSagaVersionID is set, load script from the pinned platform version
//  2. Otherwise, load the script from the saga definition itself
//  3. Verify the script hash matches ScriptHashAtStart
//
// Returns the script content for replay, or an error if the pinned version is
// not found or the hash doesn't match.
func (vp *VersionPinning) ResolveForReplay(ctx context.Context, instance *pkgsaga.SagaInstance) (string, error) {
	var script string

	if instance.PlatformSagaVersionID != nil {
		// Load from pinned platform version
		platformDef, err := vp.registry.GetPlatformSagaByID(ctx, *instance.PlatformSagaVersionID)
		if err != nil {
			if errors.Is(err, ErrPlatformDefinitionNotFound) {
				return "", fmt.Errorf("%w: platform_saga_version_id=%s",
					ErrPinnedVersionNotFound, instance.PlatformSagaVersionID)
			}
			return "", fmt.Errorf("load pinned platform version %s: %w",
				instance.PlatformSagaVersionID, err)
		}
		script = platformDef.Script

		vp.logger.Debug("resolved pinned platform script for replay",
			"instance_id", instance.ID,
			"platform_saga_version_id", instance.PlatformSagaVersionID,
			"platform_saga_name", platformDef.Name)
	} else {
		// Load from saga definition directly
		def, err := vp.registry.GetByID(ctx, instance.SagaDefinitionID)
		if err != nil {
			return "", fmt.Errorf("load saga definition for replay: %w", err)
		}
		script = def.ResolvedScript
		if script == "" {
			script = def.Script
		}
	}

	if script == "" {
		return "", fmt.Errorf("%w: instance_id=%s, definition_id=%s",
			ErrNoScriptSource, instance.ID, instance.SagaDefinitionID)
	}

	// Verify script hash if one was recorded at start
	if instance.ScriptHashAtStart != "" {
		if err := VerifyScriptHash(script, instance.ScriptHashAtStart); err != nil {
			vp.logger.Error("script hash mismatch during replay",
				"instance_id", instance.ID,
				"expected_hash", instance.ScriptHashAtStart,
				"actual_hash", ComputeScriptHash(script))
			return "", err
		}
	}

	return script, nil
}

// GetResolvedDefinition retrieves a saga definition with its fully resolved script.
// This is a convenience method that combines GetByID with platform fallback resolution.
func (vp *VersionPinning) GetResolvedDefinition(ctx context.Context, id uuid.UUID) (*Definition, error) {
	def, err := vp.registry.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// If no resolved script, the definition has no script source
	if def.ResolvedScript == "" && def.Script == "" {
		return nil, fmt.Errorf("%w: definition_id=%s", ErrNoScriptSource, id)
	}

	return def, nil
}
