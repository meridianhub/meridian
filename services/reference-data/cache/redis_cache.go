// Package cache provides caching infrastructure for instrument definitions
// with tenant isolation and TTL-based expiration.
package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Default Redis L2 cache configuration values.
const (
	// DefaultRedisL2TTL is the base TTL for L2 cache entries (1 hour).
	DefaultRedisL2TTL = 1 * time.Hour

	// DefaultRedisL2TTLJitter is the maximum random variation added to TTL.
	// This prevents thundering herd when many entries expire simultaneously.
	DefaultRedisL2TTLJitter = 5 * time.Minute

	// DefaultRedisKeyPrefix is the default key prefix for instrument cache entries.
	DefaultRedisKeyPrefix = "refdata"
)

// ErrRedisClientRequired is returned when Redis client is nil.
var ErrRedisClientRequired = errors.New("redis client is required")

// RedisL2CacheOption configures a RedisL2Cache.
type RedisL2CacheOption func(*RedisL2Cache)

// WithRedisKeyPrefix sets the key prefix for Redis keys.
func WithRedisKeyPrefix(prefix string) RedisL2CacheOption {
	return func(c *RedisL2Cache) {
		c.keyPrefix = prefix
	}
}

// WithRedisL2TTL sets the base TTL and jitter for L2 cache entries.
func WithRedisL2TTL(baseTTL, jitter time.Duration) RedisL2CacheOption {
	return func(c *RedisL2Cache) {
		c.baseTTL = baseTTL
		c.ttlJitter = jitter
	}
}

// RedisL2Cache provides Redis-based L2 caching for instrument definitions.
// It stores proto-serialized InstrumentDefinition objects with tenant-scoped keys.
//
// Key format: {keyPrefix}:instrument:{tenantID}:{code}:{version}
//
// Thread-safety: All methods are safe for concurrent use.
type RedisL2Cache struct {
	// client is the Redis client for cache operations.
	client redis.Cmdable

	// keyPrefix is the prefix for all Redis keys.
	keyPrefix string

	// baseTTL is the base time-to-live for cache entries.
	baseTTL time.Duration

	// ttlJitter is the maximum random variation added to TTL.
	ttlJitter time.Duration
}

// NewRedisL2Cache creates a new Redis L2 cache.
// The client can be *redis.Client, *redis.ClusterClient, or any redis.Cmdable.
func NewRedisL2Cache(client redis.Cmdable, opts ...RedisL2CacheOption) (*RedisL2Cache, error) {
	if client == nil {
		return nil, ErrRedisClientRequired
	}

	c := &RedisL2Cache{
		client:    client,
		keyPrefix: DefaultRedisKeyPrefix,
		baseTTL:   DefaultRedisL2TTL,
		ttlJitter: DefaultRedisL2TTLJitter,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Get retrieves an instrument definition from the L2 cache.
// Returns nil if the entry is not found or tenant context is missing.
// This method does NOT return errors on cache miss - only nil.
func (c *RedisL2Cache) Get(ctx context.Context, code string, version int) *registry.InstrumentDefinition {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	key := c.instrumentKey(tenantID, code, version)
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		// Both redis.Nil (not found) and actual errors return nil
		// L2 cache misses are expected and should not propagate errors
		return nil
	}

	// Deserialize protobuf
	var pbInstrument referencedatav1.InstrumentDefinition
	if err := proto.Unmarshal(data, &pbInstrument); err != nil {
		// Corrupted data - treat as cache miss
		// Optionally, we could delete the corrupted entry here
		return nil
	}

	// Convert protobuf to domain model
	return fromProto(&pbInstrument)
}

// Put stores an instrument definition in the L2 cache.
// Does nothing if tenant context is missing.
func (c *RedisL2Cache) Put(ctx context.Context, code string, version int, def *registry.InstrumentDefinition) {
	if def == nil {
		return
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	// Convert to protobuf
	pbInstrument := toProto(def)

	// Serialize protobuf
	data, err := proto.Marshal(pbInstrument)
	if err != nil {
		// Serialization failure - log and continue (cache miss is acceptable)
		return
	}

	key := c.instrumentKey(tenantID, code, version)
	ttl := c.jitteredTTL()

	// Store with TTL - ignore errors (cache failures shouldn't break the application)
	_ = c.client.Set(ctx, key, data, ttl).Err()
}

// Invalidate removes a specific entry from the L2 cache.
func (c *RedisL2Cache) Invalidate(ctx context.Context, code string, version int) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	key := c.instrumentKey(tenantID, code, version)
	_ = c.client.Del(ctx, key).Err()
}

// InvalidateCode removes all versions of an instrument code for the tenant.
// Uses pattern matching with SCAN to find and delete all matching keys.
// Uses UNLINK instead of DEL for non-blocking deletion.
func (c *RedisL2Cache) InvalidateCode(ctx context.Context, code string) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	// Pattern to match all versions: {prefix}:instrument:{tenantID}:{code}:*
	pattern := c.instrumentKeyPattern(tenantID, code)

	// Use SCAN to find matching keys (safer than KEYS for production)
	var cursor uint64
	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return // Best effort invalidation
		}

		if len(keys) > 0 {
			// Use UNLINK for non-blocking deletion (Redis 4.0+)
			_ = c.client.Unlink(ctx, keys...).Err()
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
}

// InvalidateAll removes all instrument cache entries for the tenant.
// Uses pattern matching with SCAN to find and delete all matching keys.
// Uses UNLINK instead of DEL for non-blocking deletion.
func (c *RedisL2Cache) InvalidateAll(ctx context.Context) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return
	}

	// Pattern to match all tenant entries: {prefix}:instrument:{tenantID}:*
	pattern := c.tenantKeyPattern(tenantID)

	// Use SCAN to find matching keys
	var cursor uint64
	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return // Best effort invalidation
		}

		if len(keys) > 0 {
			// Use UNLINK for non-blocking deletion (Redis 4.0+)
			_ = c.client.Unlink(ctx, keys...).Err()
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
}

// instrumentKey generates the Redis key for a specific instrument.
// Format: {keyPrefix}:instrument:{tenantID}:{code}:{version}
func (c *RedisL2Cache) instrumentKey(tenantID tenant.TenantID, code string, version int) string {
	return fmt.Sprintf("%s:instrument:%s:%s:%d", c.keyPrefix, tenantID, code, version)
}

// instrumentKeyPattern generates a pattern to match all versions of an instrument.
// Format: {keyPrefix}:instrument:{tenantID}:{code}:*
func (c *RedisL2Cache) instrumentKeyPattern(tenantID tenant.TenantID, code string) string {
	return fmt.Sprintf("%s:instrument:%s:%s:*", c.keyPrefix, tenantID, code)
}

// tenantKeyPattern generates a pattern to match all instruments for a tenant.
// Format: {keyPrefix}:instrument:{tenantID}:*
func (c *RedisL2Cache) tenantKeyPattern(tenantID tenant.TenantID) string {
	return fmt.Sprintf("%s:instrument:%s:*", c.keyPrefix, tenantID)
}

// jitteredTTL returns the base TTL plus a random jitter.
// The jitter helps prevent thundering herd when many entries expire.
func (c *RedisL2Cache) jitteredTTL() time.Duration {
	if c.ttlJitter == 0 {
		return c.baseTTL
	}

	// Generate random jitter in range [-ttlJitter, +ttlJitter]
	jitterRange := int64(c.ttlJitter) * 2
	jitter := rand.Int64N(jitterRange) - int64(c.ttlJitter)

	return c.baseTTL + time.Duration(jitter)
}

// toProto converts InstrumentDefinition to protobuf.
func toProto(def *registry.InstrumentDefinition) *referencedatav1.InstrumentDefinition {
	pb := &referencedatav1.InstrumentDefinition{
		Id:                       def.ID.String(),
		Code:                     def.Code,
		Version:                  int32(def.Version),
		Dimension:                dimensionToProto(def.Dimension),
		Precision:                int32(def.Precision),
		Status:                   statusToProto(def.Status),
		IsSystem:                 def.IsSystem,
		ValidationExpression:     def.ValidationExpression,
		FungibilityKeyExpression: def.FungibilityKeyExpression,
		ErrorMessageExpression:   def.ErrorMessageExpression,
		AttributeSchema:          string(def.AttributeSchema),
		DisplayName:              def.DisplayName,
		Description:              def.Description,
		CreatedAt:                timestamppb.New(def.CreatedAt),
	}

	if def.ActivatedAt != nil {
		pb.ActivatedAt = timestamppb.New(*def.ActivatedAt)
	}

	if def.DeprecatedAt != nil {
		pb.DeprecatedAt = timestamppb.New(*def.DeprecatedAt)
	}

	return pb
}

// fromProto converts protobuf to InstrumentDefinition.
func fromProto(pb *referencedatav1.InstrumentDefinition) *registry.InstrumentDefinition {
	def := &registry.InstrumentDefinition{
		Code:                     pb.Code,
		Version:                  int(pb.Version),
		Dimension:                dimensionFromProto(pb.Dimension),
		Precision:                int(pb.Precision),
		Status:                   statusFromProto(pb.Status),
		IsSystem:                 pb.IsSystem,
		ValidationExpression:     pb.ValidationExpression,
		FungibilityKeyExpression: pb.FungibilityKeyExpression,
		ErrorMessageExpression:   pb.ErrorMessageExpression,
		AttributeSchema:          []byte(pb.AttributeSchema),
		DisplayName:              pb.DisplayName,
		Description:              pb.Description,
	}

	// Parse UUID
	if id, err := uuid.Parse(pb.Id); err == nil {
		def.ID = id
	}

	// Parse timestamps
	if pb.CreatedAt != nil {
		def.CreatedAt = pb.CreatedAt.AsTime()
	}

	if pb.ActivatedAt != nil {
		t := pb.ActivatedAt.AsTime()
		def.ActivatedAt = &t
	}

	if pb.DeprecatedAt != nil {
		t := pb.DeprecatedAt.AsTime()
		def.DeprecatedAt = &t
	}

	return def
}

// dimensionToProto converts domain Dimension to protobuf Dimension.
func dimensionToProto(d registry.Dimension) referencedatav1.Dimension {
	switch d {
	case registry.DimensionMonetary:
		return referencedatav1.Dimension_DIMENSION_CURRENCY
	case registry.DimensionEnergy:
		return referencedatav1.Dimension_DIMENSION_ENERGY
	case registry.DimensionMass:
		return referencedatav1.Dimension_DIMENSION_MASS
	case registry.DimensionVolume:
		return referencedatav1.Dimension_DIMENSION_VOLUME
	case registry.DimensionTime:
		return referencedatav1.Dimension_DIMENSION_TIME
	case registry.DimensionCompute:
		return referencedatav1.Dimension_DIMENSION_COMPUTE
	case registry.DimensionQuantity:
		return referencedatav1.Dimension_DIMENSION_COUNT
	case registry.DimensionCarbon:
		return referencedatav1.Dimension_DIMENSION_CARBON
	case registry.DimensionData:
		return referencedatav1.Dimension_DIMENSION_DATA
	default:
		return referencedatav1.Dimension_DIMENSION_UNSPECIFIED
	}
}

// dimensionFromProto converts protobuf Dimension to domain Dimension.
func dimensionFromProto(d referencedatav1.Dimension) registry.Dimension {
	switch d {
	case referencedatav1.Dimension_DIMENSION_CURRENCY:
		return registry.DimensionMonetary
	case referencedatav1.Dimension_DIMENSION_ENERGY:
		return registry.DimensionEnergy
	case referencedatav1.Dimension_DIMENSION_MASS:
		return registry.DimensionMass
	case referencedatav1.Dimension_DIMENSION_VOLUME:
		return registry.DimensionVolume
	case referencedatav1.Dimension_DIMENSION_TIME:
		return registry.DimensionTime
	case referencedatav1.Dimension_DIMENSION_COMPUTE:
		return registry.DimensionCompute
	case referencedatav1.Dimension_DIMENSION_COUNT:
		return registry.DimensionQuantity
	case referencedatav1.Dimension_DIMENSION_CARBON:
		return registry.DimensionCarbon
	case referencedatav1.Dimension_DIMENSION_DATA:
		return registry.DimensionData
	case referencedatav1.Dimension_DIMENSION_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// statusToProto converts domain Status to protobuf InstrumentStatus.
func statusToProto(s registry.Status) referencedatav1.InstrumentStatus {
	switch s {
	case registry.StatusDraft:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT
	case registry.StatusActive:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE
	case registry.StatusDeprecated:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED
	default:
		return referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED
	}
}

// statusFromProto converts protobuf InstrumentStatus to domain Status.
func statusFromProto(s referencedatav1.InstrumentStatus) registry.Status {
	switch s {
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT:
		return registry.StatusDraft
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE:
		return registry.StatusActive
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED:
		return registry.StatusDeprecated
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}
