package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ManifestBindingSource implements SagaBindingSource by querying the latest
// applied manifest from the manifest_versions table in the tenant's schema.
// It extracts saga definitions with "api:" trigger prefix to build the
// (api_path -> saga_name) binding map.
type ManifestBindingSource struct {
	pool *pgxpool.Pool
}

// NewManifestBindingSource creates a new ManifestBindingSource.
func NewManifestBindingSource(pool *pgxpool.Pool) *ManifestBindingSource {
	return &ManifestBindingSource{pool: pool}
}

// manifestJSON is the minimal structure needed to extract saga triggers from the manifest.
type manifestJSON struct {
	Sagas []sagaJSON `json:"sagas"`
}

type sagaJSON struct {
	Name    string `json:"name"`
	Trigger string `json:"trigger"`
}

// GetBindingsForTenant queries the latest applied manifest for the given tenant
// and returns a map of api_path -> saga_name for all sagas with "api:" triggers.
func (s *ManifestBindingSource) GetBindingsForTenant(ctx context.Context, tenantID string) (map[string]string, error) {
	tid, err := tenant.NewTenantID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant ID %q: %w", tenantID, err)
	}
	schemaName := pq.QuoteIdentifier(tid.SchemaName())

	query := fmt.Sprintf(
		`SELECT manifest_json FROM %s.manifest_versions
		 WHERE apply_status = 'APPLIED'
		 ORDER BY applied_at DESC
		 LIMIT 1`, schemaName)

	var manifestRaw []byte
	if err := s.pool.QueryRow(ctx, query).Scan(&manifestRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("query manifest bindings: %w", err)
	}

	var manifest manifestJSON
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, fmt.Errorf("unmarshal manifest JSON: %w", err)
	}

	return extractAPIBindings(manifest.Sagas, tenantID), nil
}

// extractAPIBindings extracts api_path -> saga_name mappings from saga definitions.
// Logs a warning if multiple sagas declare the same API path.
func extractAPIBindings(sagas []sagaJSON, tenantID string) map[string]string {
	bindings := make(map[string]string)
	for _, saga := range sagas {
		if strings.HasPrefix(saga.Trigger, "api:") {
			path := strings.TrimPrefix(saga.Trigger, "api:")
			if existing, ok := bindings[path]; ok {
				slog.Warn("duplicate api path in manifest, later saga overwrites earlier",
					"tenant_id", tenantID,
					"path", path,
					"overwritten_saga", existing,
					"overwriting_saga", saga.Name,
				)
			}
			bindings[path] = saga.Name
		}
	}
	return bindings
}
