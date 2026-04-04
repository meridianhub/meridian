package registry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// namespaceInstrument is the fixed UUID namespace for deterministic instrument IDs.
var namespaceInstrument = uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")

// platformInstrumentSpec defines a platform-level instrument definition.
type platformInstrumentSpec struct {
	Code        string
	Dimension   Dimension
	Precision   int
	DisplayName string
	Description string
}

// platformInstruments defines the canonical platform instruments covering all
// dimensions referenced by the platform account type blueprints. These are seeded
// into every new tenant as system definitions with IsSystem=true.
var platformInstruments = []platformInstrumentSpec{
	{Code: "GBP", Dimension: DimensionMonetary, Precision: 2, DisplayName: "British Pound Sterling", Description: "ISO 4217 currency: GBP"},
	{Code: "USD", Dimension: DimensionMonetary, Precision: 2, DisplayName: "US Dollar", Description: "ISO 4217 currency: USD"},
	{Code: "EUR", Dimension: DimensionMonetary, Precision: 2, DisplayName: "Euro", Description: "ISO 4217 currency: EUR"},
	{Code: "TONNE_CO2E", Dimension: DimensionCarbon, Precision: 6, DisplayName: "Tonne CO2 Equivalent", Description: "Carbon credit unit - tonnes of CO2 equivalent"},
	{Code: "KWH", Dimension: DimensionEnergy, Precision: 3, DisplayName: "Kilowatt Hour", Description: "Energy unit - kilowatt hours"},
}

// InstrumentSeeder seeds platform instrument definitions into tenant schemas.
type InstrumentSeeder struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewInstrumentSeeder creates a new InstrumentSeeder.
func NewInstrumentSeeder(pool *pgxpool.Pool) *InstrumentSeeder {
	return &InstrumentSeeder{
		pool:   pool,
		logger: slog.Default().With("component", "instrument_seeder"),
	}
}

// SeedTenant seeds all platform instruments into a specific tenant's schema.
// Idempotent: uses ON CONFLICT (code, version) DO NOTHING.
func (s *InstrumentSeeder) SeedTenant(ctx context.Context, tenantID tenant.TenantID) error {
	logger := s.logger.With("tenant_id", tenantID.String())
	logger.Info("seeding platform instruments")

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	seeded := 0
	for _, inst := range platformInstruments {
		inserted, seedErr := s.seedInstrument(ctx, tx, inst)
		if seedErr != nil {
			return fmt.Errorf("seed %s: %w", inst.Code, seedErr)
		}
		if inserted {
			seeded++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	logger.Info("platform instruments seeded",
		"total", len(platformInstruments),
		"seeded", seeded,
		"skipped", len(platformInstruments)-seeded)
	return nil
}

func (s *InstrumentSeeder) seedInstrument(ctx context.Context, tx pgx.Tx, spec platformInstrumentSpec) (bool, error) {
	id := uuid.NewSHA1(namespaceInstrument, []byte(spec.Code))
	now := time.Now()

	result, err := tx.Exec(ctx, `
		INSERT INTO instrument_definition (
			id, code, version, dimension, precision, status, is_system,
			fungibility_key_expression, display_name, description,
			created_at, updated_at, activated_at
		) VALUES (
			$1, $2, 1, $3, $4, 'ACTIVE', true,
			'', $5, $6,
			$7, $8, $9
		) ON CONFLICT (code, version) DO NOTHING`,
		id, spec.Code, string(spec.Dimension), spec.Precision,
		spec.DisplayName, spec.Description,
		now, now, now,
	)
	if err != nil {
		return false, fmt.Errorf("insert instrument: %w", err)
	}

	return result.RowsAffected() > 0, nil
}

// AsPostProvisioningHook returns a function compatible with the provisioning worker's
// PostProvisioningHook signature.
func (s *InstrumentSeeder) AsPostProvisioningHook() func(ctx context.Context, tenantID tenant.TenantID) error {
	return s.SeedTenant
}
