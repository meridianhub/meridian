// Package main provides the Atlas GORM schema loader for extracting database schema from Go models.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"ariga.io/atlas-provider-gorm/gormschema"
	"github.com/meridianhub/meridian/internal/domain/models"
)

const (
	schemaCurrentAccount  = "current_account"
	schemaPositionKeeping = "position_keeping"
)

func main() {
	// Parse schema filter flag
	schemaFilter := flag.String("schema", "", "Filter models by schema (current_account, position_keeping)")
	flag.Parse()

	// Determine which models to load based on schema filter
	var modelList []interface{}

	switch *schemaFilter {
	case schemaCurrentAccount:
		modelList = []interface{}{
			&models.Customer{},
			&models.Account{},
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
		// For position_keeping, we need both schemas since transactions reference accounts
		var schemaStmt string
		if *schemaFilter == schemaPositionKeeping {
			schemaStmt = "CREATE SCHEMA IF NOT EXISTS current_account;\nCREATE SCHEMA IF NOT EXISTS position_keeping;\n\n"
		} else {
			schemaStmt = fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;\n\n", *schemaFilter)
		}
		output = schemaStmt + stmts
	}

	if _, err := io.WriteString(os.Stdout, output); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write schema: %v\n", err)
		os.Exit(1)
	}
}
