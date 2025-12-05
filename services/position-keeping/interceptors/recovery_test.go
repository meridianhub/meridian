package interceptors_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/meridianhub/meridian/services/position-keeping/interceptors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRecoveryUnaryInterceptor_NoPanic tests normal operation without panics
func TestRecoveryUnaryInterceptor_NoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryUnaryInterceptor(logger)

	expectedResp := "success"
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return expectedResp, nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	resp, err := interceptor(context.Background(), nil, info, handler)

	require.NoError(t, err)
	assert.Equal(t, expectedResp, resp)
}

// TestRecoveryUnaryInterceptor_PanicString tests recovery from string panic
func TestRecoveryUnaryInterceptor_PanicString(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryUnaryInterceptor(logger)

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		panic("something went wrong")
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	resp, err := interceptor(context.Background(), nil, info, handler)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "internal server error")
}

// TestRecoveryUnaryInterceptor_PanicError tests recovery from error panic
func TestRecoveryUnaryInterceptor_PanicError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryUnaryInterceptor(logger)

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		panic(assert.AnError)
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	resp, err := interceptor(context.Background(), nil, info, handler)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// TestRecoveryUnaryInterceptor_PanicNil tests recovery from nil panic
func TestRecoveryUnaryInterceptor_PanicNil(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryUnaryInterceptor(logger)

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		panic(nil)
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	// Note: panic(nil) does panic in Go and recover() catches it
	resp, err := interceptor(context.Background(), nil, info, handler)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// TestRecoveryUnaryInterceptor_HandlerError tests normal error returns are not affected
func TestRecoveryUnaryInterceptor_HandlerError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryUnaryInterceptor(logger)

	expectedErr := status.Error(codes.InvalidArgument, "bad request")
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return nil, expectedErr
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	resp, err := interceptor(context.Background(), nil, info, handler)

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, expectedErr, err)
}

// TestRecoveryStreamInterceptor_NoPanic tests stream handler without panic
func TestRecoveryStreamInterceptor_NoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryStreamInterceptor(logger)

	handler := func(_ interface{}, _ grpc.ServerStream) error {
		return nil
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/test.Service/StreamMethod",
	}

	err := interceptor(nil, &mockServerStream{}, info, handler)

	require.NoError(t, err)
}

// TestRecoveryStreamInterceptor_Panic tests stream handler with panic
func TestRecoveryStreamInterceptor_Panic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryStreamInterceptor(logger)

	handler := func(_ interface{}, _ grpc.ServerStream) error {
		panic("stream panic")
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/test.Service/StreamMethod",
	}

	err := interceptor(nil, &mockServerStream{}, info, handler)

	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// TestRecoveryStreamInterceptorWithWrappedStream_SendPanic tests panic in SendMsg
func TestRecoveryStreamInterceptorWithWrappedStream_SendPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryStreamInterceptorWithWrappedStream(logger)

	mockStream := &mockServerStream{
		sendPanic: true,
	}

	handler := func(_ interface{}, stream grpc.ServerStream) error {
		// This will panic during SendMsg
		return stream.SendMsg("test")
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/test.Service/StreamMethod",
	}

	err := interceptor(nil, mockStream, info, handler)

	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// TestRecoveryStreamInterceptorWithWrappedStream_RecvPanic tests panic in RecvMsg
func TestRecoveryStreamInterceptorWithWrappedStream_RecvPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	interceptor := interceptors.RecoveryStreamInterceptorWithWrappedStream(logger)

	mockStream := &mockServerStream{
		recvPanic: true,
	}

	handler := func(_ interface{}, stream grpc.ServerStream) error {
		var msg string
		// This will panic during RecvMsg
		return stream.RecvMsg(&msg)
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/test.Service/StreamMethod",
	}

	err := interceptor(nil, mockStream, info, handler)

	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// mockServerStream implements grpc.ServerStream for testing
type mockServerStream struct {
	grpc.ServerStream
	sendPanic bool
	recvPanic bool
}

func (m *mockServerStream) SendMsg(_ interface{}) error {
	if m.sendPanic {
		panic("send panic")
	}
	return nil
}

func (m *mockServerStream) RecvMsg(_ interface{}) error {
	if m.recvPanic {
		panic("recv panic")
	}
	return nil
}

func (m *mockServerStream) Context() context.Context {
	return context.Background()
}
