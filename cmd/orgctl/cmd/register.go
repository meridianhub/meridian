package cmd

import (
	"context"
	"fmt"
	"os"

	organizationv1 "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

var (
	registerID              string
	registerName            string
	registerSettlementAsset string
	registerSubdomain       string
	registerMetadata        map[string]string
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a new organization",
	Long: `Register a new organization in the Meridian platform.

This command creates a new organization entry in the platform registry. The organization
ID must be unique and follow the pattern: alphanumeric characters and underscores only,
1-50 characters.

The operation is idempotent - registering an existing organization ID will succeed
without creating a duplicate.

Examples:
  # Register a bank with GBP settlement
  orgctl register --id=acme_bank --name="Acme Bank" --settlement-asset=GBP

  # Register with custom settlement asset
  orgctl register --id=un_wfp --name="UN World Food Program" --settlement-asset=RICE-VOUCHER

  # Register with subdomain and metadata
  orgctl register --id=test_org --name="Test Org" --settlement-asset=USD \
    --subdomain=test.demo.meridian.io --metadata tier=enterprise`,
	RunE: runRegister,
}

func init() {
	rootCmd.AddCommand(registerCmd)

	registerCmd.Flags().StringVar(&registerID, "id", "", "Organization ID (required, alphanumeric + underscore, 1-50 chars)")
	registerCmd.Flags().StringVar(&registerName, "name", "", "Display name (required)")
	registerCmd.Flags().StringVar(&registerSettlementAsset, "settlement-asset", "", "Primary settlement asset (required, e.g., GBP, USD, GPU-HOUR)")
	registerCmd.Flags().StringVar(&registerSubdomain, "subdomain", "", "API subdomain (optional)")
	registerCmd.Flags().StringToStringVar(&registerMetadata, "metadata", nil, "Key-value metadata (optional, format: key=value)")

	_ = registerCmd.MarkFlagRequired("id")
	_ = registerCmd.MarkFlagRequired("name")
	_ = registerCmd.MarkFlagRequired("settlement-asset")
}

func runRegister(_ *cobra.Command, _ []string) error {
	orgClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = orgClient.Close() }()

	// Convert metadata to protobuf Struct
	var metadata *structpb.Struct
	if len(registerMetadata) > 0 {
		metadataMap := make(map[string]interface{})
		for k, v := range registerMetadata {
			metadataMap[k] = v
		}
		var metadataErr error
		metadata, metadataErr = structpb.NewStruct(metadataMap)
		if metadataErr != nil {
			fmt.Fprintf(os.Stderr, "Error: Invalid metadata: %v\n", metadataErr)
			return metadataErr
		}
	}

	req := &organizationv1.InitiateOrganizationRequest{
		OrganizationId:  registerID,
		DisplayName:     registerName,
		SettlementAsset: registerSettlementAsset,
		Subdomain:       registerSubdomain,
		Metadata:        metadata,
	}

	resp, err := orgClient.InitiateOrganization(context.Background(), req)
	exitCode := handleGRPCError(err, "register organization")
	if exitCode != 0 && err != nil {
		return err
	}

	if resp != nil && resp.Organization != nil {
		org := resp.Organization
		fmt.Printf("Organization registered successfully:\n")
		fmt.Printf("  ID:               %s\n", org.OrganizationId)
		fmt.Printf("  Name:             %s\n", org.DisplayName)
		fmt.Printf("  Settlement Asset: %s\n", org.SettlementAsset)
		fmt.Printf("  Status:           %s\n", org.Status.String())
		if org.Subdomain != "" {
			fmt.Printf("  Subdomain:        %s\n", org.Subdomain)
		}
		fmt.Printf("  Created At:       %s\n", org.CreatedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
	}

	return nil
}
