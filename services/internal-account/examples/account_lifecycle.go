//go:build ignore

// Package examples provides runnable examples demonstrating Internal Account service usage.
//
// This file demonstrates the full account lifecycle:
//
//	Create (ACTIVE) -> Suspend (SUSPENDED) -> Activate (ACTIVE) -> Close (CLOSED)
//
// Key points:
// - Accounts start in ACTIVE status (no PENDING state for internal accounts)
// - SUSPENDED is a temporary, reversible state (can return to ACTIVE)
// - CLOSED is a terminal state (cannot be reopened or modified)
// - All control actions require a reason for audit trail
//
// Run with: go run ./services/internal-account/examples/account_lifecycle.go
package main

import (
	"context"
	"log"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	ibav1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	ctx = metadata.AppendToOutgoingContext(ctx,
		"x-organization", "lifecycle-demo",
	)

	// =========================================================================
	// Step 1: CREATE - Account starts in ACTIVE status
	// =========================================================================
	log.Println("=== Step 1: CREATE ===")
	log.Println("Creating a new holding account...")

	createResp, err := client.InitiateInternalAccount(ctx, &ibav1.InitiateInternalAccountRequest{
		AccountCode:     "HOLD-LIFECYCLE-DEMO",
		Name:            "Lifecycle Demo Holding Account",
		ProductTypeCode: "HOLDING_GBP",
		InstrumentCode:  "USD",
		Description:     "Demonstration account for lifecycle transitions",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "lifecycle-demo-" + time.Now().Format("20060102150405"),
		},
	})
	if err != nil {
		log.Fatalf("Failed to create account: %v", err)
	}

	accountID := createResp.AccountId
	log.Printf("Account created:")
	log.Printf("  ID:     %s", accountID)
	log.Printf("  Status: %s (accounts always start as ACTIVE)", createResp.Facility.AccountStatus)
	verifyStatus(createResp.Facility.AccountStatus, ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE)

	// =========================================================================
	// Step 2: SUSPEND - Temporarily freeze the account
	// =========================================================================
	log.Println("\n=== Step 2: SUSPEND ===")
	log.Println("Suspending account for quarterly audit review...")

	suspendResp, err := client.ControlInternalAccount(ctx, &ibav1.ControlInternalAccountRequest{
		AccountId:     accountID,
		ControlAction: ibav1.ControlAction_CONTROL_ACTION_SUSPEND,
		// Reason is REQUIRED (minimum 10 characters for audit completeness)
		// Good reasons explain WHY the action is being taken
		Reason: "Quarterly compliance audit - pending documentation review per FIN-2026-Q1",
	})
	if err != nil {
		log.Fatalf("Failed to suspend account: %v", err)
	}

	log.Printf("Account suspended:")
	log.Printf("  Status:    %s", suspendResp.Facility.AccountStatus)
	log.Printf("  Timestamp: %s", suspendResp.ActionTimestamp.AsTime().Format(time.RFC3339))
	verifyStatus(suspendResp.Facility.AccountStatus, ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED)

	// Show that suspended account cannot be used for new transactions
	// (This would be enforced by Financial Accounting when posting)
	log.Println("\n  Note: Suspended accounts cannot process new transactions")
	log.Println("  Financial Accounting will reject postings to suspended accounts")

	// =========================================================================
	// Step 3: ACTIVATE - Restore the account to operational status
	// =========================================================================
	log.Println("\n=== Step 3: ACTIVATE ===")
	log.Println("Audit complete, reactivating account...")

	activateResp, err := client.ControlInternalAccount(ctx, &ibav1.ControlInternalAccountRequest{
		AccountId:     accountID,
		ControlAction: ibav1.ControlAction_CONTROL_ACTION_ACTIVATE,
		Reason:        "Quarterly audit completed successfully - no issues found per audit report AUD-2026-0042",
	})
	if err != nil {
		log.Fatalf("Failed to activate account: %v", err)
	}

	log.Printf("Account reactivated:")
	log.Printf("  Status:    %s", activateResp.Facility.AccountStatus)
	log.Printf("  Timestamp: %s", activateResp.ActionTimestamp.AsTime().Format(time.RFC3339))
	verifyStatus(activateResp.Facility.AccountStatus, ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE)

	// =========================================================================
	// Step 4: Update account (possible while ACTIVE)
	// =========================================================================
	log.Println("\n=== Step 4: UPDATE ===")
	log.Println("Updating account description...")

	updateResp, err := client.UpdateInternalAccount(ctx, &ibav1.UpdateInternalAccountRequest{
		AccountId:   accountID,
		Description: "Lifecycle demo account - audit cleared Q1 2026",
		// Use optimistic locking to prevent conflicts
		ExpectedVersion: activateResp.Facility.Version,
	})
	if err != nil {
		log.Fatalf("Failed to update account: %v", err)
	}

	log.Printf("Account updated:")
	log.Printf("  Description: %s", updateResp.Facility.Description)
	log.Printf("  Version:     %d", updateResp.Facility.Version)

	// =========================================================================
	// Step 5: CLOSE - Permanently close the account
	// =========================================================================
	log.Println("\n=== Step 5: CLOSE ===")
	log.Println("Closing account permanently...")
	log.Println("WARNING: CLOSED is a TERMINAL state - account cannot be reopened!")

	closeResp, err := client.ControlInternalAccount(ctx, &ibav1.ControlInternalAccountRequest{
		AccountId:     accountID,
		ControlAction: ibav1.ControlAction_CONTROL_ACTION_CLOSE,
		Reason:        "Account consolidated into HOLD-MAIN per finance directive FIN-2026-CONSOLIDATE-001",
	})
	if err != nil {
		log.Fatalf("Failed to close account: %v", err)
	}

	log.Printf("Account closed:")
	log.Printf("  Status:    %s", closeResp.Facility.AccountStatus)
	log.Printf("  Timestamp: %s", closeResp.ActionTimestamp.AsTime().Format(time.RFC3339))
	verifyStatus(closeResp.Facility.AccountStatus, ibav1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED)

	// =========================================================================
	// Step 6: Verify closed account behavior
	// =========================================================================
	log.Println("\n=== Step 6: VERIFY CLOSED BEHAVIOR ===")

	// Attempt to activate a closed account - should fail
	log.Println("Attempting to reactivate closed account (should fail)...")
	_, err = client.ControlInternalAccount(ctx, &ibav1.ControlInternalAccountRequest{
		AccountId:     accountID,
		ControlAction: ibav1.ControlAction_CONTROL_ACTION_ACTIVATE,
		Reason:        "Attempting to reopen closed account",
	})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() == codes.FailedPrecondition {
			log.Printf("  Expected error: %s", st.Message())
			log.Printf("  CLOSED accounts cannot be reactivated")
		} else {
			log.Fatalf("Unexpected error: %v", err)
		}
	} else {
		log.Fatal("ERROR: Should not be able to activate a closed account!")
	}

	// Attempt to update a closed account - should fail
	log.Println("\nAttempting to update closed account (should fail)...")
	_, err = client.UpdateInternalAccount(ctx, &ibav1.UpdateInternalAccountRequest{
		AccountId:   accountID,
		Description: "Attempting to modify closed account",
	})
	if err != nil {
		st, _ := status.FromError(err)
		log.Printf("  Expected error: %s", st.Message())
		log.Printf("  CLOSED accounts cannot be modified")
	} else {
		log.Fatal("ERROR: Should not be able to update a closed account!")
	}

	// The account can still be retrieved for audit purposes
	log.Println("\nRetrieving closed account for audit (should succeed)...")
	retrieveResp, err := client.RetrieveInternalAccount(ctx, &ibav1.RetrieveInternalAccountRequest{
		AccountId: accountID,
	})
	if err != nil {
		log.Fatalf("Failed to retrieve account: %v", err)
	}
	log.Printf("  Retrieved account: %s (status: %s)",
		retrieveResp.Facility.AccountCode,
		retrieveResp.Facility.AccountStatus)
	log.Printf("  CLOSED accounts remain queryable for audit trail")

	// =========================================================================
	// Summary: State Transition Diagram
	// =========================================================================
	log.Println("\n=== LIFECYCLE SUMMARY ===")
	log.Println("")
	log.Println("                     +---------+")
	log.Println("     CREATE          |  ACTIVE |")
	log.Println("  ------------------>|         |<-------+")
	log.Println("                     +----+----+        |")
	log.Println("                          |            |")
	log.Println("                 SUSPEND  |   ACTIVATE |")
	log.Println("                          v            |")
	log.Println("                     +---------+       |")
	log.Println("                     |SUSPENDED|-------+")
	log.Println("                     +----+----+")
	log.Println("                          |")
	log.Println("                   CLOSE  |  CLOSE")
	log.Println("      ACTIVE ------------>v<---------- SUSPENDED")
	log.Println("                     +---------+")
	log.Println("                     | CLOSED  | (Terminal)")
	log.Println("                     +---------+")
	log.Println("")
	log.Println("Valid transitions:")
	log.Println("  - CREATE: -> ACTIVE")
	log.Println("  - SUSPEND: ACTIVE -> SUSPENDED")
	log.Println("  - ACTIVATE: SUSPENDED -> ACTIVE")
	log.Println("  - CLOSE: ACTIVE -> CLOSED, SUSPENDED -> CLOSED")
	log.Println("")
	log.Println("Notes:")
	log.Println("  - There is NO PENDING state (internal accounts created immediately)")
	log.Println("  - There is NO DELETE operation (use CLOSE for audit compliance)")
	log.Println("  - All control actions require a reason (min 10 chars)")
	log.Println("")
	log.Println("Example completed successfully!")
}

// verifyStatus checks that the actual status matches expected, panics if not.
func verifyStatus(actual, expected ibav1.InternalAccountStatus) {
	if actual != expected {
		log.Fatalf("Status mismatch: expected %s, got %s", expected, actual)
	}
}
