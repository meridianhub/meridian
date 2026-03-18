// Package grpc provides utilities for creating gRPC client connections
// with DNS-based load balancing for Kubernetes headless services.
//
// The primary entry point is [NewClient], which constructs a gRPC connection
// with sensible keepalive defaults and round-robin load balancing suited for
// direct-to-pod routing via Kubernetes headless services.
package grpc
