package valuation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// SystemMethod defines a SYSTEM default valuation method.
type SystemMethod struct {
	Name             string
	InputInstrument  string
	OutputInstrument string
	LogicScript      string
	Description      string
}

// SystemPolicy defines a SYSTEM default valuation policy.
type SystemPolicy struct {
	Name          string
	CelExpression string
	OutputType    string
	EstimatedCost int
	Description   string
}

// DefaultMethods returns the SYSTEM default valuation methods.
func DefaultMethods() []SystemMethod {
	identityScript := `def evaluate(amount, rate, context):
    return amount
`
	return []SystemMethod{
		{
			Name:             "SYSTEM_IDENTITY_USD",
			InputInstrument:  "USD",
			OutputInstrument: "USD",
			LogicScript:      identityScript,
			Description:      "Identity valuation for USD - returns amount unchanged",
		},
		{
			Name:             "SYSTEM_IDENTITY_GBP",
			InputInstrument:  "GBP",
			OutputInstrument: "GBP",
			LogicScript:      identityScript,
			Description:      "Identity valuation for GBP - returns amount unchanged",
		},
		{
			Name:             "SYSTEM_IDENTITY_EUR",
			InputInstrument:  "EUR",
			OutputInstrument: "EUR",
			LogicScript:      identityScript,
			Description:      "Identity valuation for EUR - returns amount unchanged",
		},
		{
			Name:             "SYSTEM_RETAIL_ENERGY",
			InputInstrument:  "KWH",
			OutputInstrument: "GBP",
			LogicScript: `def evaluate(amount, rate, context):
    return amount * rate
`,
			Description: "Retail energy valuation - converts KWH to GBP using rate",
		},
		{
			Name:             "SYSTEM_CARBON_CREDIT",
			InputInstrument:  "TONNE_CO2E",
			OutputInstrument: "GBP",
			LogicScript: `def evaluate(amount, rate, context):
    return amount * rate
`,
			Description: "Carbon credit valuation - converts TONNE_CO2E to GBP using rate",
		},
	}
}

// DefaultPolicies returns the SYSTEM default valuation policies.
func DefaultPolicies() []SystemPolicy {
	return []SystemPolicy{
		{
			Name:          "SYSTEM_IDENTITY",
			CelExpression: "amount",
			OutputType:    "string",
			EstimatedCost: 1,
			Description:   "Identity policy - returns amount unchanged",
		},
		{
			Name:          "SYSTEM_POSITIVE_AMOUNT",
			CelExpression: "parse_int(amount) > 0",
			OutputType:    "bool",
			EstimatedCost: 2,
			Description:   "Validates that the amount is a positive integer",
		},
		{
			Name:          "SYSTEM_AMOUNT_UNDER_LIMIT",
			CelExpression: "parse_int(amount) > 0 && parse_int(amount) < 1000000",
			OutputType:    "bool",
			EstimatedCost: 3,
			Description:   "Validates amount is positive and under 1,000,000",
		},
	}
}

// Seeder seeds SYSTEM default valuation methods and policies.
type Seeder struct {
	pool *pgxpool.Pool
}

// NewSeeder creates a new valuation seeder.
func NewSeeder(pool *pgxpool.Pool) *Seeder {
	return &Seeder{pool: pool}
}

// SeedTenant seeds all SYSTEM defaults into a specific tenant's schema.
// Idempotent: uses ON CONFLICT DO NOTHING.
func (s *Seeder) SeedTenant(ctx context.Context, tenantID tenant.TenantID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", schemaName))
	if err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	if err := s.seedMethods(ctx, tx); err != nil {
		return err
	}
	if err := s.seedPolicies(ctx, tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Seeder) seedMethods(ctx context.Context, tx pgx.Tx) error {
	now := time.Now()

	for _, m := range DefaultMethods() {
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("valuation.method."+m.Name))
		hash := sha256Hash(m.LogicScript)

		_, err := tx.Exec(ctx, `
			INSERT INTO valuation_method (
				id, name, version, input_instrument, output_instrument,
				logic_script, logic_hash, required_policies, lifecycle_status,
				is_system, description, created_at, activated_at, valid_from
			) VALUES (
				$1, $2, 1, $3, $4,
				$5, $6, '{}', 'ACTIVE',
				true, $7, $8, $8, $8
			) ON CONFLICT (name, version) DO NOTHING`,
			id, m.Name, m.InputInstrument, m.OutputInstrument,
			m.LogicScript, hash, m.Description, now,
		)
		if err != nil {
			return fmt.Errorf("seed method %s: %w", m.Name, err)
		}
	}
	return nil
}

func (s *Seeder) seedPolicies(ctx context.Context, tx pgx.Tx) error {
	now := time.Now()

	for _, p := range DefaultPolicies() {
		id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("valuation.policy."+p.Name))
		hash := sha256Hash(p.CelExpression)

		_, err := tx.Exec(ctx, `
			INSERT INTO valuation_policy (
				id, name, version, cel_expression, cel_hash,
				output_type, estimated_cost, lifecycle_status,
				is_system, description, created_at, activated_at, valid_from
			) VALUES (
				$1, $2, 1, $3, $4,
				$5, $6, 'ACTIVE',
				true, $7, $8, $8, $8
			) ON CONFLICT (name, version) DO NOTHING`,
			id, p.Name, p.CelExpression, hash,
			p.OutputType, p.EstimatedCost, p.Description, now,
		)
		if err != nil {
			return fmt.Errorf("seed policy %s: %w", p.Name, err)
		}
	}
	return nil
}

// AsPostProvisioningHook returns a function compatible with the provisioning worker's
// PostProvisioningHook signature.
func (s *Seeder) AsPostProvisioningHook() func(ctx context.Context, tenantID tenant.TenantID) error {
	return s.SeedTenant
}

func sha256Hash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}
