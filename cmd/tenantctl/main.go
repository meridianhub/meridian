// Package main is the entry point for the tenantctl CLI.
//
// tenantctl is a command-line tool for managing tenants in the Meridian platform.
// It provides commands to register, retrieve, list, and manage the lifecycle of
// tenants through the Tenant Service gRPC API.
package main

import "github.com/meridianhub/meridian/cmd/tenantctl/cmd"

func main() {
	cmd.Execute()
}
