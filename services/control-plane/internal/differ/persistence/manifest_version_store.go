// Package persistence provides PostgreSQL/CockroachDB storage for
// manifest version snapshots used by the differ.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/protobuf/encoding/protojson"
)

// ErrInvalidManifestJSON is returned when marshaled manifest JSON is not valid.
var ErrInvalidManifestJSON = errors.New("marshaled manifest is not valid JSON")

// PostgresManifestVersionStore implements differ.ManifestVersionStore
// using PostgreSQL/CockroachDB as the backing store.
type PostgresManifestVersionStore struct {
	pool *pgxpool.Pool
}

// NewPostgresManifestVersionStore creates a store backed by a pgx connection pool.
func NewPostgresManifestVersionStore(pool *pgxpool.Pool) *PostgresManifestVersionStore {
	return &PostgresManifestVersionStore{pool: pool}
}

// GetLatestApplied returns the most recently applied manifest version.
// Returns nil, nil if no manifest has been applied yet.
func (s *PostgresManifestVersionStore) GetLatestApplied(ctx context.Context) (*differ.ManifestVersion, error) {
	var result *differ.ManifestVersion
	err := s.withReadTransaction(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, version, manifest_json, applied_at, applied_by
			FROM manifest_version
			ORDER BY applied_at DESC
			LIMIT 1
		`)

		var (
			id           string
			version      string
			manifestJSON []byte
			appliedAt    time.Time
			appliedBy    string
		)

		err := row.Scan(&id, &version, &manifestJSON, &appliedAt, &appliedBy)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("scan manifest version: %w", err)
		}

		manifest := &controlplanev1.Manifest{}
		if err := protojson.Unmarshal(manifestJSON, manifest); err != nil {
			return fmt.Errorf("unmarshal manifest JSON: %w", err)
		}

		result = &differ.ManifestVersion{
			ID:        id,
			Version:   version,
			Manifest:  manifest,
			AppliedAt: appliedAt,
			AppliedBy: appliedBy,
		}
		return nil
	})
	return result, err
}

// Save stores a new manifest version after successful application.
func (s *PostgresManifestVersionStore) Save(ctx context.Context, manifest *controlplanev1.Manifest, appliedBy string) error {
	return s.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		version := manifest.GetVersion()

		manifestJSON, err := protojson.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("marshal manifest to JSON: %w", err)
		}

		if !json.Valid(manifestJSON) {
			return ErrInvalidManifestJSON
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO manifest_version (version, manifest_json, applied_by)
			VALUES ($1, $2, $3)
		`, version, manifestJSON, appliedBy)
		if err != nil {
			return fmt.Errorf("insert manifest version: %w", err)
		}

		return nil
	})
}

// setSearchPath sets the PostgreSQL search_path for tenant isolation.
// Returns tenant.ErrMissingTenantContext if the context has no tenant ID.
func (s *PostgresManifestVersionStore) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.ErrMissingTenantContext
	}

	schemaName := pgx.Identifier{tenantID.SchemaName()}.Sanitize()
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// withReadTransaction executes a read-only function within a transaction with tenant scoping.
func (s *PostgresManifestVersionStore) withReadTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := s.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// withWriteTransaction executes a write function within a transaction with tenant scoping.
func (s *PostgresManifestVersionStore) withWriteTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := s.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
