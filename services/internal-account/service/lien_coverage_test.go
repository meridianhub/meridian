package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// ---------------------------------------------------------------------------
// storeIdempotencyResultOrCleanup – direct unit tests
// ---------------------------------------------------------------------------

func TestStoreIdempotencyResultOrCleanup_NilService(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	key := idempotency.Key{TenantID: "t", RequestID: "r"}
	// No panic when idempotencyService is nil
	svc.storeIdempotencyResultOrCleanup(context.Background(), key, &pb.ExecuteLienResponse{}, "test")
}

func TestStoreIdempotencyResultOrCleanup_EmptyRequestID(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	// Empty RequestID → no-op
	key := idempotency.Key{TenantID: "t", RequestID: ""}
	svc.storeIdempotencyResultOrCleanup(context.Background(), key, &pb.ExecuteLienResponse{}, "test")
}

func TestStoreIdempotencyResultOrCleanup_StoresResult(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	key := idempotency.Key{
		TenantID:  "test-tenant",
		Namespace: idempotencyNamespace,
		Operation: "execute_lien",
		EntityID:  uuid.New().String(),
		RequestID: "store-test-001",
	}

	resp := &pb.ExecuteLienResponse{
		Lien: &pb.Lien{
			LienId: uuid.New().String(),
			Status: pb.LienStatus_LIEN_STATUS_EXECUTED,
		},
	}

	svc.storeIdempotencyResultOrCleanup(context.Background(), key, resp, "execute_lien")

	// Verify result was stored
	mockIdemp.mu.Lock()
	_, stored := mockIdemp.results[key.String()]
	mockIdemp.mu.Unlock()
	assert.True(t, stored, "result should be stored in idempotency service")
}

func TestStoreIdempotencyResultOrCleanup_StoreError(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	mockIdemp.storeErr = assert.AnError
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	key := idempotency.Key{
		TenantID:  "test-tenant",
		Namespace: idempotencyNamespace,
		Operation: "execute_lien",
		EntityID:  uuid.New().String(),
		RequestID: "store-err-001",
	}

	resp := &pb.ExecuteLienResponse{}

	// Should not panic when store fails
	svc.storeIdempotencyResultOrCleanup(context.Background(), key, resp, "execute_lien")
}

// ---------------------------------------------------------------------------
// buildInitiateLienResponse – with mock reference data client
// ---------------------------------------------------------------------------

func TestBuildInitiateLienResponse_SimpleLien(t *testing.T) {
	repo := newMockRepository()
	// Use an instrumentMap that returns precision 2 for "GBP"
	refDataClient := newInstrumentMap(map[string]int32{"GBP": 2})
	svc, err := NewServiceFull(repo, nil, refDataClient, testLogger(), nil)
	require.NoError(t, err)

	lien, err := domain.NewLien(uuid.New(), 10050, "GBP", "", "PAY-BUILD-001", nil)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))
	resp, err := svc.buildInitiateLienResponse(ctx, lien)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "100.5", resp.Lien.Amount.Amount)
	assert.Nil(t, resp.ValuedAmount)
}

func TestBuildInitiateLienResponse_WithExpiry(t *testing.T) {
	repo := newMockRepository()
	refDataClient := newInstrumentMap(map[string]int32{"GBP": 2})
	svc, err := NewServiceFull(repo, nil, refDataClient, testLogger(), nil)
	require.NoError(t, err)

	future := time.Now().Add(24 * time.Hour)
	lien, err := domain.NewLien(uuid.New(), 5000, "GBP", "bucket-1", "PAY-EXPIRY-001", &future)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))
	resp, err := svc.buildInitiateLienResponse(ctx, lien)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Lien.ExpiresAt)
}

func TestBuildInitiateLienResponse_WithValuedLien(t *testing.T) {
	repo := newMockRepository()
	refDataClient := newInstrumentMap(map[string]int32{"GBP": 2, "kWh": 3})
	svc, err := NewServiceFull(repo, nil, refDataClient, testLogger(), nil)
	require.NoError(t, err)

	reservedQty := &domain.InstrumentAmount{
		Amount:         decimal.NewFromFloat(100.0),
		InstrumentCode: "kWh",
	}
	valuedAmt := &domain.InstrumentAmount{
		Amount:         decimal.NewFromFloat(35.00),
		InstrumentCode: "GBP",
	}
	analysisJSON := json.RawMessage(`{"method":"spot","rate":0.35}`)

	lien, err := domain.NewValuedLien(
		uuid.New(), 3500, "GBP", "", "PAY-VALUED-001", nil,
		reservedQty, valuedAmt, analysisJSON,
	)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))
	resp, err := svc.buildInitiateLienResponse(ctx, lien)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.ValuedAmount)
	assert.Equal(t, "35", resp.ValuedAmount.Amount)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
	assert.NotNil(t, resp.Lien.ReservedQuantity)
	assert.Equal(t, "100", resp.Lien.ReservedQuantity.Amount)
	// ValuationAnalysis was provided so Basis should be populated
	assert.NotNil(t, resp.Basis)
}

func TestBuildInitiateLienResponse_NilReferenceDataClient(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil) // no refDataClient
	require.NoError(t, err)

	lien, err := domain.NewLien(uuid.New(), 1000, "GBP", "", "PAY-NOREF-001", nil)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))
	_, err = svc.buildInitiateLienResponse(ctx, lien)
	require.Error(t, err) // FailedPrecondition: reference data client required
}

// ---------------------------------------------------------------------------
// DatabaseHealthChecker – Name and Check
// ---------------------------------------------------------------------------

func TestDatabaseHealthChecker_Name(t *testing.T) {
	checker := NewDatabaseHealthChecker(nil, time.Second)
	assert.Equal(t, "database", checker.Name())
}

// ---------------------------------------------------------------------------
// HealthChecker.Check – component-level check path
// ---------------------------------------------------------------------------

func TestHealthChecker_Check_ByComponentName(t *testing.T) {
	pkHealthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		Repository:                  persistence.NewRepository(nil),
		PositionKeepingHealthClient: pkHealthClient,
		Logger:                      testLogger(),
	})
	require.NoError(t, err)

	// Check a specific component by name
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "positionkeeping",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestHealthChecker_Check_UnknownService(t *testing.T) {
	pkHealthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		Repository:                  persistence.NewRepository(nil),
		PositionKeepingHealthClient: pkHealthClient,
		Logger:                      testLogger(),
	})
	require.NoError(t, err)

	// Unknown service name → UNKNOWN status
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "nonexistent-service",
	})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}

// ---------------------------------------------------------------------------
// RetrieveLien – findByID not found path (uses fake repo that won't call DB)
// ---------------------------------------------------------------------------

func TestRetrieveLien_NotFound_WithFakeLienRepo(t *testing.T) {
	// We can test FindByID returning ErrLienNotFound by using a real DB testcontainer,
	// but that's covered in the integration test. Here we test the pre-DB path:
	// ensure invalid UUID path is covered (different from nil-repo test).

	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	// UUID format but "not-a-uuid" suffix ensures we hit the UUID parse failure
	_, err = svc.RetrieveLien(context.Background(), &pb.RetrieveLienRequest{
		LienId: "12345678-1234-1234-1234-not-a-uuid-!!!",
	})
	require.Error(t, err)
}
