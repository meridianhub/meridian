// Package main provides the Atlas GORM schema loader for extracting database schema from Go models.
package main

import (
	"fmt"
	"io"
	"os"

	"ariga.io/atlas-provider-gorm/gormschema"
	"github.com/meridianhub/meridian/internal/domain/models"
)

func main() {
	stmts, err := gormschema.New("postgres").Load(
		&models.Account{},
		&models.Transaction{},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load gorm schema: %v\n", err)
		os.Exit(1)
	}
	if _, err := io.WriteString(os.Stdout, stmts); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write schema: %v\n", err)
		os.Exit(1)
	}
}
