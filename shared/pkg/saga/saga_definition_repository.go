package saga

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GormSagaDefinitionRepository persists pinned saga definitions using GORM.
//
// FindOrCreate enforces the immutability invariant: a given (name, version) pair
// maps to exactly one immutable row, identified by the script's SHA-256 hash.
// Concurrent FindOrCreate calls for the same (name, version, script) are safe
// thanks to the unique index on (name, version) - the loser of a race retries
// the lookup and returns the winner's row.
type GormSagaDefinitionRepository struct {
	db *gorm.DB
}

// NewSagaDefinitionRepository constructs a GORM-backed repository.
func NewSagaDefinitionRepository(db *gorm.DB) *GormSagaDefinitionRepository {
	return &GormSagaDefinitionRepository{db: db}
}

// Compile-time interface check.
var _ SagaDefinitionRepository = (*GormSagaDefinitionRepository)(nil)

// FindByID returns the definition with the given ID or ErrSagaDefinitionNotFound.
func (r *GormSagaDefinitionRepository) FindByID(ctx context.Context, id uuid.UUID) (*SagaDefinition, error) {
	if id == uuid.Nil {
		return nil, ErrSagaDefinitionNotFound
	}
	var def SagaDefinition
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&def).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSagaDefinitionNotFound
		}
		return nil, fmt.Errorf("find saga definition by id: %w", err)
	}
	return &def, nil
}

// FindOrCreate returns an existing matching row or inserts a new one.
//
// Lookup order:
//  1. Existing row with (name, version) AND matching script hash -> reuse.
//  2. Existing row with (name, version) but DIFFERENT hash -> ErrSagaDefinitionHashMismatch.
//  3. No row with (name, version) -> insert new row.
//
// Concurrency: the unique index on (name, version) ensures only one INSERT wins.
// On a race the losing INSERT errors out; we then re-read the winning row and
// validate its hash matches.
func (r *GormSagaDefinitionRepository) FindOrCreate(
	ctx context.Context,
	name string,
	version int,
	script string,
	paramsSchema JSONB,
) (*SagaDefinition, error) {
	hash := ComputeSagaDefinitionScriptHash(script)

	// Step 1: lookup by (name, version).
	existing, err := r.findByNameVersion(ctx, name, version)
	switch {
	case err == nil:
		if existing.ScriptHash != hash {
			return nil, fmt.Errorf("%w: name=%s version=%d (stored=%s incoming=%s)",
				ErrSagaDefinitionHashMismatch, name, version, existing.ScriptHash, hash)
		}
		return existing, nil
	case !errors.Is(err, ErrSagaDefinitionNotFound):
		return nil, err
	}

	// Step 2: insert. If a concurrent caller wins the race, re-read and validate.
	def := &SagaDefinition{
		Name:         name,
		Version:      version,
		Script:       script,
		ParamsSchema: paramsSchema,
		ScriptHash:   hash,
	}
	createErr := r.db.WithContext(ctx).Create(def).Error
	if createErr == nil {
		return def, nil
	}

	// Treat any insert failure as a potential race: re-read and validate.
	// (CockroachDB returns a unique-violation error class on the duplicate; we
	// don't pattern-match on driver error codes to stay portable.)
	raceWinner, lookupErr := r.findByNameVersion(ctx, name, version)
	if lookupErr != nil {
		if errors.Is(lookupErr, ErrSagaDefinitionNotFound) {
			return nil, fmt.Errorf("insert saga definition: %w", createErr)
		}
		return nil, fmt.Errorf("insert saga definition (%w) and re-read failed: %w", createErr, lookupErr)
	}
	if raceWinner.ScriptHash != hash {
		return nil, fmt.Errorf("%w: name=%s version=%d (stored=%s incoming=%s)",
			ErrSagaDefinitionHashMismatch, name, version, raceWinner.ScriptHash, hash)
	}
	return raceWinner, nil
}

// findByNameVersion returns the row for (name, version) or
// (nil, ErrSagaDefinitionNotFound) when no row exists.
func (r *GormSagaDefinitionRepository) findByNameVersion(
	ctx context.Context,
	name string,
	version int,
) (*SagaDefinition, error) {
	var def SagaDefinition
	err := r.db.WithContext(ctx).
		Where("name = ? AND version = ?", name, version).
		First(&def).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSagaDefinitionNotFound
		}
		return nil, fmt.Errorf("lookup saga definition by name+version: %w", err)
	}
	return &def, nil
}
