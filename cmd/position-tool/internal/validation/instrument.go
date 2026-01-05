package validation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
)

// InstrumentCheckerConfig holds configuration for the instrument checker.
type InstrumentCheckerConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50051").
	// If set, overrides Kubernetes DNS-based discovery.
	Target string

	// ServiceName is the Kubernetes service name for Reference Data.
	ServiceName string

	// Namespace is the Kubernetes namespace (defaults to "default").
	Namespace string

	// Port is the service port number (defaults to 50051).
	Port int

	// Timeout is the default timeout for RPC calls (defaults to 30s).
	Timeout time.Duration

	// CacheSize is the maximum number of instruments to cache (defaults to 1000).
	CacheSize int

	// CacheTTL is how long to cache instrument definitions (defaults to 5 minutes).
	CacheTTL time.Duration

	// Logger is used for structured logging.
	Logger *slog.Logger

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption

	// CreateMissingInstruments enables auto-creation of instruments in DRAFT status.
	CreateMissingInstruments bool
}

// DefaultInstrumentCheckerConfig returns configuration with sensible defaults.
func DefaultInstrumentCheckerConfig() InstrumentCheckerConfig {
	return InstrumentCheckerConfig{
		ServiceName: "reference-data",
		Namespace:   "default",
		Port:        50051,
		Timeout:     30 * time.Second,
		CacheSize:   1000,
		CacheTTL:    5 * time.Minute,
	}
}

// CachedInstrument holds a cached instrument definition.
type CachedInstrument struct {
	// Definition is the instrument definition.
	Definition *referencedatav1.InstrumentDefinition

	// CachedAt is when this entry was cached.
	CachedAt time.Time
}

// InstrumentChecker validates that instruments exist and are active.
// It uses an LRU cache to minimize gRPC calls.
//
// Thread-safety: All methods are safe for concurrent use.
type InstrumentChecker struct {
	client  referencedatav1.ReferenceDataServiceClient
	conn    *grpc.ClientConn
	timeout time.Duration
	logger  *slog.Logger

	createMissing bool

	// cache stores instrument definitions keyed by "code:version"
	cache    map[string]*CachedInstrument
	cacheTTL time.Duration
	mu       sync.RWMutex

	// Stats
	cacheHits   int64
	cacheMisses int64
	created     int64
}

// NewInstrumentChecker creates a new instrument checker with the given configuration.
func NewInstrumentChecker(ctx context.Context, cfg InstrumentCheckerConfig) (*InstrumentChecker, error) {
	applyInstrumentDefaults(&cfg)

	conn, err := createInstrumentConnection(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create reference data connection: %w", err)
	}

	return &InstrumentChecker{
		client:        referencedatav1.NewReferenceDataServiceClient(conn),
		conn:          conn,
		timeout:       cfg.Timeout,
		logger:        cfg.Logger,
		createMissing: cfg.CreateMissingInstruments,
		cache:         make(map[string]*CachedInstrument),
		cacheTTL:      cfg.CacheTTL,
	}, nil
}

func applyInstrumentDefaults(cfg *InstrumentCheckerConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	if cfg.Port == 0 {
		cfg.Port = 50051
	}
	if cfg.CacheSize == 0 {
		cfg.CacheSize = 1000
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 5 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
}

func createInstrumentConnection(ctx context.Context, cfg InstrumentCheckerConfig) (*grpc.ClientConn, error) {
	if cfg.ServiceName != "" && cfg.Target == "" {
		return platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
			ServiceName: cfg.ServiceName,
			Namespace:   cfg.Namespace,
			Port:        cfg.Port,
			DialOptions: cfg.DialOptions,
		})
	}

	if cfg.Target != "" {
		dialOpts := cfg.DialOptions
		if dialOpts == nil {
			dialOpts = []grpc.DialOption{
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			}
		}
		return grpc.NewClient(cfg.Target, dialOpts...)
	}

	return nil, errors.New("either Target or ServiceName must be provided")
}

// InstrumentCheckResult contains the result of an instrument existence check.
type InstrumentCheckResult struct {
	// Exists indicates if the instrument exists.
	Exists bool

	// IsActive indicates if the instrument is in ACTIVE status.
	IsActive bool

	// Definition is the instrument definition if found.
	Definition *referencedatav1.InstrumentDefinition

	// WasCreated indicates if the instrument was auto-created.
	WasCreated bool
}

// Check verifies that an instrument exists and is active.
// Uses the cache for frequently accessed instruments.
func (ic *InstrumentChecker) Check(ctx context.Context, code string, version int) (*InstrumentCheckResult, error) {
	// Check cache first
	cached := ic.getFromCache(code, version)
	if cached != nil {
		atomic.AddInt64(&ic.cacheHits, 1)
		return &InstrumentCheckResult{
			Exists:     true,
			IsActive:   cached.Definition.Status == referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			Definition: cached.Definition,
		}, nil
	}
	atomic.AddInt64(&ic.cacheMisses, 1)

	// Fetch from service
	ctx, cancel := context.WithTimeout(ctx, ic.timeout)
	defer cancel()

	resp, err := ic.client.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
		Code:    code,
		Version: int32(version),
	})
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.NotFound {
			// Instrument not found - optionally create it
			if ic.createMissing {
				return ic.createInstrument(ctx, code)
			}
			return &InstrumentCheckResult{Exists: false}, nil
		}
		return nil, fmt.Errorf("failed to retrieve instrument %s v%d: %w", code, version, err)
	}

	// Cache the result
	ic.addToCache(code, version, resp.Instrument)

	return &InstrumentCheckResult{
		Exists:     true,
		IsActive:   resp.Instrument.Status == referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		Definition: resp.Instrument,
	}, nil
}

// createInstrument auto-creates a missing instrument in DRAFT status.
func (ic *InstrumentChecker) createInstrument(ctx context.Context, code string) (*InstrumentCheckResult, error) {
	ic.logger.Info("auto-creating missing instrument",
		"code", code,
		"status", "DRAFT",
	)

	// Create with minimal required fields - dimension defaults to QUANTITY
	resp, err := ic.client.RegisterInstrument(ctx, &referencedatav1.RegisterInstrumentRequest{
		Code:        code,
		Dimension:   referencedatav1.Dimension_DIMENSION_COUNT,
		Precision:   2, // Default precision
		DisplayName: code,
		Description: "Auto-created during bulk import",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to auto-create instrument %s: %w", code, err)
	}

	atomic.AddInt64(&ic.created, 1)

	return &InstrumentCheckResult{
		Exists:     true,
		IsActive:   false, // Created in DRAFT status
		Definition: resp.Instrument,
		WasCreated: true,
	}, nil
}

// CheckBatch verifies multiple instruments in a batch.
// Returns a map of instrument code to check result.
func (ic *InstrumentChecker) CheckBatch(ctx context.Context, codes []string, version int) (map[string]*InstrumentCheckResult, error) {
	results := make(map[string]*InstrumentCheckResult)
	var uncached []string

	// Check cache for all codes
	for _, code := range codes {
		if cached := ic.getFromCache(code, version); cached != nil {
			atomic.AddInt64(&ic.cacheHits, 1)
			results[code] = &InstrumentCheckResult{
				Exists:     true,
				IsActive:   cached.Definition.Status == referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				Definition: cached.Definition,
			}
		} else {
			atomic.AddInt64(&ic.cacheMisses, 1)
			uncached = append(uncached, code)
		}
	}

	// Fetch uncached instruments
	for _, code := range uncached {
		result, err := ic.Check(ctx, code, version)
		if err != nil {
			return nil, err
		}
		results[code] = result
	}

	return results, nil
}

// GetAttributeSchema retrieves the JSON Schema for an instrument's attributes.
func (ic *InstrumentChecker) GetAttributeSchema(ctx context.Context, code string, version int) (string, error) {
	// Check cache first
	cached := ic.getFromCache(code, version)
	if cached != nil {
		return cached.Definition.AttributeSchema, nil
	}

	// Fetch from service
	ctx, cancel := context.WithTimeout(ctx, ic.timeout)
	defer cancel()

	resp, err := ic.client.GetAttributeSchema(ctx, &referencedatav1.GetAttributeSchemaRequest{
		Code:    code,
		Version: int32(version),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get attribute schema for %s v%d: %w", code, version, err)
	}

	return resp.JsonSchema, nil
}

// cacheKey generates the cache key for an instrument.
func cacheKey(code string, version int) string {
	if version == 0 {
		return code + ":latest"
	}
	return fmt.Sprintf("%s:%d", code, version)
}

// getFromCache retrieves an instrument from cache if not expired.
func (ic *InstrumentChecker) getFromCache(code string, version int) *CachedInstrument {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	key := cacheKey(code, version)
	cached, ok := ic.cache[key]
	if !ok {
		return nil
	}

	// Check if expired
	if time.Since(cached.CachedAt) > ic.cacheTTL {
		return nil
	}

	return cached
}

// addToCache adds an instrument to the cache.
func (ic *InstrumentChecker) addToCache(code string, version int, def *referencedatav1.InstrumentDefinition) {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	key := cacheKey(code, version)
	ic.cache[key] = &CachedInstrument{
		Definition: def,
		CachedAt:   time.Now(),
	}
}

// InvalidateCache removes an instrument from the cache.
func (ic *InstrumentChecker) InvalidateCache(code string, version int) {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	delete(ic.cache, cacheKey(code, version))
}

// ClearCache removes all entries from the cache.
func (ic *InstrumentChecker) ClearCache() {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	ic.cache = make(map[string]*CachedInstrument)
}

// InstrumentCacheStats contains statistics about the instrument cache.
type InstrumentCacheStats struct {
	// Hits is the number of cache hits.
	Hits int64

	// Misses is the number of cache misses.
	Misses int64

	// Size is the current number of cached entries.
	Size int

	// Created is the number of instruments auto-created.
	Created int64
}

// Stats returns cache statistics.
func (ic *InstrumentChecker) Stats() InstrumentCacheStats {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	return InstrumentCacheStats{
		Hits:    atomic.LoadInt64(&ic.cacheHits),
		Misses:  atomic.LoadInt64(&ic.cacheMisses),
		Size:    len(ic.cache),
		Created: atomic.LoadInt64(&ic.created),
	}
}

// HitRate returns the cache hit rate as a percentage.
func (ic *InstrumentChecker) HitRate() float64 {
	hits := atomic.LoadInt64(&ic.cacheHits)
	misses := atomic.LoadInt64(&ic.cacheMisses)
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total) * 100
}

// Close releases the gRPC connection.
func (ic *InstrumentChecker) Close() error {
	if ic.conn != nil {
		return ic.conn.Close()
	}
	return nil
}

// InstrumentCheckerInterface defines the interface for instrument checking.
// This allows for easy mocking in tests.
type InstrumentCheckerInterface interface {
	Check(ctx context.Context, code string, version int) (*InstrumentCheckResult, error)
	CheckBatch(ctx context.Context, codes []string, version int) (map[string]*InstrumentCheckResult, error)
	GetAttributeSchema(ctx context.Context, code string, version int) (string, error)
	Stats() InstrumentCacheStats
	Close() error
}

// Ensure InstrumentChecker implements the interface.
var _ InstrumentCheckerInterface = (*InstrumentChecker)(nil)

// MockInstrumentChecker is a mock implementation for testing.
type MockInstrumentChecker struct {
	Instruments map[string]*referencedatav1.InstrumentDefinition
	CheckFunc   func(ctx context.Context, code string, version int) (*InstrumentCheckResult, error)
}

// Check implements InstrumentCheckerInterface.
func (m *MockInstrumentChecker) Check(ctx context.Context, code string, version int) (*InstrumentCheckResult, error) {
	if m.CheckFunc != nil {
		return m.CheckFunc(ctx, code, version)
	}

	if def, ok := m.Instruments[code]; ok {
		return &InstrumentCheckResult{
			Exists:     true,
			IsActive:   def.Status == referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			Definition: def,
		}, nil
	}

	return &InstrumentCheckResult{Exists: false}, nil
}

// CheckBatch implements InstrumentCheckerInterface.
func (m *MockInstrumentChecker) CheckBatch(ctx context.Context, codes []string, version int) (map[string]*InstrumentCheckResult, error) {
	results := make(map[string]*InstrumentCheckResult)
	for _, code := range codes {
		result, err := m.Check(ctx, code, version)
		if err != nil {
			return nil, err
		}
		results[code] = result
	}
	return results, nil
}

// GetAttributeSchema implements InstrumentCheckerInterface.
func (m *MockInstrumentChecker) GetAttributeSchema(_ context.Context, code string, _ int) (string, error) {
	if def, ok := m.Instruments[code]; ok {
		return def.AttributeSchema, nil
	}
	return "", fmt.Errorf("instrument %s not found", code)
}

// Stats implements InstrumentCheckerInterface.
func (m *MockInstrumentChecker) Stats() InstrumentCacheStats {
	return InstrumentCacheStats{Size: len(m.Instruments)}
}

// Close implements InstrumentCheckerInterface.
func (m *MockInstrumentChecker) Close() error {
	return nil
}
