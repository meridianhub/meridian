package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

// slugReplacementPattern matches any character that is not lowercase alphanumeric or hyphen.
var slugReplacementPattern = regexp.MustCompile(`[^a-z0-9-]+`)

// consecutiveHyphensPattern matches multiple consecutive hyphens.
var consecutiveHyphensPattern = regexp.MustCompile(`-+`)

// ErrSlugAutoGenerationFailed is returned when auto-generation cannot produce a valid slug.
var ErrSlugAutoGenerationFailed = errors.New("slug auto-generation failed")

// generateSlugFromName converts a display name to a URL-safe slug following DNS subdomain rules.
// It converts to lowercase, replaces non-alphanumeric characters (except hyphens) with a single hyphen,
// collapses consecutive hyphens, trims leading/trailing hyphens, and truncates to 63 characters if needed.
func generateSlugFromName(name string) string {
	// Convert to lowercase
	slug := strings.ToLower(name)

	// Replace any sequence of non-alphanumeric characters (except hyphens) with a single hyphen
	slug = slugReplacementPattern.ReplaceAllString(slug, "-")

	// Collapse consecutive hyphens into a single hyphen
	slug = consecutiveHyphensPattern.ReplaceAllString(slug, "-")

	// Trim leading and trailing hyphens
	slug = strings.Trim(slug, "-")

	// Truncate to 63 characters if needed (DNS subdomain limit)
	if len(slug) > 63 {
		slug = slug[:63]
		// Ensure we don't end with a hyphen after truncation
		slug = strings.TrimRight(slug, "-")
	}

	return slug
}

var (
	registerID              string
	registerName            string
	registerSettlementAsset string
	registerSubdomain       string
	registerSlug            string
	registerMetadata        map[string]string
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a new tenant",
	Long: `Register a new tenant in the Meridian platform.

This command creates a new tenant entry in the platform registry. The tenant
ID must be unique and follow the pattern: alphanumeric characters and underscores only,
1-50 characters.

The CLI treats duplicate registrations as idempotent - if the tenant already
exists, the command exits successfully without error.

Examples:
  # Register a bank with GBP settlement
  tenantctl register --id=acme_bank --name="Acme Bank" --settlement-asset=GBP

  # Register with custom settlement asset
  tenantctl register --id=un_wfp --name="UN World Food Program" --settlement-asset=RICE-VOUCHER

  # Register with subdomain and metadata
  tenantctl register --id=test_org --name="Test Org" --settlement-asset=USD \
    --subdomain=test.demo.meridian.io --metadata tier=enterprise

  # Register with explicit slug for API subdomain
  tenantctl register --id=acme_bank --name="Acme Bank" --settlement-asset=GBP --slug=acme-bank`,
	RunE: runRegister,
}

func init() {
	rootCmd.AddCommand(registerCmd)

	registerCmd.Flags().StringVar(&registerID, "id", "", "Tenant ID (required, alphanumeric + underscore, 1-50 chars)")
	registerCmd.Flags().StringVar(&registerName, "name", "", "Display name (required)")
	registerCmd.Flags().StringVar(&registerSettlementAsset, "settlement-asset", "", "Primary settlement asset (required, e.g., GBP, USD, GPU-HOUR)")
	registerCmd.Flags().StringVar(&registerSubdomain, "subdomain", "", "API subdomain (optional)")
	registerCmd.Flags().StringVar(&registerSlug, "slug", "", "URL-safe slug for API subdomain (auto-generated if not provided)")
	registerCmd.Flags().StringToStringVar(&registerMetadata, "metadata", nil, "Key-value metadata (optional, format: key=value)")

	_ = registerCmd.MarkFlagRequired("id")
	_ = registerCmd.MarkFlagRequired("name")
	_ = registerCmd.MarkFlagRequired("settlement-asset")
}

func runRegister(_ *cobra.Command, _ []string) error {
	// Determine slug: use provided value or auto-generate from display name
	slug := registerSlug
	if slug == "" {
		slug = generateSlugFromName(registerName)
		if slug == "" {
			// Auto-generation produced empty slug (e.g., display name was all special characters)
			fmt.Fprintf(os.Stderr, "Error: Could not auto-generate slug from display name '%s'. Please provide --slug flag.\n", registerName)
			return fmt.Errorf("%w: display name '%s' contains no alphanumeric characters", ErrSlugAutoGenerationFailed, registerName)
		}
		fmt.Printf("Auto-generated slug: %s\n", slug)
	}

	// Validate slug (client-side validation before gRPC call)
	if err := domain.ValidateSlug(slug); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Invalid slug: %v\n", err)
		return err
	}

	tenantClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = tenantClient.Close() }()

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

	req := &tenantv1.InitiateTenantRequest{
		TenantId:        registerID,
		DisplayName:     registerName,
		SettlementAsset: registerSettlementAsset,
		Subdomain:       registerSubdomain,
		Slug:            slug,
		Metadata:        metadata,
	}

	resp, err := tenantClient.InitiateTenant(context.Background(), req)
	exitCode := handleGRPCError(err, "register tenant")
	if exitCode != 0 && err != nil {
		return err
	}

	if resp != nil && resp.Tenant != nil {
		tenant := resp.Tenant
		fmt.Printf("Tenant registered successfully:\n")
		fmt.Printf("  ID:               %s\n", tenant.TenantId)
		fmt.Printf("  Name:             %s\n", tenant.DisplayName)
		fmt.Printf("  Settlement Asset: %s\n", tenant.SettlementAsset)
		fmt.Printf("  Status:           %s\n", tenant.Status.String())
		if tenant.Slug != "" {
			fmt.Printf("  Slug:             %s\n", tenant.Slug)
		}
		if tenant.Subdomain != "" {
			fmt.Printf("  Subdomain:        %s\n", tenant.Subdomain)
		}
		fmt.Printf("  Created At:       %s\n", tenant.CreatedAt.AsTime().Format("2006-01-02 15:04:05 UTC"))
	}

	return nil
}
