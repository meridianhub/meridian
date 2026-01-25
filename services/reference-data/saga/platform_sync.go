// Package saga provides platform saga definition sync functionality.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
// The sync logic:
//  1. Loads all embedded .star files from the defaults directory
//  2. Extracts version from "# Version: X.Y.Z" comment in each script
//  3. For each saga, compares embedded version with database version
//  4. Updates if embedded version is newer (semver comparison)
//  5. Inserts if saga doesn't exist in database
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

		// Generate deterministic UUID based on name
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga."+meta.Name))

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

// syncSaga syncs a single saga to the database.
// Returns true if the saga was inserted/updated, false if skipped.
func (s *PlatformSync) syncSaga(ctx context.Context, saga PlatformSagaDefinition) (bool, error) {
	// Check if saga exists and get current version
	var existingVersion string
	err := s.pool.QueryRow(ctx, `
		SELECT version FROM public.platform_saga_definition WHERE name = $1
	`, saga.Name).Scan(&existingVersion)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("query existing version: %w", err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		// Insert new saga
		_, err = s.pool.Exec(ctx, `
			INSERT INTO public.platform_saga_definition
				(id, name, version, script, display_name, description)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, saga.ID, saga.Name, saga.Version, saga.Script, saga.DisplayName, saga.Description)
		if err != nil {
			return false, fmt.Errorf("insert saga: %w", err)
		}

		s.logger.Info("inserted platform saga",
			"name", saga.Name,
			"version", saga.Version)
		return true, nil
	}

	// Compare versions using semver
	shouldUpdate, err := shouldUpdateVersion(existingVersion, saga.Version)
	if err != nil {
		return false, fmt.Errorf("compare versions: %w", err)
	}

	if !shouldUpdate {
		s.logger.Debug("skipping saga (version not newer)",
			"name", saga.Name,
			"existing_version", existingVersion,
			"embedded_version", saga.Version)
		return false, nil
	}

	// Update saga with newer version
	_, err = s.pool.Exec(ctx, `
		UPDATE public.platform_saga_definition
		SET version = $1, script = $2, display_name = $3, description = $4, updated_at = NOW()
		WHERE name = $5
	`, saga.Version, saga.Script, saga.DisplayName, saga.Description, saga.Name)
	if err != nil {
		return false, fmt.Errorf("update saga: %w", err)
	}

	s.logger.Info("updated platform saga",
		"name", saga.Name,
		"old_version", existingVersion,
		"new_version", saga.Version)
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

// shouldUpdateVersion returns true if newVersion is greater than existingVersion.
// Uses semver comparison for proper version ordering.
func shouldUpdateVersion(existingVersion, newVersion string) (bool, error) {
	existingVer, err := semver.NewVersion(existingVersion)
	if err != nil {
		return false, fmt.Errorf("parse existing version %q: %w", existingVersion, err)
	}

	newVer, err := semver.NewVersion(newVersion)
	if err != nil {
		return false, fmt.Errorf("parse new version %q: %w", newVersion, err)
	}

	return newVer.GreaterThan(existingVer), nil
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
