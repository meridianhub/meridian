// DEPRECATED: This file is no longer used by the Atlas migration system.
//
// The actual Atlas integration is handled by cmd/atlas-loader/main.go, which is referenced
// in the atlas.*.hcl configuration files via the external_schema data source.
//
// Historical purpose: This file was an early attempt at Atlas integration before the
// current external loader pattern was implemented.
//
// This file can be safely removed in a future cleanup PR.

package models

import (
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// LoadModels loads all GORM models for Atlas schema inspection
// DEPRECATED: Not used. See cmd/atlas-loader/main.go instead.
func LoadModels() (*gorm.DB, error) {
	// Create a database connection for Atlas schema inspection
	db, err := gorm.Open(postgres.New(postgres.Config{
		DriverName: "postgres",
		DSN:        getAtlasDSN(),
	}), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// AutoMigrate all models - this creates the schema that Atlas will inspect
	err = db.AutoMigrate(
		&Customer{},
		&Account{},
		&Transaction{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to auto-migrate models: %w", err)
	}

	return db, nil
}

// getAtlasDSN builds the DSN for Atlas schema inspection from environment variables
func getAtlasDSN() string {
	host := getEnvOrDefault("ATLAS_DB_HOST", "localhost")
	user := getEnvOrDefault("ATLAS_DB_USER", "postgres")
	password := getEnvOrDefault("ATLAS_DB_PASSWORD", "postgres")
	dbname := getEnvOrDefault("ATLAS_DB_NAME", "atlas_dev")
	port := getEnvOrDefault("ATLAS_DB_PORT", "5432")

	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		host, user, password, dbname, port)
}

// getEnvOrDefault returns the value of an environment variable or a default value
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
