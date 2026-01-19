// Package main is the entry point for the market-data-tool CLI.
//
// market-data-tool is a command-line tool for bulk market data import in the
// Meridian platform. It provides commands for importing observations from CSV
// files to the Market Information Service via gRPC.
//
// The tool is designed for high-throughput bulk operations with proper
// progress reporting, validation, and checkpoint support.
package main

import "github.com/meridianhub/meridian/cmd/market-data-tool/cmd"

func main() {
	cmd.Execute()
}
