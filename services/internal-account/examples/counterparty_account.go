//go:build ignore

// Package examples provides runnable examples demonstrating Internal Account service usage.
//
// This file demonstrates NOSTRO and VOSTRO account setup for counterparty banking.
// - NOSTRO: "Our account at your bank" - Our funds held at a counterparty bank
// - VOSTRO: "Your account at our bank" - Their funds held at our bank
//
// Run with: go run ./services/internal-account/examples/counterparty_account.go
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
	// Connect to service
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
		"x-organization", "acme-corp",
	)

	// =========================================================================
	// Example 1: Create a NOSTRO account
	// =========================================================================
	// NOSTRO = "Our account at your bank"
	// Used when we hold funds at a counterparty bank for foreign currency operations

	log.Println("=== Creating NOSTRO Account ===")
	log.Println("NOSTRO accounts represent our funds held at counterparty banks")

	nostroReq := &ibav1.InitiateInternalAccountRequest{
		AccountCode:     "NOSTRO-EUR-DEUTSCHE",
		Name:            "EUR Nostro at Deutsche Bank Frankfurt",
		ProductTypeCode: "NOSTRO_EUR",
		InstrumentCode:  "EUR",
		Description:     "Primary EUR nostro account for European settlements",

		// CounterpartyDetails is REQUIRED for NOSTRO/VOSTRO accounts
		// Validation will reject requests without this for these account types
		CounterpartyDetails: &ibav1.CounterpartyDetails{
			// CounterpartyId is our internal identifier for the counterparty
			CounterpartyId: "deutsche-frankfurt",

			// CounterpartyName is the official name of the counterparty bank
			CounterpartyName: "Deutsche Bank AG, Frankfurt",

			// CounterpartyExternalRef is our account number at their bank
			// This is the IBAN or account reference they gave us
			CounterpartyExternalRef: "DE89370400440532013000",

			// SWIFT/BIC codes and other product-specific attributes go in the
			// attributes map (product type determines which attributes are relevant)
			Attributes: map[string]string{
				"swift_code": "DEUTDEFF",
			},

			// CounterpartyType must match the account type
			CounterpartyType: ibav1.CounterpartyType_COUNTERPARTY_TYPE_NOSTRO,
		},

		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-nostro-eur-deutsche-20260115",
		},
	}

	nostroResp, err := client.InitiateInternalAccount(ctx, nostroReq)
	if err != nil {
		log.Fatalf("Failed to create NOSTRO account: %v", err)
	}

	log.Printf("NOSTRO account created:")
	log.Printf("  Account ID:       %s", nostroResp.AccountId)
	log.Printf("  Account Code:     %s", nostroResp.Facility.AccountCode)
	if cd := nostroResp.Facility.GetCounterpartyDetails(); cd != nil {
		log.Printf("  Counterparty:     %s", cd.CounterpartyName)
		log.Printf("  External Ref:     %s", cd.CounterpartyExternalRef)
	}

	// =========================================================================
	// Example 2: Create a VOSTRO account
	// =========================================================================
	// VOSTRO = "Your account at our bank"
	// Used when a counterparty bank holds funds with us

	log.Println("\n=== Creating VOSTRO Account ===")
	log.Println("VOSTRO accounts represent counterparty funds held at our bank")

	vostroReq := &ibav1.InitiateInternalAccountRequest{
		AccountCode:     "VOSTRO-JPY-MUFG",
		Name:            "JPY Vostro for MUFG Tokyo",
		ProductTypeCode: "VOSTRO_JPY",
		InstrumentCode:  "JPY",
		Description:     "MUFG Bank's JPY account held at our institution",

		CounterpartyDetails: &ibav1.CounterpartyDetails{
			CounterpartyId:   "mufg-tokyo",
			CounterpartyName: "MUFG Bank, Ltd., Tokyo",

			// For VOSTRO, CounterpartyExternalRef is our internal reference for their account
			CounterpartyExternalRef: "VOSTRO-MUFG-001",

			Attributes: map[string]string{
				"swift_code": "BOTKJPJT",
			},

			CounterpartyType: ibav1.CounterpartyType_COUNTERPARTY_TYPE_VOSTRO,
		},

		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "create-vostro-jpy-mufg-20260115",
		},
	}

	vostroResp, err := client.InitiateInternalAccount(ctx, vostroReq)
	if err != nil {
		log.Fatalf("Failed to create VOSTRO account: %v", err)
	}

	log.Printf("VOSTRO account created:")
	log.Printf("  Account ID:       %s", vostroResp.AccountId)
	log.Printf("  Account Code:     %s", vostroResp.Facility.AccountCode)
	if cd := vostroResp.Facility.GetCounterpartyDetails(); cd != nil {
		log.Printf("  Counterparty:     %s", cd.CounterpartyName)
		log.Printf("  Our Reference:    %s", cd.CounterpartyExternalRef)
	}

	// =========================================================================
	// Example 3: List all counterparty accounts
	// =========================================================================

	log.Println("\n=== Listing Counterparty Accounts ===")

	// List NOSTRO accounts
	nostroList, err := client.ListInternalAccounts(ctx, &ibav1.ListInternalAccountsRequest{
		BehaviorClassFilter: "NOSTRO",
		StatusFilter:        ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
	})
	if err != nil {
		log.Fatalf("Failed to list NOSTRO accounts: %v", err)
	}

	log.Printf("Active NOSTRO accounts: %d", len(nostroList.Facilities))
	for _, acc := range nostroList.Facilities {
		cd := acc.GetCounterpartyDetails()
		counterpartyName := ""
		if cd != nil {
			counterpartyName = cd.CounterpartyName
		}
		log.Printf("  - %s: %s at %s", acc.AccountCode, acc.InstrumentCode, counterpartyName)
	}

	// List VOSTRO accounts
	vostroList, err := client.ListInternalAccounts(ctx, &ibav1.ListInternalAccountsRequest{
		BehaviorClassFilter: "VOSTRO",
		StatusFilter:        ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
	})
	if err != nil {
		log.Fatalf("Failed to list VOSTRO accounts: %v", err)
	}

	log.Printf("Active VOSTRO accounts: %d", len(vostroList.Facilities))
	for _, acc := range vostroList.Facilities {
		cd := acc.GetCounterpartyDetails()
		counterpartyName := ""
		if cd != nil {
			counterpartyName = cd.CounterpartyName
		}
		log.Printf("  - %s: %s for %s", acc.AccountCode, acc.InstrumentCode, counterpartyName)
	}

	// =========================================================================
	// Example 4: Update counterparty details
	// =========================================================================

	log.Println("\n=== Updating Counterparty Details ===")

	// Update the NOSTRO account's external reference (e.g., IBAN changed)
	updateReq := &ibav1.UpdateInternalAccountRequest{
		AccountId: nostroResp.AccountId,

		// Update counterparty details
		CounterpartyDetails: &ibav1.CounterpartyDetails{
			CounterpartyId:          "deutsche-frankfurt",
			CounterpartyName:        "Deutsche Bank AG, Frankfurt", // Name unchanged
			CounterpartyExternalRef: "DE89370400440532013001",      // New IBAN
			Attributes: map[string]string{
				"swift_code": "DEUTDEFF",
			},
			CounterpartyType: ibav1.CounterpartyType_COUNTERPARTY_TYPE_NOSTRO,
		},

		// Optimistic locking - use current version to prevent conflicts
		ExpectedVersion: nostroResp.Facility.Version,
	}

	updateResp, err := client.UpdateInternalAccount(ctx, updateReq)
	if err != nil {
		log.Fatalf("Failed to update account: %v", err)
	}

	log.Printf("Account updated:")
	if cd := updateResp.Facility.GetCounterpartyDetails(); cd != nil {
		log.Printf("  New External Ref: %s", cd.CounterpartyExternalRef)
	}
	log.Printf("  Version:          %d -> %d", nostroResp.Facility.Version, updateResp.Facility.Version)

	log.Println("\nExample completed successfully!")
}
