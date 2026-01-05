// Package main is the entry point for the instrument-cli CLI.
//
// instrument-cli is a command-line tool for simulating instrument transactions.
// It provides dry-run capabilities to test validation rules, bucket key generation,
// and position previews without persisting data.
package main

import "github.com/meridianhub/meridian/cmd/instrument-cli/cmd"

func main() {
	cmd.Execute()
}
