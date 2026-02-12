package worker

import (
	"context"
	"fmt"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GrpcReconciliationClient adapts the generated gRPC client to the
// ReconciliationClient interface used by the scheduler.
type GrpcReconciliationClient struct {
	client reconciliationv1.AccountReconciliationServiceClient
}

// NewGrpcReconciliationClient creates a new adapter wrapping the gRPC client.
func NewGrpcReconciliationClient(client reconciliationv1.AccountReconciliationServiceClient) *GrpcReconciliationClient {
	return &GrpcReconciliationClient{client: client}
}

// InitiateReconciliation calls the gRPC InitiateAccountReconciliation RPC and
// returns the run ID. If the server returns AlreadyExists, it wraps the error
// with ErrRunAlreadyExists so the scheduler can handle deduplication.
func (c *GrpcReconciliationClient) InitiateReconciliation(ctx context.Context, req InitiateRequest) (string, error) {
	scope, err := parseScope(req.Scope)
	if err != nil {
		return "", err
	}
	settlementType, err := parseSettlementType(req.SettlementType)
	if err != nil {
		return "", err
	}

	resp, err := c.client.InitiateAccountReconciliation(ctx, &reconciliationv1.InitiateAccountReconciliationRequest{
		AccountId:      req.AccountID,
		Scope:          scope,
		SettlementType: settlementType,
		PeriodStart:    timestamppb.New(req.PeriodStart),
		PeriodEnd:      timestamppb.New(req.PeriodEnd),
		InitiatedBy:    req.InitiatedBy,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return "", fmt.Errorf("%w: %s", ErrRunAlreadyExists, st.Message())
		}
		return "", fmt.Errorf("initiate reconciliation RPC failed: %w", err)
	}

	if resp.GetRun() == nil {
		return "", ErrNilRunResponse
	}

	return resp.GetRun().GetRunId(), nil
}

func parseScope(s string) (reconciliationv1.ReconciliationScope, error) {
	switch s {
	case "ACCOUNT":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT, nil
	case "INSTRUMENT":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT, nil
	case "PORTFOLIO":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO, nil
	case "FULL":
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL, nil
	default:
		return reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED,
			fmt.Errorf("%w: %q", ErrUnknownScope, s)
	}
}

func parseSettlementType(s string) (reconciliationv1.SettlementType, error) {
	switch s {
	case "DAILY":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY, nil
	case "WEEKLY":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY, nil
	case "MONTHLY":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY, nil
	case "ON_DEMAND":
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND, nil
	default:
		return reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED,
			fmt.Errorf("%w: %q", ErrUnknownSettlementType, s)
	}
}
