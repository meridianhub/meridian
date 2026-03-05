// Package cookbook provides embedded access to the Meridian Cookbook registry
// and pattern files. The embedded FS is used by the MCP server's discover tool
// to resolve pattern compatibility at runtime without filesystem access.
package cookbook

import "embed"

// FS contains the cookbook registry index and all pattern JSON files.
// The embedded paths are:
//   - registry.json — the registry index listing all available items
//   - patterns/*.json — individual pattern detail files (when present)
//
// The `all:` prefix is used for the patterns directory to ensure the embed
// directive does not fail when the directory contains only non-Go files
// (e.g., .gitkeep). All files under patterns/ are included in the FS.
//
//go:embed registry.json all:patterns
var FS embed.FS
