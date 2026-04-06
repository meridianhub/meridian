// Package cmd implements the seed-dev CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identitybootstrap "github.com/meridianhub/meridian/services/identity/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// ErrManifestValidation is returned when the manifest contains validation errors.
var ErrManifestValidation = errors.New("manifest validation failed")

// ErrManifestApplyFailed is returned when the manifest apply returns a non-success status.
var ErrManifestApplyFailed = errors.New("manifest apply failed")

// ErrTenantNotActive is returned when the tenant provisioning status is not ACTIVE.
var ErrTenantNotActive = errors.New("tenant not active")

// ErrProvisioningFailed is returned when tenant provisioning reaches a terminal failure state.
var ErrProvisioningFailed = errors.New("tenant provisioning failed")

// ErrDatabaseURLRequired is returned when DATABASE_URL is needed but not set.
var ErrDatabaseURLRequired = errors.New("DATABASE_URL required for demo user seeding")

var (
	gatewayURL       string
	grpcAddr         string
	controlPlaneAddr string
	manifestPath     string
	tenantID         string
	tenantSlug       string
	timeout          time.Duration
	skipManifest     bool
	withFixtures     bool
	forceApply       bool
	displayName      string
	subdomain        string
)

var rootCmd = &cobra.Command{
	Use:   "seed-dev",
	Short: "Seed a dev tenant with manifest configuration",
	Long: `seed-dev creates a dev tenant and applies a manifest configuration.

This tool is idempotent:
  - If the tenant already exists, creation is skipped
  - If the manifest is already applied, it will be re-applied (idempotent)

Examples:
  # Default: create dev_tenant and apply examples/manifests/energy.json
  seed-dev

  # Custom manifest
  seed-dev --manifest examples/manifests/carbon.json

  # Skip manifest application (tenant creation only)
  seed-dev --skip-manifest

  # Custom gateway and gRPC addresses
  seed-dev --gateway-url=http://meridian:8090 --grpc-addr=meridian:50051

  # Tilt mode: separate service addresses
  seed-dev --grpc-addr=localhost:50056 --control-plane-addr=localhost:50062`,
	RunE: runSeed,
}

// Execute adds all child commands to the root command and executes it.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringVar(&gatewayURL, "gateway-url",
		getEnvOrDefault("GATEWAY_URL", "http://localhost:8090"),
		"Gateway HTTP URL (used for health check)")
	rootCmd.Flags().StringVar(&grpcAddr, "grpc-addr",
		getEnvOrDefault("GRPC_ADDR", "localhost:50051"),
		"gRPC server address for tenant service (host:port)")
	rootCmd.Flags().StringVar(&controlPlaneAddr, "control-plane-addr",
		getEnvOrDefault("CONTROL_PLANE_ADDR", ""),
		"gRPC address for control-plane service (defaults to --grpc-addr)")
	rootCmd.Flags().StringVar(&manifestPath, "manifest",
		getEnvOrDefault("MANIFEST_PATH", "examples/manifests/energy.json"),
		"Path to manifest JSON file")
	rootCmd.Flags().StringVar(&tenantID, "tenant-id",
		getEnvOrDefault("TENANT_ID", "dev_tenant"),
		"Tenant ID to create/configure")
	rootCmd.Flags().StringVar(&tenantSlug, "tenant-slug",
		getEnvOrDefault("TENANT_SLUG", "dev-tenant"),
		"Tenant URL slug")
	rootCmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute,
		"Overall operation timeout")
	rootCmd.Flags().BoolVar(&skipManifest, "skip-manifest", false,
		"Skip manifest application (tenant creation only)")
	rootCmd.Flags().BoolVar(&withFixtures, "with-fixtures", false,
		"Seed demo fixture data (customers, accounts, balances, market data) after manifest application")
	rootCmd.Flags().BoolVar(&forceApply, "force", false,
		"Force manifest apply, converting destructive change errors into warnings")
	rootCmd.Flags().StringVar(&displayName, "display-name", "",
		"Tenant display name (default: derived from tenant slug)")
	rootCmd.Flags().StringVar(&subdomain, "subdomain", "",
		"Tenant subdomain (default: <tenant-slug>.localhost)")
}

func runSeed(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fmt.Printf("Waiting for gateway at %s ...\n", gatewayURL)

	err := await.New().
		AtMost(60 * time.Second).
		PollInterval(2 * time.Second).
		WithContext(ctx).
		Until(func() bool {
			return checkHealth(gatewayURL)
		})
	if err != nil {
		return fmt.Errorf("gateway did not become healthy: %w", err)
	}
	fmt.Println("Gateway is healthy.")

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to gRPC server %s: %w", grpcAddr, err)
	}
	defer func() { _ = conn.Close() }()

	fmt.Printf("Creating tenant '%s' ...\n", tenantID)
	if err := createTenant(ctx, conn, tenantID, tenantSlug); err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}

	// Wait for tenant provisioning to complete before applying manifests.
	// The provisioner runs asynchronously - the schema must exist before
	// any tenant-scoped operations (WithGormTenantScope validates this).
	fmt.Println("Waiting for tenant provisioning ...")
	if err := waitForTenantReady(ctx, conn, tenantID); err != nil {
		return fmt.Errorf("wait for tenant provisioning: %w", err)
	}

	if skipManifest {
		fmt.Println("Skipping manifest application (--skip-manifest set).")
	} else {
		// Use separate connection for control-plane if address differs from tenant service
		cpAddr := controlPlaneAddr
		if cpAddr == "" {
			cpAddr = grpcAddr
		}
		manifestConn := conn
		if cpAddr != grpcAddr {
			manifestConn, err = grpc.NewClient(cpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("connect to control-plane gRPC server %s: %w", cpAddr, err)
			}
			defer func() { _ = manifestConn.Close() }()
		}

		fmt.Printf("Applying manifest from %s ...\n", manifestPath)
		if err := applyManifest(ctx, manifestConn, tenantID, manifestPath, forceApply); err != nil {
			return fmt.Errorf("apply manifest: %w", err)
		}
	}

	if withFixtures {
		fmt.Println("\n=== Seeding Fixture Data ===")
		var fixtureErr error
		switch tenantID {
		case "payg_energy":
			fixtureErr = runPaygFixtures(ctx, conn, tenantID)
		default:
			fixtureErr = runFixtures(ctx, conn, tenantID)
		}
		if fixtureErr != nil {
			return fmt.Errorf("seed fixtures: %w", fixtureErr)
		}
	}

	// Seed demo operator user from DEMO_OPERATOR_* env vars.
	// This runs unconditionally (not gated by --with-fixtures) because
	// the operator user is needed for Dex login on every deploy. The
	// seeder is a no-op when env vars are not set (production).
	if err := seedDemoOperator(ctx); err != nil {
		return fmt.Errorf("seed demo operator: %w", err)
	}

	fmt.Println("Seed complete.")
	return nil
}

// checkHealth returns true if the gateway /healthz endpoint responds with 200.
func checkHealth(gwURL string) bool {
	resp, err := http.Get(gwURL + "/healthz") //nolint:noctx // polling function, no context available
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// createTenant calls InitiateTenant and treats AlreadyExists as success.
func createTenant(ctx context.Context, conn *grpc.ClientConn, id, slug string) error {
	client := tenantv1.NewTenantServiceClient(conn)

	// Derive display name from slug if not explicitly set.
	dn := displayName
	if dn == "" {
		dn = strings.ReplaceAll(slug, "-", " ")
		dn = strings.Title(dn) //nolint:staticcheck // strings.Title is fine for simple slug capitalization
	}

	// Derive subdomain if not explicitly set.
	sd := subdomain
	if sd == "" {
		sd = slug + ".localhost"
	}

	req := &tenantv1.InitiateTenantRequest{
		TenantId:        id,
		DisplayName:     dn,
		SettlementAsset: "GBP",
		Subdomain:       sd,
		Slug:            slug,
	}

	resp, err := client.InitiateTenant(ctx, req)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			fmt.Println("Dev tenant already exists (idempotent).")
			return nil
		}
		return err
	}

	if resp.GetTenant() != nil {
		fmt.Printf("Dev tenant created successfully (ID: %s).\n", resp.GetTenant().GetTenantId())
	} else {
		fmt.Println("Dev tenant created successfully.")
	}
	return nil
}

// unmarshalManifestFile reads and parses a manifest JSON file into a proto message.
// Exposed for testing.
func unmarshalManifestFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest file: %w", err)
	}
	var manifest controlplanev1.Manifest
	if err := protojson.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest JSON: %w", err)
	}
	return nil
}

// applyManifest reads a manifest JSON file and calls ApplyManifest.
func applyManifest(ctx context.Context, conn *grpc.ClientConn, tid, path string, force bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest file: %w", err)
	}

	var manifest controlplanev1.Manifest
	if err := protojson.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest JSON: %w", err)
	}

	client := controlplanev1.NewApplyManifestServiceClient(conn)

	// Pass tenant ID as gRPC metadata (x-tenant-id header).
	md := metadata.Pairs("x-tenant-id", tid)
	callCtx := metadata.NewOutgoingContext(ctx, md)

	req := &controlplanev1.ApplyManifestRequest{
		Manifest:  &manifest,
		DryRun:    false,
		AppliedBy: "seed-dev",
		Force:     force,
	}

	resp, err := client.ApplyManifest(callCtx, req)
	if err != nil {
		return fmt.Errorf("ApplyManifest RPC: %w", err)
	}

	fmt.Printf("  Job ID: %s\n", resp.GetJobId())
	fmt.Printf("  Status: %s\n", resp.GetStatus().String())
	if diff := resp.GetDiffSummary(); diff != "" {
		fmt.Printf("  Changes: %s\n", diff)
	}
	if len(resp.GetValidationErrors()) > 0 {
		fmt.Printf("  Validation errors: %d\n", len(resp.GetValidationErrors()))
		for _, ve := range resp.GetValidationErrors() {
			fmt.Printf("    [%s] %s: %s\n", ve.GetSeverity(), ve.GetPath(), ve.GetMessage())
		}
		return fmt.Errorf("%w: %d error(s)", ErrManifestValidation, len(resp.GetValidationErrors()))
	}

	// Print step results for debugging (visible in CI logs).
	for _, sr := range resp.GetStepResults() {
		fmt.Printf("  Step [%s]: %s — %s\n", sr.GetStepName(), sr.GetStatus().String(), sr.GetMessage())
		for k, v := range sr.GetDetails() {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}
	for phase, detail := range resp.GetPhaseStatus() {
		fmt.Printf("  Phase [%s]: %s %s\n", phase, detail.GetStatus(), detail.GetError())
	}

	// Check response status — a nil-executor or saga failure returns a non-success status.
	switch resp.GetStatus() { //nolint:exhaustive // default catches future enum additions
	case controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
		controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN:
		// success
	default:
		return fmt.Errorf("%w: %s", ErrManifestApplyFailed, resp.GetStatus().String())
	}

	fmt.Println("Manifest applied successfully.")
	return nil
}

// waitForTenantReady polls GetTenantProvisioningStatus until the tenant schema
// is provisioned (status ACTIVE) or the context is cancelled.
func waitForTenantReady(ctx context.Context, conn *grpc.ClientConn, id string) error {
	client := tenantv1.NewTenantServiceClient(conn)

	var terminalErr error
	err := await.New().
		AtMost(60 * time.Second).
		PollInterval(2 * time.Second).
		WithContext(ctx).
		Until(func() bool {
			resp, err := client.GetTenantProvisioningStatus(ctx, &tenantv1.GetTenantProvisioningStatusRequest{
				TenantId: id,
			})
			if err != nil {
				fmt.Printf("  provisioning check failed: %v\n", err)
				return false
			}
			switch resp.GetOverallStatus() { //nolint:exhaustive // default handles remaining transitional states
			case tenantv1.TenantStatus_TENANT_STATUS_ACTIVE:
				fmt.Println("Tenant provisioned and active.")
				return true
			case tenantv1.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED:
				terminalErr = fmt.Errorf("%w: %s", ErrProvisioningFailed, resp.GetOverallStatus().String())
				return true // stop polling
			default:
				return false
			}
		})
	if terminalErr != nil {
		return terminalErr
	}
	return err
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// seedDemoOperator seeds demo operator identities from DEMO_OPERATOR_* env
// vars. This uses a direct database connection (not gRPC) because the identity
// service's SetPassword RPC requires an invitation token, not a plaintext
// password - there is no admin "create user with password" API by design.
//
// No-op when DEMO_OPERATOR_EMAIL or DEMO_OPERATOR_PASSWORD are not set, making
// this safe to call in any environment including production.
func seedDemoOperator(ctx context.Context) error {
	email := os.Getenv("DEMO_OPERATOR_EMAIL")
	password := os.Getenv("DEMO_OPERATOR_PASSWORD")
	if email == "" || password == "" {
		return nil // not configured, skip silently
	}

	fmt.Println("\n--- Seed Demo Operator ---")

	baseDSN := os.Getenv("DATABASE_URL")
	if baseDSN == "" {
		return ErrDatabaseURLRequired
	}

	// DATABASE_URL points to the platform database (meridian_platform).
	// The identity repo needs meridian_identity. Derive the DSN by
	// replacing the database component, matching how the main binary
	// routes per-service connections via ServiceDatabases.
	identityDSN, err := replaceDatabase(baseDSN, "meridian_identity")
	if err != nil {
		return fmt.Errorf("derive identity DSN: %w", err)
	}

	db, err := bootstrap.NewDatabase(ctx, bootstrap.DatabaseConfig{
		DSN:          identityDSN,
		MaxOpenConns: 2,
		MaxIdleConns: 1,
	})
	if err != nil {
		return fmt.Errorf("connect to identity database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get underlying sql.DB for cleanup: %w", err)
	}
	defer sqlDB.Close()

	repo := identitypersistence.NewRepository(db)
	if err := identitybootstrap.SeedDemoUsers(ctx, repo); err != nil {
		return fmt.Errorf("seed demo users: %w", err)
	}

	return nil
}

// replaceDatabase swaps the database name in a PostgreSQL DSN URL.
func replaceDatabase(baseDSN, database string) (string, error) {
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}
