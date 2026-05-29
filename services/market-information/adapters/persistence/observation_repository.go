// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ObservationRepository implements domain.ObservationRepository using PostgreSQL.
// Supports bi-temporal queries with quality ladder resolution.
type ObservationRepository struct {
	baseRepository
	datasetRepo    *DataSetRepository
	masterTenantID tenant.TenantID // Pre-parsed at construction for efficiency
}

// NewObservationRepository creates a new PostgreSQL observation repository.
// The masterTenantID is parsed once at construction to avoid repeated parsing on every query.
// Panics if masterTenantID is invalid - this should be caught at startup.
func NewObservationRepository(pool *pgxpool.Pool, datasetRepo *DataSetRepository, masterTenantID string) *ObservationRepository {
	parsedID, err := tenant.NewTenantID(masterTenantID)
	if err != nil {
		panic(fmt.Sprintf("invalid masterTenantID %q: %v", masterTenantID, err))
	}
	return &ObservationRepository{
		baseRepository: newBaseRepository(pool),
		datasetRepo:    datasetRepo,
		masterTenantID: parsedID,
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

// CountByDataset returns the total number of observations for a dataset.
// When includeSuperseded is false, only active (non-superseded) observations are counted.
// Returns 0 (not an error) if the dataset exists but has no observations.
// Returns ErrDataSetNotFound if the dataset does not exist.
func (r *ObservationRepository) CountByDataset(ctx context.Context, dataSetCode string, includeSuperseded bool) (int64, error) {
	var count int64

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Resolve dataset definition ID from code
		dataSetDefID, err := r.resolveDataSetDefinitionID(ctx, tx, dataSetCode)
		if err != nil {
			return err
		}

		query := `
			SELECT COUNT(*)
			FROM market_price_observation
			WHERE dataset_definition_id = $1`

		if !includeSuperseded {
			query += " AND superseded_by IS NULL"
		}

		err = tx.QueryRow(ctx, query, dataSetDefID).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to count observations: %w", err)
		}

		return nil
	})

	return count, err
}

// Ensure ObservationRepository implements domain.ObservationRepository.
var _ domain.ObservationRepository = (*ObservationRepository)(nil)
