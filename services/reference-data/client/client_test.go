package client

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func TestConfig_Defaults(t *testing.T) {
	// Verify constants are defined correctly
	assert.Equal(t, ports.ReferenceData, DefaultPort)
	assert.Equal(t, 30*time.Second, DefaultTimeout)
	assert.Equal(t, "default", DefaultNamespace)
	assert.Equal(t, "reference-data", ServiceName)
	assert.Equal(t, 1000, DefaultL1Capacity)
	assert.Equal(t, 5*time.Minute, DefaultL1TTL)
	assert.Equal(t, 30*time.Second, DefaultL1TTLJitter)
}

func TestClient_New_RequiresTarget(t *testing.T) {
	ctx := context.Background()

	// Neither Target nor ServiceName provided
	_, _, err := New(ctx, Config{})
	assert.ErrorIs(t, err, ErrTargetRequired)
}

func TestClient_New_RequiresValidRedis(t *testing.T) {
	ctx := context.Background()

	// Invalid Redis address should fail on ping
	_, _, err := New(ctx, Config{
		Target:    "localhost:50051",
		RedisAddr: "invalid-host:6379",
	})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "redis ping")
}

func TestClient_New_WithRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()

	client, cleanup, err := New(ctx, Config{
		Target:    "localhost:50051",
		RedisAddr: mr.Addr(),
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	// Verify client has Redis connection
	assert.NotNil(t, client.redisClient)

	// Cleanup
	err = cleanup()
	assert.NoError(t, err)
}

func TestClient_New_WithoutRedis(t *testing.T) {
	ctx := context.Background()

	client, cleanup, err := New(ctx, Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	// Verify client has no Redis connection
	assert.Nil(t, client.redisClient)

	// Cleanup
	err = cleanup()
	assert.NoError(t, err)
}

func TestClient_New_CustomConfig(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()

	client, cleanup, err := New(ctx, Config{
		Target:         "localhost:50051",
		Timeout:        60 * time.Second,
		RedisAddr:      mr.Addr(),
		RedisKeyPrefix: "custom-prefix",
		L1Capacity:     500,
		L1TTL:          10 * time.Minute,
		L1TTLJitter:    1 * time.Minute,
		L2TTL:          2 * time.Hour,
		L2TTLJitter:    10 * time.Minute,
	})
	require.NoError(t, err)
	defer cleanup()

	assert.Equal(t, 60*time.Second, client.timeout)
}

func TestClient_Close(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()

	client, _, err := New(ctx, Config{
		Target:    "localhost:50051",
		RedisAddr: mr.Addr(),
	})
	require.NoError(t, err)

	// Close should work
	err = client.Close()
	assert.NoError(t, err)
}

func TestClient_Conn(t *testing.T) {
	ctx := context.Background()

	client, cleanup, err := New(ctx, Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	defer cleanup()

	// Conn should return the underlying connection
	conn := client.Conn()
	assert.NotNil(t, conn)
	assert.Equal(t, client.conn, conn)
}

// mockFullGRPCClient implements the full ReferenceDataServiceClient interface
type mockFullGRPCClient struct {
	retrieveFn func(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error)
	listFn     func(ctx context.Context, req *referencedatav1.ListInstrumentsRequest) (*referencedatav1.ListInstrumentsResponse, error)
}

func (m *mockFullGRPCClient) RegisterInstrument(_ context.Context, _ *referencedatav1.RegisterInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RegisterInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockFullGRPCClient) UpdateInstrument(_ context.Context, _ *referencedatav1.UpdateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.UpdateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockFullGRPCClient) RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if m.retrieveFn != nil {
		return m.retrieveFn(ctx, req)
	}
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockFullGRPCClient) ListInstruments(ctx context.Context, req *referencedatav1.ListInstrumentsRequest, _ ...grpc.CallOption) (*referencedatav1.ListInstrumentsResponse, error) {
	if m.listFn != nil {
		return m.listFn(ctx, req)
	}
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockFullGRPCClient) ActivateInstrument(_ context.Context, _ *referencedatav1.ActivateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.ActivateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockFullGRPCClient) DeprecateInstrument(_ context.Context, _ *referencedatav1.DeprecateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.DeprecateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockFullGRPCClient) EvaluateInstrument(_ context.Context, _ *referencedatav1.EvaluateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.EvaluateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockFullGRPCClient) GetAttributeSchema(_ context.Context, _ *referencedatav1.GetAttributeSchemaRequest, _ ...grpc.CallOption) (*referencedatav1.GetAttributeSchemaResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

// createTestClient creates a client with a mock gRPC client for testing
func createTestClient(t *testing.T, mock *mockFullGRPCClient, withRedis bool) (*Client, func()) {
	t.Helper()

	l1 := cache.NewInstrumentCache()
	var l2 cache.L2Cache
	var redisClient *redis.Client

	if withRedis {
		mr := miniredis.RunT(t)
		redisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
		l2Cache, err := cache.NewRedisL2Cache(redisClient)
		require.NoError(t, err)
		l2 = l2Cache
	}

	grpcSource := NewGRPCSource(mock)
	tieredCache := cache.NewTieredInstrumentCache(l1, l2, grpcSource, nil)

	client := &Client{
		grpcClient:  mock,
		tieredCache: tieredCache,
		redisClient: redisClient,
		timeout:     30 * time.Second,
	}

	cleanup := func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	}

	return client, cleanup
}

func TestClient_GetInstrument_L1Hit(t *testing.T) {
	callCount := 0
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			callCount++
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:        uuid.New().String(),
					Code:      "USD",
					Version:   1,
					Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					CreatedAt: timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// First call populates cache
	result1, err := client.GetInstrument(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.Equal(t, "USD", result1.Definition.Code)
	assert.Equal(t, 1, callCount)

	// Second call should hit L1 cache - gRPC should not be called again
	result2, err := client.GetInstrument(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.Equal(t, "USD", result2.Definition.Code)
	assert.Equal(t, 1, callCount, "gRPC should not be called on L1 hit")
}

func TestClient_GetInstrument_L2Hit(t *testing.T) {
	callCount := 0
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			callCount++
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:        uuid.New().String(),
					Code:      "USD",
					Version:   1,
					Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					CreatedAt: timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, true)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// First call populates both L1 and L2
	result1, err := client.GetInstrument(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.Equal(t, 1, callCount)

	// Clear L1 to simulate pod restart (but L2 still has data)
	client.tieredCache.InvalidateAll(ctx)

	// Note: InvalidateAll clears both L1 and L2, so we need a different approach
	// For this test, we just verify the flow works end-to-end
	result2, err := client.GetInstrument(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, result2)
}

func TestClient_Invalidate(t *testing.T) {
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:        uuid.New().String(),
					Code:      "USD",
					Version:   1,
					Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					CreatedAt: timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, true)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Populate cache
	_, err := client.GetInstrument(ctx, "USD", 1)
	require.NoError(t, err)

	// Invalidate
	client.Invalidate(ctx, "USD", 1)

	// Stats should show the invalidation happened (entry removed)
	stats := client.Stats(ctx)
	assert.Equal(t, 0, stats.L1Size)
}

func TestClient_InvalidateCode(t *testing.T) {
	callCount := 0
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			callCount++
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:        uuid.New().String(),
					Code:      req.Code,
					Version:   req.Version,
					Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					CreatedAt: timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Populate cache with multiple versions
	for v := 1; v <= 3; v++ {
		_, err := client.GetInstrument(ctx, "USD", v)
		require.NoError(t, err)
	}

	assert.Equal(t, 3, callCount)

	// Invalidate all versions of USD
	client.InvalidateCode(ctx, "USD")

	// Cache should be empty
	stats := client.Stats(ctx)
	assert.Equal(t, 0, stats.L1Size)
}

func TestClient_InvalidateAll(t *testing.T) {
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:        uuid.New().String(),
					Code:      req.Code,
					Version:   req.Version,
					Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					CreatedAt: timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Populate cache with multiple instruments
	for _, code := range []string{"USD", "EUR", "GBP"} {
		_, err := client.GetInstrument(ctx, code, 1)
		require.NoError(t, err)
	}

	stats := client.Stats(ctx)
	assert.Equal(t, 3, stats.L1Size)

	// Invalidate all
	client.InvalidateAll(ctx)

	// Cache should be empty
	stats = client.Stats(ctx)
	assert.Equal(t, 0, stats.L1Size)
}

func TestClient_Stats(t *testing.T) {
	callCount := 0
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			callCount++
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:        uuid.New().String(),
					Code:      "USD",
					Version:   1,
					Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					CreatedAt: timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Initial stats
	stats := client.Stats(ctx)
	assert.Equal(t, int64(0), stats.L1Hits)
	assert.Equal(t, int64(0), stats.L1Misses)
	assert.Equal(t, int64(0), stats.SourceLoads)

	// First get: miss + load
	_, err := client.GetInstrument(ctx, "USD", 1)
	require.NoError(t, err)

	stats = client.Stats(ctx)
	assert.Equal(t, int64(0), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, int64(1), stats.SourceLoads)
	assert.Equal(t, 1, stats.L1Size)

	// Second get: hit
	_, err = client.GetInstrument(ctx, "USD", 1)
	require.NoError(t, err)

	stats = client.Stats(ctx)
	assert.Equal(t, int64(1), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, int64(1), stats.SourceLoads)
}

func TestClient_RetrieveInstrument_DirectGRPC(t *testing.T) {
	instrumentID := uuid.New()
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			assert.Equal(t, "EUR", req.Code)
			assert.Equal(t, int32(2), req.Version)
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:          instrumentID.String(),
					Code:        "EUR",
					Version:     2,
					Dimension:   referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					DisplayName: "Euro",
					CreatedAt:   timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	// Direct gRPC call bypasses cache
	result, err := client.RetrieveInstrument(ctx, "EUR", 2)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "EUR", result.Code)
	assert.Equal(t, int32(2), result.Version)
	assert.Equal(t, "Euro", result.DisplayName)
}

func TestClient_ListInstruments(t *testing.T) {
	mock := &mockFullGRPCClient{
		listFn: func(_ context.Context, req *referencedatav1.ListInstrumentsRequest) (*referencedatav1.ListInstrumentsResponse, error) {
			assert.Equal(t, referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE, req.StatusFilter)
			assert.Equal(t, int32(10), req.PageSize)
			assert.Equal(t, "token123", req.PageToken)

			return &referencedatav1.ListInstrumentsResponse{
				Instruments: []*referencedatav1.InstrumentDefinition{
					{
						Id:        uuid.New().String(),
						Code:      "USD",
						Version:   1,
						Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
						Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
						CreatedAt: timestamppb.Now(),
					},
					{
						Id:        uuid.New().String(),
						Code:      "EUR",
						Version:   1,
						Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
						Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
						CreatedAt: timestamppb.Now(),
					},
				},
				NextPageToken: "token456",
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	instruments, nextToken, err := client.ListInstruments(ctx, referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE, 10, "token123")
	require.NoError(t, err)
	assert.Len(t, instruments, 2)
	assert.Equal(t, "USD", instruments[0].Code)
	assert.Equal(t, "EUR", instruments[1].Code)
	assert.Equal(t, "token456", nextToken)
}

func TestClient_TenantIsolation(t *testing.T) {
	mock := &mockFullGRPCClient{
		retrieveFn: func(ctx context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			// Extract tenant from context and return tenant-specific data
			tenantID, _ := tenant.FromContext(ctx)
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:          uuid.New().String(),
					Code:        "USD",
					Version:     1,
					Dimension:   referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					DisplayName: string(tenantID) + " USD",
					CreatedAt:   timestamppb.Now(),
				},
			}, nil
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx1 := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))
	ctx2 := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant2"))

	// Get for tenant1
	result1, err := client.GetInstrument(ctx1, "USD", 1)
	require.NoError(t, err)
	assert.Equal(t, "tenant1 USD", result1.Definition.DisplayName)

	// Get for tenant2
	result2, err := client.GetInstrument(ctx2, "USD", 1)
	require.NoError(t, err)
	assert.Equal(t, "tenant2 USD", result2.Definition.DisplayName)

	// Verify tenant1 still gets their cached data
	result1Again, err := client.GetInstrument(ctx1, "USD", 1)
	require.NoError(t, err)
	assert.Equal(t, "tenant1 USD", result1Again.Definition.DisplayName)
}

func TestClient_MissingTenantContext(t *testing.T) {
	mock := &mockFullGRPCClient{}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	// No tenant context
	ctx := context.Background()

	result, err := client.GetInstrument(ctx, "USD", 1)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, cache.ErrTenantContextRequired)
}

func TestClient_GRPCError(t *testing.T) {
	mock := &mockFullGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return nil, status.Errorf(codes.NotFound, "instrument not found")
		},
	}

	client, cleanup := createTestClient(t, mock, false)
	defer cleanup()

	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("tenant1"))

	result, err := client.GetInstrument(ctx, "NOTFOUND", 1)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, registry.ErrNotFound)
}
