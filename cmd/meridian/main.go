// Package main is the entry point for the Meridian unified binary.
//
// It supports a --migrate flag that applies all embedded service migrations
// to CockroachDB before starting the application.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/meridianhub/meridian/internal/migrations"
	"github.com/meridianhub/meridian/services"
)

func main() {
	migrate := flag.Bool("migrate", false, "Apply all embedded SQL migrations to CockroachDB and exit")
	databaseURL := flag.String("database-url", "", "Superuser DSN for CockroachDB (e.g., postgres://root@localhost:26257/defaultdb?sslmode=disable)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	if *migrate {
		dsn := *databaseURL
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		if dsn == "" {
			fmt.Fprintln(os.Stderr, "error: --database-url flag or DATABASE_URL environment variable required for --migrate")
			os.Exit(1)
		}

		ctx := context.Background()
		if err := migrations.RunMigrations(ctx, services.MigrationFS, dsn, logger); err != nil {
			logger.Error("migration failed", "error", err)
			os.Exit(1)
		}

		logger.Info("all migrations applied successfully")
		return
	}

	fmt.Println("meridian: unified binary (use --migrate to apply database migrations)")
}
