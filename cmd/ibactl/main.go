// ibactl is a CLI tool for managing Internal Accounts in Meridian.
//
// Usage:
//
//	ibactl provision-defaults <tenant-id>  # Provision default accounts for a tenant
//	ibactl provision-defaults --all        # Provision for all active tenants
//
// See ibactl --help for full usage.
package main

import "github.com/meridianhub/meridian/cmd/ibactl/cmd"

func main() {
	cmd.Execute()
}
