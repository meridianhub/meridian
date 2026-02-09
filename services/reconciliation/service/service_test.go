package service

import (
	"context"
	"testing"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewAccountReconciliationService(t *testing.T) {
	svc := NewAccountReconciliationService()
	require.NotNil(t, svc)
}

func TestAllRPCsReturnUnimplemented(t *testing.T) {
	svc := NewAccountReconciliationService()
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "InitiateAccountReconciliation",
			call: func() error {
				_, err := svc.InitiateAccountReconciliation(ctx, &reconciliationv1.InitiateAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "ExecuteAccountReconciliation",
			call: func() error {
				_, err := svc.ExecuteAccountReconciliation(ctx, &reconciliationv1.ExecuteAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "RetrieveAccountReconciliation",
			call: func() error {
				_, err := svc.RetrieveAccountReconciliation(ctx, &reconciliationv1.RetrieveAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "ControlAccountReconciliation",
			call: func() error {
				_, err := svc.ControlAccountReconciliation(ctx, &reconciliationv1.ControlAccountReconciliationRequest{})
				return err
			},
		},
		{
			name: "ListReconciliationResults",
			call: func() error {
				_, err := svc.ListReconciliationResults(ctx, &reconciliationv1.ListReconciliationResultsRequest{})
				return err
			},
		},
		{
			name: "AssertBalance",
			call: func() error {
				_, err := svc.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{})
				return err
			},
		},
		{
			name: "InitiateDispute",
			call: func() error {
				_, err := svc.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{})
				return err
			},
		},
		{
			name: "ControlDispute",
			call: func() error {
				_, err := svc.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{})
				return err
			},
		},
		{
			name: "RetrieveDispute",
			call: func() error {
				_, err := svc.RetrieveDispute(ctx, &reconciliationv1.RetrieveDisputeRequest{})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok, "error should be a gRPC status")
			assert.Equal(t, codes.Unimplemented, st.Code())
		})
	}
}
