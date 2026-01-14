// Package benchmarks_test provides performance benchmarks for the Internal Bank Account service.
//
// These benchmarks measure gRPC service operations with real PostgreSQL instances.
// Target metrics from requirements:
//   - Account creation: P99 < 50ms
//   - Balance queries: P99 < 50ms (via Position Keeping integration)
//   - Account lookups: P99 < 5ms
//
// Benchmark scenarios:
//   - Single operations (Initiate, Retrieve, Update, GetBalance)
//   - List operations with various filters
//   - Lookup operations (by ID and by Code)
//
// Run with: go test -bench=Benchmark -benchmem -benchtime=10s
package benchmarks_test

import (
	"fmt"
	"testing"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
)

// BenchmarkInitiateAccount_Single benchmarks single account creation.
// Target: P99 < 50ms for account creation.
func BenchmarkInitiateAccount_Single(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		req := &pb.InitiateInternalBankAccountRequest{
			AccountCode:    fmt.Sprintf("BENCH-%08d", i),
			Name:           fmt.Sprintf("Benchmark Account %d", i),
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		}
		b.StartTimer()

		_, err := tc.service.InitiateInternalBankAccount(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetBalance_Single benchmarks single balance query via Position Keeping integration.
// Target: P99 < 50ms for balance retrieval.
// Note: Requires mock position keeping client to be configured in test container.
func BenchmarkGetBalance_Single(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-create an account to query balance for
	createReq := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "BENCH-BALANCE-001",
		Name:           "Balance Benchmark Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "GBP",
	}
	createResp, err := tc.service.InitiateInternalBankAccount(ctx, createReq)
	if err != nil {
		b.Fatal(err)
	}

	balanceReq := &pb.GetBalanceRequest{
		AccountId: createResp.Facility.AccountCode,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.service.GetBalance(ctx, balanceReq)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRetrieveAccount_ByID benchmarks account lookup by UUID.
// Target: P99 < 5ms for single lookup.
func BenchmarkRetrieveAccount_ByID(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-create an account to retrieve
	createReq := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    "BENCH-RETRIEVE-ID-001",
		Name:           "Retrieve by ID Benchmark Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "GBP",
	}
	createResp, err := tc.service.InitiateInternalBankAccount(ctx, createReq)
	if err != nil {
		b.Fatal(err)
	}

	// Use the generated account_id (UUID-based business ID)
	retrieveReq := &pb.RetrieveInternalBankAccountRequest{
		AccountId: createResp.AccountId,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.service.RetrieveInternalBankAccount(ctx, retrieveReq)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRetrieveAccount_ByCode benchmarks account lookup by account_code.
// Target: P99 < 5ms for single lookup.
func BenchmarkRetrieveAccount_ByCode(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-create an account to retrieve
	accountCode := "BENCH-RETRIEVE-CODE-001"
	createReq := &pb.InitiateInternalBankAccountRequest{
		AccountCode:    accountCode,
		Name:           "Retrieve by Code Benchmark Account",
		AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCode: "GBP",
	}
	_, err := tc.service.InitiateInternalBankAccount(ctx, createReq)
	if err != nil {
		b.Fatal(err)
	}

	// Use the account_code for lookup
	retrieveReq := &pb.RetrieveInternalBankAccountRequest{
		AccountId: accountCode,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.service.RetrieveInternalBankAccount(ctx, retrieveReq)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUpdateAccount_Single benchmarks single account update (name change).
// Target: Baseline measurement for update operations.
func BenchmarkUpdateAccount_Single(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a fresh account for each update
		createReq := &pb.InitiateInternalBankAccountRequest{
			AccountCode:    fmt.Sprintf("BENCH-UPDATE-%08d", i),
			Name:           "Update Benchmark Account",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		}
		createResp, err := tc.service.InitiateInternalBankAccount(ctx, createReq)
		if err != nil {
			b.Fatal(err)
		}

		updateReq := &pb.UpdateInternalBankAccountRequest{
			AccountId: createResp.Facility.AccountCode,
			Name:      fmt.Sprintf("Updated Account Name %d", i),
		}
		b.StartTimer()

		_, err = tc.service.UpdateInternalBankAccount(ctx, updateReq)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListAccounts runs sub-benchmarks for various list filter scenarios.
func BenchmarkListAccounts(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-populate diverse accounts for list operations
	instrumentCodes := []string{"GBP", "USD", "EUR"}
	accountTypes := []pb.InternalAccountType{
		pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING,
		pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE,
		pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE,
		pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE,
	}

	// Create 100 accounts with varied attributes
	for i := 0; i < 100; i++ {
		req := &pb.InitiateInternalBankAccountRequest{
			AccountCode:    fmt.Sprintf("BENCH-LIST-%04d", i),
			Name:           fmt.Sprintf("List Benchmark Account %d", i),
			AccountType:    accountTypes[i%len(accountTypes)],
			InstrumentCode: instrumentCodes[i%len(instrumentCodes)],
		}
		_, err := tc.service.InitiateInternalBankAccount(ctx, req)
		if err != nil {
			b.Fatal(err)
		}

		// Suspend some accounts for status filter testing
		if i%10 == 0 {
			controlReq := &pb.ControlInternalBankAccountRequest{
				AccountId:     fmt.Sprintf("BENCH-LIST-%04d", i),
				ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:        "Benchmark test suspension",
			}
			_, err := tc.service.ControlInternalBankAccount(ctx, controlReq)
			if err != nil {
				b.Fatal(err)
			}
		}
	}

	// Run sub-benchmarks for different filter scenarios
	b.Run("NoFilter", func(b *testing.B) {
		req := &pb.ListInternalBankAccountsRequest{
			Pagination: &commonpb.Pagination{
				PageSize: 20,
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := tc.service.ListInternalBankAccounts(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ByType", func(b *testing.B) {
		req := &pb.ListInternalBankAccountsRequest{
			AccountTypeFilter: pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			Pagination: &commonpb.Pagination{
				PageSize: 20,
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := tc.service.ListInternalBankAccounts(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ByInstrument", func(b *testing.B) {
		req := &pb.ListInternalBankAccountsRequest{
			InstrumentCodeFilter: "GBP",
			Pagination: &commonpb.Pagination{
				PageSize: 20,
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := tc.service.ListInternalBankAccounts(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ByStatus", func(b *testing.B) {
		req := &pb.ListInternalBankAccountsRequest{
			StatusFilter: pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			Pagination: &commonpb.Pagination{
				PageSize: 20,
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := tc.service.ListInternalBankAccounts(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("MultiFilter", func(b *testing.B) {
		req := &pb.ListInternalBankAccountsRequest{
			AccountTypeFilter:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCodeFilter: "GBP",
			StatusFilter:         pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			Pagination: &commonpb.Pagination{
				PageSize: 20,
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := tc.service.ListInternalBankAccounts(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkListAccounts_LargeDataset benchmarks list operations with a larger dataset.
// This tests performance with 1000 accounts in the database.
func BenchmarkListAccounts_LargeDataset(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	// Pre-populate 1000 accounts
	for i := 0; i < 1000; i++ {
		req := &pb.InitiateInternalBankAccountRequest{
			AccountCode:    fmt.Sprintf("BENCH-LARGE-%06d", i),
			Name:           fmt.Sprintf("Large Dataset Account %d", i),
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		}
		_, err := tc.service.InitiateInternalBankAccount(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}

	listReq := &pb.ListInternalBankAccountsRequest{
		Pagination: &commonpb.Pagination{
			PageSize: 50,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.service.ListInternalBankAccounts(ctx, listReq)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkControlAccount_StatusTransition benchmarks account status transitions.
func BenchmarkControlAccount_StatusTransition(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := tc.ctx

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a fresh account for each transition
		createReq := &pb.InitiateInternalBankAccountRequest{
			AccountCode:    fmt.Sprintf("BENCH-CONTROL-%08d", i),
			Name:           "Control Benchmark Account",
			AccountType:    pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "GBP",
		}
		_, err := tc.service.InitiateInternalBankAccount(ctx, createReq)
		if err != nil {
			b.Fatal(err)
		}

		controlReq := &pb.ControlInternalBankAccountRequest{
			AccountId:     fmt.Sprintf("BENCH-CONTROL-%08d", i),
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "Benchmark status transition",
		}
		b.StartTimer()

		_, err = tc.service.ControlInternalBankAccount(ctx, controlReq)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Note: BenchmarkConcurrentReads is defined in load_test.go with a more comprehensive implementation.
// Note: BenchmarkMixedWorkload is defined in load_test.go with a more comprehensive implementation.
