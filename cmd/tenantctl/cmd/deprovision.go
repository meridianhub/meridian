package cmd

import (
	"context"
	"fmt"
	"os"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/spf13/cobra"
)

var deprovisionConfirm bool

var deprovisionCmd = &cobra.Command{
	Use:   "deprovision <tenant-id>",
	Short: "Deprovision a tenant",
	Long: `Deprovision a tenant in the Meridian platform.

This command changes the tenant status to 'deprovisioned'. This is a lifecycle
transition that marks the tenant as no longer active. The --confirm flag is
required to prevent accidental deprovisioning.

The operation is idempotent - deprovisioning a non-existent or already deprovisioned
tenant will succeed without error.

Examples:
  # Deprovision a tenant
  tenantctl deprovision acme_bank --confirm

  # Deprovision a test tenant
  tenantctl deprovision test_org --confirm`,
	Args: cobra.ExactArgs(1),
	RunE: runDeprovision,
}

func init() {
	rootCmd.AddCommand(deprovisionCmd)

	deprovisionCmd.Flags().BoolVar(&deprovisionConfirm, "confirm", false, "Confirm the deprovisioning operation (required)")
	_ = deprovisionCmd.MarkFlagRequired("confirm")
}

func runDeprovision(_ *cobra.Command, args []string) error {
	tenantID := args[0]

	if !deprovisionConfirm {
		fmt.Fprintf(os.Stderr, "Error: --confirm flag is required to deprovision a tenant\n")
		return ErrConfirmRequired
	}

	tenantClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = tenantClient.Close() }()

	req := &tenantv1.UpdateTenantStatusRequest{
		TenantId: tenantID,
		Status:   tenantv1.TenantStatus_TENANT_STATUS_DEPROVISIONED,
	}

	resp, err := tenantClient.UpdateTenantStatus(context.Background(), req)
	exitCode := handleGRPCError(err, "deprovision tenant")
	if exitCode != 0 && err != nil {
		return err
	}

	if resp != nil && resp.Tenant != nil {
		tenant := resp.Tenant
		fmt.Printf("Tenant deprovisioned successfully:\n")
		fmt.Printf("  ID:               %s\n", tenant.TenantId)
		fmt.Printf("  Name:             %s\n", tenant.DisplayName)
		fmt.Printf("  Status:           %s\n", tenant.Status.String())
		if tenant.DeprovisionedAt != nil {
			fmt.Printf("  Deprovisioned At: %s\n", tenant.DeprovisionedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
		}
	}

	return nil
}
