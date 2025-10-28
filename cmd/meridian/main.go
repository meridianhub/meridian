// Package main is the entry point for the Meridian open banking ledger service.
package main

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	fmt.Printf("Meridian v%s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
}
