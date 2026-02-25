//go:build ignore

// Package examples provides runnable examples demonstrating Internal Account service usage.
//
// This file demonstrates creating multi-asset internal accounts for:
// - Energy (KWH) - Kilowatt-hours for utility/energy trading
// - Compute (GPU_HOUR) - GPU compute allocation for AI/ML workloads
// - Carbon (TONNE_CO2E) - Carbon credits for emissions trading
//
// Meridian supports tracking these non-traditional assets with the same rigor as fiat currency.
//
// Run with: go run ./services/internal-account/examples/multi_asset.go
package main

import (
	"context"
	"log"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	ibav1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	conn, err := grpc.NewClient(
		"localhost:50057",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := ibav1.NewInternalAccountServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ctx = metadata.AppendToOutgoingContext(ctx,
		"x-organization", "energy-grid-co",
	)

	// =========================================================================
	// Example 1: Energy Inventory Account (KWH)
	// =========================================================================
	// Track energy inventory for utility operations, renewable energy trading,
	// or microgrid management

	log.Println("=== Creating Energy Inventory Account ===")
	log.Println("Energy accounts track kilowatt-hours for utility and trading operations")

	energyReq := &ibav1.InitiateInternalAccountRequest{
		AccountCode: "INV-ENERGY-SOLAR-FARM-1",
		Name:        "Solar Farm 1 Energy Inventory",

		// INVENTORY accounts track non-cash assets
		ProductTypeCode: "INVENTORY_KWH",

		// KWH is a standard energy instrument code
		// Reference Data service defines the unit as "kilowatt-hour"
		// Dimension: ENERGY
		InstrumentCode: "KWH",

		Description: "Aggregated solar energy generation from Solar Farm 1 - Bay Area",

		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-energy-solar-farm-1-20260115",
		},
	}

	energyResp, err := client.InitiateInternalAccount(ctx, energyReq)
	if err != nil {
		log.Fatalf("Failed to create energy account: %v", err)
	}

	log.Printf("Energy account created:")
	log.Printf("  Account ID:   %s", energyResp.AccountId)
	log.Printf("  Code:         %s", energyResp.Facility.AccountCode)
	log.Printf("  Instrument:   %s (Energy in kilowatt-hours)", energyResp.Facility.InstrumentCode)
	log.Printf("  Type:         %s", energyResp.Facility.AccountType)

	// =========================================================================
	// Example 2: GPU Compute Allocation Account
	// =========================================================================
	// Track GPU compute hours for AI training, inference, or cloud billing

	log.Println("\n=== Creating GPU Compute Account ===")
	log.Println("Compute accounts track GPU-hours for AI/ML workloads")

	gpuReq := &ibav1.InitiateInternalAccountRequest{
		AccountCode: "INV-GPU-CLUSTER-A100",
		Name:        "A100 GPU Cluster Allocation Pool",

		ProductTypeCode: "INVENTORY_KWH",

		// GPU_HOUR represents one hour of GPU compute time
		// Dimension: COMPUTE
		InstrumentCode: "GPU_HOUR",

		Description: "Pre-allocated GPU compute for AI training cluster - NVIDIA A100 80GB",

		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-gpu-cluster-a100-20260115",
		},
	}

	gpuResp, err := client.InitiateInternalAccount(ctx, gpuReq)
	if err != nil {
		log.Fatalf("Failed to create GPU account: %v", err)
	}

	log.Printf("GPU compute account created:")
	log.Printf("  Account ID:   %s", gpuResp.AccountId)
	log.Printf("  Code:         %s", gpuResp.Facility.AccountCode)
	log.Printf("  Instrument:   %s (Compute in GPU-hours)", gpuResp.Facility.InstrumentCode)

	// =========================================================================
	// Example 3: Carbon Credit Account
	// =========================================================================
	// Track carbon credits for emissions trading and offset programs

	log.Println("\n=== Creating Carbon Credit Account ===")
	log.Println("Carbon accounts track tonnes of CO2 equivalent for emissions trading")

	carbonReq := &ibav1.InitiateInternalAccountRequest{
		AccountCode: "INV-CARBON-OFFSET-2026",
		Name:        "2026 Carbon Offset Inventory",

		ProductTypeCode: "INVENTORY_KWH",

		// TONNE_CO2E represents one metric tonne of CO2 equivalent
		// Dimension: CARBON
		InstrumentCode: "TONNE_CO2E",

		Description: "Verified carbon credits for 2026 offset program - Verra VCS certified",

		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-carbon-offset-2026-20260115",
		},
	}

	carbonResp, err := client.InitiateInternalAccount(ctx, carbonReq)
	if err != nil {
		log.Fatalf("Failed to create carbon account: %v", err)
	}

	log.Printf("Carbon credit account created:")
	log.Printf("  Account ID:   %s", carbonResp.AccountId)
	log.Printf("  Code:         %s", carbonResp.Facility.AccountCode)
	log.Printf("  Instrument:   %s (Carbon in tonnes CO2e)", carbonResp.Facility.InstrumentCode)

	// =========================================================================
	// Example 4: Carbon Suspense Account
	// =========================================================================
	// Suspense accounts hold unverified or disputed assets

	log.Println("\n=== Creating Carbon Suspense Account ===")
	log.Println("Suspense accounts hold assets pending verification or dispute resolution")

	suspenseReq := &ibav1.InitiateInternalAccountRequest{
		AccountCode: "SUS-CARBON-PENDING",
		Name:        "Carbon Credits Pending Verification",

		// SUSPENSE accounts hold unidentified or disputed items
		ProductTypeCode: "SUSPENSE_GBP",

		InstrumentCode: "TONNE_CO2E",

		Description: "Carbon credits awaiting third-party verification",

		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-carbon-suspense-20260115",
		},
	}

	suspenseResp, err := client.InitiateInternalAccount(ctx, suspenseReq)
	if err != nil {
		log.Fatalf("Failed to create suspense account: %v", err)
	}

	log.Printf("Suspense account created:")
	log.Printf("  Account ID:   %s", suspenseResp.AccountId)
	log.Printf("  Type:         %s (for pending items)", suspenseResp.Facility.AccountType)

	// =========================================================================
	// Example 5: Revenue Account for Energy Sales
	// =========================================================================
	// Revenue accounts track income from asset sales

	log.Println("\n=== Creating Energy Revenue Account ===")
	log.Println("Revenue accounts track income from energy sales to the grid")

	revenueReq := &ibav1.InitiateInternalAccountRequest{
		AccountCode: "REV-ENERGY-GRID-SALES",
		Name:        "Grid Energy Sales Revenue",

		// REVENUE accounts track income streams
		ProductTypeCode: "REVENUE_GBP",

		// Revenue in USD for energy sold to grid
		InstrumentCode: "USD",

		Description: "Revenue from energy sales to California ISO",

		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-energy-revenue-20260115",
		},
	}

	revenueResp, err := client.InitiateInternalAccount(ctx, revenueReq)
	if err != nil {
		log.Fatalf("Failed to create revenue account: %v", err)
	}

	log.Printf("Revenue account created:")
	log.Printf("  Account ID:   %s", revenueResp.AccountId)
	log.Printf("  Type:         %s", revenueResp.Facility.AccountType)
	log.Printf("  Instrument:   %s (Revenue in dollars)", revenueResp.Facility.InstrumentCode)

	// =========================================================================
	// Summary: List all inventory accounts
	// =========================================================================

	log.Println("\n=== Summary: All Inventory Accounts ===")

	listResp, err := client.ListInternalAccounts(ctx, &ibav1.ListInternalAccountsRequest{
		BehaviorClassFilter: "INVENTORY",
	})
	if err != nil {
		log.Fatalf("Failed to list accounts: %v", err)
	}

	log.Printf("Total inventory accounts: %d", len(listResp.Facilities))
	for _, acc := range listResp.Facilities {
		log.Printf("  - %s: %s (%s)",
			acc.AccountCode,
			acc.Name,
			acc.InstrumentCode)
	}

	log.Println("\nExample completed successfully!")
	log.Println("\nSupported instrument dimensions:")
	log.Println("  - CURRENCY: USD, EUR, GBP, JPY, etc.")
	log.Println("  - ENERGY: KWH, MWH, THERM")
	log.Println("  - COMPUTE: GPU_HOUR, CPU_SECOND")
	log.Println("  - CARBON: TONNE_CO2E")
	log.Println("  - MASS: KG, TON, LB")
	log.Println("  - VOLUME: LITRE, GALLON, BARREL")
	log.Println("  - TIME: HOUR, DAY")
	log.Println("  - DATA: GB, TB")
	log.Println("  - COUNT: UNIT, TOKEN, VOUCHER")
}
