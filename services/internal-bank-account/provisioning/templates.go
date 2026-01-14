// Package provisioning provides tenant-specific account template customization.
package provisioning

import (
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
)

// TemplateSet defines a named collection of account templates.
// Different tenant types (banks, energy companies, etc.) can use different template sets.
type TemplateSet struct {
	// Name identifies the template set (e.g., "default", "energy", "compute").
	Name string

	// Description explains what the template set is for.
	Description string

	// Templates is the list of account templates in this set.
	Templates []AccountTemplate
}

// BuiltInTemplateSets provides predefined template sets for common tenant types.
var BuiltInTemplateSets = map[string]TemplateSet{
	"default": {
		Name:        "default",
		Description: "Standard banking accounts (clearing, revenue, expense, suspense)",
		Templates:   DefaultAccounts,
	},
	"energy": {
		Name:        "energy",
		Description: "Accounts for energy trading and settlement",
		Templates:   EnergyAccounts,
	},
	"compute": {
		Name:        "compute",
		Description: "Accounts for compute resource billing",
		Templates:   ComputeAccounts,
	},
	"minimal": {
		Name:        "minimal",
		Description: "Minimal set of accounts (suspense only)",
		Templates:   MinimalAccounts,
	},
}

// EnergyAccounts provides templates for energy trading companies.
// Includes energy-specific clearing accounts in addition to standard financial accounts.
var EnergyAccounts = []AccountTemplate{
	// Standard currency clearing accounts
	{
		Code:           "CLR-GBP-DEPOSIT",
		Name:           "GBP Deposit Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Clearing account for GBP deposits pending settlement",
	},
	{
		Code:           "CLR-GBP-WITHDRAW",
		Name:           "GBP Withdrawal Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Clearing account for GBP withdrawals pending settlement",
	},

	// Energy clearing accounts
	{
		Code:           "CLR-KWH-DELIVERY",
		Name:           "KWH Delivery Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "KWH",
		Dimension:      DimensionEnergy,
		Description:    "Clearing account for energy delivery pending settlement",
	},
	{
		Code:           "CLR-KWH-RECEIPT",
		Name:           "KWH Receipt Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "KWH",
		Dimension:      DimensionEnergy,
		Description:    "Clearing account for energy receipt pending settlement",
	},

	// Energy inventory account
	{
		Code:           "INV-KWH-WHOLESALE",
		Name:           "Wholesale Energy Inventory",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_INVENTORY,
		InstrumentCode: "KWH",
		Dimension:      DimensionEnergy,
		Description:    "Inventory account for wholesale energy holdings",
	},

	// Revenue accounts
	{
		Code:           "REV-ENERGY-SALES",
		Name:           "Energy Sales Revenue",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Revenue from energy sales",
	},
	{
		Code:           "REV-GRID-FEE",
		Name:           "Grid Access Fee Revenue",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Revenue from grid access fees",
	},

	// Expense accounts
	{
		Code:           "EXP-GRID-CONNECTION",
		Name:           "Grid Connection Expense",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Expense for grid connection costs",
	},
	{
		Code:           "EXP-ENERGY-PROCUREMENT",
		Name:           "Energy Procurement Expense",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Expense for energy procurement",
	},

	// Suspense
	{
		Code:           "SUS-ENERGY-GENERAL",
		Name:           "Energy General Suspense",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Suspense account for unidentified energy-related transactions",
	},
}

// ComputeAccounts provides templates for compute resource billing (AI/cloud).
// Includes compute-specific accounts for GPU hours, data transfer, etc.
var ComputeAccounts = []AccountTemplate{
	// Standard currency clearing
	{
		Code:           "CLR-USD-DEPOSIT",
		Name:           "USD Deposit Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
		Dimension:      DimensionCurrency,
		Description:    "Clearing account for USD deposits",
	},
	{
		Code:           "CLR-USD-WITHDRAW",
		Name:           "USD Withdrawal Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "USD",
		Dimension:      DimensionCurrency,
		Description:    "Clearing account for USD withdrawals",
	},

	// Compute clearing accounts
	{
		Code:           "CLR-GPU-HOUR-DELIVERY",
		Name:           "GPU Hour Delivery Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "GPU-HOUR",
		Dimension:      DimensionCompute,
		Description:    "Clearing account for GPU hour delivery",
	},
	{
		Code:           "CLR-CPU-HOUR-DELIVERY",
		Name:           "CPU Hour Delivery Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "CPU-HOUR",
		Dimension:      DimensionCompute,
		Description:    "Clearing account for CPU hour delivery",
	},

	// Data transfer clearing
	{
		Code:           "CLR-DATA-EGRESS",
		Name:           "Data Egress Clearing",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "GB-DATA",
		Dimension:      DimensionData,
		Description:    "Clearing account for data egress",
	},

	// Inventory
	{
		Code:           "INV-GPU-CAPACITY",
		Name:           "GPU Capacity Inventory",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_INVENTORY,
		InstrumentCode: "GPU-HOUR",
		Dimension:      DimensionCompute,
		Description:    "Inventory of available GPU compute hours",
	},

	// Revenue accounts
	{
		Code:           "REV-COMPUTE-BILLING",
		Name:           "Compute Billing Revenue",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE,
		InstrumentCode: "USD",
		Dimension:      DimensionCurrency,
		Description:    "Revenue from compute resource billing",
	},
	{
		Code:           "REV-DATA-TRANSFER",
		Name:           "Data Transfer Revenue",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE,
		InstrumentCode: "USD",
		Dimension:      DimensionCurrency,
		Description:    "Revenue from data transfer fees",
	},

	// Expense
	{
		Code:           "EXP-INFRASTRUCTURE",
		Name:           "Infrastructure Expense",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE,
		InstrumentCode: "USD",
		Dimension:      DimensionCurrency,
		Description:    "Expense for infrastructure costs",
	},

	// Suspense
	{
		Code:           "SUS-COMPUTE-GENERAL",
		Name:           "Compute General Suspense",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE,
		InstrumentCode: "USD",
		Dimension:      DimensionCurrency,
		Description:    "Suspense account for unidentified compute transactions",
	},
}

// MinimalAccounts provides the minimum viable set of accounts.
// Useful for tenants that want to configure accounts manually.
var MinimalAccounts = []AccountTemplate{
	{
		Code:           "SUS-GENERAL",
		Name:           "General Suspense Account",
		Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE,
		InstrumentCode: "GBP",
		Dimension:      DimensionCurrency,
		Description:    "Suspense account for unidentified transactions",
	},
}

// GetTemplateSet returns a template set by name.
// Returns nil if the template set is not found.
func GetTemplateSet(name string) *TemplateSet {
	if ts, ok := BuiltInTemplateSets[name]; ok {
		return &ts
	}
	return nil
}

// ListTemplateSets returns the names of all available template sets.
func ListTemplateSets() []string {
	names := make([]string, 0, len(BuiltInTemplateSets))
	for name := range BuiltInTemplateSets {
		names = append(names, name)
	}
	return names
}
