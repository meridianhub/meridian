// Package main is the entry point for the seed-dev CLI.
//
// seed-dev creates a dev tenant and applies an example manifest configuration.
// It is idempotent: if the tenant already exists or the manifest has already
// been applied, it succeeds without error.
package main

import "github.com/meridianhub/meridian/cmd/seed-dev/cmd"

func main() {
	cmd.Execute()
}
