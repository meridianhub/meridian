package accounttype

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// namespaceAccountType is the fixed UUID namespace for deterministic blueprint IDs.
// Using a stable SHA1 namespace ensures the same Code always produces the same UUID
// regardless of when or where the seeder runs.
var namespaceAccountType = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

// platformBlueprintSpec defines a platform-level account type blueprint.
type platformBlueprintSpec struct {
	Code           string
	DisplayName    string
	BehaviorClass  BehaviorClass
	NormalBalance  NormalBalance
	InstrumentCode string
}

// platformBlueprints defines the 12 canonical account type blueprints covering
// the full BehaviorClass range. These are seeded into every new tenant as system
// definitions with IsSystem=true.
//
// DefaultSagaPrefix is intentionally omitted so activation does not require
// saga definitions to exist at seeding time. Tenants configure saga routing
// post-seeding via their own saga definitions.
var platformBlueprints = []platformBlueprintSpec{
	{Code: "CURRENT_ACCOUNT_GBP", DisplayName: "GBP Current Account", BehaviorClass: BehaviorClassCustomer, NormalBalance: NormalBalanceCredit, InstrumentCode: "GBP"},
	{Code: "CURRENT_ACCOUNT_USD", DisplayName: "USD Current Account", BehaviorClass: BehaviorClassCustomer, NormalBalance: NormalBalanceCredit, InstrumentCode: "USD"},
	{Code: "CURRENT_ACCOUNT_EUR", DisplayName: "EUR Current Account", BehaviorClass: BehaviorClassCustomer, NormalBalance: NormalBalanceCredit, InstrumentCode: "EUR"},
	{Code: "CLEARING_GBP", DisplayName: "GBP Clearing Account", BehaviorClass: BehaviorClassClearing, NormalBalance: NormalBalanceDebit, InstrumentCode: "GBP"},
	{Code: "NOSTRO_GBP", DisplayName: "GBP Nostro Account", BehaviorClass: BehaviorClassNostro, NormalBalance: NormalBalanceDebit, InstrumentCode: "GBP"},
	{Code: "VOSTRO_GBP", DisplayName: "GBP Vostro Account", BehaviorClass: BehaviorClassVostro, NormalBalance: NormalBalanceCredit, InstrumentCode: "GBP"},
	{Code: "HOLDING_ESCROW", DisplayName: "Escrow Holding Account", BehaviorClass: BehaviorClassHolding, NormalBalance: NormalBalanceCredit, InstrumentCode: "GBP"},
	{Code: "SUSPENSE_UNALLOCATED", DisplayName: "Unallocated Suspense", BehaviorClass: BehaviorClassSuspense, NormalBalance: NormalBalanceDebit, InstrumentCode: "GBP"},
	{Code: "REVENUE_FEES", DisplayName: "Fee Revenue Account", BehaviorClass: BehaviorClassRevenue, NormalBalance: NormalBalanceCredit, InstrumentCode: "GBP"},
	{Code: "EXPENSE_OPERATIONS", DisplayName: "Operations Expense", BehaviorClass: BehaviorClassExpense, NormalBalance: NormalBalanceDebit, InstrumentCode: "GBP"},
	{Code: "CARBON_CREDIT_HOLDING", DisplayName: "Carbon Credit Holding", BehaviorClass: BehaviorClassInventory, NormalBalance: NormalBalanceDebit, InstrumentCode: "TONNE_CO2E"},
	{Code: "INVENTORY_KWH", DisplayName: "kWh Inventory Account", BehaviorClass: BehaviorClassInventory, NormalBalance: NormalBalanceDebit, InstrumentCode: "KWH"},
}

// BlueprintSeeder seeds platform account type blueprints into tenant schemas.
type BlueprintSeeder struct {
	registry Registry
	logger   *slog.Logger
}

// NewBlueprintSeeder creates a new BlueprintSeeder backed by the given Registry.
func NewBlueprintSeeder(registry Registry) *BlueprintSeeder {
	return &BlueprintSeeder{
		registry: registry,
		logger:   slog.Default().With("component", "account_type_blueprint_seeder"),
	}
}

// SeedTenant seeds all platform blueprints into the tenant identified by tenantID.
// It delegates to SeedPlatformBlueprints with a tenant-scoped context.
func (s *BlueprintSeeder) SeedTenant(ctx context.Context, tenantID tenant.TenantID) error {
	tctx := tenant.WithTenant(ctx, tenantID)
	s.logger.Info("seeding platform account type blueprints", "tenant_id", tenantID.String())
	if err := SeedPlatformBlueprints(tctx, s.registry); err != nil {
		s.logger.Error("failed to seed platform blueprints", "tenant_id", tenantID.String(), "error", err)
		return err
	}
	s.logger.Info("platform account type blueprints seeded", "tenant_id", tenantID.String(), "count", len(platformBlueprints))
	return nil
}

// AsPostProvisioningHook returns a function compatible with the provisioning worker's
// PostProvisioningHook signature for seeding blueprints into newly provisioned tenants.
//
// Usage:
//
//	seeder := accounttype.NewBlueprintSeeder(registry)
//	worker.RegisterPostProvisioningHook("account-type-blueprints", seeder.AsPostProvisioningHook())
func (s *BlueprintSeeder) AsPostProvisioningHook() func(ctx context.Context, tenantID tenant.TenantID) error {
	return s.SeedTenant
}

// SeedPlatformBlueprints seeds the 12 canonical platform account type blueprints
// into the registry using ctx (which must carry tenant context).
//
// Each blueprint is created as DRAFT then immediately activated. The operation is
// idempotent: CreateDraft uses ON CONFLICT DO NOTHING and ActivateAccountType
// returns nil if already ACTIVE. Running SeedPlatformBlueprints multiple times
// produces identical state.
//
// Blueprint IDs are deterministic: uuid.NewSHA1(namespaceAccountType, []byte(Code)).
// This ensures the same blueprint always gets the same UUID across tenants and re-runs.
//
// All blueprints have IsSystem=true. System blueprints reject UpdateDefinition and
// DeprecateAccountType calls with ErrSystemAccountTypeReadOnly.
func SeedPlatformBlueprints(ctx context.Context, registry Registry) error {
	for _, bp := range platformBlueprints {
		def := &Definition{
			ID:             uuid.NewSHA1(namespaceAccountType, []byte(bp.Code)),
			Code:           bp.Code,
			Version:        1,
			DisplayName:    bp.DisplayName,
			BehaviorClass:  bp.BehaviorClass,
			NormalBalance:  bp.NormalBalance,
			InstrumentCode: bp.InstrumentCode,
			IsSystem:       true,
			Status:         StatusDraft,
			Attributes:     map[string]any{},
		}

		if err := registry.CreateDraft(ctx, def); err != nil {
			return fmt.Errorf("seed %s: %w", bp.Code, err)
		}

		if err := registry.ActivateAccountType(ctx, bp.Code, 1); err != nil {
			return fmt.Errorf("activate %s: %w", bp.Code, err)
		}
	}

	return nil
}
