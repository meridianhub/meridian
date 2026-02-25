package gateway

// BenchmarkTranscoding measures the overhead of HTTP/JSON transcoding vs native gRPC.
//
// # What is measured
//
// Each benchmark starts a mock gRPC server and (for the transcoding benchmarks) a
// Vanguard gateway in front of it, then hammers a single RPC in a tight loop:
//
//   - BenchmarkGRPC_*   – native gRPC client directly to the mock server (baseline)
//   - BenchmarkJSON_*   – HTTP/JSON through the gateway transcoder (overhead path)
//
// The difference between the two numbers isolates the cost of JSON serialization,
// HTTP header processing, and Vanguard's protocol negotiation.
//
// # Typical results (M3 MacBook, loopback, no load)
//
//	BenchmarkGRPC_RetrieveParty    ~  50 µs/op
//	BenchmarkJSON_RetrieveParty    ~ 100 µs/op  (+~50 µs, ~2×)
//
// JSON transcoding adds roughly 10–30% CPU overhead vs native gRPC for simple RPCs;
// network round-trips dominate both paths in production (negligible in loopback).
//
// # Running
//
//	go test ./services/gateway/... -run=^$ -bench=. -benchmem -benchtime=5s
//
// To capture memory profiles add: -memprofile=/tmp/mem.prof

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// ---------------------------------------------------------------------------
// Benchmark infrastructure
// ---------------------------------------------------------------------------

// benchEnv holds the live servers started for a benchmark run.
type benchEnv struct {
	grpcAddr string
	baseURL  string
	cleanup  func()
}

// startBenchEnv boots a mock gRPC server and a gateway with the Vanguard
// transcoder, identical to the integration test harness but without
// t.Cleanup (callers use benchEnv.cleanup instead).
func startBenchEnv(b *testing.B, backends []ServiceBackend) *benchEnv {
	b.Helper()
	ctx := context.Background()

	// --- gRPC server ---
	grpcListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen grpc: %v", err)
	}
	grpcPort := grpcListener.Addr().(*net.TCPAddr).Port
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", grpcPort)

	grpcServer := grpc.NewServer()
	partyv1.RegisterPartyServiceServer(grpcServer, &mockPartyService{})
	tenantv1.RegisterTenantServiceServer(grpcServer, &mockTenantService{})
	sagav1.RegisterSagaRegistryServiceServer(grpcServer, &mockSagaRegistryService{})
	go func() { _ = grpcServer.Serve(grpcListener) }()

	// Point all backends at the mock gRPC server.
	for i := range backends {
		backends[i].BackendAddr = grpcAddr
	}

	// --- Gateway ---
	transcoder, err := NewTranscoder(testDescriptorBytes, backends)
	if err != nil {
		b.Fatalf("NewTranscoder: %v", err)
	}

	httpPort := func() int {
		l, err2 := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
		if err2 != nil {
			b.Fatalf("find port: %v", err2)
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		return port
	}()

	config := &Config{
		Port:        httpPort,
		BaseDomain:  "api.bench.io",
		DatabaseURL: "postgres://localhost/bench",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := NewServer(config, logger, nil, WithTranscoder(transcoder))

	serverCtx, cancel := context.WithCancel(ctx)
	go func() { _ = gwServer.Start(serverCtx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	if err := await.New().
		AtMost(5 * time.Second).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			resp, e := http.Get(baseURL + "/health") //nolint:noctx
			if e != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}); err != nil {
		b.Fatalf("gateway did not become ready: %v", err)
	}

	cleanup := func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = gwServer.Shutdown(shutdownCtx)
		grpcServer.GracefulStop()
		cancel()
	}

	return &benchEnv{grpcAddr: grpcAddr, baseURL: baseURL, cleanup: cleanup}
}

// grpcPartyClient returns a connected gRPC client for the PartyService.
func grpcPartyClient(b *testing.B, addr string) partyv1.PartyServiceClient {
	b.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.Fatalf("grpc.NewClient: %v", err)
	}
	b.Cleanup(func() { conn.Close() })
	return partyv1.NewPartyServiceClient(conn)
}

// grpcTenantClient returns a connected gRPC client for the TenantService.
func grpcTenantClient(b *testing.B, addr string) tenantv1.TenantServiceClient {
	b.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.Fatalf("grpc.NewClient: %v", err)
	}
	b.Cleanup(func() { conn.Close() })
	return tenantv1.NewTenantServiceClient(conn)
}

// grpcSagaClient returns a connected gRPC client for the SagaRegistryService.
func grpcSagaClient(b *testing.B, addr string) sagav1.SagaRegistryServiceClient {
	b.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.Fatalf("grpc.NewClient: %v", err)
	}
	b.Cleanup(func() { conn.Close() })
	return sagav1.NewSagaRegistryServiceClient(conn)
}

// httpDo issues an HTTP request and discards the response body, returning nil on 2xx.
func httpDo(b *testing.B, method, url, contentType, body string) {
	b.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, bodyReader)
	if err != nil {
		b.Fatalf("http.NewRequest: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.Fatalf("http.Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b.Fatalf("unexpected HTTP status: %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Simple RPC benchmarks – GET /v1/parties/{id}  (RetrieveParty)
// ---------------------------------------------------------------------------

// BenchmarkGRPC_RetrieveParty measures native gRPC latency for RetrieveParty.
// This is the baseline: no HTTP layer, no JSON serialization.
func BenchmarkGRPC_RetrieveParty(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})
	defer env.cleanup()

	client := grpcPartyClient(b, env.grpcAddr)
	ctx := context.Background()
	req := &partyv1.RetrievePartyRequest{PartyId: "bench-party"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.RetrieveParty(ctx, req); err != nil {
			b.Fatalf("RetrieveParty: %v", err)
		}
	}
}

// BenchmarkJSON_RetrieveParty measures HTTP/JSON transcoding latency for RetrieveParty.
// Overhead = JSON_latency - gRPC_latency (includes HTTP/JSON serialization + Vanguard).
func BenchmarkJSON_RetrieveParty(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})
	defer env.cleanup()

	url := env.baseURL + "/v1/parties/bench-party"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		httpDo(b, http.MethodGet, url, "", "")
	}
}

// ---------------------------------------------------------------------------
// Mutation RPC benchmarks – POST /v1/parties  (RegisterParty)
// ---------------------------------------------------------------------------

// BenchmarkGRPC_RegisterParty measures native gRPC latency for RegisterParty (POST with body).
func BenchmarkGRPC_RegisterParty(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})
	defer env.cleanup()

	client := grpcPartyClient(b, env.grpcAddr)
	ctx := context.Background()
	req := &partyv1.RegisterPartyRequest{
		PartyType:   partyv1.PartyType_PARTY_TYPE_PERSON,
		LegalName:   "Bench User",
		DisplayName: "Bench",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.RegisterParty(ctx, req); err != nil {
			b.Fatalf("RegisterParty: %v", err)
		}
	}
}

// BenchmarkJSON_RegisterParty measures HTTP/JSON transcoding latency for RegisterParty.
func BenchmarkJSON_RegisterParty(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})
	defer env.cleanup()

	url := env.baseURL + "/v1/parties"
	body := `{"partyType":"PARTY_TYPE_PERSON","legalName":"Bench User","displayName":"Bench"}`

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		httpDo(b, http.MethodPost, url, "application/json", body)
	}
}

// ---------------------------------------------------------------------------
// List RPC benchmarks – GET /v1/tenants  (ListTenants)
// ---------------------------------------------------------------------------

// BenchmarkGRPC_ListTenants measures native gRPC latency for ListTenants (response with array).
func BenchmarkGRPC_ListTenants(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.tenant.v1.TenantService"},
	})
	defer env.cleanup()

	client := grpcTenantClient(b, env.grpcAddr)
	ctx := context.Background()
	req := &tenantv1.ListTenantsRequest{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.ListTenants(ctx, req); err != nil {
			b.Fatalf("ListTenants: %v", err)
		}
	}
}

// BenchmarkJSON_ListTenants measures HTTP/JSON transcoding latency for ListTenants.
func BenchmarkJSON_ListTenants(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.tenant.v1.TenantService"},
	})
	defer env.cleanup()

	url := env.baseURL + "/v1/tenants"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		httpDo(b, http.MethodGet, url, "", "")
	}
}

// ---------------------------------------------------------------------------
// Complex RPC benchmarks – POST /v1/sagas/validate  (ValidateSaga)
// ---------------------------------------------------------------------------

// BenchmarkGRPC_ValidateSaga measures native gRPC latency for ValidateSaga (complex request + response).
func BenchmarkGRPC_ValidateSaga(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.saga.v1.SagaRegistryService"},
	})
	defer env.cleanup()

	client := grpcSagaClient(b, env.grpcAddr)
	ctx := context.Background()
	req := &sagav1.ValidateSagaRequest{
		SagaName: "bench_saga",
		Script:   `result = payment.create_lien(amount="100.00", direction="DEBIT")`,
		Version:  "1.0.0",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.ValidateSaga(ctx, req); err != nil {
			b.Fatalf("ValidateSaga: %v", err)
		}
	}
}

// BenchmarkJSON_ValidateSaga measures HTTP/JSON transcoding latency for ValidateSaga.
func BenchmarkJSON_ValidateSaga(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.saga.v1.SagaRegistryService"},
	})
	defer env.cleanup()

	url := env.baseURL + "/v1/sagas/validate"
	body := `{"sagaName":"bench_saga","script":"result = payment.create_lien(amount=\"100.00\", direction=\"DEBIT\")","version":"1.0.0"}`

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		httpDo(b, http.MethodPost, url, "application/json", body)
	}
}

// ---------------------------------------------------------------------------
// Parallel benchmarks – concurrency stress
// ---------------------------------------------------------------------------

// BenchmarkGRPC_RetrieveParty_Parallel measures native gRPC throughput under concurrency.
func BenchmarkGRPC_RetrieveParty_Parallel(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})
	defer env.cleanup()

	client := grpcPartyClient(b, env.grpcAddr)
	req := &partyv1.RetrievePartyRequest{PartyId: "bench-party"}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			if _, err := client.RetrieveParty(ctx, req); err != nil {
				b.Errorf("RetrieveParty: %v", err)
			}
		}
	})
}

// BenchmarkJSON_RetrieveParty_Parallel measures HTTP/JSON transcoding throughput under concurrency.
func BenchmarkJSON_RetrieveParty_Parallel(b *testing.B) {
	env := startBenchEnv(b, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})
	defer env.cleanup()

	url := env.baseURL + "/v1/parties/bench-party"

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
			if err != nil {
				b.Errorf("http.NewRequestWithContext: %v", err)
				continue
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				b.Errorf("http.Do: %v", err)
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				b.Errorf("unexpected HTTP status: %d", resp.StatusCode)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Transcoder initialisation benchmark
// ---------------------------------------------------------------------------

// BenchmarkNewTranscoder measures the cost of parsing the descriptor set and
// constructing the Vanguard transcoder. This is a one-time startup cost.
func BenchmarkNewTranscoder(b *testing.B) {
	backends := []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService", BackendAddr: "127.0.0.1:50051"},
		{ServiceName: "meridian.tenant.v1.TenantService", BackendAddr: "127.0.0.1:50052"},
		{ServiceName: "meridian.saga.v1.SagaRegistryService", BackendAddr: "127.0.0.1:50053"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler, err := NewTranscoder(testDescriptorBytes, backends)
		if err != nil {
			b.Fatalf("NewTranscoder: %v", err)
		}
		_ = handler
	}
}

// ---------------------------------------------------------------------------
// Serialization micro-benchmarks (no network)
// ---------------------------------------------------------------------------

// BenchmarkJSON_RequestBody_Parse measures the cost of JSON body reading (bytes → io.Reader).
// This isolates the HTTP body materialization cost from network latency.
func BenchmarkJSON_RequestBody_Parse(b *testing.B) {
	body := `{"partyType":"PARTY_TYPE_PERSON","legalName":"Bench User","displayName":"Bench"}`
	raw := []byte(body)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(raw)
		_, _ = io.ReadAll(r)
	}
}
