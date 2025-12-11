package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	organizationv1 "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	"github.com/spf13/cobra"
)

var getOutputJSON bool

var getCmd = &cobra.Command{
	Use:   "get <organization-id>",
	Short: "Get organization details",
	Long: `Get detailed information about a specific organization.

This command retrieves and displays all details for the specified organization,
including metadata, version, and timestamps.

Examples:
  # Get organization details
  orgctl get acme_bank

  # Get organization details as JSON
  orgctl get acme_bank --output=json`,
	Args: cobra.ExactArgs(1),
	RunE: runGet,
}

func init() {
	rootCmd.AddCommand(getCmd)

	getCmd.Flags().BoolVar(&getOutputJSON, "output", false, "Output as JSON")
	getCmd.Flags().Lookup("output").NoOptDefVal = "true"
}

func runGet(_ *cobra.Command, args []string) error {
	orgID := args[0]

	orgClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = orgClient.Close() }()

	req := &organizationv1.RetrieveOrganizationRequest{
		OrganizationId: orgID,
	}

	resp, err := orgClient.RetrieveOrganization(context.Background(), req)
	exitCode := handleGRPCError(err, fmt.Sprintf("get organization '%s'", orgID))
	if exitCode != 0 && err != nil {
		return err
	}

	if resp == nil || resp.Organization == nil {
		fmt.Fprintf(os.Stderr, "Error: Organization not found: %s\n", orgID)
		return fmt.Errorf("%w: %s", ErrOrganizationNotFound, orgID)
	}

	org := resp.Organization

	if getOutputJSON {
		return outputJSON(org)
	}

	outputText(org)
	return nil
}

func outputJSON(org *organizationv1.Organization) error {
	output := map[string]interface{}{
		"organization_id":  org.OrganizationId,
		"display_name":     org.DisplayName,
		"settlement_asset": org.SettlementAsset,
		"status":           formatStatus(org.Status),
		"version":          org.Version,
	}
	if org.Subdomain != "" {
		output["subdomain"] = org.Subdomain
	}
	if org.CreatedAt != nil {
		output["created_at"] = org.CreatedAt.AsTime().Format("2006-01-02T15:04:05Z")
	}
	if org.DeprovisionedAt != nil {
		output["deprovisioned_at"] = org.DeprovisionedAt.AsTime().Format("2006-01-02T15:04:05Z")
	}
	if org.Metadata != nil {
		output["metadata"] = org.Metadata.AsMap()
	}

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to marshal JSON: %v\n", err)
		return err
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func outputText(org *organizationv1.Organization) {
	fmt.Printf("Organization Details:\n")
	fmt.Printf("  ID:               %s\n", org.OrganizationId)
	fmt.Printf("  Name:             %s\n", org.DisplayName)
	fmt.Printf("  Settlement Asset: %s\n", org.SettlementAsset)
	fmt.Printf("  Status:           %s\n", formatStatus(org.Status))
	fmt.Printf("  Version:          %d\n", org.Version)

	if org.Subdomain != "" {
		fmt.Printf("  Subdomain:        %s\n", org.Subdomain)
	}

	if org.CreatedAt != nil {
		fmt.Printf("  Created At:       %s\n", org.CreatedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
	}

	if org.DeprovisionedAt != nil {
		fmt.Printf("  Deprovisioned At: %s\n", org.DeprovisionedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
	}

	if org.Metadata != nil && len(org.Metadata.Fields) > 0 {
		fmt.Printf("  Metadata:\n")
		for key, value := range org.Metadata.AsMap() {
			fmt.Printf("    %s: %v\n", key, value)
		}
	}
}
