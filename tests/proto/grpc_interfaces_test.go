package proto_test

import (
	"context"
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"google.golang.org/grpc"
)

// mockFinancialAccountingServiceServer implements the generated service interface.
type mockFinancialAccountingServiceServer struct {
	financialaccountingv1.UnimplementedFinancialAccountingServiceServer
}

func (m *mockFinancialAccountingServiceServer) CaptureLedgerPosting(
	_ context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	return &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: &financialaccountingv1.LedgerPosting{
			Id:                    "test-id",
			FinancialBookingLogId: req.FinancialBookingLogId,
			PostingDirection:      req.PostingDirection,
			PostingAmount:         req.PostingAmount,
			AccountId:             req.AccountId,
			ValueDate:             req.ValueDate,
			PostingResult:         "Test posting",
			Status:                commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		},
	}, nil
}

func (m *mockFinancialAccountingServiceServer) RetrieveLedgerPosting(
	_ context.Context,
	req *financialaccountingv1.RetrieveLedgerPostingRequest,
) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	return &financialaccountingv1.RetrieveLedgerPostingResponse{
		LedgerPosting: &financialaccountingv1.LedgerPosting{
			Id: req.Id,
		},
	}, nil
}

func (m *mockFinancialAccountingServiceServer) UpdateLedgerPosting(
	_ context.Context,
	req *financialaccountingv1.UpdateLedgerPostingRequest,
) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	return &financialaccountingv1.UpdateLedgerPostingResponse{
		LedgerPosting: &financialaccountingv1.LedgerPosting{
			Id: req.Id,
		},
	}, nil
}

// mockPositionKeepingServiceServer implements the generated service interface.
type mockPositionKeepingServiceServer struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer
}

func (m *mockPositionKeepingServiceServer) InitiateFinancialPositionLog(
	_ context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return &positionkeepingv1.InitiateFinancialPositionLogResponse{
		Log: &positionkeepingv1.FinancialPositionLog{
			LogId:     "test-log-id",
			AccountId: req.AccountId,
		},
	}, nil
}

// mockCurrentAccountServiceServer implements the generated service interface.
type mockCurrentAccountServiceServer struct {
	currentaccountv1.UnimplementedCurrentAccountServiceServer
}

func (m *mockCurrentAccountServiceServer) InitiateCurrentAccount(
	_ context.Context,
	_ *currentaccountv1.InitiateCurrentAccountRequest,
) (*currentaccountv1.InitiateCurrentAccountResponse, error) {
	return &currentaccountv1.InitiateCurrentAccountResponse{
		AccountId: "test-account-id",
	}, nil
}

// mockHealthServiceServer implements the generated health service interface.
type mockHealthServiceServer struct {
	commonv1.UnimplementedHealthServiceServer
}

func (m *mockHealthServiceServer) Check(
	_ context.Context,
	_ *commonv1.CheckRequest,
) (*commonv1.CheckResponse, error) {
	return &commonv1.CheckResponse{
		Status: commonv1.CheckResponse_SERVING_STATUS_SERVING,
	}, nil
}

// TestGRPCServiceInterfaces verifies that gRPC service interfaces are correctly generated.
func TestGRPCServiceInterfaces(t *testing.T) {
	t.Run("FinancialAccountingService interface", func(_ *testing.T) {
		// Verify the service server interface can be implemented
		var _ financialaccountingv1.FinancialAccountingServiceServer = &mockFinancialAccountingServiceServer{}

		// Verify the service can be registered
		server := grpc.NewServer()
		mockServer := &mockFinancialAccountingServiceServer{}
		financialaccountingv1.RegisterFinancialAccountingServiceServer(server, mockServer)

		// Verify service descriptor exists
		if financialaccountingv1.FinancialAccountingService_ServiceDesc.ServiceName != "meridian.financial_accounting.v1.FinancialAccountingService" {
			t.Errorf("unexpected service name: %s", financialaccountingv1.FinancialAccountingService_ServiceDesc.ServiceName)
		}

		// Verify all expected methods exist
		expectedMethods := map[string]bool{
			"CaptureLedgerPosting":  false,
			"RetrieveLedgerPosting": false,
			"UpdateLedgerPosting":   false,
		}
		for _, method := range financialaccountingv1.FinancialAccountingService_ServiceDesc.Methods {
			if _, exists := expectedMethods[method.MethodName]; exists {
				expectedMethods[method.MethodName] = true
			}
		}
		for methodName, found := range expectedMethods {
			if !found {
				t.Errorf("expected method %s not found in service descriptor", methodName)
			}
		}

		server.Stop()
	})

	t.Run("PositionKeepingService interface", func(_ *testing.T) {
		// Verify the service server interface can be implemented
		var _ positionkeepingv1.PositionKeepingServiceServer = &mockPositionKeepingServiceServer{}

		// Verify the service can be registered
		server := grpc.NewServer()
		mockServer := &mockPositionKeepingServiceServer{}
		positionkeepingv1.RegisterPositionKeepingServiceServer(server, mockServer)

		// Verify service descriptor exists
		if positionkeepingv1.PositionKeepingService_ServiceDesc.ServiceName != "meridian.position_keeping.v1.PositionKeepingService" {
			t.Errorf("unexpected service name: %s", positionkeepingv1.PositionKeepingService_ServiceDesc.ServiceName)
		}

		server.Stop()
	})

	t.Run("CurrentAccountService interface", func(_ *testing.T) {
		// Verify the service server interface can be implemented
		var _ currentaccountv1.CurrentAccountServiceServer = &mockCurrentAccountServiceServer{}

		// Verify the service can be registered
		server := grpc.NewServer()
		mockServer := &mockCurrentAccountServiceServer{}
		currentaccountv1.RegisterCurrentAccountServiceServer(server, mockServer)

		// Verify service descriptor exists
		if currentaccountv1.CurrentAccountService_ServiceDesc.ServiceName != "meridian.current_account.v1.CurrentAccountService" {
			t.Errorf("unexpected service name: %s", currentaccountv1.CurrentAccountService_ServiceDesc.ServiceName)
		}

		server.Stop()
	})

	t.Run("HealthService interface", func(_ *testing.T) {
		// Verify the service server interface can be implemented
		var _ commonv1.HealthServiceServer = &mockHealthServiceServer{}

		// Verify the service can be registered
		server := grpc.NewServer()
		mockServer := &mockHealthServiceServer{}
		commonv1.RegisterHealthServiceServer(server, mockServer)

		// Verify service descriptor exists
		if commonv1.HealthService_ServiceDesc.ServiceName != "meridian.common.v1.HealthService" {
			t.Errorf("unexpected service name: %s", commonv1.HealthService_ServiceDesc.ServiceName)
		}

		server.Stop()
	})
}

// TestGRPCClientInterfaces verifies that gRPC client interfaces are correctly generated.
func TestGRPCClientInterfaces(t *testing.T) {
	t.Run("client interface types exist", func(_ *testing.T) {
		// Verify client interface types are generated
		var _ financialaccountingv1.FinancialAccountingServiceClient
		var _ positionkeepingv1.PositionKeepingServiceClient
		var _ currentaccountv1.CurrentAccountServiceClient
		var _ commonv1.HealthServiceClient
	})
}

// TestUnimplementedServerStubs verifies that Unimplemented server stubs are generated.
func TestUnimplementedServerStubs(t *testing.T) {
	t.Run("unimplemented server stubs exist", func(_ *testing.T) {
		// Verify unimplemented server stubs can be embedded
		var _ financialaccountingv1.FinancialAccountingServiceServer = &financialaccountingv1.UnimplementedFinancialAccountingServiceServer{}
		var _ positionkeepingv1.PositionKeepingServiceServer = &positionkeepingv1.UnimplementedPositionKeepingServiceServer{}
		var _ currentaccountv1.CurrentAccountServiceServer = &currentaccountv1.UnimplementedCurrentAccountServiceServer{}
		var _ commonv1.HealthServiceServer = &commonv1.UnimplementedHealthServiceServer{}
	})
}
