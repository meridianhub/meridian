// Package main is the entry point for the orgctl CLI.
//
// orgctl is a command-line tool for managing organizations in the Meridian platform.
// It provides commands to register, retrieve, list, and manage the lifecycle of
// organizations through the Organization Service gRPC API.
package main

import "github.com/meridianhub/meridian/cmd/orgctl/cmd"

func main() {
	cmd.Execute()
}
