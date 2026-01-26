// Package saga provides platform saga definition sync functionality.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// versionCommentRegex matches "# Version: X.Y.Z" comments at the start of the script.
var versionCommentRegex = regexp.MustCompile(`(?m)^#\s*Version:\s*(\d+\.\d+\.\d+)\s*$`)

// ErrEmbeddedScriptNotFound is returned when a saga script is not found in the embedded filesystem.
var ErrEmbeddedScriptNotFound = errors.New("embedded script not found")

// PlatformSagaDefinition represents a saga definition in the platform table.
type PlatformSagaDefinition struct {
	ID          uuid.UUID
	Name        string
	Version     string
	Script      string
	DisplayName string
	Description string
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
//  1. Loads all embedded .star files from the defaults directory
//  2. Extracts version from "# Version: X.Y.Z" comment in each script
//  3. For each saga, checks if the exact (name, version) pair already exists
//  4. Inserts a new row if the (name, version) pair is not found
//  5. Skips if the exact (name, version) already exists (idempotent)
//
// Old versions are never overwritten or deleted. This guarantees that running
// saga instances which pinned a PlatformSagaVersionID at execution time can
// always replay using the exact script they started with.
func (s *PlatformSync) SyncPlatformDefaults(ctx context.Context) error {
	s.logger.Info("starting platform saga sync")

	// Load all embedded sagas
	sagas, err := s.loadEmbeddedSagas()
	if err != nil {
		return fmt.Errorf("load embedded sagas: %w", err)
	}

	s.logger.Info("loaded embedded sagas", "count", len(sagas))

	// Sync each saga
	var syncedCount, skippedCount int
	for _, saga := range sagas {
		synced, err := s.syncSaga(ctx, saga)
		if err != nil {
			return fmt.Errorf("sync saga %s: %w", saga.Name, err)
		}
		if synced {
			syncedCount++
		} else {
			skippedCount++
		}
	}

	s.logger.Info("platform saga sync completed",
		"synced", syncedCount,
		"skipped", skippedCount,
		"total", len(sagas))

	return nil
}

// loadEmbeddedSagas reads all embedded .star files and parses their metadata.
func (s *PlatformSync) loadEmbeddedSagas() ([]PlatformSagaDefinition, error) {
	defaults := PlatformDefaults()
	sagas := make([]PlatformSagaDefinition, 0, len(defaults))

	for _, meta := range defaults {
		// Read embedded script
		script, err := s.readEmbeddedScript(meta.Filename)
		if err != nil {
			return nil, fmt.Errorf("read script %s: %w", meta.Filename, err)
		}

		// Extract version from script
		version := extractVersionFromScript(script)
		if version == "" {
			// Default to 1.0.0 if no version comment found
			version = "1.0.0"
			s.logger.Warn("no version comment found in script, using default",
				"filename", meta.Filename,
				"default_version", version)
		}

		// Generate deterministic UUID based on name and version
		// Each (name, version) pair gets a unique, reproducible ID
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga."+meta.Name+"."+version))

		sagas = append(sagas, PlatformSagaDefinition{
			ID:          id,
			Name:        meta.Name,
			Version:     version,
			Script:      script,
			DisplayName: meta.DisplayName,
			Description: meta.Description,
		})
	}

	return sagas, nil
}

// readEmbeddedScript reads a saga script from the embedded filesystem.
func (s *PlatformSync) readEmbeddedScript(filename string) (string, error) {
	scripts, err := GetEmbeddedScripts()
	if err != nil {
		return "", err
	}

	script, ok := scripts[filename]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrEmbeddedScriptNotFound, filename)
	}

	return script, nil
}

// syncSaga syncs a single saga version to the database using INSERT-only semantics.
// Returns true if the saga was inserted, false if skipped (already exists).
//
// Old versions are never modified or deleted. Each unique (name, version) pair
// gets its own row, ensuring pinned saga instances can always replay correctly.
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

	// INSERT new row for this version
	_, err = s.pool.Exec(ctx, `
		INSERT INTO public.platform_saga_definition
			(id, name, version, script, display_name, description)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, saga.ID, saga.Name, saga.Version, saga.Script, saga.DisplayName, saga.Description)
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

	s.logger.Info("inserted platform saga",
		"name", saga.Name,
		"version", saga.Version)
	return true, nil
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
