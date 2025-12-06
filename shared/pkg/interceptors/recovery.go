// Package interceptors provides shared gRPC interceptors for all Meridian services.
package interceptors

import (
	"context"
	"log/slog"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RecoveryUnaryInterceptor recovers from panics in unary RPCs and converts them to gRPC errors.
//
// Design rationale:
// - Prevents service crashes from panics in business logic
// - Logs panic details with stack trace for debugging
// - Returns Internal error to clients (avoids exposing internals)
// - Allows graceful degradation instead of process termination
func RecoveryUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		// Capture panics and convert to gRPC errors
		defer func() {
			if r := recover(); r != nil {
				// Log panic with full stack trace
				logger.Error("panic recovered in gRPC handler",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(debug.Stack()))

				// Return Internal error without exposing panic details
				err = status.Errorf(codes.Internal, "internal server error")
				resp = nil
			}
		}()

		// Call the handler
		return handler(ctx, req)
	}
}

// RecoveryStreamInterceptor recovers from panics in streaming RPCs and converts them to gRPC errors.
func RecoveryStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		// Capture panics and convert to gRPC errors
		defer func() {
			if r := recover(); r != nil {
				// Log panic with full stack trace
				logger.Error("panic recovered in gRPC stream handler",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(debug.Stack()))

				// Return Internal error without exposing panic details
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()

		// Call the handler
		return handler(srv, ss)
	}
}

// wrappedServerStream wraps grpc.ServerStream to add panic recovery to individual stream operations.
type wrappedServerStream struct {
	grpc.ServerStream
	logger *slog.Logger
	method string
}

// SendMsg recovers from panics during message sending.
func (w *wrappedServerStream) SendMsg(m interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("panic in SendMsg",
				"method", w.method,
				"panic", r,
				"stack", string(debug.Stack()))
			err = status.Errorf(codes.Internal, "internal server error")
		}
	}()
	return w.ServerStream.SendMsg(m)
}

// RecvMsg recovers from panics during message receiving.
func (w *wrappedServerStream) RecvMsg(m interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("panic in RecvMsg",
				"method", w.method,
				"panic", r,
				"stack", string(debug.Stack()))
			err = status.Errorf(codes.Internal, "internal server error")
		}
	}()
	return w.ServerStream.RecvMsg(m)
}

// RecoveryStreamInterceptorWithWrappedStream provides more granular panic recovery for streaming RPCs.
// This wraps each Send/Recv operation in addition to the handler itself.
func RecoveryStreamInterceptorWithWrappedStream(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		// Wrap the stream to add panic recovery to Send/Recv
		wrapped := &wrappedServerStream{
			ServerStream: ss,
			logger:       logger,
			method:       info.FullMethod,
		}

		// Capture panics in handler
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered in gRPC stream handler",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(debug.Stack()))

				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()

		// Call the handler with wrapped stream
		return handler(srv, wrapped)
	}
}
