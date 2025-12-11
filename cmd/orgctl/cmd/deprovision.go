package cmd

import (
	"context"
	"fmt"
	"os"

	organizationv1 "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	"github.com/spf13/cobra"
)

var deprovisionConfirm bool

var deprovisionCmd = &cobra.Command{
	Use:   "deprovision <organization-id>",
	Short: "Deprovision an organization",
	Long: `Deprovision an organization in the Meridian platform.

This command changes the organization status to 'deprovisioned'. This is a lifecycle
transition that marks the organization as no longer active. The --confirm flag is
required to prevent accidental deprovisioning.

The operation is idempotent - deprovisioning a non-existent or already deprovisioned
organization will succeed without error.

Note: This operation cannot be retried automatically due to its non-idempotent nature.
If the operation fails due to a transient error, you must manually retry.

Examples:
  # Deprovision an organization
  orgctl deprovision acme_bank --confirm

  # Deprovision a test organization
  orgctl deprovision test_org --confirm`,
	Args: cobra.ExactArgs(1),
	RunE: runDeprovision,
}

func init() {
	rootCmd.AddCommand(deprovisionCmd)

	deprovisionCmd.Flags().BoolVar(&deprovisionConfirm, "confirm", false, "Confirm the deprovisioning operation (required)")
	_ = deprovisionCmd.MarkFlagRequired("confirm")
}

func runDeprovision(_ *cobra.Command, args []string) error {
	orgID := args[0]

	if !deprovisionConfirm {
		fmt.Fprintf(os.Stderr, "Error: --confirm flag is required to deprovision an organization\n")
		return ErrConfirmRequired
	}

	orgClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = orgClient.Close() }()

	req := &organizationv1.UpdateOrganizationStatusRequest{
		OrganizationId: orgID,
		Status:         organizationv1.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED,
	}

	resp, err := orgClient.UpdateOrganizationStatus(context.Background(), req)
	exitCode := handleGRPCError(err, "deprovision organization")
	if exitCode != 0 && err != nil {
		return err
	}

	if resp != nil && resp.Organization != nil {
		org := resp.Organization
		fmt.Printf("Organization deprovisioned successfully:\n")
		fmt.Printf("  ID:               %s\n", org.OrganizationId)
		fmt.Printf("  Name:             %s\n", org.DisplayName)
		fmt.Printf("  Status:           %s\n", org.Status.String())
		if org.DeprovisionedAt != nil {
			fmt.Printf("  Deprovisioned At: %s\n", org.DeprovisionedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
		}
	}

	return nil
}
