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
	"github.com/meridianhub/meridian/shared/domain/models"
)

const (
	schemaCurrentAccount      = "current_account"
	schemaPositionKeeping     = "position_keeping"
	schemaFinancialAccounting = "financial_accounting"
)

func main() {
	// Parse schema filter flag
	schemaFilter := flag.String("schema", "", "Filter models by schema (current_account, position_keeping, financial_accounting)")
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

	// Prepend CREATE SCHEMA statements for all referenced schemas
	output := stmts
	if *schemaFilter != "" {
		var schemaStmt string
		switch *schemaFilter {
		case schemaPositionKeeping:
			// For position_keeping, we need both schemas since transactions reference accounts
			schemaStmt = "CREATE SCHEMA IF NOT EXISTS current_account;\nCREATE SCHEMA IF NOT EXISTS position_keeping;\n\n"
		case schemaFinancialAccounting:
			schemaStmt = "CREATE SCHEMA IF NOT EXISTS financial_accounting;\n\n"
		default:
			schemaStmt = fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;\n\n", *schemaFilter)
		}
		output = schemaStmt + stmts
	}

	if _, err := io.WriteString(os.Stdout, output); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write schema: %v\n", err)
		os.Exit(1)
	}
}
