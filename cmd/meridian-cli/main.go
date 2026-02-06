// Package main is the entry point for meridian-cli.
//
// meridian-cli provides command-line tools for the Meridian platform,
// including saga script validation for local testing.
package main

import "github.com/meridianhub/meridian/cmd/meridian-cli/cmd"

func main() {
	cmd.Execute()
}
