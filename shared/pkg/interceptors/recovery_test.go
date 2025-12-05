package interceptors

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRecoveryUnaryInterceptor_NoPanic(t *testing.T) {
	logger := testLogger()
	interceptor := RecoveryUnaryInterceptor(logger)

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "success", nil
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	resp, err := interceptor(context.Background(), "request", info, handler)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if resp != "success" {
		t.Errorf("Expected 'success', got: %v", resp)
	}
}

func TestRecoveryUnaryInterceptor_WithPanic(t *testing.T) {
	logger := testLogger()
	interceptor := RecoveryUnaryInterceptor(logger)

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		panic("test panic")
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	resp, err := interceptor(context.Background(), "request", info, handler)

	if resp != nil {
		t.Errorf("Expected nil response, got: %v", resp)
	}
	if err == nil {
		t.Error("Expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
	if st.Message() != "internal server error" {
		t.Errorf("Expected 'internal server error', got: %v", st.Message())
	}
}

func TestRecoveryUnaryInterceptor_HandlerError(t *testing.T) {
	logger := testLogger()
	interceptor := RecoveryUnaryInterceptor(logger)

	expectedErr := status.Error(codes.InvalidArgument, "bad request")
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return nil, expectedErr
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	resp, err := interceptor(context.Background(), "request", info, handler)

	if resp != nil {
		t.Errorf("Expected nil response, got: %v", resp)
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("Expected expectedErr, got: %v", err)
	}
}

// mockServerStream implements grpc.ServerStream for testing
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestRecoveryStreamInterceptor_NoPanic(t *testing.T) {
	logger := testLogger()
	interceptor := RecoveryStreamInterceptor(logger)

	handler := func(_ interface{}, _ grpc.ServerStream) error {
		return nil
	}

	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
	stream := &mockServerStream{ctx: context.Background()}
	err := interceptor(nil, stream, info, handler)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

func TestRecoveryStreamInterceptor_WithPanic(t *testing.T) {
	logger := testLogger()
	interceptor := RecoveryStreamInterceptor(logger)

	handler := func(_ interface{}, _ grpc.ServerStream) error {
		panic("stream panic")
	}

	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
	stream := &mockServerStream{ctx: context.Background()}
	err := interceptor(nil, stream, info, handler)

	if err == nil {
		t.Error("Expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}

func TestRecoveryStreamInterceptorWithWrappedStream_NoPanic(t *testing.T) {
	logger := testLogger()
	interceptor := RecoveryStreamInterceptorWithWrappedStream(logger)

	handler := func(_ interface{}, _ grpc.ServerStream) error {
		return nil
	}

	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
	stream := &mockServerStream{ctx: context.Background()}
	err := interceptor(nil, stream, info, handler)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

func TestRecoveryStreamInterceptorWithWrappedStream_WithPanic(t *testing.T) {
	logger := testLogger()
	interceptor := RecoveryStreamInterceptorWithWrappedStream(logger)

	handler := func(_ interface{}, _ grpc.ServerStream) error {
		panic("wrapped stream panic")
	}

	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
	stream := &mockServerStream{ctx: context.Background()}
	err := interceptor(nil, stream, info, handler)

	if err == nil {
		t.Error("Expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Expected Internal code, got: %v", st.Code())
	}
}
