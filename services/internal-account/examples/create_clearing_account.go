//go:build ignore

// Package examples provides runnable examples demonstrating Internal Account service usage.
//
// This file shows how to create a simple clearing account with proper tenant context.
//
// Run with: go run ./services/internal-account/examples/create_clearing_account.go
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
	// Connect to the Internal Account service
	// In production, use TLS credentials and service discovery
	conn, err := grpc.NewClient(
		"localhost:50057",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Create the gRPC client
	client := ibav1.NewInternalAccountServiceClient(conn)

	// Create a context with timeout and tenant information
	// The x-organization header is required for multi-tenant isolation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Add tenant context via gRPC metadata
	// This determines which database schema the account is created in
	ctx = metadata.AppendToOutgoingContext(ctx,
		"x-organization", "acme-corp", // Tenant identifier
	)

	// Build the create account request
	// Clearing accounts are used for settlement and clearing operations
	req := &ibav1.InitiateInternalAccountRequest{
		// AccountCode is a business-friendly unique identifier
		// Pattern: ^[A-Z0-9_-]+$, max 50 characters
		AccountCode: "CLR-USD-001",

		// Name is the human-readable display name
		Name: "Primary USD Clearing Account",

		// AccountType determines the operational purpose
		// CLEARING is used for settlement and clearing operations
		ProductTypeCode: "CLEARING_GBP",

		// InstrumentCode references the asset type from Reference Data service
		// Common values: USD, EUR, GBP, KWH, GPU_HOUR, TONNE_CO2E
		InstrumentCode: "USD",

		// Description provides additional context for audit and operations
		Description: "Primary clearing account for USD settlement operations",

		// IdempotencyKey ensures exactly-once processing
		// Use a unique key per logical operation to handle retries safely
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-clr-usd-001-20260115",
		},
	}

	// Execute the create request
	log.Println("Creating clearing account...")
	resp, err := client.InitiateInternalAccount(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create account: %v", err)
	}

	// Log the created account details
	log.Printf("Account created successfully!")
	log.Printf("  Account ID:   %s", resp.AccountId)
	log.Printf("  Account Code: %s", resp.Facility.AccountCode)
	log.Printf("  Name:         %s", resp.Facility.Name)
	log.Printf("  Type:         %s", resp.Facility.AccountType)
	log.Printf("  Status:       %s", resp.Facility.AccountStatus)
	log.Printf("  Instrument:   %s", resp.Facility.InstrumentCode)
	log.Printf("  Created At:   %s", resp.Facility.CreatedAt.AsTime().Format(time.RFC3339))

	// Verify the account was created in ACTIVE status
	// New accounts always start as ACTIVE (no PENDING state for internal accounts)
	if resp.Facility.AccountStatus != ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE {
		log.Fatalf("Unexpected status: expected ACTIVE, got %s", resp.Facility.AccountStatus)
	}

	log.Println("Example completed successfully!")
}
