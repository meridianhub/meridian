package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/spf13/cobra"
)

var getOutputFormat string

var getCmd = &cobra.Command{
	Use:   "get <tenant-id>",
	Short: "Get tenant details",
	Long: `Get detailed information about a specific tenant.

This command retrieves and displays all details for the specified tenant,
including metadata, version, and timestamps.

Examples:
  # Get tenant details
  tenantctl get acme_bank

  # Get tenant details as JSON
  tenantctl get acme_bank --output json
  tenantctl get acme_bank -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runGet,
}

func init() {
	rootCmd.AddCommand(getCmd)

	getCmd.Flags().StringVarP(&getOutputFormat, "output", "o", "text", "Output format (text or json)")
}

func runGet(_ *cobra.Command, args []string) error {
	tenantID := args[0]

	tenantClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = tenantClient.Close() }()

	req := &tenantv1.RetrieveTenantRequest{
		TenantId: tenantID,
	}

	resp, err := tenantClient.RetrieveTenant(context.Background(), req)
	if err != nil {
		exitCode := handleGRPCError(err, fmt.Sprintf("get tenant '%s'", tenantID))
		if exitCode != 0 {
			return err
		}
		// NotFound is treated as idempotent success by handleGRPCError (already logged)
		return nil
	}

	if resp == nil || resp.Tenant == nil {
		fmt.Fprintf(os.Stderr, "Error: Tenant not found: %s\n", tenantID)
		return fmt.Errorf("%w: %s", ErrTenantNotFound, tenantID)
	}

	tenant := resp.Tenant

	if getOutputFormat == "json" {
		return outputJSON(tenant)
	}

	outputText(tenant)
	return nil
}

func outputJSON(tenant *tenantv1.Tenant) error {
	output := map[string]interface{}{
		"tenant_id":        tenant.TenantId,
		"display_name":     tenant.DisplayName,
		"settlement_asset": tenant.SettlementAsset,
		"status":           formatStatus(tenant.Status),
		"version":          tenant.Version,
	}
	if tenant.Subdomain != "" {
		output["subdomain"] = tenant.Subdomain
	}
	if tenant.CreatedAt != nil {
		output["created_at"] = tenant.CreatedAt.AsTime().Format("2006-01-02T15:04:05Z")
	}
	if tenant.DeprovisionedAt != nil {
		output["deprovisioned_at"] = tenant.DeprovisionedAt.AsTime().Format("2006-01-02T15:04:05Z")
	}
	if tenant.Metadata != nil {
		output["metadata"] = tenant.Metadata.AsMap()
	}

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to marshal JSON: %v\n", err)
		return err
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func outputText(tenant *tenantv1.Tenant) {
	fmt.Printf("Tenant Details:\n")
	fmt.Printf("  ID:               %s\n", tenant.TenantId)
	fmt.Printf("  Name:             %s\n", tenant.DisplayName)
	fmt.Printf("  Settlement Asset: %s\n", tenant.SettlementAsset)
	fmt.Printf("  Status:           %s\n", formatStatus(tenant.Status))
	fmt.Printf("  Version:          %d\n", tenant.Version)

	if tenant.Subdomain != "" {
		fmt.Printf("  Subdomain:        %s\n", tenant.Subdomain)
	}

	if tenant.CreatedAt != nil {
		fmt.Printf("  Created At:       %s\n", tenant.CreatedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
	}

	if tenant.DeprovisionedAt != nil {
		fmt.Printf("  Deprovisioned At: %s\n", tenant.DeprovisionedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
	}

	if tenant.Metadata != nil && len(tenant.Metadata.Fields) > 0 {
		fmt.Printf("  Metadata:\n")
		for key, value := range tenant.Metadata.AsMap() {
			fmt.Printf("    %s: %v\n", key, value)
		}
	}
}
