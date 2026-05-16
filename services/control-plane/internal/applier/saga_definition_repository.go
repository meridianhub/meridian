package applier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// SagaDefinitionRepository pins resolved saga definitions in the control-plane
// database so the resume path can load the exact script that ran originally,
// rather than re-resolving the (potentially updated) manifest state.
//
// This is a pgx-based implementation that operates on the same saga_definitions
// schema as the GORM-managed shared/pkg/saga.GormSagaDefinitionRepository.
type SagaDefinitionRepository struct {
	pool *pgxpool.Pool
}

// NewSagaDefinitionRepository creates a SagaDefinitionRepository backed by the
// given pgxpool. Returns nil if the pool is nil.
func NewSagaDefinitionRepository(pool *pgxpool.Pool) *SagaDefinitionRepository {
	if pool == nil {
		return nil
	}
	return &SagaDefinitionRepository{pool: pool}
}

// FindByID retrieves a saga definition by its immutable ID.
// Returns saga.ErrSagaDefinitionNotFound when no row exists.
func (r *SagaDefinitionRepository) FindByID(ctx context.Context, id uuid.UUID) (*saga.SagaDefinition, error) {
	if id == uuid.Nil {
		return nil, saga.ErrSagaDefinitionNotFound
	}

	def := &saga.SagaDefinition{}
	var paramsRaw []byte

	err := r.pool.QueryRow(ctx,
		`SELECT id, name, version, script, params_schema, script_hash, created_at
		 FROM saga_definitions WHERE id = $1`,
		id,
	).Scan(&def.ID, &def.Name, &def.Version, &def.Script, &paramsRaw, &def.ScriptHash, &def.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, saga.ErrSagaDefinitionNotFound
		}
		return nil, fmt.Errorf("query saga definition by id: %w", err)
	}

	if len(paramsRaw) > 0 {
		var schema saga.JSONB
		if unmarshalErr := json.Unmarshal(paramsRaw, &schema); unmarshalErr != nil {
			return nil, fmt.Errorf("decode saga definition params_schema: %w", unmarshalErr)
		}
		def.ParamsSchema = schema
	}

	return def, nil
}

// FindOrCreate returns an existing row matching (name, version, script hash),
// or inserts a new row if no (name, version) entry exists. Same contract as
// the shared/pkg/saga repository.
//
// Returns saga.ErrSagaDefinitionHashMismatch when (name, version) is present
// but the stored script hash differs from the incoming script.
func (r *SagaDefinitionRepository) FindOrCreate(
	ctx context.Context,
	name, version, script string,
	paramsSchema saga.JSONB,
) (*saga.SagaDefinition, error) {
	hash := saga.ComputeSagaDefinitionScriptHash(script)

	existing, err := r.findByNameVersion(ctx, name, version)
	switch {
	case err == nil:
		if existing.ScriptHash != hash {
			return nil, fmt.Errorf("%w: name=%s version=%s (stored=%s incoming=%s)",
				saga.ErrSagaDefinitionHashMismatch, name, version, existing.ScriptHash, hash)
		}
		return existing, nil
	case !errors.Is(err, saga.ErrSagaDefinitionNotFound):
		return nil, err
	}

	// Serialize params_schema. Empty payloads stored as NULL.
	var paramsBytes []byte
	if paramsSchema != nil {
		b, marshalErr := json.Marshal(paramsSchema)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode params_schema: %w", marshalErr)
		}
		paramsBytes = b
	}

	id := uuid.New()
	createdAt := time.Now().UTC()
	_, insertErr := r.pool.Exec(ctx,
		`INSERT INTO saga_definitions
			(id, name, version, script, params_schema, script_hash, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, name, version, script, paramsBytes, hash, createdAt,
	)
	if insertErr == nil {
		return &saga.SagaDefinition{
			ID:           id,
			Name:         name,
			Version:      version,
			Script:       script,
			ParamsSchema: paramsSchema,
			ScriptHash:   hash,
			CreatedAt:    createdAt,
		}, nil
	}

	// Treat any insert failure as a possible race on the unique (name, version)
	// index: re-read and validate the winner's hash.
	raceWinner, lookupErr := r.findByNameVersion(ctx, name, version)
	if lookupErr != nil {
		if errors.Is(lookupErr, saga.ErrSagaDefinitionNotFound) {
			return nil, fmt.Errorf("insert saga definition: %w", insertErr)
		}
		return nil, fmt.Errorf("insert saga definition (%w) and re-read failed: %w", insertErr, lookupErr)
	}
	if raceWinner.ScriptHash != hash {
		return nil, fmt.Errorf("%w: name=%s version=%s (stored=%s incoming=%s)",
			saga.ErrSagaDefinitionHashMismatch, name, version, raceWinner.ScriptHash, hash)
	}
	return raceWinner, nil
}

func (r *SagaDefinitionRepository) findByNameVersion(
	ctx context.Context,
	name, version string,
) (*saga.SagaDefinition, error) {
	def := &saga.SagaDefinition{}
	var paramsRaw []byte

	err := r.pool.QueryRow(ctx,
		`SELECT id, name, version, script, params_schema, script_hash, created_at
		 FROM saga_definitions WHERE name = $1 AND version = $2`,
		name, version,
	).Scan(&def.ID, &def.Name, &def.Version, &def.Script, &paramsRaw, &def.ScriptHash, &def.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, saga.ErrSagaDefinitionNotFound
		}
		return nil, fmt.Errorf("lookup saga definition by name+version: %w", err)
	}

	if len(paramsRaw) > 0 {
		var schema saga.JSONB
		if unmarshalErr := json.Unmarshal(paramsRaw, &schema); unmarshalErr != nil {
			return nil, fmt.Errorf("decode saga definition params_schema: %w", unmarshalErr)
		}
		def.ParamsSchema = schema
	}

	return def, nil
}

// Compile-time check that we satisfy the shared interface.
var _ saga.SagaDefinitionRepository = (*SagaDefinitionRepository)(nil)
