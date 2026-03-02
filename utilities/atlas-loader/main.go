// Package main provides the Atlas GORM schema loader for extracting database schema from Go models.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"ariga.io/atlas-provider-gorm/gormschema"
	capersistence "github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	fapersistence "github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	partypersistence "github.com/meridianhub/meridian/services/party/adapters/persistence"
	popersistence "github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/domain/models"
)

// Schema filter constants for selecting service-specific models.
// With database-per-service architecture, each service has its own database
// and tenant provisioner creates org_{tenant_id} schemas as needed.
const (
	schemaCurrentAccount      = "current_account"
	schemaPositionKeeping     = "position_keeping"
	schemaFinancialAccounting = "financial_accounting"
	schemaIdentity            = "identity"
	schemaParty               = "party"
	schemaPaymentOrder        = "payment_order"
	schemaPlatform            = "platform"
)

func main() {
	// Parse schema filter flag
	schemaFilter := flag.String("schema", "", "Filter models by schema (current_account, position_keeping, financial_accounting, identity, party, payment_order, platform)")
	flag.Parse()

	// Determine which models to load based on schema filter
	var modelList []interface{}

	switch *schemaFilter {
	case schemaCurrentAccount:
		// Use service-specific entities that match the actual migrations
		// Customer uses shared model (no service-specific entity yet)
		// Account uses CurrentAccountEntity which includes Version, OverdraftRate, etc.
		// Lien uses LienEntity for balance holds
		modelList = []interface{}{
			&models.Customer{},
			&capersistence.CurrentAccountEntity{},
			&capersistence.LienEntity{},
		}
	case schemaPositionKeeping:
		// FinancialPositionLog references Account via AccountID, so Account must be included
		// for GORM to generate proper foreign key constraints
		modelList = []interface{}{
			&models.Account{}, // FK reference
			&models.FinancialPositionLog{},
			&models.TransactionLogEntry{},
			&models.TransactionLineage{},
			&models.AuditTrailEntry{},
		}
	case schemaFinancialAccounting:
		// Financial accounting has its own schema for ledger and booking logs
		modelList = []interface{}{
			&fapersistence.FinancialBookingLogEntity{},
			&fapersistence.LedgerPostingEntity{},
		}
	case schemaIdentity:
		// Identity service for platform user accounts, roles, and invitations
		modelList = []interface{}{
			&identitypersistence.IdentityEntity{},
			&identitypersistence.RoleAssignmentEntity{},
			&identitypersistence.InvitationEntity{},
		}
	case schemaParty:
		// Party service for customer/organization identity management
		modelList = []interface{}{
			&partypersistence.PartyEntity{},
			&partypersistence.PartyTypeDefinitionEntity{},
		}
	case schemaPaymentOrder:
		// Payment order service for payment processing
		modelList = []interface{}{
			&popersistence.PaymentOrderEntity{},
		}
	case schemaPlatform:
		// Platform/tenant service for multi-tenancy management
		modelList = []interface{}{
			&tenantpersistence.TenantEntity{},
		}
	case "":
		// No filter - load all models (for backward compatibility)
		modelList = []interface{}{
			&models.Customer{},
			&models.Account{},
			&models.FinancialPositionLog{},
			&models.TransactionLogEntry{},
			&models.TransactionLineage{},
			&models.AuditTrailEntry{},
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown schema filter: %s\n", *schemaFilter)
		os.Exit(1)
	}

	stmts, err := gormschema.New("postgres").Load(modelList...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load gorm schema: %v\n", err)
		os.Exit(1)
	}

	// Output table DDL directly without schema creation statements.
	// With database-per-service architecture:
	// - Each service has its own database (meridian_current_account, meridian_party, etc.)
	// - Tenant provisioner creates org_{tenant_id} schemas before running migrations
	// - Tables use unqualified names with search_path routing to tenant schemas
	if _, err := io.WriteString(os.Stdout, stmts); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write schema: %v\n", err)
		os.Exit(1)
	}
}
