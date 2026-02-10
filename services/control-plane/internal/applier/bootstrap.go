package applier

import (
	"context"
	"embed"
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

//go:embed all:defaults
var embeddedSagas embed.FS

// versionFilenamePattern matches version filenames like "v1.0.0.star".
var versionFilenamePattern = regexp.MustCompile(`^v(\d+\.\d+\.\d+)\.star$`)

// ErrNoEmbeddedScript is returned when the apply_manifest saga script is not found.
var ErrNoEmbeddedScript = errors.New("embedded apply_manifest saga script not found")

// Bootstrap upserts the apply_manifest saga into public.platform_saga_definition
// on Control Plane startup. This ensures the saga is available for all tenants
// via ADR-0028 platform default fallback resolution.
//
// The bootstrap is idempotent: if the saga already exists with the same version,
// it is skipped. If a newer version exists, it is inserted alongside the old one.
type Bootstrap struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewBootstrap creates a new Bootstrap instance.
func NewBootstrap(pool *pgxpool.Pool) *Bootstrap {
	return &Bootstrap{
		pool:   pool,
		logger: slog.Default().With("component", "apply_manifest_bootstrap"),
	}
}

// EnsurePlatformSaga upserts the apply_manifest saga into the platform table.
// This should be called during Control Plane startup.
func (b *Bootstrap) EnsurePlatformSaga(ctx context.Context) error {
	b.logger.Info("ensuring apply_manifest platform saga is registered")

	script, version, err := loadEmbeddedApplyManifest()
	if err != nil {
		return fmt.Errorf("load embedded saga: %w", err)
	}

	// Generate deterministic UUID based on name and version
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("platform.saga.apply_manifest."+version))

	// Check if exact (name, version) already exists
	var existingID uuid.UUID
	err = b.pool.QueryRow(ctx,
		`SELECT id FROM public.platform_saga_definition
		 WHERE name = $1 AND version = $2`,
		"apply_manifest", version,
	).Scan(&existingID)
	if err == nil {
		b.logger.Info("apply_manifest saga already exists",
			"version", version,
			"id", existingID)
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing saga: %w", err)
	}

	// Insert new version
	_, err = b.pool.Exec(ctx,
		`INSERT INTO public.platform_saga_definition
			(id, name, version, script, status, display_name, description)
		 VALUES ($1, $2, $3, $4, 'ACTIVE', $5, $6)`,
		id, "apply_manifest", version, script,
		"Apply Manifest",
		"Platform saga for applying tenant manifests with phased execution and automatic compensation.",
	)
	if err != nil {
		// Handle race condition: another instance may have inserted concurrently
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			b.logger.Info("apply_manifest saga inserted by concurrent process", "version", version)
			return nil
		}
		return fmt.Errorf("insert platform saga: %w", err)
	}

	b.logger.Info("apply_manifest platform saga registered",
		"version", version,
		"id", id)
	return nil
}

// loadEmbeddedApplyManifest reads the apply_manifest saga from the embedded filesystem.
// Returns the script content and the version string.
func loadEmbeddedApplyManifest() (string, string, error) {
	dirPath := path.Join("defaults", "apply_manifest")

	entries, err := embeddedSagas.ReadDir(dirPath)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrNoEmbeddedScript, err)
	}

	var latestScript string
	var latestVersion string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := versionFilenamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}

		version := matches[1]
		filePath := path.Join(dirPath, entry.Name())

		content, err := embeddedSagas.ReadFile(filePath)
		if err != nil {
			return "", "", fmt.Errorf("read %s: %w", filePath, err)
		}

		if isSemverGreater(version, latestVersion) {
			latestVersion = version
			latestScript = strings.TrimSpace(string(content))
		}
	}

	if latestScript == "" {
		return "", "", ErrNoEmbeddedScript
	}

	return latestScript, latestVersion, nil
}

// isSemverGreater returns true if version a is greater than version b.
func isSemverGreater(a, b string) bool {
	if b == "" {
		return true
	}

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < 3 && i < len(aParts) && i < len(bParts); i++ {
		ai := parseInt(aParts[i])
		bi := parseInt(bParts[i])
		if ai != bi {
			return ai > bi
		}
	}
	return false
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
