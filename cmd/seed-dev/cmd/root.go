// Package cmd implements the seed-dev CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
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

var (
	gatewayURL       string
	grpcAddr         string
	controlPlaneAddr string
	manifestPath     string
	tenantID         string
	tenantSlug       string
	timeout          time.Duration
	skipManifest     bool
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

	if skipManifest {
		fmt.Println("Skipping manifest application (--skip-manifest set).")
		fmt.Println("Seed complete.")
		return nil
	}

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
	if err := applyManifest(ctx, manifestConn, tenantID, manifestPath); err != nil {
		return fmt.Errorf("apply manifest: %w", err)
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

	req := &tenantv1.InitiateTenantRequest{
		TenantId:        id,
		DisplayName:     "Dev Tenant",
		SettlementAsset: "GBP",
		Subdomain:       id + ".localhost",
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
func applyManifest(ctx context.Context, conn *grpc.ClientConn, tid, path string) error {
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
	}

	// Check response status — a nil-executor or saga failure returns a non-success status.
	switch resp.GetStatus() {
	case controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
		controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN:
		// success
	default:
		return fmt.Errorf("%w: %s", ErrManifestApplyFailed, resp.GetStatus().String())
	}

	fmt.Println("Manifest applied successfully.")
	return nil
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
