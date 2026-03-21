// Package bootstrap implements the master tenant bootstrap process for Meridian.
//
// The bootstrap provisions org_meridian_master schemas across all service databases,
// ensures the platform apply_manifest saga is registered, and validates the
// platform economy manifest.
package bootstrap

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	controlplaneservice "github.com/meridianhub/meridian/services/control-plane/service"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

//go:embed platform_manifest.json
var platformManifestJSON []byte

// MasterTenantID is the well-known tenant ID for the master/platform tenant.
const MasterTenantID = "meridian_master"

// ErrInvalidManifestJSON is returned when the embedded platform manifest is not valid JSON.
var ErrInvalidManifestJSON = errors.New("embedded platform manifest is not valid JSON")

// ErrManifestValidation is returned when the platform manifest fails validation.
var ErrManifestValidation = errors.New("platform manifest validation failed")

// Config holds the dependencies needed for bootstrap.
type Config struct {
	// PlatformDB is the GORM connection to meridian_platform for provisioning status.
	PlatformDB *gorm.DB

	// ControlPlanePool is the pgxpool connection used for the control-plane database
	// (saga definitions, apply jobs).
	ControlPlanePool *pgxpool.Pool

	// ProvisionerConfig controls which services get org_meridian_master schemas.
	// If nil, DefaultConfig() is used.
	ProvisionerConfig *provisioner.Config

	// Logger for structured logging.
	Logger *slog.Logger
}

// Run executes the master tenant bootstrap process:
//  1. Provisions org_meridian_master schemas across all service databases
//  2. Ensures the platform apply_manifest saga definition exists
//  3. Validates the embedded platform economy manifest
//
// The process is idempotent — safe to run multiple times.
func Run(ctx context.Context, cfg Config) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "master_tenant_bootstrap")

	logger.Info("starting master tenant bootstrap")

	// Step 1: Provision schemas
	if err := provisionSchemas(ctx, cfg, logger); err != nil {
		return fmt.Errorf("schema provisioning: %w", err)
	}

	// Step 2: Ensure platform saga
	if err := ensurePlatformSaga(ctx, cfg.ControlPlanePool, logger); err != nil {
		return fmt.Errorf("platform saga: %w", err)
	}

	// Step 3: Validate platform manifest
	if err := validatePlatformManifest(logger); err != nil {
		return fmt.Errorf("manifest validation: %w", err)
	}

	// Step 4: Seed platform manifest into master tenant schema
	if err := seedPlatformManifest(ctx, cfg.PlatformDB, logger); err != nil {
		return fmt.Errorf("seed platform manifest: %w", err)
	}

	logger.Info("master tenant bootstrap completed successfully")
	return nil
}

// provisionSchemas creates org_meridian_master schemas in all service databases.
func provisionSchemas(ctx context.Context, cfg Config, logger *slog.Logger) error {
	logger.Info("provisioning master tenant schemas")

	provConfig := cfg.ProvisionerConfig
	if provConfig == nil {
		provConfig = provisioner.DefaultConfig()
	}

	prov, err := provisioner.NewPostgresProvisioner(cfg.PlatformDB, provConfig)
	if err != nil {
		return fmt.Errorf("create provisioner: %w", err)
	}

	tenantID := tenant.TenantID(MasterTenantID)

	// Check if already provisioned
	status, err := prov.GetProvisioningStatus(ctx, tenantID)
	if err == nil && status.State == provisioner.StateActive {
		logger.Info("master tenant schemas already provisioned")
		return nil
	}

	// Initialize provisioning status if needed
	if err := prov.InitializeProvisioningStatus(ctx, tenantID); err != nil {
		logger.Warn("failed to initialize provisioning status (may already exist)", "error", err)
	}

	// Provision schemas
	if err := prov.ProvisionSchemas(ctx, tenantID); err != nil {
		return fmt.Errorf("provision schemas for %s: %w", MasterTenantID, err)
	}

	logger.Info("master tenant schemas provisioned successfully",
		"tenant_id", MasterTenantID,
		"services", len(provConfig.Services))
	return nil
}

// ensurePlatformSaga registers the apply_manifest saga in platform_saga_definition.
func ensurePlatformSaga(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	logger.Info("ensuring platform saga definition")

	if err := controlplaneservice.EnsurePlatformSaga(ctx, pool); err != nil {
		return fmt.Errorf("ensure platform saga: %w", err)
	}

	logger.Info("platform saga definition ensured")
	return nil
}

// validatePlatformManifest loads and validates the embedded platform manifest.
func validatePlatformManifest(logger *slog.Logger) error {
	logger.Info("validating platform economy manifest")

	mf, err := LoadPlatformManifest()
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}

	result, err := controlplaneservice.ValidateManifest(mf, nil)
	if err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}

	if !result.Valid {
		for _, e := range result.Errors {
			logger.Error("manifest validation error",
				"path", e.Path,
				"code", e.Code,
				"message", e.Message)
		}
		return fmt.Errorf("%w: %d errors", ErrManifestValidation, len(result.Errors))
	}

	logger.Info("platform manifest validated",
		"instruments", len(mf.Instruments),
		"account_types", len(mf.AccountTypes),
		"valuation_rules", len(mf.ValuationRules),
		"warnings", len(result.Warnings))
	return nil
}

// seedPlatformManifest stores the platform manifest in the master tenant's
// manifest_versions table so the Economy and Starlark Config pages display
// content immediately after bootstrap. Idempotent - skips if a manifest
// version already exists.
func seedPlatformManifest(ctx context.Context, db *gorm.DB, logger *slog.Logger) error {
	logger.Info("seeding platform manifest into master tenant schema")

	tenantID := tenant.TenantID(MasterTenantID)
	tenantCtx := tenant.WithTenant(ctx, tenantID)

	mf, err := LoadPlatformManifest()
	if err != nil {
		return fmt.Errorf("load platform manifest: %w", err)
	}

	seeded, err := controlplaneservice.SeedManifestVersion(tenantCtx, db, mf, "system:bootstrap")
	if err != nil {
		return fmt.Errorf("seed manifest version: %w", err)
	}

	if !seeded {
		logger.Info("platform manifest already seeded, skipping")
		return nil
	}

	logger.Info("platform manifest seeded successfully",
		"tenant_id", MasterTenantID,
		"version", mf.Version)
	return nil
}

// LoadPlatformManifest loads the embedded platform economy manifest as a proto Manifest.
func LoadPlatformManifest() (*controlplanev1.Manifest, error) {
	// First verify it's valid JSON
	if !json.Valid(platformManifestJSON) {
		return nil, ErrInvalidManifestJSON
	}

	mf := &controlplanev1.Manifest{}
	if err := protojson.Unmarshal(platformManifestJSON, mf); err != nil {
		return nil, fmt.Errorf("unmarshal platform manifest: %w", err)
	}

	return mf, nil
}
