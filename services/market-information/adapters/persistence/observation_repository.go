// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/shopspring/decimal"
)

// ObservationRepository implements domain.ObservationRepository using PostgreSQL.
// Supports bi-temporal queries with quality ladder resolution.
type ObservationRepository struct {
	baseRepository
}

// NewObservationRepository creates a new PostgreSQL observation repository.
func NewObservationRepository(pool *pgxpool.Pool) *ObservationRepository {
	return &ObservationRepository{
		baseRepository: newBaseRepository(pool),
	}
}

// Record persists a new market price observation.
// This is an append-only operation - observations are never updated in place.
// When a higher quality observation is recorded, it automatically marks lower quality
// observations for the same resolution key as superseded.
func (r *ObservationRepository) Record(ctx context.Context, obs domain.MarketPriceObservation) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		userID := getUserFromContext(ctx)

		// First, resolve dataset definition ID from code
		dataSetDefID, err := r.resolveDataSetDefinitionID(ctx, tx, obs.DataSetCode())
		if err != nil {
			return err
		}

		// Insert the new observation
		insertQuery := `
			INSERT INTO market_price_observation (
				id, dataset_definition_id, data_source_id, resolution_key,
				observed_at, valid_from, valid_to, created_at, created_by,
				quality, observation_context, numeric_value, text_value,
				superseded_by, causation_id
			) VALUES (
				$1, $2, $3, $4,
				$5, $6, $7, $8, $9,
				$10, $11, $12, $13,
				$14, $15
			)`

		validFrom := nullTimeValue(obs.ValidFrom())
		validTo := nullTimeValue(obs.ValidTo())
		supersededBy := nullUUIDValue(obs.SupersededBy())
		causationID := nullUUIDNonNil(obs.CausationID())

		_, err = tx.Exec(ctx, insertQuery,
			obs.ID(),
			dataSetDefID,
			obs.SourceID(),
			obs.ResolutionKey(),
			obs.ObservedAt(),
			validFrom,
			validTo,
			obs.CreatedAt(),
			userID,
			obs.QualityLevel().Int(),
			[]byte("{}"), // observation_context - placeholder
			obs.Value(),
			nil, // text_value - numeric observations use numeric_value
			supersededBy,
			causationID,
		)
		if err != nil {
			return fmt.Errorf("failed to insert observation: %w", err)
		}

		// Mark lower quality observations as superseded
		// Only supersede observations with lower quality for the same resolution key
		supersedQuery := `
			UPDATE market_price_observation
			SET superseded_by = $1
			WHERE dataset_definition_id = $2
				AND resolution_key = $3
				AND superseded_by IS NULL
				AND id != $1
				AND quality < $4`

		_, err = tx.Exec(ctx, supersedQuery,
			obs.ID(),
			dataSetDefID,
			obs.ResolutionKey(),
			obs.QualityLevel().Int(),
		)
		if err != nil {
			return fmt.Errorf("failed to supersede lower quality observations: %w", err)
		}

		return nil
	})
}

// FindByID retrieves an observation by its unique identifier.
// Returns ErrObservationNotFound if the observation does not exist.
func (r *ObservationRepository) FindByID(ctx context.Context, id uuid.UUID) (domain.MarketPriceObservation, error) {
	var result domain.MarketPriceObservation

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
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
			WHERE o.id = $1`

		obs, err := r.scanObservation(ctx, tx, query, id)
		if err != nil {
			return err
		}

		result = obs
		return nil
	})

	return result, err
}

// Query retrieves observations matching the query criteria.
// Returns an empty slice if no observations match.
// Results are ordered by ObservedAt descending (most recent first).
func (r *ObservationRepository) Query(ctx context.Context, query domain.ObservationQuery) ([]domain.MarketPriceObservation, error) {
	var results []domain.MarketPriceObservation

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// First, resolve dataset definition ID from code
		dataSetDefID, err := r.resolveDataSetDefinitionID(ctx, tx, query.DataSetCode)
		if err != nil {
			return err
		}

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

		// Filter by resolution key
		if query.ResolutionKey != nil {
			sqlQuery += fmt.Sprintf(" AND o.resolution_key = $%d", argPos)
			args = append(args, *query.ResolutionKey)
			argPos++
		}

		// Filter by observed time range
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

		// Filter by quality level
		if query.QualityLevel != nil {
			sqlQuery += fmt.Sprintf(" AND o.quality = $%d", argPos)
			args = append(args, query.QualityLevel.Int())
			argPos++
		}

		// Filter superseded observations
		if !query.IncludeSuperseded {
			sqlQuery += " AND o.superseded_by IS NULL"
		}

		// Order by observed_at descending
		sqlQuery += " ORDER BY o.observed_at DESC"

		// Apply limit
		limit := query.Limit
		if limit <= 0 {
			limit = 100 // Default limit
		}
		sqlQuery += fmt.Sprintf(" LIMIT $%d", argPos)
		args = append(args, limit)

		rows, err := tx.Query(ctx, sqlQuery, args...)
		if err != nil {
			return fmt.Errorf("failed to query observations: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			obs, err := r.scanObservationFromRows(rows)
			if err != nil {
				return err
			}
			results = append(results, obs)
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating observations: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

// GetLatest retrieves the most recent non-superseded observation
// for a specific dataset and resolution key combination.
// Uses the quality ladder for precedence: VERIFIED > ACTUAL > ESTIMATE.
// Returns ErrObservationNotFound if no matching observation exists.
func (r *ObservationRepository) GetLatest(ctx context.Context, dataSetCode string, resolutionKey string) (domain.MarketPriceObservation, error) {
	var result domain.MarketPriceObservation

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// First, resolve dataset definition ID from code
		dataSetDefID, err := r.resolveDataSetDefinitionID(ctx, tx, dataSetCode)
		if err != nil {
			return err
		}

		// Use the bi-temporal index for efficient query
		// Order by quality DESC (VERIFIED > ACTUAL > ESTIMATE), then observed_at DESC, then trust_level DESC
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
			ORDER BY o.quality DESC, o.observed_at DESC, s.trust_level DESC, o.created_at DESC
			LIMIT 1`

		obs, err := r.scanObservation(ctx, tx, query, dataSetDefID, resolutionKey)
		if err != nil {
			return err
		}

		result = obs
		return nil
	})

	return result, err
}

// RetrieveObservation retrieves the best observation for a resolution key at a specific knowledge time.
// This enables bi-temporal "time travel" queries - what did we know at a given point in time?
// Uses the quality ladder with trust level tiebreaker:
// ORDER BY quality DESC, observed_at DESC, trust_level DESC, created_at DESC
//
// Parameters:
//   - dataSetCode: The dataset code to query
//   - resolutionKey: The unique resolution key (e.g., "EUR/USD")
//   - knowledgeBaseTime: The point in time to query "what was known". Use time.Time{} for current knowledge.
func (r *ObservationRepository) RetrieveObservation(ctx context.Context, dataSetCode string, resolutionKey string, knowledgeBaseTime time.Time) (domain.MarketPriceObservation, error) {
	var result domain.MarketPriceObservation

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// First, resolve dataset definition ID from code
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

		// If knowledgeBaseTime is zero, use current time
		kbt := knowledgeBaseTime
		if kbt.IsZero() {
			kbt = time.Now()
		}

		obs, err := r.scanObservation(ctx, tx, query, dataSetDefID, resolutionKey, kbt)
		if err != nil {
			return err
		}

		result = obs
		return nil
	})

	return result, err
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

// Ensure ObservationRepository implements domain.ObservationRepository.
var _ domain.ObservationRepository = (*ObservationRepository)(nil)
