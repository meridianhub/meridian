// Package persistence observation query and scan helpers extracted from observation_repository.go.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
)

// Query retrieves observations matching the query criteria with cursor-based pagination.
// Returns the observations, a next page token (empty if no more results), and any error.
// Results are ordered by created_at descending (most recent first) for cursor consistency.
// Returns ErrInvalidPageToken (wrapped as domain.ErrInvalidPageToken) if the pageToken format is invalid.
func (r *ObservationRepository) Query(ctx context.Context, query domain.ObservationQuery) ([]domain.MarketPriceObservation, string, error) {
	// Apply pagination defaults and limits
	pageSize := query.Limit
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	// Parse cursor token
	cursorTime, cursorID, err := parseCursorToken(query.PageToken)
	if err != nil {
		return nil, "", domain.ErrInvalidPageToken
	}

	var results []domain.MarketPriceObservation
	var nextPageToken string

	err = r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		dataSetDefID, err := r.resolveDataSetDefinitionID(ctx, tx, query.DataSetCode)
		if err != nil {
			return err
		}

		sqlQuery, args := buildObservationFilterQuery(dataSetDefID, query, cursorTime, cursorID, pageSize)

		rows, err := tx.Query(ctx, sqlQuery, args...)
		if err != nil {
			return fmt.Errorf("failed to query observations: %w", err)
		}
		defer rows.Close()

		observations, err := r.scanObservationRows(rows)
		if err != nil {
			return err
		}

		results, nextPageToken = paginateObservations(observations, pageSize)
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	return results, nextPageToken, nil
}

// observationWithMeta pairs an observation with metadata needed for cursor pagination.
type observationWithMeta struct {
	obs       domain.MarketPriceObservation
	createdAt time.Time
	id        uuid.UUID
}

// buildObservationFilterQuery constructs the SQL query and args for observation filtering,
// applying resolution key, time range, quality, superseded, and cursor filters.
func buildObservationFilterQuery(
	dataSetDefID uuid.UUID,
	query domain.ObservationQuery,
	cursorTime time.Time,
	cursorID uuid.UUID,
	pageSize int,
) (string, []interface{}) {
	sqlQuery := `
		SELECT o.id, o.dataset_definition_id, o.data_source_id, o.resolution_key,
			o.observed_at, o.valid_from, o.valid_to, o.created_at,
			o.quality, o.numeric_value, o.text_value,
			o.superseded_by, o.causation_id,
			d.code as dataset_code,
			s.trust_level
		FROM market_price_observation o
		JOIN dataset_definition d ON o.dataset_definition_id = d.id
		JOIN data_source s ON o.data_source_id = s.id
		WHERE o.dataset_definition_id = $1`

	args := []interface{}{dataSetDefID}
	argPos := 2

	if query.ResolutionKey != nil {
		sqlQuery += fmt.Sprintf(" AND o.resolution_key = $%d", argPos)
		args = append(args, *query.ResolutionKey)
		argPos++
	}

	if query.ObservedAfter != nil {
		sqlQuery += fmt.Sprintf(" AND o.observed_at > $%d", argPos)
		args = append(args, *query.ObservedAfter)
		argPos++
	}
	if query.ObservedBefore != nil {
		sqlQuery += fmt.Sprintf(" AND o.observed_at < $%d", argPos)
		args = append(args, *query.ObservedBefore)
		argPos++
	}

	if query.QualityLevel != nil {
		sqlQuery += fmt.Sprintf(" AND o.quality = $%d", argPos)
		args = append(args, query.QualityLevel.Int())
		argPos++
	}

	if !query.IncludeSuperseded {
		sqlQuery += " AND o.superseded_by IS NULL"
	}

	if !cursorTime.IsZero() {
		sqlQuery += fmt.Sprintf(" AND (date_trunc('second', o.created_at) < $%d OR (date_trunc('second', o.created_at) = $%d AND o.id < $%d))",
			argPos, argPos+1, argPos+2)
		args = append(args, cursorTime, cursorTime, cursorID)
		argPos += 3
	}

	sqlQuery += " ORDER BY date_trunc('second', o.created_at) DESC, o.id DESC"
	sqlQuery += fmt.Sprintf(" LIMIT $%d", argPos)
	args = append(args, pageSize+1)

	return sqlQuery, args
}

// scanObservationRows scans all rows into observationWithMeta slices.
func (r *ObservationRepository) scanObservationRows(rows pgx.Rows) ([]observationWithMeta, error) {
	var observations []observationWithMeta
	for rows.Next() {
		obs, err := r.scanObservationFromRows(rows)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observationWithMeta{
			obs:       obs,
			createdAt: obs.CreatedAt(),
			id:        obs.ID(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating observations: %w", err)
	}
	return observations, nil
}

// paginateObservations trims results to pageSize and generates a next page token if needed.
func paginateObservations(observations []observationWithMeta, pageSize int) ([]domain.MarketPriceObservation, string) {
	var nextPageToken string
	hasMore := len(observations) > pageSize
	if hasMore {
		observations = observations[:pageSize]
		last := observations[len(observations)-1]
		nextPageToken = formatCursorToken(last.createdAt, last.id)
	}

	results := make([]domain.MarketPriceObservation, 0, len(observations))
	for _, o := range observations {
		results = append(results, o.obs)
	}
	return results, nextPageToken
}

// GetLatest retrieves the most recent non-superseded observation
// for a specific dataset and resolution key combination.
// Uses the quality ladder for precedence: VERIFIED > ACTUAL > ESTIMATE.
// For shared datasets, implements hierarchical lookup: tenant-first, then master fallback.
// Returns ErrObservationNotFound if no matching observation exists.
func (r *ObservationRepository) GetLatest(ctx context.Context, dataSetCode string, resolutionKey string) (domain.MarketPriceObservation, error) {
	// Delegate to RetrieveObservation with zero time (current knowledge)
	return r.RetrieveObservation(ctx, dataSetCode, resolutionKey, time.Time{})
}

// RetrieveObservation retrieves the best observation for a resolution key at a specific knowledge time.
// This enables bi-temporal "time travel" queries - what did we know at a given point in time?
// Uses the quality ladder with trust level tiebreaker:
// ORDER BY quality DESC, observed_at DESC, trust_level DESC, created_at DESC
//
// For shared datasets, implements hierarchical lookup:
// 1. Query tenant-specific schema first
// 2. If shared dataset and not found, fall through to master tenant schema
// 3. For RESTRICTED datasets, verify tenant has active entitlements
//
// Parameters:
//   - dataSetCode: The dataset code to query
//   - resolutionKey: The unique resolution key (e.g., "EUR/USD")
//   - knowledgeBaseTime: The point in time to query "what was known". Use time.Time{} for current knowledge.
func (r *ObservationRepository) RetrieveObservation(ctx context.Context, dataSetCode string, resolutionKey string, knowledgeBaseTime time.Time) (domain.MarketPriceObservation, error) {
	// If knowledgeBaseTime is zero, use current time
	kbt := knowledgeBaseTime
	if kbt.IsZero() {
		kbt = time.Now()
	}

	// Step 1: Check if dataset is shared (determines whether fallback is allowed)
	// IMPORTANT: Dataset metadata lookups must use master tenant context, not the tenant-scoped context.
	// The dataset_definition table (is_shared, access_level) is the authoritative source of truth
	// for sharing configuration and lives in the master schema. Even though test helpers may copy
	// dataset definitions to tenant schemas, the master schema is canonical for configuration decisions.
	//
	// There is a benign race condition here: if dataset config is updated between this check and the
	// actual observation query, we may use stale is_shared/access_level values. This is acceptable
	// because dataset config changes are rare and eventual consistency is sufficient.
	tenantID, hasTenantContext := tenant.FromContext(ctx)
	masterCtx := tenant.WithTenant(ctx, r.masterTenantID)
	dataset, err := r.datasetRepo.FindByCode(masterCtx, dataSetCode)
	if err != nil {
		return domain.MarketPriceObservation{}, err
	}

	// Step 2: For RESTRICTED datasets with tenant context, verify entitlements upfront.
	// This check applies regardless of where the data is stored (tenant or master schema).
	// For RESTRICTED datasets, access is only granted if the tenant has an active entitlement.
	// PUBLIC datasets skip this check entirely. PRIVATE datasets are tenant-isolated by schema.
	//
	// Security note: When no tenant context exists (system/background jobs), RESTRICTED datasets
	// are accessible without entitlement checks. This is intentional - system processes need
	// full data access for operations like ingestion, reconciliation, and reporting. Tenant context
	// should always be set for user-facing API requests to enforce proper access control.
	if dataset.AccessLevel() == domain.AccessLevelRestricted && hasTenantContext {
		hasAccess, err := r.checkTenantAccess(ctx, tenantID, dataSetCode)
		if err != nil {
			return domain.MarketPriceObservation{}, err
		}
		if !hasAccess {
			return domain.MarketPriceObservation{}, domain.ErrAccessDenied
		}
	}

	// Step 3: Query current schema first (tenant-specific or default/public)
	obs, err := r.queryObservationInSchema(ctx, dataSetCode, resolutionKey, kbt)
	if err == nil {
		return obs, nil // Found in current schema
	}
	// Non-NotFound errors indicate real database issues (connection, permissions, etc.).
	// We intentionally do NOT fall back to master for real errors - this prevents masking
	// underlying issues that should be surfaced and investigated.
	if !errors.Is(err, domain.ErrObservationNotFound) {
		return domain.MarketPriceObservation{}, err
	}
	// Not found in current schema - proceed to hierarchical fallback if applicable

	// Step 4: If shared dataset and not found in tenant schema, try master fallback.
	// When no tenant context exists (e.g., background jobs, system queries), we're already
	// querying the default schema (likely master), so no fallback is needed.
	if dataset.IsShared() && hasTenantContext {
		// Fall through to master tenant schema (masterCtx was created with r.masterTenantID above)
		return r.queryObservationInSchema(masterCtx, dataSetCode, resolutionKey, kbt)
	}

	// Not found (either not shared, or already queried master fallback, or no tenant context)
	return domain.MarketPriceObservation{}, domain.ErrObservationNotFound
}

// queryObservationInSchema performs the actual database query in the current tenant scope.
// This method respects the search_path set by baseRepository's transaction wrapper.
func (r *ObservationRepository) queryObservationInSchema(ctx context.Context, dataSetCode string, resolutionKey string, knowledgeBaseTime time.Time) (domain.MarketPriceObservation, error) {
	var result domain.MarketPriceObservation

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Resolve dataset definition ID from code
		dataSetDefID, err := r.resolveDataSetDefinitionID(ctx, tx, dataSetCode)
		if err != nil {
			return err
		}

		// Bi-temporal query: find the best observation that was known at knowledgeBaseTime
		// JOIN data_source for trust_level in ordering
		query := `
			SELECT o.id, o.dataset_definition_id, o.data_source_id, o.resolution_key,
				o.observed_at, o.valid_from, o.valid_to, o.created_at,
				o.quality, o.numeric_value, o.text_value,
				o.superseded_by, o.causation_id,
				d.code as dataset_code,
				s.trust_level
			FROM market_price_observation o
			JOIN dataset_definition d ON o.dataset_definition_id = d.id
			JOIN data_source s ON o.data_source_id = s.id
			WHERE o.dataset_definition_id = $1
				AND o.resolution_key = $2
				AND o.superseded_by IS NULL
				AND o.created_at <= $3
			ORDER BY o.quality DESC, o.observed_at DESC, s.trust_level DESC, o.created_at DESC
			LIMIT 1`

		obs, err := r.scanObservation(ctx, tx, query, dataSetDefID, resolutionKey, knowledgeBaseTime)
		if err != nil {
			return err
		}

		result = obs
		return nil
	})

	return result, err
}

// checkTenantAccess verifies tenant has rights to access shared dataset.
// Queries the tenant_data_entitlements table to check for active entitlements.
//
// IMPORTANT: This query intentionally uses the public schema (not tenant-scoped).
// The tenant_data_entitlements table is a global registry that lives in the public schema,
// not replicated per tenant. Using pool.QueryRow directly ensures we query public.tenant_data_entitlements.
//
// Note: This check runs outside the observation query transaction. There's a small TOCTOU window
// where entitlement could be revoked between this check and data access. This is acceptable
// because entitlement revocations are rare and eventual consistency is sufficient for this use case.
func (r *ObservationRepository) checkTenantAccess(ctx context.Context, tenantID tenant.TenantID, dataSetCode string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM public.tenant_data_entitlements
			WHERE tenant_id = $1 AND dataset_code = $2 AND is_active = TRUE
			AND (expires_at IS NULL OR expires_at > NOW())
		)`

	var hasAccess bool
	err := r.pool.QueryRow(ctx, query, tenantID.String(), dataSetCode).Scan(&hasAccess)
	if err != nil {
		return false, fmt.Errorf("failed to check tenant access: %w", err)
	}

	return hasAccess, nil
}

// resolveDataSetDefinitionID looks up the dataset definition ID from code.
func (r *ObservationRepository) resolveDataSetDefinitionID(ctx context.Context, tx pgx.Tx, code string) (uuid.UUID, error) {
	var id uuid.UUID
	query := `
		SELECT id FROM dataset_definition
		WHERE code = $1 AND status = 'ACTIVE' AND deleted_at IS NULL
		ORDER BY version DESC
		LIMIT 1`

	err := tx.QueryRow(ctx, query, code).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, domain.ErrDataSetNotFound
		}
		return uuid.Nil, fmt.Errorf("failed to resolve dataset definition ID: %w", err)
	}

	return id, nil
}

// scanObservation executes a query and scans a single observation.
func (r *ObservationRepository) scanObservation(ctx context.Context, tx pgx.Tx, query string, args ...interface{}) (domain.MarketPriceObservation, error) {
	var (
		id                  uuid.UUID
		dataSetDefinitionID uuid.UUID
		dataSourceID        uuid.UUID
		resolutionKey       string
		observedAt          time.Time
		validFrom           sql.NullTime
		validTo             sql.NullTime
		createdAt           time.Time
		quality             int
		numericValue        decimal.NullDecimal
		textValue           sql.NullString
		supersededBy        uuid.NullUUID
		causationID         uuid.NullUUID
		dataSetCode         string
		trustLevel          int
	)

	err := tx.QueryRow(ctx, query, args...).Scan(
		&id,
		&dataSetDefinitionID,
		&dataSourceID,
		&resolutionKey,
		&observedAt,
		&validFrom,
		&validTo,
		&createdAt,
		&quality,
		&numericValue,
		&textValue,
		&supersededBy,
		&causationID,
		&dataSetCode,
		&trustLevel,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.MarketPriceObservation{}, domain.ErrObservationNotFound
		}
		return domain.MarketPriceObservation{}, fmt.Errorf("failed to scan observation: %w", err)
	}

	return r.buildObservation(
		id, dataSourceID, resolutionKey, observedAt, validFrom, validTo,
		createdAt, quality, numericValue, supersededBy, causationID,
		dataSetCode, trustLevel,
	), nil
}

// scanObservationFromRows scans an observation from rows.
func (r *ObservationRepository) scanObservationFromRows(rows pgx.Rows) (domain.MarketPriceObservation, error) {
	var (
		id                  uuid.UUID
		dataSetDefinitionID uuid.UUID
		dataSourceID        uuid.UUID
		resolutionKey       string
		observedAt          time.Time
		validFrom           sql.NullTime
		validTo             sql.NullTime
		createdAt           time.Time
		quality             int
		numericValue        decimal.NullDecimal
		textValue           sql.NullString
		supersededBy        uuid.NullUUID
		causationID         uuid.NullUUID
		dataSetCode         string
		trustLevel          int
	)

	err := rows.Scan(
		&id,
		&dataSetDefinitionID,
		&dataSourceID,
		&resolutionKey,
		&observedAt,
		&validFrom,
		&validTo,
		&createdAt,
		&quality,
		&numericValue,
		&textValue,
		&supersededBy,
		&causationID,
		&dataSetCode,
		&trustLevel,
	)
	if err != nil {
		return domain.MarketPriceObservation{}, fmt.Errorf("failed to scan observation row: %w", err)
	}

	return r.buildObservation(
		id, dataSourceID, resolutionKey, observedAt, validFrom, validTo,
		createdAt, quality, numericValue, supersededBy, causationID,
		dataSetCode, trustLevel,
	), nil
}

// buildObservation constructs a domain observation from scanned values.
func (r *ObservationRepository) buildObservation(
	id uuid.UUID,
	dataSourceID uuid.UUID,
	resolutionKey string,
	observedAt time.Time,
	validFrom sql.NullTime,
	validTo sql.NullTime,
	createdAt time.Time,
	quality int,
	numericValue decimal.NullDecimal,
	supersededBy uuid.NullUUID,
	causationID uuid.NullUUID,
	dataSetCode string,
	trustLevel int,
) domain.MarketPriceObservation {
	builder := domain.NewMarketPriceObservationBuilder().
		WithID(id).
		WithDataSetCode(dataSetCode).
		WithSourceID(dataSourceID).
		WithResolutionKey(resolutionKey).
		WithObservedAt(observedAt).
		WithCreatedAt(createdAt).
		WithQualityLevel(domain.QualityLevel(quality)).
		WithTrustLevel(trustLevel)

	// Set value
	if numericValue.Valid {
		builder.WithValue(numericValue.Decimal)
	}

	// Set valid time bounds
	if validFrom.Valid {
		builder.WithValidFrom(validFrom.Time)
	}
	if validTo.Valid {
		builder.WithValidTo(validTo.Time)
	}

	// Set supersession info
	if supersededBy.Valid {
		supersededByPtr := supersededBy.UUID
		builder.WithSupersededBy(&supersededByPtr)
	}

	// Set causation ID
	if causationID.Valid {
		builder.WithCausationID(causationID.UUID)
	}

	return builder.Build()
}
