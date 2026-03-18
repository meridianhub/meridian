package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// ---------------------------------------------------------------------------
// mockUnhealthyChecker – returns StatusUnhealthy for logHealthCheck path testing
// ---------------------------------------------------------------------------

type mockUnhealthyChecker struct{}

func (m *mockUnhealthyChecker) Name() string { return "test-db" }

func (m *mockUnhealthyChecker) Check(_ context.Context) health.ComponentResult {
	return health.ComponentResult{
		Name:    "test-db",
		Status:  health.StatusUnhealthy,
		Message: "simulated db failure",
		Error:   assert.AnError,
	}
}

// ---------------------------------------------------------------------------
// mockWatchServer – implements grpc_health_v1.Health_WatchServer for Watch() tests
// ---------------------------------------------------------------------------

type mockWatchServer struct {
	mu        sync.Mutex
	ctx       context.Context
	responses []*grpc_health_v1.HealthCheckResponse
}

func (m *mockWatchServer) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, resp)
	return nil
}

func (m *mockWatchServer) Context() context.Context     { return m.ctx }
func (m *mockWatchServer) SetHeader(metadata.MD) error  { return nil }
func (m *mockWatchServer) SendHeader(metadata.MD) error { return nil }
func (m *mockWatchServer) SetTrailer(metadata.MD)       {}
func (m *mockWatchServer) SendMsg(interface{}) error    { return nil }
func (m *mockWatchServer) RecvMsg(interface{}) error    { return nil }

// ---------------------------------------------------------------------------
// logHealthCheck unhealthy path
// ---------------------------------------------------------------------------

// TestLogHealthCheck_UnhealthyPath exercises the StatusUnhealthy branch of logHealthCheck,
// which logs per-component errors when the overall status is unhealthy/unknown.
func TestLogHealthCheck_UnhealthyPath(t *testing.T) {
	hc := &HealthChecker{
		logger:       testLogger(),
		aggregator:   health.NewAggregator([]health.Checker{&mockUnhealthyChecker{}}),
		serviceName:  "internal-account",
		checkTimeout: 5 * time.Second,
	}

	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "",
	})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)
}

// ---------------------------------------------------------------------------
// mapStatusToGRPC default case
// ---------------------------------------------------------------------------

// TestMapStatusToGRPC_DefaultCase covers the default branch (out-of-range status value).
func TestMapStatusToGRPC_DefaultCase(t *testing.T) {
	hc := &HealthChecker{logger: testLogger()}
	result := hc.mapStatusToGRPC(health.Status(99)) // out-of-range int hits default
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, result)
}

// ---------------------------------------------------------------------------
// PositionKeepingHealthChecker timeout path
// ---------------------------------------------------------------------------

// TestPositionKeepingHealthChecker_TimeoutPath covers checkCtx.Err() != nil branch.
// Passing an already-cancelled context causes the timeout check to fire.
func TestPositionKeepingHealthChecker_TimeoutPath(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}
	checker := NewPositionKeepingHealthChecker(healthClient, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before Check is called

	result := checker.Check(ctx)
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "timeout")
}

// ---------------------------------------------------------------------------
// Watch – context cancelled path
// ---------------------------------------------------------------------------

// TestWatch_ContextCancelled covers the main Watch() path:
// initial Check + Send, then the select loop fires ctx.Done() immediately.
func TestWatch_ContextCancelled(t *testing.T) {
	pkHealthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}
	hc := &HealthChecker{
		logger: testLogger(),
		aggregator: health.NewAggregator([]health.Checker{
			NewPositionKeepingHealthChecker(pkHealthClient, 5*time.Second),
		}),
		serviceName:  "internal-account",
		checkTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	stream := &mockWatchServer{ctx: ctx}
	err := hc.Watch(&grpc_health_v1.HealthCheckRequest{Service: ""}, stream)
	require.Error(t, err)
	stream.mu.Lock()
	n := len(stream.responses)
	stream.mu.Unlock()
	assert.Equal(t, 1, n) // initial health response was sent
}

// ---------------------------------------------------------------------------
// DatabaseHealthChecker timeout path (requires real DB)
// ---------------------------------------------------------------------------

// TestDatabaseHealthChecker_Check_TimeoutPath covers the checkCtx.Err() != nil branch.
// An already-cancelled context causes the timeout check to fire after Ping() returns.
func TestDatabaseHealthChecker_Check_TimeoutPath(t *testing.T) {
	db, cleanup := testdb.SetupPostgres(t, nil)
	defer cleanup()

	repo := persistence.NewRepository(db)
	checker := NewDatabaseHealthChecker(repo, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before Check is called

	result := checker.Check(ctx)
	assert.Equal(t, health.StatusUnhealthy, result.Status)
	assert.Contains(t, result.Message, "timeout")
}

// ---------------------------------------------------------------------------
// Watch – ticker fires path
// ---------------------------------------------------------------------------

// TestWatch_TickerFires covers the ticker.C branch in Watch():
// The HealthChecker sends the initial response, then the ticker fires
// (checkTimeout = 5ms), a second response is sent, then we cancel the context.
func TestWatch_TickerFires(t *testing.T) {
	pkHealthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}
	hc := &HealthChecker{
		logger: testLogger(),
		aggregator: health.NewAggregator([]health.Checker{
			NewPositionKeepingHealthChecker(pkHealthClient, 5*time.Second),
		}),
		serviceName:  "internal-account",
		checkTimeout: 5 * time.Millisecond, // very short so ticker fires fast
	}

	ctx, cancel := context.WithCancel(context.Background())

	stream := &mockWatchServer{ctx: ctx}

	done := make(chan error, 1)
	go func() {
		done <- hc.Watch(&grpc_health_v1.HealthCheckRequest{Service: ""}, stream)
	}()

	// Wait until at least 2 responses have been sent (initial + at least one ticker response)
	require.NoError(t, await.New().
		AtMost(2*time.Second).
		PollInterval(time.Millisecond).
		Until(func() bool {
			stream.mu.Lock()
			defer stream.mu.Unlock()
			return len(stream.responses) >= 2
		}), "should receive at least 2 health watch responses")

	cancel() // cancel context to terminate Watch

	<-done // wait for Watch to return

	stream.mu.Lock()
	n := len(stream.responses)
	stream.mu.Unlock()
	assert.GreaterOrEqual(t, n, 2, "should have at least initial + one ticker response")
}
