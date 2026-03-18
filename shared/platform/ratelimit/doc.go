// Package ratelimit provides per-tenant, per-method rate limiting for gRPC services.
//
// A token-bucket limiter is maintained per (tenant, gRPC method) pair. Requests that
// exceed the configured rate receive a gRPC ResourceExhausted error. Idle limiters are
// evicted on a configurable cleanup interval to bound memory usage.
//
// Prometheus metrics are collected for rejected and allowed requests.
//
// # Usage
//
//	interceptor := ratelimit.NewInterceptor(ratelimit.DefaultConfig())
//	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(interceptor.Unary()))
package ratelimit
