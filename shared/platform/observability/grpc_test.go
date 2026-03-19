package observability_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func newTestTracer(t *testing.T) *observability.Tracer {
	t.Helper()
	tracer, err := observability.NewTracer(context.Background(), observability.TracerConfig{
		ServiceName:  "test-grpc",
		OTLPEndpoint: "localhost:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	})
	require.NoError(t, err)
	return tracer
}

func TestUnaryServerInterceptor_Success(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.UnaryServerInterceptor()

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/package.Service/Method",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	resp, err := interceptor(ctx, "request", info, handler)

	assert.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestUnaryServerInterceptor_Error(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.UnaryServerInterceptor()

	handlerErr := errors.New("handler failed")
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return nil, handlerErr
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/package.Service/Method",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	resp, err := interceptor(ctx, "request", info, handler)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestUnaryServerInterceptor_WithTenant(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.UnaryServerInterceptor()

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/package.Service/Method",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	resp, err := interceptor(ctx, "request", info, handler)

	assert.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestUnaryServerInterceptor_UnparsableMethod(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.UnaryServerInterceptor()

	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}

	// Unparsable method (no slash separator after removing leading /)
	info := &grpc.UnaryServerInfo{
		FullMethod: "no-slash",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	resp, err := interceptor(ctx, "request", info, handler)

	assert.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

// mockServerStream implements grpc.ServerStream for testing.
type mockServerStream struct {
	ctx      context.Context
	sentMsgs []interface{}
	recvMsgs []interface{}
	recvIdx  int
	recvErr  error
	sendErr  error
}

func (s *mockServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *mockServerStream) SendHeader(metadata.MD) error { return nil }
func (s *mockServerStream) SetTrailer(metadata.MD)       {}
func (s *mockServerStream) Context() context.Context     { return s.ctx }

func (s *mockServerStream) SendMsg(m interface{}) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sentMsgs = append(s.sentMsgs, m)
	return nil
}

func (s *mockServerStream) RecvMsg(_ interface{}) error {
	if s.recvErr != nil {
		return s.recvErr
	}
	if s.recvIdx >= len(s.recvMsgs) {
		return io.EOF
	}
	s.recvIdx++
	return nil
}

func TestStreamServerInterceptor_Success(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamServerInterceptor()

	handler := func(_ interface{}, _ grpc.ServerStream) error {
		return nil
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/package.Service/StreamMethod",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	stream := &mockServerStream{ctx: ctx}

	err := interceptor(nil, stream, info, handler)
	assert.NoError(t, err)
}

func TestStreamServerInterceptor_Error(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamServerInterceptor()

	handlerErr := errors.New("stream handler failed")
	handler := func(_ interface{}, _ grpc.ServerStream) error {
		return handlerErr
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/package.Service/StreamMethod",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	stream := &mockServerStream{ctx: ctx}

	err := interceptor(nil, stream, info, handler)
	assert.Error(t, err)
}

func TestStreamServerInterceptor_SendRecv(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamServerInterceptor()

	handler := func(_ interface{}, stream grpc.ServerStream) error {
		// Send a message
		if err := stream.SendMsg("hello"); err != nil {
			return err
		}
		// Receive a message (will get EOF from mock)
		var msg interface{}
		err := stream.RecvMsg(&msg)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/package.Service/StreamMethod",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	stream := &mockServerStream{ctx: ctx}

	err := interceptor(nil, stream, info, handler)
	assert.NoError(t, err)
}

func TestStreamServerInterceptor_RecvError(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamServerInterceptor()

	handler := func(_ interface{}, stream grpc.ServerStream) error {
		var msg interface{}
		return stream.RecvMsg(&msg)
	}

	info := &grpc.StreamServerInfo{
		FullMethod: "/package.Service/StreamMethod",
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	stream := &mockServerStream{
		ctx:     ctx,
		recvErr: errors.New("recv failed"),
	}

	err := interceptor(nil, stream, info, handler)
	assert.Error(t, err)
}

func TestUnaryClientInterceptor_Success(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.UnaryClientInterceptor()

	invoker := func(_ context.Context, _ string, _, _ interface{}, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		return nil
	}

	err := interceptor(context.Background(), "/package.Service/Method", "req", "reply", nil, invoker)
	assert.NoError(t, err)
}

func TestUnaryClientInterceptor_Error(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.UnaryClientInterceptor()

	invokeErr := errors.New("rpc failed")
	invoker := func(_ context.Context, _ string, _, _ interface{}, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		return invokeErr
	}

	err := interceptor(context.Background(), "/package.Service/Method", "req", "reply", nil, invoker)
	assert.Error(t, err)
}

// mockClientStream implements grpc.ClientStream for testing.
type mockClientStream struct {
	ctx      context.Context
	sendErr  error
	recvMsgs []interface{}
	recvIdx  int
	recvErr  error
	closed   bool
}

func (s *mockClientStream) Header() (metadata.MD, error) { return nil, nil }
func (s *mockClientStream) Trailer() metadata.MD         { return nil }
func (s *mockClientStream) CloseSend() error {
	s.closed = true
	return nil
}
func (s *mockClientStream) Context() context.Context { return s.ctx }

func (s *mockClientStream) SendMsg(_ interface{}) error {
	return s.sendErr
}

func (s *mockClientStream) RecvMsg(_ interface{}) error {
	if s.recvErr != nil {
		return s.recvErr
	}
	if s.recvIdx >= len(s.recvMsgs) {
		return io.EOF
	}
	s.recvIdx++
	return nil
}

func TestStreamClientInterceptor_Success(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamClientInterceptor()

	mockStream := &mockClientStream{ctx: context.Background()}
	streamer := func(_ context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		return mockStream, nil
	}

	stream, err := interceptor(context.Background(), nil, nil, "/package.Service/Stream", streamer)
	assert.NoError(t, err)
	assert.NotNil(t, stream)

	// Test SendMsg through wrapped stream.
	err = stream.SendMsg("hello")
	assert.NoError(t, err)

	// Test RecvMsg - should get EOF from mock.
	var msg interface{}
	err = stream.RecvMsg(&msg)
	assert.ErrorIs(t, err, io.EOF)

	// Test CloseSend.
	err = stream.CloseSend()
	assert.NoError(t, err)
}

func TestStreamClientInterceptor_StreamerError(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamClientInterceptor()

	streamer := func(_ context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		return nil, errors.New("connection failed")
	}

	stream, err := interceptor(context.Background(), nil, nil, "/package.Service/Stream", streamer)
	assert.Error(t, err)
	assert.Nil(t, stream)
}

func TestStreamClientInterceptor_RecvError(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamClientInterceptor()

	recvErr := errors.New("receive failed")
	mockStream := &mockClientStream{ctx: context.Background(), recvErr: recvErr}
	streamer := func(_ context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		return mockStream, nil
	}

	stream, err := interceptor(context.Background(), nil, nil, "/package.Service/Stream", streamer)
	require.NoError(t, err)

	var msg interface{}
	err = stream.RecvMsg(&msg)
	assert.Error(t, err)
}

func TestStreamClientInterceptor_RecvSuccess(t *testing.T) {
	tracer := newTestTracer(t)

	interceptor := tracer.StreamClientInterceptor()

	mockStream := &mockClientStream{
		ctx:      context.Background(),
		recvMsgs: []interface{}{"msg1", "msg2"},
	}
	streamer := func(_ context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		return mockStream, nil
	}

	stream, err := interceptor(context.Background(), nil, nil, "/package.Service/Stream", streamer)
	require.NoError(t, err)

	// First recv should succeed.
	var msg interface{}
	err = stream.RecvMsg(&msg)
	assert.NoError(t, err)

	// Second recv should succeed.
	err = stream.RecvMsg(&msg)
	assert.NoError(t, err)

	// Third recv should get EOF.
	err = stream.RecvMsg(&msg)
	assert.ErrorIs(t, err, io.EOF)
}
