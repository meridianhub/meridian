package saga

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
