package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/spf13/cobra"
)

// ErrInvalidStatus is returned when an invalid status string is provided.
var ErrInvalidStatus = errors.New("invalid status: must be one of: active, suspended, deprovisioned")

var (
	listStatus    string
	listPageSize  int32
	listPageToken string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tenants",
	Long: `List all tenants in the Meridian platform.

This command retrieves and displays all registered tenants. You can filter
by status to show only active, suspended, or deprovisioned tenants.

Examples:
  # List all tenants
  tenantctl list

  # List only active tenants
  tenantctl list --status=active

  # List suspended tenants
  tenantctl list --status=suspended

  # List with custom page size
  tenantctl list --page-size=100

  # Fetch next page using page token
  tenantctl list --page-token=<token>`,
	RunE: runList,
}

func init() {
	rootCmd.AddCommand(listCmd)

	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (active, suspended, deprovisioned)")
	listCmd.Flags().Int32Var(&listPageSize, "page-size", 50, "Number of results per page (max 1000)")
	listCmd.Flags().StringVar(&listPageToken, "page-token", "", "Page token from a previous list response")
}

func runList(_ *cobra.Command, _ []string) error {
	tenantClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = tenantClient.Close() }()

	req := &tenantv1.ListTenantsRequest{
		PageSize:  listPageSize,
		PageToken: listPageToken,
	}

	// Parse status filter
	if listStatus != "" {
		status, parseErr := parseStatus(listStatus)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", parseErr)
			return parseErr
		}
		req.StatusFilter = status
	}

	resp, err := tenantClient.ListTenants(context.Background(), req)
	exitCode := handleGRPCError(err, "list tenants")
	if exitCode != 0 && err != nil {
		return err
	}

	if resp == nil || len(resp.Tenants) == 0 {
		fmt.Println("No tenants found")
		return nil
	}

	// Print as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSLUG\tSTATUS\tSETTLEMENT ASSET\tCREATED AT")
	_, _ = fmt.Fprintln(w, "--\t----\t----\t------\t----------------\t----------")

	for _, tenant := range resp.Tenants {
		createdAt := ""
		if tenant.CreatedAt != nil {
			createdAt = tenant.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
		}
		slug := tenant.Slug
		if slug == "" {
			slug = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			tenant.TenantId,
			tenant.DisplayName,
			slug,
			formatStatus(tenant.Status),
			tenant.SettlementAsset,
			createdAt,
		)
	}
	_ = w.Flush()

	// Show pagination info
	if resp.NextPageToken != "" {
		fmt.Printf("\nMore results available. Use --page-token=%s to fetch next page.\n", resp.NextPageToken)
	}

	return nil
}

// parseStatus converts a string status to protobuf TenantStatus.
func parseStatus(s string) (tenantv1.TenantStatus, error) {
	switch strings.ToLower(s) {
	case "provisioning_pending":
		return tenantv1.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING, nil
	case "active":
		return tenantv1.TenantStatus_TENANT_STATUS_ACTIVE, nil
	case "suspended":
		return tenantv1.TenantStatus_TENANT_STATUS_SUSPENDED, nil
	case "deprovisioned":
		return tenantv1.TenantStatus_TENANT_STATUS_DEPROVISIONED, nil
	default:
		return tenantv1.TenantStatus_TENANT_STATUS_UNSPECIFIED,
			fmt.Errorf("%w: got '%s'", ErrInvalidStatus, s)
	}
}

// formatStatus converts protobuf TenantStatus to a display string.
func formatStatus(s tenantv1.TenantStatus) string {
	switch s {
	case tenantv1.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING:
		return "provisioning_pending"
	case tenantv1.TenantStatus_TENANT_STATUS_PROVISIONING:
		return "provisioning"
	case tenantv1.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED:
		return "provisioning_failed"
	case tenantv1.TenantStatus_TENANT_STATUS_ACTIVE:
		return "active"
	case tenantv1.TenantStatus_TENANT_STATUS_SUSPENDED:
		return "suspended"
	case tenantv1.TenantStatus_TENANT_STATUS_DEPROVISIONED:
		return "deprovisioned"
	case tenantv1.TenantStatus_TENANT_STATUS_UNSPECIFIED:
		return "unspecified"
	default:
		return "unknown"
	}
}
