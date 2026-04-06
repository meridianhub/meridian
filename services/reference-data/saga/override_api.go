// Package saga provides the CreateTenantOverride API for replacing platform
// default sagas with custom tenant scripts.
//
// When a tenant is provisioned, saga_definition rows are created as system sagas
// with scripts copied directly from the platform defaults. This API allows tenants
// to replace these system defaults with custom scripts when they need different
// saga behavior.
//
// Validation rules:
//   - The override script must be meaningfully different from the platform default
//     (similarity check via Levenshtein distance)
//   - The saga must be a system default (either via platform_ref or is_system=true)
//   - A saga that already has a custom script cannot be overridden again through this API
//   - The override creates a new version of the saga (is_system=false) and
//     records audit metadata (override_reason, platform_version_at_override)
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// migrationSimilarityThreshold is the threshold for considering a tenant script
// identical to the platform default during migration. Intentionally stricter (0.95)
// than the override threshold (0.90) because migration targets near-identical copies.
const migrationSimilarityThreshold = 0.95

// Override error types.
var (
	// ErrAlreadyOverridden is returned when attempting to override a saga that
	// already has a custom script (is not using platform reference).
	ErrAlreadyOverridden = errors.New("saga already has a custom override")

	// ErrNotPlatformReferenced is returned when attempting to override a saga
	// that does not use a platform reference.
	ErrNotPlatformReferenced = errors.New("saga does not use a platform reference")

	// ErrOverrideReasonRequired is returned when no override reason is provided.
	ErrOverrideReasonRequired = errors.New("override reason is required for audit trail")

	// ErrOverrideScriptEmpty is returned when the override script is empty.
	ErrOverrideScriptEmpty = errors.New("override script must not be empty")
)

// OverrideRequest contains the parameters for creating a tenant override.
type OverrideRequest struct {
	// SagaName is the name of the saga to override.
	SagaName string

	// Script is the custom Starlark script for the override.
	Script string

	// OverrideReason explains why the tenant needs a custom script.
	OverrideReason string

	// SimilarityThreshold overrides the default similarity threshold (0.0-1.0).
	// If zero, DefaultSimilarityThreshold is used.
	SimilarityThreshold float64
}

// OverrideResult contains the outcome of a tenant override operation.
type OverrideResult struct {
	// OverrideDefinition is the newly created saga definition with the custom script.
	OverrideDefinition *Definition

	// PlatformVersion is the version of the platform saga that was active at override time.
	PlatformVersion string

	// SimilarityRatio is the similarity between the override and platform scripts.
	SimilarityRatio float64
}

// OverrideService provides the tenant override API.
type OverrideService struct {
	pool     *pgxpool.Pool
	registry *PostgresRegistry
	logger   *slog.Logger
}

// NewOverrideService creates a new override service.
func NewOverrideService(pool *pgxpool.Pool, registry *PostgresRegistry) *OverrideService {
	return &OverrideService{
		pool:     pool,
		registry: registry,
		logger:   slog.Default().With("component", "saga_override"),
	}
}

// CreateTenantOverride replaces a platform-referenced saga with a custom script.
//
// The process:
//  1. Look up the existing saga definition (must have platform_ref)
//  2. Load the platform script for similarity comparison
//  3. Reject if override is too similar to platform default
//  4. Create a new saga version with the custom script (DRAFT status)
//  5. Record audit metadata: override_reason, platform_version_at_override
//
// The new override is created as a DRAFT - the caller must activate it separately.
func (s *OverrideService) CreateTenantOverride(ctx context.Context, req OverrideRequest) (*OverrideResult, error) {
	logger := s.logger.With("saga_name", req.SagaName)

	// Validate input
	if err := s.validateRequest(req); err != nil {
		return nil, err
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, tenant.ErrMissingTenantContext
	}

	logger = logger.With("tenant_id", tenantID.String())

	// Look up the current active saga definition
	existingDef, err := s.registry.GetActive(ctx, req.SagaName)
	if err != nil {
		return nil, fmt.Errorf("look up active saga %q: %w", req.SagaName, err)
	}

	// Validate override eligibility and check script similarity
	platformDef, simResult, err := s.validateOverrideEligibility(ctx, existingDef, req)
	if err != nil {
		return nil, err
	}

	logger.Info("similarity check passed",
		"similarity_ratio", simResult.Ratio,
		"threshold", req.SimilarityThreshold)

	// Create the override as a new version
	newVersion := existingDef.Version + 1

	overrideDef := &Definition{
		Name:                      req.SagaName,
		Version:                   newVersion,
		Script:                    req.Script,
		DisplayName:               existingDef.DisplayName,
		Description:               existingDef.Description,
		OverrideReason:            req.OverrideReason,
		PlatformVersionAtOverride: platformDef.Version,
	}

	if err := s.registry.CreateDraft(ctx, overrideDef); err != nil {
		return nil, fmt.Errorf("create override draft: %w", err)
	}

	logger.Info("tenant override created",
		"override_id", overrideDef.ID,
		"version", overrideDef.Version,
		"platform_version", platformDef.Version,
		"similarity_ratio", simResult.Ratio)

	return &OverrideResult{
		OverrideDefinition: overrideDef,
		PlatformVersion:    platformDef.Version,
		SimilarityRatio:    simResult.Ratio,
	}, nil
}

// validateOverrideEligibility checks that the saga is a platform default (either via platform_ref
// or as a system saga with a copied script), not already overridden, and that the override script
// is sufficiently different from the platform default.
func (s *OverrideService) validateOverrideEligibility(ctx context.Context, existingDef *Definition, req OverrideRequest) (*PlatformSagaDefinition, *SimilarityResult, error) {
	if !existingDef.IsSystem && existingDef.Script != "" {
		return nil, nil, fmt.Errorf("%w: saga %q already has custom script", ErrAlreadyOverridden, req.SagaName)
	}

	// Look up the platform saga: by ID if platform_ref is set, otherwise by name
	// for system sagas that were seeded with scripts copied directly.
	var platformDef *PlatformSagaDefinition
	var err error
	if existingDef.PlatformRef != nil {
		platformDef, err = s.registry.GetPlatformSagaByID(ctx, *existingDef.PlatformRef)
		if err != nil {
			return nil, nil, fmt.Errorf("load platform saga for comparison: %w", err)
		}
	} else if existingDef.IsSystem {
		platformDef, err = s.registry.GetPlatformSagaByName(ctx, req.SagaName)
		if err != nil {
			return nil, nil, fmt.Errorf("load platform saga by name for comparison: %w", err)
		}
	} else {
		return nil, nil, fmt.Errorf("%w: saga %q (id=%s)", ErrNotPlatformReferenced, req.SagaName, existingDef.ID)
	}

	threshold := req.SimilarityThreshold
	if threshold == 0 {
		threshold = DefaultSimilarityThreshold
	}

	simResult := ComputeSimilarityWithThreshold(req.Script, platformDef.Script, threshold)
	if simResult.TooSimilar {
		return nil, nil, fmt.Errorf(
			"%w: override is %.1f%% similar to platform default (threshold: %.1f%%)",
			ErrScriptTooSimilar,
			simResult.Ratio*100,
			threshold*100,
		)
	}

	return platformDef, &simResult, nil
}

// validateRequest checks the override request for required fields.
func (s *OverrideService) validateRequest(req OverrideRequest) error {
	if req.SagaName == "" {
		return ErrNotFound
	}
	if req.Script == "" {
		return ErrOverrideScriptEmpty
	}
	if req.OverrideReason == "" {
		return ErrOverrideReasonRequired
	}
	return nil
}

// MigrateToPlatformRef converts existing script-copied saga definitions to
// platform-referenced ones. This is used by the admin migration tool.
//
// For each tenant saga that has a script identical to a platform default:
//  1. Set platform_ref to the matching platform saga
//  2. Clear the script field (set to NULL)
//  3. Set is_system=true
//
// Returns the number of sagas migrated and any errors encountered.
func (s *OverrideService) MigrateToPlatformRef(
	ctx context.Context,
	tenantID tenant.TenantID,
	dryRun bool,
) ([]MigrationResult, error) {
	logger := s.logger.With("tenant_id", tenantID.String(), "dry_run", dryRun)
	logger.Info("starting platform reference migration")

	// Get all platform sagas (read-only, outside the retryable transaction).
	platformSagas, err := s.loadPlatformSagas(ctx)
	if err != nil {
		return nil, fmt.Errorf("load platform sagas: %w", err)
	}

	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())

	var results []MigrationResult
	err = withCRDBRetry(ctx, func() error {
		var txErr error
		results, txErr = s.runMigrationTx(ctx, schemaName, platformSagas, dryRun)
		return txErr
	})
	if err != nil {
		return nil, err
	}

	// Log per-saga outcomes after the transaction has committed successfully,
	// so retried transactions don't produce duplicate log entries.
	for _, r := range results {
		if r.Action == MigrationActionMigrated {
			logger.Info("migrated saga to platform reference",
				"saga_name", r.SagaName,
				"saga_id", r.SagaID,
				"platform_ref", r.PlatformRefID,
				"similarity", r.SimilarityRatio)
		}
	}

	logger.Info("platform reference migration completed",
		"total", len(results),
		"dry_run", dryRun)

	return results, nil
}

// runMigrationTx executes the migration within a single transaction.
func (s *OverrideService) runMigrationTx(
	ctx context.Context,
	schemaName string,
	platformSagas map[string]PlatformSagaDefinition,
	dryRun bool,
) ([]MigrationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", schemaName))
	if err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}

	candidates, err := s.loadTenantSagaCandidates(ctx, tx)
	if err != nil {
		return nil, err
	}

	results := make([]MigrationResult, 0, len(candidates))
	for _, ts := range candidates {
		result, migrateErr := s.evaluateCandidate(ctx, tx, ts, platformSagas, dryRun)
		if migrateErr != nil {
			return nil, migrateErr
		}
		results = append(results, result)
	}

	if !dryRun {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit migration: %w", err)
		}
	}

	return results, nil
}

// withCRDBRetry retries the provided function on CockroachDB serialization errors
// (SQLSTATE 40001). These errors are expected under SERIALIZABLE isolation and are
// safe to retry by re-executing the entire transaction.
func withCRDBRetry(ctx context.Context, fn func() error) error {
	const maxRetries = 5
	backoff := 50 * time.Millisecond

	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if attempt >= maxRetries || !isCRDBRetryError(err) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 2*time.Second {
			backoff = 2 * time.Second
		}
	}
}

// isCRDBRetryError returns true if the error is a CockroachDB serialization conflict
// (SQLSTATE 40001) that can be resolved by retrying the transaction.
func isCRDBRetryError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

// tenantSaga represents a tenant's saga definition candidate for migration.
type tenantSaga struct {
	id          uuid.UUID
	name        string
	version     int
	script      string
	isSystem    bool
	platformRef *uuid.UUID
}

// loadTenantSagaCandidates queries all active saga definitions from the tenant schema.
func (s *OverrideService) loadTenantSagaCandidates(ctx context.Context, tx pgx.Tx) ([]tenantSaga, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, name, version, script, is_system, platform_ref
		FROM saga_definition
		WHERE status = 'ACTIVE'
		ORDER BY name, version`)
	if err != nil {
		return nil, fmt.Errorf("query tenant sagas: %w", err)
	}
	defer rows.Close()

	var candidates []tenantSaga
	for rows.Next() {
		var ts tenantSaga
		var script *string
		err := rows.Scan(&ts.id, &ts.name, &ts.version, &script, &ts.isSystem, &ts.platformRef)
		if err != nil {
			return nil, fmt.Errorf("scan tenant saga: %w", err)
		}
		if script != nil {
			ts.script = *script
		}
		candidates = append(candidates, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenant sagas: %w", err)
	}
	return candidates, nil
}

// evaluateCandidate determines the migration action for a single tenant saga.
// Does not log outcomes — the caller logs after the transaction commits to avoid
// duplicate entries on CockroachDB transaction retries.
func (s *OverrideService) evaluateCandidate(
	ctx context.Context,
	tx pgx.Tx,
	ts tenantSaga,
	platformSagas map[string]PlatformSagaDefinition,
	dryRun bool,
) (MigrationResult, error) {
	if ts.platformRef != nil {
		return MigrationResult{
			SagaName: ts.name,
			SagaID:   ts.id,
			Action:   MigrationActionSkipped,
			Reason:   "already has platform_ref",
		}, nil
	}

	platformSaga, ok := platformSagas[ts.name]
	if !ok {
		return MigrationResult{
			SagaName: ts.name,
			SagaID:   ts.id,
			Action:   MigrationActionSkipped,
			Reason:   "no matching platform saga",
		}, nil
	}

	simResult := ComputeSimilarityWithThreshold(ts.script, platformSaga.Script, migrationSimilarityThreshold)
	if !simResult.TooSimilar {
		return MigrationResult{
			SagaName:        ts.name,
			SagaID:          ts.id,
			Action:          MigrationActionSkipped,
			Reason:          fmt.Sprintf("script differs from platform (%.1f%% similar)", simResult.Ratio*100),
			SimilarityRatio: simResult.Ratio,
		}, nil
	}

	if dryRun {
		return MigrationResult{
			SagaName:        ts.name,
			SagaID:          ts.id,
			Action:          MigrationActionWouldMigrate,
			Reason:          fmt.Sprintf("script matches platform (%.1f%% similar)", simResult.Ratio*100),
			SimilarityRatio: simResult.Ratio,
			PlatformRefID:   &platformSaga.ID,
		}, nil
	}

	_, err := tx.Exec(ctx, `
		UPDATE saga_definition
		SET script = NULL, platform_ref = $1, is_system = true, updated_at = now()
		WHERE id = $2`,
		platformSaga.ID, ts.id)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("migrate saga %s: %w", ts.name, err)
	}

	return MigrationResult{
		SagaName:        ts.name,
		SagaID:          ts.id,
		Action:          MigrationActionMigrated,
		Reason:          fmt.Sprintf("script matches platform (%.1f%% similar)", simResult.Ratio*100),
		SimilarityRatio: simResult.Ratio,
		PlatformRefID:   &platformSaga.ID,
	}, nil
}

// MigrationResult represents the outcome of migrating a single saga definition.
type MigrationResult struct {
	// SagaName is the name of the saga.
	SagaName string

	// SagaID is the UUID of the saga definition.
	SagaID uuid.UUID

	// Action describes what happened: "migrated", "skipped", "would_migrate" (dry-run).
	Action string

	// Reason explains why the action was taken.
	Reason string

	// SimilarityRatio is the similarity between tenant and platform scripts.
	SimilarityRatio float64

	// PlatformRefID is the platform saga ID (set when migrated or would_migrate).
	PlatformRefID *uuid.UUID
}

// loadPlatformSagas loads all platform saga definitions indexed by name.
func (s *OverrideService) loadPlatformSagas(ctx context.Context) (map[string]PlatformSagaDefinition, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, version, script, display_name, description
		FROM public.platform_saga_definition
		ORDER BY name,
			(string_to_array(version, '.'))[1]::INT,
			(string_to_array(version, '.'))[2]::INT,
			(string_to_array(version, '.'))[3]::INT`)
	if err != nil {
		return nil, fmt.Errorf("query platform sagas: %w", err)
	}
	defer rows.Close()

	result := make(map[string]PlatformSagaDefinition)
	for rows.Next() {
		var psd PlatformSagaDefinition
		var displayName, description *string
		err := rows.Scan(&psd.ID, &psd.Name, &psd.Version, &psd.Script, &displayName, &description)
		if err != nil {
			return nil, fmt.Errorf("scan platform saga: %w", err)
		}
		if displayName != nil {
			psd.DisplayName = *displayName
		}
		if description != nil {
			psd.Description = *description
		}
		result[psd.Name] = psd
	}

	return result, rows.Err()
}
