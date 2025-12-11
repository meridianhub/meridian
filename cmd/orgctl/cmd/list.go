package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	organizationv1 "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	"github.com/spf13/cobra"
)

// ErrInvalidStatus is returned when an invalid status string is provided.
var ErrInvalidStatus = errors.New("invalid status: must be one of: active, suspended, deprovisioned")

var (
	listStatus   string
	listPageSize int32
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all organizations",
	Long: `List all organizations in the Meridian platform.

This command retrieves and displays all registered organizations. You can filter
by status to show only active, suspended, or deprovisioned organizations.

Examples:
  # List all organizations
  orgctl list

  # List only active organizations
  orgctl list --status=active

  # List suspended organizations
  orgctl list --status=suspended

  # List with custom page size
  orgctl list --page-size=100`,
	RunE: runList,
}

func init() {
	rootCmd.AddCommand(listCmd)

	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (active, suspended, deprovisioned)")
	listCmd.Flags().Int32Var(&listPageSize, "page-size", 50, "Number of results per page (max 1000)")
}

func runList(_ *cobra.Command, _ []string) error {
	orgClient, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create client: %v\n", err)
		return err
	}
	defer func() { _ = orgClient.Close() }()

	req := &organizationv1.ListOrganizationsRequest{
		PageSize: listPageSize,
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

	resp, err := orgClient.ListOrganizations(context.Background(), req)
	exitCode := handleGRPCError(err, "list organizations")
	if exitCode != 0 && err != nil {
		return err
	}

	if resp == nil || len(resp.Organizations) == 0 {
		fmt.Println("No organizations found")
		return nil
	}

	// Print as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSTATUS\tSETTLEMENT ASSET\tCREATED AT")
	_, _ = fmt.Fprintln(w, "--\t----\t------\t----------------\t----------")

	for _, org := range resp.Organizations {
		createdAt := ""
		if org.CreatedAt != nil {
			createdAt = org.CreatedAt.AsTime().Format("2006-01-02 15:04:05")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			org.OrganizationId,
			org.DisplayName,
			formatStatus(org.Status),
			org.SettlementAsset,
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

// parseStatus converts a string status to protobuf OrganizationStatus.
func parseStatus(s string) (organizationv1.OrganizationStatus, error) {
	switch strings.ToLower(s) {
	case "active":
		return organizationv1.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE, nil
	case "suspended":
		return organizationv1.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED, nil
	case "deprovisioned":
		return organizationv1.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED, nil
	default:
		return organizationv1.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED,
			fmt.Errorf("%w: got '%s'", ErrInvalidStatus, s)
	}
}

// formatStatus converts protobuf OrganizationStatus to a display string.
func formatStatus(s organizationv1.OrganizationStatus) string {
	switch s {
	case organizationv1.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE:
		return "active"
	case organizationv1.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED:
		return "suspended"
	case organizationv1.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED:
		return "deprovisioned"
	case organizationv1.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED:
		return "unspecified"
	default:
		return "unknown"
	}
}
