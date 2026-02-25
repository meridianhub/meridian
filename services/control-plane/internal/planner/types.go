// Package planner transforms a diff plan into a dependency-ordered sequence
// of gRPC calls to downstream services (Reference Data, Internal Account,
// Saga Registry). It ensures resources are created in the correct order
// respecting inter-resource dependencies.
package planner

import (
	"fmt"
	"strings"

	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
)

// Phase represents an execution phase in the dependency graph.
// Resources in the same phase can be executed in parallel; phases must
// be executed sequentially (phase N completes before phase N+1 starts).
type Phase int

const (
	// PhaseInstruments registers instruments with Reference Data (no dependencies).
	PhaseInstruments Phase = 1
	// PhaseAccountTypes registers account types and initiates internal accounts
	// (depends on instruments being registered first).
	PhaseAccountTypes Phase = 2
	// PhaseValuationRules registers valuation rules with Reference Data
	// (depends on instruments being registered first).
	PhaseValuationRules Phase = 3
	// PhaseSagas registers saga definitions with the Saga Registry
	// (depends on account types and instruments being registered first).
	PhaseSagas Phase = 4
	// PhaseMappings registers mapping definitions with Reference Data
	// (can be registered after instruments and account types are provisioned).
	PhaseMappings Phase = 5
	// PhasePartyTypes registers party type definitions with the Party Service
	// (no dependencies on other manifest sections).
	PhasePartyTypes Phase = 6
	// PhaseSeedData provisions seed data via various service calls
	// (depends on all above being registered first).
	PhaseSeedData Phase = 7
)

// PhaseLabel returns a human-readable label for a phase.
func PhaseLabel(p Phase) string {
	switch p {
	case PhaseInstruments:
		return "Instruments"
	case PhaseAccountTypes:
		return "Account Types"
	case PhaseValuationRules:
		return "Valuation Rules"
	case PhaseSagas:
		return "Saga Definitions"
	case PhaseMappings:
		return "Mapping Definitions"
	case PhaseSeedData:
		return "Seed Data"
	case PhasePartyTypes:
		return "Party Types"
	default:
		return fmt.Sprintf("Phase(%d)", p)
	}
}

// GRPCMethod identifies a specific gRPC service method to call.
type GRPCMethod string

// gRPC method constants for each resource type and action.
const (
	// Reference Data Service
	MethodRegisterInstrument  GRPCMethod = "meridian.reference_data.v1.ReferenceDataService/RegisterInstrument"
	MethodUpdateInstrument    GRPCMethod = "meridian.reference_data.v1.ReferenceDataService/UpdateInstrument"
	MethodDeprecateInstrument GRPCMethod = "meridian.reference_data.v1.ReferenceDataService/DeprecateInstrument"
	MethodActivateInstrument  GRPCMethod = "meridian.reference_data.v1.ReferenceDataService/ActivateInstrument"

	// Internal Account Service
	MethodInitiateAccount GRPCMethod = "meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount"

	// Saga Registry Service
	MethodCreateSagaDraft      GRPCMethod = "meridian.saga.v1.SagaRegistryService/CreateSagaDraft"
	MethodUpdateSagaDefinition GRPCMethod = "meridian.saga.v1.SagaRegistryService/UpdateSagaDefinition"
	MethodDeprecateSaga        GRPCMethod = "meridian.saga.v1.SagaRegistryService/DeprecateSaga"
	MethodActivateSaga         GRPCMethod = "meridian.saga.v1.SagaRegistryService/ActivateSaga"

	// Party Service
	MethodRegisterPartyType GRPCMethod = "meridian.party.v1.PartyService/RegisterPartyType"
	MethodUpdatePartyType   GRPCMethod = "meridian.party.v1.PartyService/UpdatePartyType"

	// Mapping Service (Reference Data)
	MethodCreateMapping    GRPCMethod = "meridian.mapping.v1.MappingService/CreateMapping"
	MethodUpdateMapping    GRPCMethod = "meridian.mapping.v1.MappingService/UpdateMapping"
	MethodDeprecateMapping GRPCMethod = "meridian.mapping.v1.MappingService/DeprecateMapping"
)

// PlannedCall represents a single gRPC call in the execution plan.
type PlannedCall struct {
	// Phase is the execution phase (1-5) for dependency ordering.
	Phase Phase

	// ResourceType identifies the manifest resource category.
	ResourceType differ.ResourceType

	// ResourceID is the stable identifier for the resource (instrument code,
	// account type code, valuation rule key, or saga name).
	ResourceID string

	// Action is the diff action that produced this call (CREATE/UPDATE/DELETE).
	Action differ.ActionType

	// GRPCMethod is the fully-qualified gRPC method to invoke.
	GRPCMethod GRPCMethod

	// IdempotencyKey is a deterministic key for safe retry.
	// Format: SHA-256(tenant_id + manifest_version + resource_type + resource_id + action)
	IdempotencyKey string

	// Description is a human-readable description of this planned call.
	Description string

	// DryRun indicates whether services should validate without committing.
	DryRun bool
}

// ExecutionPlan is the complete ordered sequence of gRPC calls
// derived from a DiffPlan. Calls are grouped into phases that must
// execute sequentially, while calls within a phase may execute in parallel.
type ExecutionPlan struct {
	// Calls is the ordered sequence of gRPC calls.
	Calls []PlannedCall

	// TenantID is the tenant this plan applies to.
	TenantID string

	// ManifestVersion is the manifest version string.
	ManifestVersion string

	// DryRun indicates whether the entire plan is a dry run.
	DryRun bool
}

// ByPhase returns calls grouped by phase in execution order.
func (p *ExecutionPlan) ByPhase() map[Phase][]PlannedCall {
	grouped := make(map[Phase][]PlannedCall)
	for _, call := range p.Calls {
		grouped[call.Phase] = append(grouped[call.Phase], call)
	}
	return grouped
}

// Phases returns the distinct phases present in the plan, in order.
func (p *ExecutionPlan) Phases() []Phase {
	seen := make(map[Phase]bool)
	var phases []Phase
	for _, call := range p.Calls {
		if !seen[call.Phase] {
			seen[call.Phase] = true
			phases = append(phases, call.Phase)
		}
	}
	return phases
}

// Summary returns a human-readable summary of the execution plan.
func (p *ExecutionPlan) Summary() string {
	counts := map[differ.ActionType]int{}
	for _, call := range p.Calls {
		counts[call.Action]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Execution Plan: %d calls", len(p.Calls))
	if p.DryRun {
		b.WriteString(" [DRY RUN]")
	}
	fmt.Fprintf(&b, "\n  Creates: %d, Updates: %d, Deletes: %d",
		counts[differ.ActionCreate],
		counts[differ.ActionUpdate],
		counts[differ.ActionDelete],
	)
	return b.String()
}

// Visualize returns a human-readable phased plan visualization.
func (p *ExecutionPlan) Visualize() string {
	var b strings.Builder

	if p.DryRun {
		b.WriteString("=== EXECUTION PLAN (DRY RUN) ===\n")
	} else {
		b.WriteString("=== EXECUTION PLAN ===\n")
	}
	fmt.Fprintf(&b, "Tenant: %s | Manifest: %s\n", p.TenantID, p.ManifestVersion)
	fmt.Fprintf(&b, "Total calls: %d\n\n", len(p.Calls))

	byPhase := p.ByPhase()
	for phase := PhaseInstruments; phase <= PhaseSeedData; phase++ { //nolint:intrange
		calls, ok := byPhase[phase]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "Phase %d: %s (%d calls)\n", phase, PhaseLabel(phase), len(calls))
		for _, call := range calls {
			actionSymbol := actionSymbol(call.Action)
			fmt.Fprintf(&b, "  %s %s %s\n", actionSymbol, call.ResourceType, call.ResourceID)
			fmt.Fprintf(&b, "    -> %s\n", call.GRPCMethod)
			if call.Description != "" {
				fmt.Fprintf(&b, "    %s\n", call.Description)
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

func actionSymbol(action differ.ActionType) string {
	switch action {
	case differ.ActionCreate:
		return "+"
	case differ.ActionUpdate:
		return "~"
	case differ.ActionDelete:
		return "-"
	case differ.ActionNoChange:
		return "="
	}
	return "?"
}
