// Package saga provides the SagaRegistry implementation backed by PostgreSQL.
//
//meridian:large-file — known oversized file; split tracked in backlog
package saga

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// PostgresRegistry implements SagaRegistry using PostgreSQL.
type PostgresRegistry struct {
	pool      *pgxpool.Pool
	validator Validator
	logger    *slog.Logger
}

// NewPostgresRegistry creates a new PostgreSQL-backed saga registry.
// The validator is optional - if nil, activation will succeed without validation.
func NewPostgresRegistry(pool *pgxpool.Pool, validator Validator) *PostgresRegistry {
	return &PostgresRegistry{
		pool:      pool,
		validator: validator,
		logger:    slog.Default().With("component", "saga_registry"),
	}
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
// In multi-tenant mode, it sets the search_path to the tenant's schema.
func (r *PostgresRegistry) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.ErrMissingTenantContext
	}

	// Quote the schema name to prevent SQL injection
	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())

	// SET LOCAL is transaction-scoped - automatically reverts on commit/rollback
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// withReadTransaction executes a read-only function within a transaction with tenant scoping.
func (r *PostgresRegistry) withReadTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit read transaction: %w", err)
	}

	return nil
}

// withWriteTransaction executes a write function within a transaction with tenant scoping.
func (r *PostgresRegistry) withWriteTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetByID retrieves a specific saga by its UUID, resolving platform fallback if needed.
func (r *PostgresRegistry) GetByID(ctx context.Context, id uuid.UUID) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
			WHERE sd.id = $1`

		row := tx.QueryRow(ctx, query, id)
		def, err := r.scanDefinitionWithFallback(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query saga definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetDefinition retrieves a specific saga by name and version, resolving platform fallback if needed.
func (r *PostgresRegistry) GetDefinition(ctx context.Context, name string, version int) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
			WHERE sd.name = $1 AND sd.version = $2`

		row := tx.QueryRow(ctx, query, name, version)
		def, err := r.scanDefinitionWithFallback(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query saga definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetActive retrieves the active saga for a name using tenant resolution with platform fallback.
// Resolution order:
//  1. Tenant override (is_system=FALSE, status=ACTIVE, highest version)
//     - If tenant saga has platform_ref, resolve script via COALESCE with platform
//  2. Platform default (is_system=TRUE, status=ACTIVE, highest version)
//     - System sagas may also have platform_ref for script inheritance
func (r *PostgresRegistry) GetActive(ctx context.Context, name string) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Step 1: Try tenant override (is_system=FALSE) with platform fallback via LEFT JOIN
		tenantQuery := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
			WHERE sd.name = $1 AND sd.status = 'ACTIVE' AND sd.is_system = FALSE
			ORDER BY sd.version DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, tenantQuery, name)
		def, err := r.scanDefinitionWithFallback(row)
		if err == nil {
			if def.UsedPlatformFallback {
				r.logger.Debug("resolved saga using platform fallback",
					"name", name,
					"saga_id", def.ID,
					"platform_ref", def.PlatformRef)
			} else {
				r.logger.Debug("resolved saga using tenant override",
					"name", name,
					"saga_id", def.ID)
			}
			result = def
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("failed to query tenant saga: %w", err)
		}

		// Step 2: Fall back to platform default (is_system=TRUE) with platform reference support
		platformQuery := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
			WHERE sd.name = $1 AND sd.status = 'ACTIVE' AND sd.is_system = TRUE
			ORDER BY sd.version DESC
			LIMIT 1`

		row = tx.QueryRow(ctx, platformQuery, name)
		def, err = r.scanDefinitionWithFallback(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query platform saga: %w", err)
		}

		r.logger.Debug("resolved saga using platform default",
			"name", name,
			"saga_id", def.ID,
			"is_system", true)
		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ListByStatus retrieves all sagas with the specified status, resolving platform fallback.
func (r *PostgresRegistry) ListByStatus(ctx context.Context, status Status) ([]*Definition, error) {
	var result []*Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
			WHERE sd.status = $1
			ORDER BY sd.name, sd.version DESC`

		rows, err := tx.Query(ctx, query, string(status))
		if err != nil {
			return fmt.Errorf("failed to query sagas by status: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanDefinitionWithFallbackFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, def)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// resolveVersionForTenantOverride bumps the version if a system saga occupies
// the requested version slot. This allows tenant overrides to coexist with
// system sagas that occupy version 1 for the same name in the tenant schema.
func resolveVersionForTenantOverride(ctx context.Context, tx pgx.Tx, def *Definition) error {
	var systemVersion sql.NullInt64
	err := tx.QueryRow(ctx,
		`SELECT version FROM saga_definition WHERE name = $1 AND version = $2 AND is_system = true`,
		def.Name, def.Version,
	).Scan(&systemVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to check system saga version: %w", err)
	}
	if !systemVersion.Valid {
		return nil
	}

	// System saga occupies this version slot; find next available
	var maxVersion sql.NullInt64
	err = tx.QueryRow(ctx,
		`SELECT MAX(version) FROM saga_definition WHERE name = $1`, def.Name,
	).Scan(&maxVersion)
	if err != nil {
		return fmt.Errorf("failed to check existing versions: %w", err)
	}
	if maxVersion.Valid {
		def.Version = int(maxVersion.Int64) + 1
	}
	return nil
}

// CreateDraft creates a new saga definition in DRAFT status.
func (r *PostgresRegistry) CreateDraft(ctx context.Context, def *Definition) error {
	// Reject system saga creation
	if def.IsSystem {
		return ErrSystemSagaReadOnly
	}

	// Require at least one script source
	if def.Script == "" && def.PlatformRef == nil {
		return ErrNoScriptSource
	}

	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// Generate ID if not set
		if def.ID == uuid.Nil {
			def.ID = uuid.New()
		}

		now := time.Now()
		def.CreatedAt = now
		def.UpdatedAt = now
		def.Status = StatusDraft

		if err := resolveVersionForTenantOverride(ctx, tx, def); err != nil {
			return err
		}

		query := `
			INSERT INTO saga_definition (
				id, name, version, script, status, is_system,
				preconditions_expression, display_name, description,
				platform_ref, override_reason, platform_version_at_override,
				created_at, updated_at,
				validation_status, complexity_score, handler_call_count, validated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9,
				$10, $11, $12,
				$13, $14,
				$15, $16, $17, $18
			)`

		// Handle script: if platform_ref is set and no script, pass NULL
		var scriptValue interface{}
		if def.Script == "" && def.PlatformRef != nil {
			scriptValue = nil
		} else {
			scriptValue = def.Script
		}

		// Default validation_status to UNVALIDATED if not set
		validationStatus := def.ValidationStatus
		if validationStatus == "" {
			validationStatus = "UNVALIDATED"
		}

		_, err := tx.Exec(ctx, query,
			def.ID, def.Name, def.Version, scriptValue, string(def.Status), def.IsSystem,
			nullString(def.PreconditionsExpression), nullString(def.DisplayName), nullString(def.Description),
			def.PlatformRef, nullString(def.OverrideReason), nullString(def.PlatformVersionAtOverride),
			def.CreatedAt, def.UpdatedAt,
			validationStatus, def.ComplexityScore, def.HandlerCallCount, def.ValidatedAt,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
				return ErrAlreadyExists
			}
			return fmt.Errorf("failed to insert saga definition: %w", err)
		}

		return nil
	})
}

// UpdateDefinition updates a DRAFT saga definition.
func (r *PostgresRegistry) UpdateDefinition(ctx context.Context, id uuid.UUID, updates *Definition) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// First, check if the saga exists and is not a system saga
		var isSystem bool
		var currentStatus string
		var currentUpdatedAt time.Time

		checkQuery := `SELECT is_system, status, updated_at FROM saga_definition WHERE id = $1`
		err := tx.QueryRow(ctx, checkQuery, id).Scan(&isSystem, &currentStatus, &currentUpdatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check saga: %w", err)
		}

		if isSystem {
			return ErrSystemSagaReadOnly
		}

		if currentStatus != string(StatusDraft) {
			return ErrNotDraft
		}

		// Update the saga
		// Use COALESCE(NULLIF(...)) pattern to preserve existing values when empty string is passed
		updateQuery := `
			UPDATE saga_definition SET
				script = COALESCE(NULLIF($1, ''), script),
				preconditions_expression = COALESCE(NULLIF($2, ''), preconditions_expression),
				display_name = COALESCE(NULLIF($3, ''), display_name),
				description = COALESCE(NULLIF($4, ''), description),
				updated_at = $5
			WHERE id = $6 AND updated_at = $7`

		now := time.Now()
		result, err := tx.Exec(ctx, updateQuery,
			updates.Script,
			updates.PreconditionsExpression,
			updates.DisplayName,
			updates.Description,
			now,
			id, currentUpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to update saga definition: %w", err)
		}

		if result.RowsAffected() == 0 {
			return ErrOptimisticLock
		}

		return nil
	})
}

// ActivateSaga transitions a saga from DRAFT to ACTIVE.
func (r *PostgresRegistry) ActivateSaga(ctx context.Context, id uuid.UUID) error {
	// First get the saga to validate it
	saga, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if saga.IsSystem {
		return ErrSystemSagaReadOnly
	}

	if saga.Status != StatusDraft {
		return ErrNotDraft
	}

	// Reject activation if no script source is resolvable
	if saga.ResolvedScript == "" {
		return ErrNoScriptSource
	}

	// Validate the saga if a validator is configured
	if r.validator != nil {
		if err := r.validator.Validate(ctx, saga); err != nil {
			return errors.Join(ErrValidationFailed, err)
		}
	}

	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		// Re-check status within transaction
		var currentStatus string
		checkQuery := `SELECT status FROM saga_definition WHERE id = $1`
		err := tx.QueryRow(ctx, checkQuery, id).Scan(&currentStatus)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check saga: %w", err)
		}

		if currentStatus != string(StatusDraft) {
			return ErrNotDraft
		}

		// Transition to ACTIVE and record activation timestamp
		updateQuery := `
			UPDATE saga_definition SET
				status = 'ACTIVE',
				activated_at = now(),
				updated_at = now()
			WHERE id = $1`

		_, err = tx.Exec(ctx, updateQuery, id)
		if err != nil {
			return fmt.Errorf("failed to activate saga: %w", err)
		}

		return nil
	})
}

// validateSagaSuccessor checks that the successor exists, is ACTIVE,
// has the same name as the source saga, and is not self-referential.
func validateSagaSuccessor(ctx context.Context, tx pgx.Tx, sagaID uuid.UUID, sagaName string, successorID uuid.UUID) error {
	if successorID == sagaID {
		return ErrSuccessorInvalid
	}

	var successorStatus, successorName string
	err := tx.QueryRow(ctx,
		`SELECT status, name FROM saga_definition WHERE id = $1`, successorID,
	).Scan(&successorStatus, &successorName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSuccessorInvalid
		}
		return fmt.Errorf("failed to validate successor: %w", err)
	}

	if successorStatus != string(StatusActive) || successorName != sagaName {
		return ErrSuccessorInvalid
	}
	return nil
}

// DeprecateSaga transitions a saga from ACTIVE to DEPRECATED.
// If successorID is provided, validates that the successor exists, is ACTIVE,
// has the same name, and is not a self-reference.
func (r *PostgresRegistry) DeprecateSaga(ctx context.Context, id uuid.UUID, successorID *uuid.UUID) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		var isSystem bool
		var currentStatus string
		var sagaName string

		checkQuery := `SELECT is_system, status, name FROM saga_definition WHERE id = $1`
		err := tx.QueryRow(ctx, checkQuery, id).Scan(&isSystem, &currentStatus, &sagaName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check saga: %w", err)
		}

		if isSystem {
			return ErrSystemSagaReadOnly
		}
		if currentStatus != string(StatusActive) {
			return ErrNotActive
		}

		if successorID != nil {
			if err := validateSagaSuccessor(ctx, tx, id, sagaName, *successorID); err != nil {
				return err
			}
		}

		updateQuery := `
			UPDATE saga_definition SET
				status = 'DEPRECATED',
				deprecated_at = now(),
				updated_at = now(),
				successor_id = $2
			WHERE id = $1`

		_, err = tx.Exec(ctx, updateQuery, id, successorID)
		if err != nil {
			return fmt.Errorf("failed to deprecate saga: %w", err)
		}

		return nil
	})
}

// scanDefinitionWithFallback scans a single row from a query that includes platform fallback columns.
// The query must select: resolved_script, sd.script, ...standard columns...,
// platform_ref, override_reason, platform_version_at_override, used_platform_fallback
func (r *PostgresRegistry) scanDefinitionWithFallback(row pgx.Row) (*Definition, error) {
	var def Definition
	var status string
	var resolvedScript string
	var rawScript sql.NullString
	var preconditionsExpr, displayName, description sql.NullString
	var successorID *uuid.UUID
	var platformRef *uuid.UUID
	var overrideReason, platformVersionAtOverride sql.NullString
	var usedPlatformFallback sql.NullBool
	var validationStatus sql.NullString
	var complexityScore, handlerCallCount sql.NullInt64

	err := row.Scan(
		&def.ID, &def.Name, &def.Version,
		&resolvedScript,
		&rawScript,
		&status, &def.IsSystem,
		&preconditionsExpr, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
		&platformRef, &overrideReason, &platformVersionAtOverride,
		&usedPlatformFallback,
		&validationStatus, &complexityScore, &handlerCallCount, &def.ValidatedAt,
	)
	if err != nil {
		return nil, err
	}

	def.Status = Status(status)
	def.SuccessorID = successorID
	def.PlatformRef = platformRef
	def.ResolvedScript = resolvedScript
	def.UsedPlatformFallback = usedPlatformFallback.Valid && usedPlatformFallback.Bool

	// Script stores the tenant's own script (may be empty for platform-ref sagas)
	if rawScript.Valid {
		def.Script = rawScript.String
	}

	if preconditionsExpr.Valid {
		def.PreconditionsExpression = preconditionsExpr.String
	}
	if displayName.Valid {
		def.DisplayName = displayName.String
	}
	if description.Valid {
		def.Description = description.String
	}
	if overrideReason.Valid {
		def.OverrideReason = overrideReason.String
	}
	if platformVersionAtOverride.Valid {
		def.PlatformVersionAtOverride = platformVersionAtOverride.String
	}
	if validationStatus.Valid {
		def.ValidationStatus = validationStatus.String
	}
	if complexityScore.Valid {
		v := int(complexityScore.Int64)
		def.ComplexityScore = &v
	}
	if handlerCallCount.Valid {
		v := int(handlerCallCount.Int64)
		def.HandlerCallCount = &v
	}

	return &def, nil
}

// scanDefinitionWithFallbackFromRows scans from pgx.Rows with platform fallback columns.
func (r *PostgresRegistry) scanDefinitionWithFallbackFromRows(rows pgx.Rows) (*Definition, error) {
	var def Definition
	var status string
	var resolvedScript string
	var rawScript sql.NullString
	var preconditionsExpr, displayName, description sql.NullString
	var successorID *uuid.UUID
	var platformRef *uuid.UUID
	var overrideReason, platformVersionAtOverride sql.NullString
	var usedPlatformFallback sql.NullBool
	var validationStatus sql.NullString
	var complexityScore, handlerCallCount sql.NullInt64

	err := rows.Scan(
		&def.ID, &def.Name, &def.Version,
		&resolvedScript,
		&rawScript,
		&status, &def.IsSystem,
		&preconditionsExpr, &displayName, &description,
		&def.CreatedAt, &def.UpdatedAt, &def.ActivatedAt, &def.DeprecatedAt, &successorID,
		&platformRef, &overrideReason, &platformVersionAtOverride,
		&usedPlatformFallback,
		&validationStatus, &complexityScore, &handlerCallCount, &def.ValidatedAt,
	)
	if err != nil {
		return nil, err
	}

	def.Status = Status(status)
	def.SuccessorID = successorID
	def.PlatformRef = platformRef
	def.ResolvedScript = resolvedScript
	def.UsedPlatformFallback = usedPlatformFallback.Valid && usedPlatformFallback.Bool

	if rawScript.Valid {
		def.Script = rawScript.String
	}

	if preconditionsExpr.Valid {
		def.PreconditionsExpression = preconditionsExpr.String
	}
	if displayName.Valid {
		def.DisplayName = displayName.String
	}
	if description.Valid {
		def.Description = description.String
	}
	if overrideReason.Valid {
		def.OverrideReason = overrideReason.String
	}
	if platformVersionAtOverride.Valid {
		def.PlatformVersionAtOverride = platformVersionAtOverride.String
	}
	if validationStatus.Valid {
		def.ValidationStatus = validationStatus.String
	}
	if complexityScore.Valid {
		v := int(complexityScore.Int64)
		def.ComplexityScore = &v
	}
	if handlerCallCount.Valid {
		v := int(handlerCallCount.Int64)
		def.HandlerCallCount = &v
	}

	return &def, nil
}

// nullString converts a string to sql.NullString, treating empty strings as NULL.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// GetPlatformSagaByID retrieves a platform saga definition from the public schema.
// This is used for version pinning: when a saga instance starts, the current platform
// version ID is recorded so replay always uses the same script.
func (r *PostgresRegistry) GetPlatformSagaByID(ctx context.Context, id uuid.UUID) (*PlatformSagaDefinition, error) {
	query := `
		SELECT id, name, version, script, display_name, description, valid_from, valid_to
		FROM public.platform_saga_definition
		WHERE id = $1`

	row := r.pool.QueryRow(ctx, query, id)

	var psd PlatformSagaDefinition
	var displayName, description sql.NullString
	var validTo *time.Time

	err := row.Scan(
		&psd.ID, &psd.Name, &psd.Version, &psd.Script,
		&displayName, &description,
		&psd.ValidFrom, &validTo,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlatformDefinitionNotFound
		}
		return nil, fmt.Errorf("failed to query platform saga definition: %w", err)
	}

	psd.ValidTo = validTo
	if displayName.Valid {
		psd.DisplayName = displayName.String
	}
	if description.Valid {
		psd.Description = description.String
	}

	return &psd, nil
}

// GetPlatformSagaByName retrieves the latest version of a platform saga definition
// by name from the public schema. When multiple versions exist, returns the one
// with the highest semver version string.
func (r *PostgresRegistry) GetPlatformSagaByName(ctx context.Context, name string) (*PlatformSagaDefinition, error) {
	query := `
		SELECT id, name, version, script, display_name, description, valid_from, valid_to
		FROM public.platform_saga_definition
		WHERE name = $1
		ORDER BY
			split_part(version, '.', 1)::int DESC,
			split_part(version, '.', 2)::int DESC,
			split_part(version, '.', 3)::int DESC
		LIMIT 1`

	row := r.pool.QueryRow(ctx, query, name)

	var psd PlatformSagaDefinition
	var displayName, description sql.NullString
	var validTo *time.Time

	err := row.Scan(
		&psd.ID, &psd.Name, &psd.Version, &psd.Script,
		&displayName, &description,
		&psd.ValidFrom, &validTo,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlatformDefinitionNotFound
		}
		return nil, fmt.Errorf("failed to query platform saga definition by name: %w", err)
	}

	psd.ValidTo = validTo
	if displayName.Valid {
		psd.DisplayName = displayName.String
	}
	if description.Valid {
		psd.Description = description.String
	}

	return &psd, nil
}

// GetPlatformSagaAtTime retrieves the platform saga definition that was active
// for the given saga name at the specified point in time.
//
// This enables historical audit queries: "Which version of saga X was active at time T?"
// The query uses the bitemporal valid_from/valid_to range to find the version where:
//   - valid_from <= asOfTime (version was effective at or before the query time)
//   - valid_to IS NULL OR valid_to > asOfTime (version had not yet been superseded)
//
// Returns ErrPlatformDefinitionNotFound if no version was active at the specified time.
func (r *PostgresRegistry) GetPlatformSagaAtTime(ctx context.Context, sagaName string, asOfTime time.Time) (*PlatformSagaDefinition, error) {
	query := `
		SELECT id, name, version, script, display_name, description, valid_from, valid_to
		FROM public.platform_saga_definition
		WHERE name = $1
			AND valid_from <= $2
			AND (valid_to IS NULL OR valid_to > $2)
		ORDER BY valid_from DESC
		LIMIT 1`

	row := r.pool.QueryRow(ctx, query, sagaName, asOfTime)

	var psd PlatformSagaDefinition
	var displayName, description sql.NullString
	var validTo *time.Time

	err := row.Scan(
		&psd.ID, &psd.Name, &psd.Version, &psd.Script,
		&displayName, &description,
		&psd.ValidFrom, &validTo,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlatformDefinitionNotFound
		}
		return nil, fmt.Errorf("failed to query platform saga at time: %w", err)
	}

	psd.ValidTo = validTo
	if displayName.Valid {
		psd.DisplayName = displayName.String
	}
	if description.Valid {
		psd.Description = description.String
	}

	return &psd, nil
}

// ComputeScriptHash computes a SHA-256 hash of the given script content.
// This is used for bi-temporal pinning: the hash is recorded when a saga instance
// starts and verified during replay to detect script corruption or drift.
func ComputeScriptHash(script string) string {
	hash := sha256.Sum256([]byte(script))
	return hex.EncodeToString(hash[:])
}

// VerifyScriptHash checks if the given script matches the expected hash.
// Returns nil if the hash matches, ErrScriptHashMismatch otherwise.
func VerifyScriptHash(script, expectedHash string) error {
	actual := ComputeScriptHash(script)
	if actual != expectedHash {
		return fmt.Errorf("%w: expected %s, got %s", ErrScriptHashMismatch, expectedHash, actual)
	}
	return nil
}
