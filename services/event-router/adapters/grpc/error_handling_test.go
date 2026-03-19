package grpc

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestHandleRecordMeasurementError_AllBranches exercises every switch branch
// in handleRecordMeasurementError, covering the 61.1% gap.
func TestHandleRecordMeasurementError_AllBranches(t *testing.T) {
	client := &PositionKeepingGRPCClient{
		logger: slog.Default(),
	}

	measurement := &auditdomain.Measurement{
		ID:        uuid.New(),
		AssetCode: "MERIDIAN-TEST-OPS",
	}

	tests := []struct {
		name string
		err  error
	}{
		{"non-gRPC error", errors.New("some network error")},
		{"InvalidArgument", status.Error(codes.InvalidArgument, "bad data")},
		{"Unavailable", status.Error(codes.Unavailable, "service down")},
		{"Internal", status.Error(codes.Internal, "server error")},
		{"DeadlineExceeded", status.Error(codes.DeadlineExceeded, "timeout")},
		{"ResourceExhausted", status.Error(codes.ResourceExhausted, "rate limited")},
		{"default/NotFound", status.Error(codes.NotFound, "not found")},
		{"default/PermissionDenied", status.Error(codes.PermissionDenied, "denied")},
		{"default/Unauthenticated", status.Error(codes.Unauthenticated, "unauth")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			// Should not panic
			client.handleRecordMeasurementError(tt.err, measurement)
		})
	}
}

// TestHandleError_AllBranches exercises every switch branch
// in SagaTriggerClient.handleError, covering the 58.3% gap.
func TestHandleError_AllBranches(t *testing.T) {
	client := &SagaTriggerClient{
		logger: slog.Default(),
	}

	tests := []struct {
		name string
		err  error
	}{
		{"non-gRPC error", errors.New("plain error")},
		{"InvalidArgument", status.Error(codes.InvalidArgument, "bad saga name")},
		{"NotFound", status.Error(codes.NotFound, "saga not found")},
		{"Unavailable", status.Error(codes.Unavailable, "control-plane down")},
		{"Internal", status.Error(codes.Internal, "server error")},
		{"DeadlineExceeded", status.Error(codes.DeadlineExceeded, "timeout")},
		{"ResourceExhausted", status.Error(codes.ResourceExhausted, "rate limited")},
		{"default/PermissionDenied", status.Error(codes.PermissionDenied, "denied")},
		{"default/Unauthenticated", status.Error(codes.Unauthenticated, "unauth")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			// Should not panic
			client.handleError(tt.err, "test_saga")
		})
	}
}

// TestPositionKeepingGRPCClient_Close_NilConn verifies Close with nil connection.
func TestPositionKeepingGRPCClient_Close_NilConn(t *testing.T) {
	client := &PositionKeepingGRPCClient{
		conn:   nil,
		logger: slog.Default(),
	}
	err := client.Close()
	assert.NoError(t, err)
}

// TestSagaTriggerClient_Close_NilConn verifies Close with nil connection.
func TestSagaTriggerClient_Close_NilConn(t *testing.T) {
	client := &SagaTriggerClient{
		conn:   nil,
		logger: slog.Default(),
	}
	err := client.Close()
	assert.NoError(t, err)
}
