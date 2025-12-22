package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <tenant-id>",
	Short: "Show tenant provisioning status",
	Long: `Show detailed provisioning status for a tenant.

This command retrieves and displays the provisioning progress for a tenant,
including per-service status, migration versions, and error details.

Use this command to monitor the progress of async tenant provisioning or
to diagnose provisioning failures.

Examples:
  # Show provisioning status for a tenant
  tenantctl status acme_bank

  # Show status for a tenant being provisioned
  tenantctl status new_tenant`,
	Args: cobra.ExactArgs(1),
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(_ *cobra.Command, args []string) error {
	tenantID := args[0]

	tenantClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = tenantClient.Close() }()

	req := &tenantv1.GetTenantProvisioningStatusRequest{
		TenantId: tenantID,
	}

	resp, err := tenantClient.GetTenantProvisioningStatus(context.Background(), req)
	if err != nil {
		exitCode := handleGRPCError(err, fmt.Sprintf("get provisioning status for '%s'", tenantID))
		if exitCode != 0 {
			return err
		}
		return nil
	}

	// Display overall status
	fmt.Printf("Tenant: %s\n", resp.TenantId)
	fmt.Printf("Overall Status: %s\n", formatStatus(resp.OverallStatus))

	if resp.ErrorMessage != "" {
		fmt.Printf("Error: %s\n", resp.ErrorMessage)
	}

	// Display service table
	if len(resp.Services) == 0 {
		fmt.Println("\nNo service provisioning records found.")
	} else {
		fmt.Println()
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "SERVICE\tSTATUS\tMIGRATION VERSION\tERROR")
		_, _ = fmt.Fprintln(w, "-------\t------\t-----------------\t-----")

		for _, svc := range resp.Services {
			errMsg := svc.ErrorMessage
			if len(errMsg) > 50 {
				errMsg = errMsg[:47] + "..."
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				svc.ServiceName,
				formatServiceStatus(svc.Status),
				svc.MigrationVersion,
				errMsg,
			)
		}
		_ = w.Flush()
	}

	// Show completion percentage
	completed := 0
	for _, svc := range resp.Services {
		if svc.Status == tenantv1.ServiceProvisioningStatus_STATUS_COMPLETED {
			completed++
		}
	}
	if len(resp.Services) > 0 {
		pct := (float64(completed) / float64(len(resp.Services))) * 100
		fmt.Printf("\nProgress: %.0f%% (%d/%d services)\n", pct, completed, len(resp.Services))
	}

	return nil
}

// formatServiceStatus converts protobuf ServiceProvisioningStatus to a display string.
func formatServiceStatus(s tenantv1.ServiceProvisioningStatus_Status) string {
	switch s {
	case tenantv1.ServiceProvisioningStatus_STATUS_PENDING:
		return "pending"
	case tenantv1.ServiceProvisioningStatus_STATUS_IN_PROGRESS:
		return "in_progress"
	case tenantv1.ServiceProvisioningStatus_STATUS_COMPLETED:
		return "completed"
	case tenantv1.ServiceProvisioningStatus_STATUS_FAILED:
		return "failed"
	case tenantv1.ServiceProvisioningStatus_STATUS_UNSPECIFIED:
		return "unspecified"
	default:
		return "unknown"
	}
}
