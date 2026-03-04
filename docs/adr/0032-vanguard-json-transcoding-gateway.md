---
name: adr-0032-vanguard-json-transcoding-gateway
description: Use Vanguard (connectrpc.com/vanguard) as the HTTP/JSON-to-gRPC transcoding layer in the API gateway
triggers:
  - Adding new protocol support to the gateway (REST, Connect, gRPC-Web)
  - Evaluating protocol translation options for gRPC backends
  - Understanding why the gateway accepts HTTP/JSON but backends speak only gRPC
  - Choosing between gRPC-gateway and Vanguard for REST transcoding
instructions: |
  The gateway uses Vanguard to translate between REST/JSON, Connect, gRPC-Web, and native gRPC.
  Backend services always speak gRPC; Vanguard handles all protocol translation at the gateway edge.
  HTTP/JSON URL paths and request shapes come from google.api.http annotations in the proto files.
  Run `make proto-descriptors` to regenerate the embedded FileDescriptorSet after proto changes.
---

# 32. Vanguard HTTP/JSON Transcoding Gateway

Date: 2026-02-20

## Status

Accepted

## Context

Meridian's backend services communicate exclusively over gRPC using Protocol Buffers. External clients
(browsers, curl, mobile applications) need to call these services using familiar HTTP/JSON semantics.

Two widely-used options exist for bridging HTTP/JSON to gRPC:

1. **grpc-gateway** (grpc-ecosystem/grpc-gateway) — generates a separate reverse proxy that translates
   REST requests to gRPC. The proxy binary is generated from proto annotations at compile time.

2. **Vanguard** (connectrpc.com/vanguard) — an embeddable Go library that translates between REST/JSON,
   Connect, gRPC-Web, and gRPC in-process, using a compiled proto `FileDescriptorSet` loaded at startup.

The gateway service needed to expose all backend gRPC services over HTTP/JSON without requiring
changes to the backend services themselves.

## Decision Drivers

* **Protocol breadth**: Support REST/JSON for external clients, Connect for browser clients, gRPC-Web
  for legacy proxies, and native gRPC passthrough — from a single gateway port.
* **No backend changes**: Backend services must not need modification. The transcoding layer should
  be transparent to them.
* **Embeddable**: The transcoder should run inside the existing gateway Go binary without a separate
  proxy process.
* **Schema-driven**: URL routing and request/response mapping must come from proto annotations
  (`google.api.http`), keeping the source of truth in the proto files.
* **Maintainability**: Generated code should be minimal; runtime schema loading is preferred over
  compile-time code generation for proxy logic.

## Considered Options

1. **grpc-gateway** — Generate an HTTP reverse proxy binary from proto annotations
2. **Vanguard** — Embed a transcoding handler in the existing gateway service
3. **Custom transcoding** — Write bespoke HTTP-to-gRPC translation per service

## Decision Outcome

Chosen option: **Vanguard**, because it satisfies all decision drivers with the least operational
complexity.

### Positive Consequences

* A single gateway binary handles REST/JSON, Connect, gRPC-Web, and gRPC-native requests without
  any additional processes.
* Backend services require zero changes — they continue to accept only gRPC connections.
* URL routing and field mapping are derived from existing `google.api.http` proto annotations,
  which are already maintained for documentation purposes.
* The compiled `FileDescriptorSet` (`descriptor.binpb`) is embedded in the binary at build time
  and loaded at startup; adding a new service requires only regenerating the descriptor and
  registering a `ServiceBackend`.
* Protocol selection is automatic, driven by the request `Content-Type` header; the same URL can
  serve REST/JSON and Connect clients without configuration.

### Negative Consequences

* The `descriptor.binpb` file must be regenerated (`make proto-descriptors`) after any proto
  change and committed to the repository.
* Vanguard is a relatively new library (connectrpc.com ecosystem); grpc-gateway has a longer
  production history and larger community.
* Streaming RPCs require HTTP/2 when using gRPC-Web or native gRPC; REST/JSON clients cannot
  use server-streaming or bidirectional-streaming RPCs.

## Pros and Cons of the Options

### grpc-gateway

Generates a standalone Go HTTP proxy from proto annotations via `protoc-gen-grpc-gateway`. The
generated proxy translates REST requests to gRPC calls.

* Good, because it has a large community and extensive documentation
* Good, because URL routing is statically checked at code-generation time
* Bad, because it produces a separate binary (or large generated source files) per service
* Bad, because it supports only REST↔gRPC translation; Connect and gRPC-Web require additional middleware
* Bad, because generated files must be regenerated and committed on every proto change, adding noise

### Vanguard

An embeddable Go library that performs live protocol translation using a runtime-loaded proto
`FileDescriptorSet`.

* Good, because it handles REST/JSON, Connect, gRPC-Web, and native gRPC from one handler
* Good, because no code generation is required beyond the existing `buf generate` step
* Good, because adding a new service only requires updating `descriptor.binpb` and a `ServiceBackend` entry
* Good, because it integrates cleanly with the existing `http.Handler` middleware chain
* Bad, because the descriptor binary must be kept in sync with proto definitions
* Bad, because it is newer and less widely adopted than grpc-gateway

### Custom transcoding

Implement HTTP-to-gRPC translation by hand for each service endpoint.

* Good, because it gives complete control over request/response transformation
* Bad, because it is a large maintenance burden as the number of endpoints grows
* Bad, because it duplicates logic already handled by proto annotations and existing libraries

## Architecture

### Protocol Flow

```
Client
  │
  ├─ REST/JSON   (Content-Type: application/json)
  ├─ Connect     (Content-Type: application/connect+json)
  ├─ gRPC-Web    (Content-Type: application/grpc-web+proto)
  │
  ▼
Gateway :8090  (HTTP/1.1 or HTTP/2)
  └─ Vanguard Transcoder
       │  translates to gRPC
       ▼
Backend Service :50051  (gRPC/HTTP2 only)
```

Native gRPC clients that support HTTP/2 can connect directly to any backend service on port 50051,
bypassing the gateway entirely.

### Service Registration

Each backend service is registered with Vanguard by providing its fully-qualified proto service
name and backend address:

```go
backends := []gateway.ServiceBackend{
    {ServiceName: "meridian.party.v1.PartyService",           BackendAddr: "party-service:50051"},
    {ServiceName: "meridian.current_account.v1.CurrentAccountService", BackendAddr: "current-account-service:50051"},
    // ... one entry per backend service
}
handler, err := gateway.NewTranscoder(descriptorBytes, backends)
```

The `descriptorBytes` are the embedded `cmd/meridian/descriptor.binpb` file, produced by
`make proto-descriptors` (`buf build api/proto -o cmd/meridian/descriptor.binpb`).

### URL Path Mapping

REST URL paths are derived from `google.api.http` annotations in the proto files:

```protobuf
rpc RegisterParty(RegisterPartyRequest) returns (RegisterPartyResponse) {
  option (google.api.http) = {
    post: "/v1/parties"
    body: "*"
  };
}
```

This maps `POST /api/v1/parties` to `PartyService.RegisterParty`. The `/api` prefix is stripped
by the gateway's route registration (`http.StripPrefix`).

### Header Propagation

The `metadataPropagationMiddleware` runs before Vanguard and:

1. Strips any incoming `x-user-id`, `x-tenant-id`, `x-auth-method`, `x-auth-roles` headers
   (to prevent spoofing).
2. Injects authenticated identity from the auth context as lowercase metadata headers, which
   Vanguard forwards to the gRPC backend as incoming metadata.

## Proto Descriptor Build Step

The descriptor is built and embedded at compile time:

```bash
# Regenerate after any proto change
make proto-descriptors
# This runs: buf build api/proto -o cmd/meridian/descriptor.binpb
```

The file is committed to the repository so that the gateway binary can be built without running
`buf build` in production CI. The `cmd/meridian/main.go` embeds it with:

```go
//go:embed descriptor.binpb
var descriptorBytes []byte
```

## Links

* [connectrpc.com/vanguard documentation](https://pkg.go.dev/connectrpc.com/vanguard)
* [grpc-ecosystem/grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway)
* [google.api.http annotation reference](https://cloud.google.com/endpoints/docs/grpc/transcoding)
* [ADR-0002: Microservices per BIAN Domain](0002-microservices-per-bian-domain.md)
* [ADR-0010: gRPC Client-Side Load Balancing](0010-grpc-client-side-load-balancing.md)
* Related code: `services/api-gateway/transcoder.go`, `cmd/meridian/main.go`

## Notes

Re-evaluate if the gateway needs advanced features not yet in Vanguard (e.g., request-level
WebSocket bridging, rich response streaming for REST clients). At that point grpc-gateway or
a custom layer may offer more control. The decision to use a single embedded `descriptor.binpb`
for all services should be revisited if the total proto surface grows large enough to cause
meaningful startup latency from descriptor parsing.
