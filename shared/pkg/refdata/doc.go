// Package refdata provides a shared InstrumentResolver interface and implementations
// for resolving instrument properties from Reference Data with in-process caching.
//
// Services use [InstrumentResolver] to obtain instrument metadata (dimension, precision,
// rounding mode) without directly depending on the Reference Data gRPC client.
// [CachedResolver] wraps any resolver with an in-process TTL cache to avoid redundant
// RPC calls for frequently-accessed instruments.
//
// For environments where Reference Data is unavailable, [FallbackResolver] chains
// multiple resolvers and returns the first successful result.
package refdata
