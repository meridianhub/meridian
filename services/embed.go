// Package services provides embedded access to all service migration SQL files.
//
// The embed.FS bundles every services/*/migrations/*.sql file at compile time,
// allowing the unified binary to apply migrations without reading the filesystem.
package services

import "embed"

// MigrationFS contains all SQL migration files from services/*/migrations/.
// Paths within the FS use the form: <service>/migrations/<filename>.sql
//
//go:embed */migrations/*.sql
var MigrationFS embed.FS
