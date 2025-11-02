package models

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// LoadModels loads all GORM models for Atlas schema inspection
// This function is used by Atlas to discover the schema from Go structs
func LoadModels() (*gorm.DB, error) {
	// Create an in-memory database connection for schema inspection
	db, err := gorm.Open(postgres.New(postgres.Config{
		DriverName: "postgres",
		DSN:        "host=localhost user=postgres password=postgres dbname=atlas_dev port=5432 sslmode=disable",
	}), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// AutoMigrate all models - this creates the schema that Atlas will inspect
	err = db.AutoMigrate(
		&Account{},
		&Transaction{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to auto-migrate models: %w", err)
	}

	return db, nil
}
