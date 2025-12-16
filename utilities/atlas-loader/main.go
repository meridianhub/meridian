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
	partypersistence "github.com/meridianhub/meridian/services/party/adapters/persistence"
	popersistence "github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/domain/models"
)

const (
	schemaCurrentAccount      = "current_account"
	schemaPositionKeeping     = "position_keeping"
	schemaFinancialAccounting = "financial_accounting"
	schemaParty               = "party"
	schemaPaymentOrder        = "payment_order"
	schemaPlatform            = "platform"
)

func main() {
	// Parse schema filter flag
	schemaFilter := flag.String("schema", "", "Filter models by schema (current_account, position_keeping, financial_accounting, party, payment_order, platform)")
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
	case schemaParty:
		// Party service for customer/organization identity management
		modelList = []interface{}{
			&partypersistence.PartyEntity{},
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

	// Prepend CREATE SCHEMA statements for referenced schemas.
	// With database-per-service architecture, most services use unqualified table names
	// in the default public schema. Only legacy services (position_keeping, financial_accounting,
	// current_account) still use schema-qualified names.
	output := stmts
	if *schemaFilter != "" {
		var schemaStmt string
		switch *schemaFilter {
		case schemaPositionKeeping:
			// For position_keeping, we need both schemas since transactions reference accounts
			schemaStmt = "CREATE SCHEMA IF NOT EXISTS current_account;\nCREATE SCHEMA IF NOT EXISTS position_keeping;\n\n"
		case schemaFinancialAccounting:
			schemaStmt = "CREATE SCHEMA IF NOT EXISTS financial_accounting;\n\n"
		case schemaCurrentAccount:
			schemaStmt = "CREATE SCHEMA IF NOT EXISTS current_account;\n\n"
		case schemaParty, schemaPaymentOrder, schemaPlatform:
			// These services use unqualified table names for multi-tenant schema routing.
			// Schema is created externally during org provisioning or set via search_path.
			// No CREATE SCHEMA prepended here.
			schemaStmt = ""
		default:
			schemaStmt = ""
		}
		output = schemaStmt + stmts
	}

	if _, err := io.WriteString(os.Stdout, output); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write schema: %v\n", err)
		os.Exit(1)
	}
}
