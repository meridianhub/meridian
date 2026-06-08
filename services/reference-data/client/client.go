// Package client provides a gRPC client for the Reference Data service with
// integrated tiered caching for high-performance instrument lookups.
//
// The client provides:
// - L1 in-memory LRU cache with compiled CEL programs (fastest, 5min TTL)
// - L2 Redis cache with serialized protos (warm, 1hr TTL)
// - L3 gRPC fallback to Reference Data Service (source of truth)
//
// Usage with Kubernetes DNS-based discovery (recommended for production):
//
//	client, cleanup, err := client.New(ctx, client.Config{
//	    ServiceName: "reference-data",
//	    Namespace:   "default",
//	    Port:        50051,
//	    RedisAddr:   "redis:6379",
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
//
// Usage without Redis (L1 cache only):
//
//	client, cleanup, err := client.New(ctx, client.Config{
//	    ServiceName: "reference-data",
//	    Namespace:   "default",
//	    Port:        50051,
//	})
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
)

const (
	// DefaultPort is the default gRPC port for the Reference Data service.
	DefaultPort = ports.ReferenceData

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for Reference Data.
	ServiceName = "reference-data"

	// DefaultL1Capacity is the default L1 cache max entries per tenant.
	DefaultL1Capacity = 1000

	// DefaultL1TTL is the default L1 cache TTL.
	DefaultL1TTL = 5 * time.Minute

	// DefaultL1TTLJitter is the default L1 TTL jitter.
	DefaultL1TTLJitter = 30 * time.Second
)

// Config holds configuration for the Reference Data client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50051").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "reference-data").
	// When specified, enables DNS-based client-side load balancing.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50051 if not specified.
	Port int

	// Timeout is the default timeout for RPC calls.
	// Defaults to 30 seconds if not specified.
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing.
	Tracer *observability.Tracer

	// Resilience is an optional configuration for circuit breaker and retry.
	Resilience *clients.ResilientClientConfig

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption

	// RedisAddr is the Redis address for L2 caching (e.g., "localhost:6379").
	// If empty, L2 cache is disabled and only L1 (in-memory) + L3 (gRPC) are used.
	RedisAddr string

	// RedisPassword is the Redis password (empty for no auth).
	RedisPassword string

	// RedisDB is the Redis database number (default: 0).
	RedisDB int

	// RedisKeyPrefix is the key prefix for Redis cache entries (default: "refdata").
	RedisKeyPrefix string

	// L1Capacity is the L1 cache max entries per tenant (default: 1000).
	L1Capacity int

	// L1TTL is the L1 cache TTL (default: 5min).
	L1TTL time.Duration

	// L1TTLJitter is the L1 TTL jitter (default: 30s).
	L1TTLJitter time.Duration

	// L2TTL is the L2 cache TTL (default: 1hr).
	L2TTL time.Duration

	// L2TTLJitter is the L2 TTL jitter (default: 5min).
	L2TTLJitter time.Duration
}

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// Client provides access to the Reference Data service with tiered caching.
type Client struct {
	conn        *grpc.ClientConn
	grpcClient  referencedatav1.ReferenceDataServiceClient
	tieredCache *cache.TieredInstrumentCache
	redisClient *redis.Client
	tracer      *observability.Tracer
	resilient   *clients.ResilientClient
	timeout     time.Duration
}

// applyDefaults sets default values for unspecified configuration fields.
func (cfg *Config) applyDefaults() {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}
	if cfg.L1Capacity == 0 {
		cfg.L1Capacity = DefaultL1Capacity
	}
	if cfg.L1TTL == 0 {
		cfg.L1TTL = DefaultL1TTL
	}
	if cfg.L1TTLJitter == 0 {
		cfg.L1TTLJitter = DefaultL1TTLJitter
	}
	if cfg.L2TTL == 0 {
		cfg.L2TTL = cache.DefaultRedisL2TTL
	}
	if cfg.L2TTLJitter == 0 {
		cfg.L2TTLJitter = cache.DefaultRedisL2TTLJitter
	}
	if cfg.RedisKeyPrefix == "" {
		cfg.RedisKeyPrefix = cache.DefaultRedisKeyPrefix
	}
}

// createL2Cache creates the Redis L2 cache if configured.
func createL2Cache(ctx context.Context, cfg Config) (cache.L2Cache, *redis.Client, error) {
	if cfg.RedisAddr == "" {
		return nil, nil, nil
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, nil, fmt.Errorf("redis ping: %w", err)
	}

	l2Cache, err := cache.NewRedisL2Cache(
		redisClient,
		cache.WithRedisKeyPrefix(cfg.RedisKeyPrefix),
		cache.WithRedisL2TTL(cfg.L2TTL, cfg.L2TTLJitter),
	)
	if err != nil {
		_ = redisClient.Close()
		return nil, nil, fmt.Errorf("create redis L2 cache: %w", err)
	}

	return l2Cache, redisClient, nil
}

// New creates a new Reference Data gRPC client with tiered caching.
//
// Returns the client, a cleanup function to close all resources, and any error.
// The cleanup function should be deferred immediately after checking the error.
func New(ctx context.Context, cfg Config) (*Client, func() error, error) {
	cfg.applyDefaults()

	conn, _, err := clients.NewConn(ctx, clients.ConnConfig{
		Target:      cfg.Target,
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		Tracer:      cfg.Tracer,
		DialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, nil, err
	}

	grpcClient := referencedatav1.NewReferenceDataServiceClient(conn)

	compiler, err := refcel.NewCompiler()
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("create CEL compiler: %w", err)
	}

	l1Cache := cache.NewInstrumentCache(
		cache.WithCacheSize(cfg.L1Capacity),
		cache.WithTTL(cfg.L1TTL, cfg.L1TTLJitter),
	)

	l2Cache, redisClient, err := createL2Cache(ctx, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	grpcSource := NewGRPCSource(grpcClient)
	tieredCache := cache.NewTieredInstrumentCache(l1Cache, l2Cache, grpcSource, compiler)

	var resilient *clients.ResilientClient
	if cfg.Resilience != nil {
		resilient = clients.NewResilientClient(*cfg.Resilience)
	}

	client := &Client{
		conn:        conn,
		grpcClient:  grpcClient,
		tieredCache: tieredCache,
		redisClient: redisClient,
		tracer:      cfg.Tracer,
		resilient:   resilient,
		timeout:     cfg.Timeout,
	}

	cleanup := func() error {
		var errs []error
		if client.redisClient != nil {
			if err := client.redisClient.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close redis: %w", err))
			}
		}
		if client.conn != nil {
			if err := client.conn.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close grpc: %w", err))
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}

	return client, cleanup, nil
}

// GetInstrument retrieves an instrument with compiled CEL programs.
// Uses tiered cache: L1 (memory) -> L2 (Redis) -> L3 (gRPC).
func (c *Client) GetInstrument(ctx context.Context, code string, version int) (*cache.CachedInstrument, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	return c.tieredCache.Get(ctx, code, version)
}

// Invalidate removes a specific entry from all cache tiers.
func (c *Client) Invalidate(ctx context.Context, code string, version int) {
	c.tieredCache.Invalidate(ctx, code, version)
}

// InvalidateCode removes all versions of an instrument code from all cache tiers.
func (c *Client) InvalidateCode(ctx context.Context, code string) {
	c.tieredCache.InvalidateCode(ctx, code)
}

// InvalidateAll removes all entries for the tenant from all cache tiers.
func (c *Client) InvalidateAll(ctx context.Context) {
	c.tieredCache.InvalidateAll(ctx)
}

// Stats returns cache statistics for monitoring.
func (c *Client) Stats(ctx context.Context) cache.TieredCacheStats {
	return c.tieredCache.Stats(ctx)
}

// Close releases all resources (gRPC connection, Redis connection).
func (c *Client) Close() error {
	var errs []error
	if c.redisClient != nil {
		if err := c.redisClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close redis: %w", err))
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close grpc: %w", err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Conn returns the underlying gRPC connection.
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

// RetrieveInstrument fetches a specific instrument by code and version directly from gRPC.
// This bypasses the cache and should only be used when fresh data is required.
// For normal lookups, use GetInstrument which uses the tiered cache.
func (c *Client) RetrieveInstrument(ctx context.Context, code string, version int) (*referencedatav1.InstrumentDefinition, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	if c.resilient != nil {
		resp, err := clients.ExecuteWithResilience(ctx, c.resilient, "RetrieveInstrument", func() (*referencedatav1.RetrieveInstrumentResponse, error) {
			return c.grpcClient.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
				Code:    code,
				Version: int32(version),
			})
		})
		if err != nil {
			return nil, fmt.Errorf("retrieve instrument: %w", err)
		}
		return resp.Instrument, nil
	}

	resp, err := c.grpcClient.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
		Code:    code,
		Version: int32(version),
	})
	if err != nil {
		return nil, fmt.Errorf("retrieve instrument: %w", err)
	}
	return resp.Instrument, nil
}

// ListInstruments returns instruments matching the filter criteria directly from gRPC.
func (c *Client) ListInstruments(ctx context.Context, statusFilter referencedatav1.InstrumentStatus, pageSize int32, pageToken string) ([]*referencedatav1.InstrumentDefinition, string, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)

	if c.resilient != nil {
		resp, err := clients.ExecuteWithResilience(ctx, c.resilient, "ListInstruments", func() (*referencedatav1.ListInstrumentsResponse, error) {
			return c.grpcClient.ListInstruments(ctx, &referencedatav1.ListInstrumentsRequest{
				StatusFilter: statusFilter,
				PageSize:     pageSize,
				PageToken:    pageToken,
			})
		})
		if err != nil {
			return nil, "", fmt.Errorf("list instruments: %w", err)
		}
		return resp.Instruments, resp.NextPageToken, nil
	}

	resp, err := c.grpcClient.ListInstruments(ctx, &referencedatav1.ListInstrumentsRequest{
		StatusFilter: statusFilter,
		PageSize:     pageSize,
		PageToken:    pageToken,
	})
	if err != nil {
		return nil, "", fmt.Errorf("list instruments: %w", err)
	}
	return resp.Instruments, resp.NextPageToken, nil
}
