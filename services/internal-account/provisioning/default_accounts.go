// Package provisioning handles the automated creation of default internal accounts
// when new tenants are provisioned. This ensures each tenant has the essential accounts
// needed for standard banking operations (clearing, revenue, expense, suspense).
package provisioning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccountTemplate defines a default account to be created during tenant provisioning.
// Templates specify the account properties needed to call InitiateInternalAccount.
type AccountTemplate struct {
	// Code is the unique business identifier for the account (e.g., "CLR-GBP-DEPOSIT").
	Code string

	// Name is the human-readable display name (e.g., "GBP Deposit Clearing").
	Name string

	// ProductTypeCode references the account type definition from the Product Directory.
	// Convention: <BEHAVIOR_CLASS>_<INSTRUMENT_CODE> (e.g., "CLEARING_GBP", "REVENUE_GBP").
	ProductTypeCode string

	// ClearingPurpose specifies the operational purpose of clearing accounts.
	// Only applicable when the product type behavior class is CLEARING.
	// Must be CLEARING_PURPOSE_UNSPECIFIED for non-clearing account types.
	// Values: DEPOSIT (incoming funds), WITHDRAWAL (outgoing funds),
	// SETTLEMENT (inter-party clearing), GENERAL (multi-purpose clearing).
	ClearingPurpose pb.ClearingPurpose

	// InstrumentCode references the instrument from Reference Data (e.g., "GBP", "USD").
	InstrumentCode string

	// Dimension describes the unit dimension (CURRENCY, ENERGY, COMPUTE, etc.).
	Dimension string

	// Description provides additional context about the account's purpose.
	Description string
}

// Dimension constants matching the database constraint.
const (
	DimensionCurrency = "CURRENCY"
	DimensionEnergy   = "ENERGY"
	DimensionMass     = "MASS"
	DimensionVolume   = "VOLUME"
	DimensionTime     = "TIME"
	DimensionCompute  = "COMPUTE"
	DimensionCarbon   = "CARBON"
	DimensionData     = "DATA"
	DimensionCount    = "COUNT"
)

// DefaultAccounts defines the standard accounts created for every new tenant.
// These accounts support core banking operations:
//   - Clearing accounts for deposit/withdrawal settlement across major currencies
//     Each clearing account specifies a ClearingPurpose (DEPOSIT or WITHDRAWAL)
//     to enable filtering and routing of funds by settlement direction.
//   - Revenue accounts for tracking fee income
//   - Expense accounts for operational costs
//   - Suspense account for unidentified transactions
var DefaultAccounts = []AccountTemplate{
	// GBP Clearing Accounts
	{
		Code:            "CLR-GBP-DEPOSIT",
		Name:            "GBP Deposit Clearing",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT,
		InstrumentCode:  "GBP",
		Dimension:       DimensionCurrency,
		Description:     "Clearing account for GBP deposits pending settlement",
	},
	{
		Code:            "CLR-GBP-WITHDRAW",
		Name:            "GBP Withdrawal Clearing",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL,
		InstrumentCode:  "GBP",
		Dimension:       DimensionCurrency,
		Description:     "Clearing account for GBP withdrawals pending settlement",
	},

	// USD Clearing Accounts
	{
		Code:            "CLR-USD-DEPOSIT",
		Name:            "USD Deposit Clearing",
		ProductTypeCode: "CLEARING_USD",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT,
		InstrumentCode:  "USD",
		Dimension:       DimensionCurrency,
		Description:     "Clearing account for USD deposits pending settlement",
	},
	{
		Code:            "CLR-USD-WITHDRAW",
		Name:            "USD Withdrawal Clearing",
		ProductTypeCode: "CLEARING_USD",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL,
		InstrumentCode:  "USD",
		Dimension:       DimensionCurrency,
		Description:     "Clearing account for USD withdrawals pending settlement",
	},

	// EUR Clearing Accounts
	{
		Code:            "CLR-EUR-DEPOSIT",
		Name:            "EUR Deposit Clearing",
		ProductTypeCode: "CLEARING_EUR",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT,
		InstrumentCode:  "EUR",
		Dimension:       DimensionCurrency,
		Description:     "Clearing account for EUR deposits pending settlement",
	},
	{
		Code:            "CLR-EUR-WITHDRAW",
		Name:            "EUR Withdrawal Clearing",
		ProductTypeCode: "CLEARING_EUR",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL,
		InstrumentCode:  "EUR",
		Dimension:       DimensionCurrency,
		Description:     "Clearing account for EUR withdrawals pending settlement",
	},

	// Revenue Accounts
	{
		Code:            "REV-TRANSACTION-FEE",
		Name:            "Transaction Fee Revenue",
		ProductTypeCode: "REVENUE_GBP",
		InstrumentCode:  "GBP",
		Dimension:       DimensionCurrency,
		Description:     "Revenue account for transaction processing fees",
	},
	{
		Code:            "REV-OVERDRAFT-INTEREST",
		Name:            "Overdraft Interest Revenue",
		ProductTypeCode: "REVENUE_GBP",
		InstrumentCode:  "GBP",
		Dimension:       DimensionCurrency,
		Description:     "Revenue account for overdraft interest charges",
	},
	{
		Code:            "REV-ACCOUNT-FEE",
		Name:            "Account Maintenance Fee Revenue",
		ProductTypeCode: "REVENUE_GBP",
		InstrumentCode:  "GBP",
		Dimension:       DimensionCurrency,
		Description:     "Revenue account for account maintenance fees",
	},

	// Expense Accounts
	{
		Code:            "EXP-PAYMENT-PROCESSING",
		Name:            "Payment Processing Expense",
		ProductTypeCode: "EXPENSE_GBP",
		InstrumentCode:  "GBP",
		Dimension:       DimensionCurrency,
		Description:     "Expense account for payment processing costs",
	},

	// Suspense Account
	{
		Code:            "SUS-GENERAL",
		Name:            "General Suspense Account",
		ProductTypeCode: "SUSPENSE_GBP",
		InstrumentCode:  "GBP",
		Dimension:       DimensionCurrency,
		Description:     "Suspense account for unidentified or pending transactions",
	},
}

// InternalAccountService defines the interface for creating internal accounts.
// This abstraction allows the provisioner to work with both the gRPC service and test mocks.
type InternalAccountService interface {
	InitiateInternalAccount(ctx context.Context, req *pb.InitiateInternalAccountRequest) (*pb.InitiateInternalAccountResponse, error)
}

// Sentinel errors for provisioning operations.
var (
	// ErrServiceNotConfigured is returned when the internal account service is not set.
	ErrServiceNotConfigured = errors.New("internal account service not configured")
)

// Result contains the outcome of provisioning default accounts.
type Result struct {
	// TenantID is the tenant that was provisioned.
	TenantID tenant.TenantID

	// Created is the number of accounts successfully created.
	Created int

	// Skipped is the number of accounts that already existed (idempotent).
	Skipped int

	// Failed is the number of accounts that failed to create.
	Failed int

	// Errors contains any errors encountered during provisioning.
	Errors []error
}

// Provisioner creates default internal accounts for tenants.
type Provisioner struct {
	service InternalAccountService
	logger  *slog.Logger
}

// NewProvisioner creates a new Provisioner with the given service.
func NewProvisioner(service InternalAccountService, logger *slog.Logger) *Provisioner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provisioner{
		service: service,
		logger:  logger,
	}
}

// ProvisionDefaultAccounts creates all default accounts for a tenant.
// This operation is idempotent - existing accounts are skipped without error.
//
// The idempotency key format is: "default-account:{tenantID}:{accountCode}"
// This ensures the same account cannot be created twice for the same tenant.
func (p *Provisioner) ProvisionDefaultAccounts(ctx context.Context, tenantID tenant.TenantID) (*Result, error) {
	return p.ProvisionFromTemplates(ctx, tenantID, DefaultAccounts)
}

// ProvisionFromTemplates creates accounts from a custom template list.
// Use this for tenant-specific account configurations (e.g., energy companies).
func (p *Provisioner) ProvisionFromTemplates(ctx context.Context, tenantID tenant.TenantID, templates []AccountTemplate) (*Result, error) {
	if p.service == nil {
		return nil, ErrServiceNotConfigured
	}

	result := &Result{
		TenantID: tenantID,
		Errors:   make([]error, 0),
	}

	p.logger.Info("starting default account provisioning",
		"tenant_id", tenantID,
		"template_count", len(templates))

	for _, template := range templates {
		// Build idempotency key scoped to tenant and account code
		idempotencyKey := fmt.Sprintf("default-account:%s:%s", tenantID, template.Code)

		req := &pb.InitiateInternalAccountRequest{
			AccountCode:     template.Code,
			Name:            template.Name,
			ProductTypeCode: template.ProductTypeCode,
			ClearingPurpose: template.ClearingPurpose,
			InstrumentCode:  template.InstrumentCode,
			Description:     template.Description,
			IdempotencyKey: &commonpb.IdempotencyKey{
				Key: idempotencyKey,
			},
		}

		_, err := p.service.InitiateInternalAccount(ctx, req)
		if err != nil {
			// Check if account already exists (idempotent case)
			if isDuplicateError(err) {
				p.logger.Debug("account already exists, skipping",
					"tenant_id", tenantID,
					"account_code", template.Code)
				result.Skipped++
				continue
			}

			// Record error but continue with other accounts
			p.logger.Warn("failed to create default account",
				"tenant_id", tenantID,
				"account_code", template.Code,
				"error", err)
			result.Failed++
			result.Errors = append(result.Errors, fmt.Errorf("failed to create %s: %w", template.Code, err))
			continue
		}

		p.logger.Debug("created default account",
			"tenant_id", tenantID,
			"account_code", template.Code,
			"product_type_code", template.ProductTypeCode)
		result.Created++
	}

	p.logger.Info("completed default account provisioning",
		"tenant_id", tenantID,
		"created", result.Created,
		"skipped", result.Skipped,
		"failed", result.Failed)

	return result, nil
}

// isDuplicateError checks if the error indicates the account already exists.
// This handles the idempotent case where we can safely skip.
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.AlreadyExists
}
