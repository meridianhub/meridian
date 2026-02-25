package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/cmd/tenantctl/client"
	"github.com/meridianhub/meridian/services/internal-account/provisioning"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/spf13/cobra"
)

// Sentinel errors for provision-defaults command.
var (
	// ErrTenantIDOrFlagRequired is returned when no tenant ID and no --all or --list-templates flag is provided.
	ErrTenantIDOrFlagRequired = errors.New("tenant ID required (or use --all or --list-templates)")
	// ErrTenantIDWithAll is returned when tenant ID is specified along with --all.
	ErrTenantIDWithAll = errors.New("cannot specify tenant ID with --all")
	// ErrUnknownTemplateSet is returned when an unknown template set is specified.
	ErrUnknownTemplateSet = errors.New("unknown template set")
	// ErrAccountsFailedToProvision is returned when some accounts failed to provision.
	ErrAccountsFailedToProvision = errors.New("accounts failed to provision")
	// ErrTenantsFailedProvisioning is returned when some tenants failed provisioning.
	ErrTenantsFailedProvisioning = errors.New("tenants failed provisioning")
)

var (
	provisionAll        bool
	tenantServiceURL    string
	dryRun              bool
	continueOnError     bool
	maxConcurrent       int
	provisioningTimeout time.Duration
	templateSet         string
	listTemplates       bool
)

var provisionDefaultsCmd = &cobra.Command{
	Use:   "provision-defaults [tenant-id]",
	Short: "Provision default internal accounts for a tenant",
	Long: `Provision default internal accounts for a tenant.

This command creates a set of internal accounts for a tenant based on
the selected template set:

  default  - Standard banking (clearing, revenue, expense, suspense)
  energy   - Energy trading (includes KWH clearing and inventory)
  compute  - Cloud/AI billing (includes GPU-hour, data transfer accounts)
  minimal  - Only suspense account (for manual configuration)

The operation is idempotent - accounts that already exist are skipped.

Examples:
  # Provision defaults for a specific tenant
  ibactl provision-defaults acme_bank

  # Use energy template set for an energy company
  ibactl provision-defaults energy_co --template-set=energy

  # Provision defaults for all active tenants
  ibactl provision-defaults --all

  # Dry run to see what would be created
  ibactl provision-defaults acme_bank --dry-run

  # List available template sets
  ibactl provision-defaults --list-templates`,
	Args: func(_ *cobra.Command, args []string) error {
		if listTemplates {
			return nil // No args needed for listing templates
		}
		if !provisionAll && len(args) == 0 {
			return ErrTenantIDOrFlagRequired
		}
		if provisionAll && len(args) > 0 {
			return ErrTenantIDWithAll
		}
		return nil
	},
	RunE: runProvisionDefaults,
}

func init() {
	rootCmd.AddCommand(provisionDefaultsCmd)

	provisionDefaultsCmd.Flags().BoolVar(&provisionAll, "all", false,
		"Provision default accounts for all active tenants")
	provisionDefaultsCmd.Flags().StringVar(&tenantServiceURL, "tenant-service-url",
		getEnvOrDefault("TENANT_SERVICE_URL", fmt.Sprintf("localhost:%d", ports.Tenant)),
		"Tenant service URL (for --all)")
	provisionDefaultsCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show what would be created without making changes")
	provisionDefaultsCmd.Flags().BoolVar(&continueOnError, "continue-on-error", false,
		"Continue provisioning other tenants if one fails (--all only)")
	provisionDefaultsCmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 5,
		"Maximum number of concurrent tenant provisioning operations (--all only)")
	provisionDefaultsCmd.Flags().DurationVar(&provisioningTimeout, "provisioning-timeout",
		30*time.Second, "Timeout for each tenant's provisioning operation")
	provisionDefaultsCmd.Flags().StringVar(&templateSet, "template-set", "default",
		"Template set to use (default, energy, compute, minimal)")
	provisionDefaultsCmd.Flags().BoolVar(&listTemplates, "list-templates", false,
		"List available template sets")
}

func runProvisionDefaults(cmd *cobra.Command, args []string) error {
	// Handle --list-templates
	if listTemplates {
		return listAvailableTemplates()
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Validate template set
	ts := provisioning.GetTemplateSet(templateSet)
	if ts == nil {
		return fmt.Errorf("%w: %s (use --list-templates to see available sets)", ErrUnknownTemplateSet, templateSet)
	}

	// Create IBA client
	ibaClient, cleanup, err := newClient()
	if err != nil {
		return fmt.Errorf("failed to create internal account client: %w", err)
	}
	defer cleanup()

	// Create provisioner
	provisioner := provisioning.NewProvisioner(ibaClient, logger)

	if provisionAll {
		return provisionAllTenants(cmd.Context(), provisioner, ts, logger)
	}

	// Single tenant
	tenantID, err := tenant.NewTenantID(args[0])
	if err != nil {
		return fmt.Errorf("invalid tenant ID: %w", err)
	}

	return provisionSingleTenant(cmd.Context(), provisioner, tenantID, ts, logger)
}

func listAvailableTemplates() error {
	fmt.Println("Available template sets:")
	fmt.Println()
	for _, name := range provisioning.ListTemplateSets() {
		ts := provisioning.GetTemplateSet(name)
		fmt.Printf("  %s\n", name)
		fmt.Printf("    Description: %s\n", ts.Description)
		fmt.Printf("    Accounts: %d\n", len(ts.Templates))
		fmt.Println()
	}
	return nil
}

func provisionSingleTenant(ctx context.Context, provisioner *provisioning.Provisioner, tenantID tenant.TenantID, ts *provisioning.TemplateSet, _ *slog.Logger) error {
	if dryRun {
		fmt.Printf("Dry run: Would provision %d accounts from '%s' template set for tenant %s:\n",
			len(ts.Templates), ts.Name, tenantID)
		for _, template := range ts.Templates {
			fmt.Printf("  - %s (%s): %s\n", template.Code, template.ProductTypeCode, template.Name)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, provisioningTimeout)
	defer cancel()

	fmt.Printf("Provisioning '%s' template accounts for tenant %s...\n", ts.Name, tenantID)

	result, err := provisioner.ProvisionFromTemplates(ctx, tenantID, ts.Templates)
	if err != nil {
		return fmt.Errorf("provisioning failed: %w", err)
	}

	fmt.Printf("Completed: created=%d, skipped=%d, failed=%d\n",
		result.Created, result.Skipped, result.Failed)

	if len(result.Errors) > 0 {
		fmt.Println("Errors:")
		for _, e := range result.Errors {
			fmt.Printf("  - %v\n", e)
		}
		return fmt.Errorf("%w: %d failed", ErrAccountsFailedToProvision, result.Failed)
	}

	return nil
}

// provisionStats tracks provisioning statistics across tenants.
type provisionStats struct {
	succeeded   int
	failed      int
	skipped     int
	totalCreate int
	totalSkip   int
}

func provisionAllTenants(ctx context.Context, provisioner *provisioning.Provisioner, ts *provisioning.TemplateSet, logger *slog.Logger) error {
	tenants, err := fetchActiveTenants(ctx)
	if err != nil {
		return err
	}

	if len(tenants) == 0 {
		fmt.Println("No active tenants found.")
		return nil
	}

	fmt.Printf("Found %d active tenants\n", len(tenants))
	fmt.Printf("Using template set: %s (%d accounts)\n", ts.Name, len(ts.Templates))

	if dryRun {
		printDryRunSummary(tenants, ts)
		return nil
	}

	stats := &provisionStats{}
	for i, t := range tenants {
		if err := provisionOneTenant(ctx, provisioner, ts, t, i+1, len(tenants), logger, stats); err != nil {
			return err
		}
	}

	printFinalSummary(stats)
	if stats.failed > 0 {
		return fmt.Errorf("%w: %d failed", ErrTenantsFailedProvisioning, stats.failed)
	}
	return nil
}

func fetchActiveTenants(ctx context.Context) ([]*pb.Tenant, error) {
	tenantClient, err := client.NewTenantClient(ctx, client.Config{
		ServiceURL: tenantServiceURL,
		Timeout:    timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create tenant client: %w", err)
	}
	defer func() { _ = tenantClient.Close() }()

	fmt.Println("Fetching active tenants...")
	resp, err := tenantClient.ListTenants(ctx, &pb.ListTenantsRequest{
		StatusFilter: pb.TenantStatus_TENANT_STATUS_ACTIVE,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list tenants: %w", err)
	}
	return resp.Tenants, nil
}

func printDryRunSummary(tenants []*pb.Tenant, ts *provisioning.TemplateSet) {
	fmt.Println("\nDry run: Would provision accounts for:")
	for _, t := range tenants {
		fmt.Printf("  - %s (%s)\n", t.TenantId, t.DisplayName)
	}
	fmt.Printf("\nEach tenant would receive %d accounts from '%s' template set.\n", len(ts.Templates), ts.Name)
}

func printFinalSummary(stats *provisionStats) {
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Tenants: succeeded=%d, failed=%d, skipped=%d\n", stats.succeeded, stats.failed, stats.skipped)
	fmt.Printf("Accounts: created=%d, skipped=%d\n", stats.totalCreate, stats.totalSkip)
}

func provisionOneTenant(
	ctx context.Context,
	provisioner *provisioning.Provisioner,
	ts *provisioning.TemplateSet,
	t *pb.Tenant,
	index, total int,
	logger *slog.Logger,
	stats *provisionStats,
) error {
	tenantID, err := tenant.NewTenantID(t.TenantId)
	if err != nil {
		logger.Warn("invalid tenant ID, skipping", "tenant_id", t.TenantId, "error", err)
		stats.skipped++
		return nil
	}

	fmt.Printf("\n[%d/%d] Provisioning tenant %s (%s)...\n", index, total, tenantID, t.DisplayName)

	opCtx, cancel := context.WithTimeout(ctx, provisioningTimeout)
	result, err := provisioner.ProvisionFromTemplates(opCtx, tenantID, ts.Templates)
	cancel()

	if err != nil {
		logger.Error("provisioning failed", "tenant_id", tenantID, "error", err)
		stats.failed++
		if !continueOnError {
			return fmt.Errorf("provisioning failed for %s: %w", tenantID, err)
		}
		return nil
	}

	fmt.Printf("  Created: %d, Skipped: %d, Failed: %d\n", result.Created, result.Skipped, result.Failed)

	if result.Failed > 0 {
		stats.failed++
		if !continueOnError {
			return fmt.Errorf("%w for %s: %d accounts", ErrAccountsFailedToProvision, tenantID, result.Failed)
		}
	} else {
		stats.succeeded++
	}

	stats.totalCreate += result.Created
	stats.totalSkip += result.Skipped
	return nil
}
