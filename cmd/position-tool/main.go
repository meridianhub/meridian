// Package main is the entry point for the position-tool CLI.
//
// position-tool is a command-line tool for bulk position management in the
// Meridian platform. It provides commands for importing positions from CSV,
// exporting positions to CSV, and rebucketing existing positions.
//
// The tool is designed for high-throughput bulk operations with proper
// progress reporting, validation, and audit trail generation.
package main

import "github.com/meridianhub/meridian/cmd/position-tool/cmd"

func main() {
	cmd.Execute()
}
