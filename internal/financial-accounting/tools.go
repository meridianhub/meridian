//go:build tools
// +build tools

// Package tools tracks dependencies for the financial accounting service.
// These imports ensure dependencies stay in go.mod even when not directly
// referenced by production code yet.
package tools

import (
	_ "github.com/google/uuid"
	_ "github.com/lib/pq"
	_ "github.com/shopspring/decimal"
)
