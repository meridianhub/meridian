// Package interceptors provides shared gRPC interceptors for all Meridian services.
//
// The interceptors in this package provide cross-cutting concerns like panic recovery,
// logging, and tracing that should be consistently applied across all gRPC services.
//
// # Panic Recovery
//
// The recovery interceptors prevent service crashes from panics in business logic:
//
//   - RecoveryUnaryInterceptor: Recovers from panics in unary RPCs
//   - RecoveryStreamInterceptor: Recovers from panics in streaming RPCs
//   - RecoveryStreamInterceptorWithWrappedStream: Provides granular recovery for Send/Recv operations
//
// All recovery interceptors log panic details with stack traces for debugging and return
// codes.Internal to clients without exposing internal error details.
//
// # Interceptor Chain Order
//
// When building an interceptor chain, the recommended order is:
//
//  1. Metrics (first to capture all requests)
//  2. Tracing (for observability)
//  3. Auth (authentication/authorization)
//  4. Recovery (last to catch all panics from above layers)
//
// Example usage:
//
//	unaryInterceptors := []grpc.UnaryServerInterceptor{
//	    metricsInterceptor,
//	    tracer.UnaryServerInterceptor(),
//	    authInterceptor,
//	    interceptors.RecoveryUnaryInterceptor(logger),
//	}
//	grpcServer := grpc.NewServer(
//	    grpc.ChainUnaryInterceptor(unaryInterceptors...),
//	)
package interceptors
