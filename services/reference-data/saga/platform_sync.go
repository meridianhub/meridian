// Package saga provides platform saga definition sync functionality.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// versionCommentRegex matches "# Version: X.Y.Z" comments at the start of the script.
var versionCommentRegex = regexp.MustCompile(`(?m)^#\s*Version:\s*(\d+\.\d+\.\d+)\s*$`)

// versionFilenameRegex matches version filenames like "v1.0.0.star".
var versionFilenameRegex = regexp.MustCompile(`^v(\d+\.\d+\.\d+)\.star$`)

// metadataHeaderRegex matches metadata header lines like "# Key: value".
var metadataHeaderRegex = regexp.MustCompile(`(?m)^#\s*(\w+):\s*(.+)$`)

// ErrEmbeddedScriptNotFound is returned when a saga script is not found in the embedded filesystem.
var ErrEmbeddedScriptNotFound = errors.New("embedded script not found")

// ErrMetadataMismatch is returned when a .star file's metadata header does not match
// the filename-derived saga name or version.
var ErrMetadataMismatch = errors.New("metadata header mismatch")

// ScriptMetadata represents parsed header metadata from a .star file.
type ScriptMetadata struct {
	SagaName        string
	Version         string
	PreviousVersion *string
	ChangeSummary   string
	Author          string
	Date            string
}

// PlatformSagaDefinition represents a saga definition in the platform table.
type PlatformSagaDefinition struct {
	ID              uuid.UUID
	Name            string
	Version         string
	Script          string
	PreviousVersion *string
	DisplayName     string
	Description     string
}

// PlatformSync synchronizes embedded saga definitions to the platform_saga_definition table.
type PlatformSync struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewPlatformSync creates a new PlatformSync instance.
func NewPlatformSync(pool *pgxpool.Pool) *PlatformSync {
	return &PlatformSync{
		pool:   pool,
		logger: slog.Default().With("component", "platform_saga_sync"),
	}
}

// SyncPlatformDefaults synchronizes all embedded saga definitions to the platform table.
// This method is idempotent - running it multiple times with the same versions has no effect.
//
// The sync logic uses INSERT-only semantics to preserve version history:
//  1. Scans per-saga versioned directories (defaults/<saga>/vX.Y.Z.star)
//  2. Extracts version from filename and validates against header metadata
//  3. For each saga version, checks if the exact (name, version) pair already exists
//  4. Inserts new versions as DEPRECATED, then activates the latest per saga
//
// Old versions are never overwritten or deleted. This guarantees that running
// saga instances which pinned a PlatformSagaVersionID at execution time can
// always replay using the exact script they started with.
func (s *PlatformSync) SyncPlatformDefaults(ctx context.Context) error {
	s.logger.Info("starting platform saga sync")

	// Load all embedded sagas from versioned directories
	sagas, err := s.loadEmbeddedSagas()
	if err != nil {
		return fmt.Errorf("load embedded sagas: %w", err)
	}

	s.logger.Info("loaded embedded sagas", "count", len(sagas))

	// Sync each saga version
	var syncedCount, skippedCount int
	for _, saga := range sagas {
		synced, err := s.syncSaga(ctx, saga)
		if err != nil {
			return fmt.Errorf("sync saga %s@%s: %w", saga.Name, saga.Version, err)
		}
		if synced {
			syncedCount++
		} else {
			skippedCount++
		}
	}

	// Activate latest version per saga, deprecate older ones (skip if no new versions)
	if syncedCount > 0 {
		if err := s.activateLatestVersions(ctx); err != nil {
			return fmt.Errorf("activate latest versions: %w", err)
		}
	}

	s.logger.Info("platform saga sync completed",
		"synced", syncedCount,
		"skipped", skippedCount,
		"total", len(sagas))

	return nil
}

// loadEmbeddedSagas discovers sagas from per-saga versioned directories.
func (s *PlatformSync) loadEmbeddedSagas() ([]PlatformSagaDefinition, error) {
	sagas := make([]PlatformSagaDefinition, 0)

	// Read top-level saga directories (deposit/, withdrawal/, etc.)
	entries, err := defaultSagas.ReadDir("defaults")
	if err != nil {
		return nil, fmt.Errorf("read defaults directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sagaDir := entry.Name()
		sagaName := sagaNameFromDir(sagaDir)

		// Read version files within saga directory
		versions, err := s.loadSagaVersions(sagaDir, sagaName)
		if err != nil {
			return nil, fmt.Errorf("load versions for %s: %w", sagaDir, err)
		}

		sagas = append(sagas, versions...)
	}

	return sagas, nil
}

// loadSagaVersions reads all vX.Y.Z.star files for a specific saga.
func (s *PlatformSync) loadSagaVersions(sagaDir, sagaName string) ([]PlatformSagaDefinition, error) {
	dirPath := path.Join("defaults", sagaDir)
	entries, err := defaultSagas.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read saga directory %s: %w", dirPath, err)
	}

	versions := make([]PlatformSagaDefinition, 0)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := versionFilenameRegex.FindStringSubmatch(entry.Name())
		if matches == nil {
			s.logger.Warn("skipping non-version file", "saga", sagaDir, "file", entry.Name())
			continue
		}

		version := matches[1] // Extract "1.0.0" from "v1.0.0.star"

		filePath := path.Join(dirPath, entry.Name())
		content, err := defaultSagas.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filePath, err)
		}

		script := strings.TrimSpace(string(content))

		// Parse metadata header and validate against filename-derived values
		metadata := parseMetadataHeader(script)
		if metadata.Version != "" && metadata.Version != version {
			return nil, fmt.Errorf("%w: version header=%s filename=%s for %s", ErrMetadataMismatch, metadata.Version, version, sagaName)
		}
		if metadata.SagaName != "" && metadata.SagaName != sagaName {
			return nil, fmt.Errorf("%w: saga header=%s dir=%s", ErrMetadataMismatch, metadata.SagaName, sagaName)
		}

		// Generate deterministic UUID based on name AND version
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga."+sagaName+"."+version))

		versions = append(versions, PlatformSagaDefinition{
			ID:              id,
			Name:            sagaName,
			Version:         version,
			Script:          script,
			PreviousVersion: metadata.PreviousVersion,
			DisplayName:     humanizeName(sagaName),
			Description:     fmt.Sprintf("Platform default saga for %s operations.", strings.ReplaceAll(sagaDir, "_", " ")),
		})
	}

	return versions, nil
}

// syncSaga syncs a single saga version to the database using INSERT-only semantics.
// Returns true if the saga was inserted, false if skipped (already exists).
//
// New versions are inserted with status=DEPRECATED. The activateLatestVersions
// method then sets the highest version per saga to ACTIVE.
func (s *PlatformSync) syncSaga(ctx context.Context, saga PlatformSagaDefinition) (bool, error) {
	// Check if exact (name, version) already exists
	var existingID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM public.platform_saga_definition
		WHERE name = $1 AND version = $2
	`, saga.Name, saga.Version).Scan(&existingID)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("query existing version: %w", err)
	}

	if err == nil {
		// Exact (name, version) already exists - skip (idempotent)
		s.logger.Debug("skipping saga (version already exists)",
			"name", saga.Name,
			"version", saga.Version)
		return false, nil
	}

	// INSERT new row for this version (as DEPRECATED; activateLatestVersions will promote)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition
			(id, name, version, script, status, previous_version, display_name, description)
		VALUES ($1, $2, $3, $4, 'DEPRECATED', $5, $6, $7)
	`, saga.ID, saga.Name, saga.Version, saga.Script, saga.PreviousVersion, saga.DisplayName, saga.Description)
	if err != nil {
		// Handle race condition: another process may have inserted the same (name, version)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			s.logger.Debug("saga version inserted by concurrent process",
				"name", saga.Name,
				"version", saga.Version)
			return false, nil
		}
		return false, fmt.Errorf("insert saga: %w", err)
	}

	s.logger.Info("inserted platform saga version",
		"name", saga.Name,
		"version", saga.Version,
		"previous", saga.PreviousVersion)
	return true, nil
}

// activateLatestVersions sets status=ACTIVE for the highest version per saga
// and DEPRECATED for all other versions in a single statement to avoid a
// transient window where no ACTIVE rows exist.
func (s *PlatformSync) activateLatestVersions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		WITH ranked_versions AS (
			SELECT name, version,
				ROW_NUMBER() OVER (
					PARTITION BY name
					ORDER BY
						COALESCE(NULLIF(split_part(version, '.', 1), ''), '0')::int DESC,
						COALESCE(NULLIF(split_part(version, '.', 2), ''), '0')::int DESC,
						COALESCE(NULLIF(split_part(version, '.', 3), ''), '0')::int DESC
				) as rn
			FROM public.platform_saga_definition
		)
		UPDATE public.platform_saga_definition psd
		SET status = CASE WHEN rv.rn = 1 THEN 'ACTIVE' ELSE 'DEPRECATED' END
		FROM ranked_versions rv
		WHERE psd.name = rv.name
			AND psd.version = rv.version
	`)
	if err != nil {
		return fmt.Errorf("activate latest versions: %w", err)
	}

	return nil
}

// parseMetadataHeader extracts structured metadata from the script header comments.
func parseMetadataHeader(script string) ScriptMetadata {
	meta := ScriptMetadata{}
	matches := metadataHeaderRegex.FindAllStringSubmatch(script, -1)

	for _, match := range matches {
		key := match[1]
		value := strings.TrimSpace(match[2])
		switch key {
		case "Saga":
			meta.SagaName = value
		case "Version":
			meta.Version = value
		case "Previous":
			if value != "" && value != "none" {
				meta.PreviousVersion = &value
			}
		case "Changed":
			meta.ChangeSummary = value
		case "Author":
			meta.Author = value
		case "Date":
			meta.Date = value
		}
	}

	return meta
}

// extractVersionFromScript extracts the version from a "# Version: X.Y.Z" comment.
// Returns empty string if no version comment is found.
func extractVersionFromScript(script string) string {
	matches := versionCommentRegex.FindStringSubmatch(script)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// humanizeName converts snake_case to Title Case.
// Example: "current_account_withdrawal" -> "Current Account Withdrawal"
func humanizeName(name string) string {
	words := strings.Split(name, "_")
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
