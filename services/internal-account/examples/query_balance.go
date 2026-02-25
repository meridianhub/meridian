//go:build ignore

// Package examples provides runnable examples demonstrating Internal Account service usage.
//
// This file shows how to query an account balance. The balance is delegated to
// Position Keeping service - Internal Account does not store balance locally.
//
// Run with: go run ./services/internal-account/examples/query_balance.go
package main

import (
	"context"
	"log"
	"time"

	ibav1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func main() {
	// Connect to the Internal Account service
	conn, err := grpc.NewClient(
		"localhost:50057",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := ibav1.NewInternalAccountServiceClient(conn)

	// Create context with tenant information
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ctx = metadata.AppendToOutgoingContext(ctx,
		"x-organization", "acme-corp",
	)

	// First, retrieve an existing account to get its ID
	// In a real application, you would have the account ID from a previous operation
	log.Println("Listing accounts to find one to query...")

	listResp, err := client.ListInternalAccounts(ctx, &ibav1.ListInternalAccountsRequest{
		// Filter for CLEARING accounts (optional)
		BehaviorClassFilter: "CLEARING",
		// Only active accounts
		StatusFilter: ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
	})
	if err != nil {
		log.Fatalf("Failed to list accounts: %v", err)
	}

	if len(listResp.Facilities) == 0 {
		log.Println("No accounts found. Please create an account first using create_clearing_account.go")
		return
	}

	// Use the first account from the list
	account := listResp.Facilities[0]
	log.Printf("Found account: %s (%s)", account.AccountCode, account.Name)

	// Query the balance for this account
	// IMPORTANT: Balance is NOT stored in Internal Account service
	// This call delegates to Position Keeping service which computes balance from transaction logs
	log.Printf("Querying balance for account ID: %s", account.AccountId)

	balanceReq := &ibav1.GetBalanceRequest{
		AccountId: account.AccountId,
	}

	balanceResp, err := client.GetBalance(ctx, balanceReq)
	if err != nil {
		// Handle specific error cases
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
			case codes.NotFound:
				log.Printf("Account not found: %s", account.AccountId)
			case codes.Unavailable:
				// Position Keeping service may be unavailable
				log.Printf("Position Keeping service unavailable - balance cannot be computed")
				log.Printf("This is expected if Position Keeping is not running")
			default:
				log.Printf("Error querying balance: %s - %s", st.Code(), st.Message())
			}
		} else {
			log.Fatalf("Failed to get balance: %v", err)
		}
		return
	}

	// Display the balance information
	log.Println("Balance retrieved successfully!")
	log.Printf("  Account ID:      %s", balanceResp.AccountId)

	if balanceResp.CurrentBalance != nil {
		log.Printf("  Amount:          %s", balanceResp.CurrentBalance.Amount)
		log.Printf("  Instrument:      %s", balanceResp.CurrentBalance.InstrumentCode)
		log.Printf("  Version:         %d", balanceResp.CurrentBalance.Version)

		// Display any attributes (e.g., batch_id, location)
		if len(balanceResp.CurrentBalance.Attributes) > 0 {
			log.Println("  Attributes:")
			for _, attr := range balanceResp.CurrentBalance.Attributes {
				log.Printf("    %s: %s", attr.Key, attr.Value)
			}
		}

		// Display validity period if set (for time-bound assets)
		if balanceResp.CurrentBalance.ValidFrom != nil {
			log.Printf("  Valid From:      %s", balanceResp.CurrentBalance.ValidFrom.AsTime().Format(time.RFC3339))
		}
		if balanceResp.CurrentBalance.ValidTo != nil {
			log.Printf("  Valid To:        %s", balanceResp.CurrentBalance.ValidTo.AsTime().Format(time.RFC3339))
		}
	} else {
		log.Printf("  Balance:         (no balance data)")
	}

	if balanceResp.AsOf != nil {
		log.Printf("  As Of:           %s", balanceResp.AsOf.AsTime().Format(time.RFC3339))
	}

	log.Println("Example completed successfully!")
}
