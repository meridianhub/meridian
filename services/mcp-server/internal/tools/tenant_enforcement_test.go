package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// These white-box tests verify that a client-supplied tenant_id cannot override
// the tenant established by the authenticated request context. The in-memory MCP
// transport used by the black-box tests does not propagate context values across
// the client/server boundary, so tenant-context enforcement is exercised by
// calling the handlers directly.

// --- resolveTenantID ---

func TestResolveTenantID_NoAuthContext_ReturnsRequested(t *testing.T) {
	got, err := resolveTenantID(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant-a" {
		t.Errorf("expected requested tenant to pass through, got %q", got)
	}
}

func TestResolveTenantID_NoAuthContext_EmptyRequested(t *testing.T) {
	got, err := resolveTenantID(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty tenant, got %q", got)
	}
}

func TestResolveTenantID_AuthContext_MatchingRequested(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	got, err := resolveTenantID(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant-a" {
		t.Errorf("expected tenant-a, got %q", got)
	}
}

func TestResolveTenantID_AuthContext_OmittedRequested_UsesAuthTenant(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	got, err := resolveTenantID(ctx, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant-a" {
		t.Errorf("expected authenticated tenant to be used, got %q", got)
	}
}

func TestResolveTenantID_AuthContext_MismatchedRequested_PermissionDenied(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	_, err := resolveTenantID(ctx, "tenant-b")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
	// The error must not echo the requested tenant_id (no tenant enumeration).
	if msg := status.Convert(err).Message(); strings.Contains(msg, "tenant-b") {
		t.Errorf("error message leaks requested tenant_id: %q", msg)
	}
}

// --- handleManifestValidate (amend) ---

func TestHandleManifestValidate_CrossTenant_DeniedAndNoBackendCall(t *testing.T) {
	called := false
	mock := &enforcementManifestApplier{
		fn: func(context.Context, *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			called = true
			return &controlplanev1.ApplyManifestResponse{}, nil
		},
	}
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	params := json.RawMessage(`{"manifest": {"version": "1.0"}, "mode": "amend", "tenant_id": "tenant-b"}`)

	_, err := handleManifestValidate(ctx, mock, params)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
	if called {
		t.Error("backend must not be called on a cross-tenant request (leak)")
	}
}

func TestHandleManifestValidate_MatchingTenant_UsesAuthTenant(t *testing.T) {
	var capturedCtx context.Context
	mock := &enforcementManifestApplier{
		fn: func(ctx context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			capturedCtx = ctx
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	params := json.RawMessage(`{"manifest": {"version": "1.0"}, "mode": "amend", "tenant_id": "tenant-a"}`)

	if _, err := handleManifestValidate(ctx, mock, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tid, ok := tenant.FromContext(capturedCtx)
	if !ok || string(tid) != "tenant-a" {
		t.Errorf("expected backend call scoped to tenant-a, got %q (ok=%v)", tid, ok)
	}
}

func TestHandleManifestValidate_OmittedTenant_UsesAuthTenant(t *testing.T) {
	var capturedCtx context.Context
	mock := &enforcementManifestApplier{
		fn: func(ctx context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			capturedCtx = ctx
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	params := json.RawMessage(`{"manifest": {"version": "1.0"}, "mode": "amend"}`)

	if _, err := handleManifestValidate(ctx, mock, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tid, ok := tenant.FromContext(capturedCtx)
	if !ok || string(tid) != "tenant-a" {
		t.Errorf("expected omitted tenant_id to fall back to auth tenant, got %q (ok=%v)", tid, ok)
	}
}

// --- handleEconomyGenerateContext ---

func TestHandleEconomyGenerateContext_CrossTenant_DeniedAndNoBackendCall(t *testing.T) {
	called := false
	mock := &enforcementGeneratorClient{
		contextFn: func(context.Context, *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			called = true
			return &controlplanev1.GetGenerationContextResponse{}, nil
		},
	}
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	params := json.RawMessage(`{"description": "x", "include_current_economy": true, "tenant_id": "tenant-b"}`)

	_, err := handleEconomyGenerateContext(ctx, mock, params)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
	if called {
		t.Error("backend must not be called on a cross-tenant request (leak)")
	}
}

func TestHandleEconomyGenerateContext_MatchingTenant_ScopesRequest(t *testing.T) {
	var captured *controlplanev1.GetGenerationContextRequest
	mock := &enforcementGeneratorClient{
		contextFn: func(_ context.Context, req *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
			captured = req
			return &controlplanev1.GetGenerationContextResponse{}, nil
		},
	}
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	params := json.RawMessage(`{"description": "x", "include_current_economy": true, "tenant_id": "tenant-a"}`)

	if _, err := handleEconomyGenerateContext(ctx, mock, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.TenantId != "tenant-a" {
		t.Errorf("expected request scoped to tenant-a, got %q", captured.TenantId)
	}
}

// --- handleEconomyGenerate ---

func TestHandleEconomyGenerate_CrossTenant_DeniedAndNoBackendCall(t *testing.T) {
	called := false
	mock := &enforcementGeneratorClient{
		generateFn: func(context.Context, *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			called = true
			return &controlplanev1.GenerateManifestResponse{}, nil
		},
	}
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	params := json.RawMessage(`{"description": "x", "mode": "amend", "tenant_id": "tenant-b"}`)

	_, err := handleEconomyGenerate(ctx, mock, params)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
	if called {
		t.Error("backend must not be called on a cross-tenant request (leak)")
	}
}

func TestHandleEconomyGenerate_OmittedTenant_UsesAuthTenant(t *testing.T) {
	var captured *controlplanev1.GenerateManifestRequest
	mock := &enforcementGeneratorClient{
		generateFn: func(_ context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			captured = req
			return &controlplanev1.GenerateManifestResponse{Valid: true}, nil
		},
	}
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("tenant-a"))
	// amend mode with no tenant_id must fall back to the authenticated tenant.
	params := json.RawMessage(`{"description": "x", "mode": "amend"}`)

	if _, err := handleEconomyGenerate(ctx, mock, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.TenantId != "tenant-a" {
		t.Errorf("expected request scoped to auth tenant-a, got %q", captured.TenantId)
	}
	if captured.Mode != controlplanev1.GenerationMode_GENERATION_MODE_AMEND {
		t.Errorf("expected AMEND mode, got %v", captured.Mode)
	}
}

// --- test doubles ---

type enforcementManifestApplier struct {
	fn func(context.Context, *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error)
}

func (m *enforcementManifestApplier) ApplyManifest(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
	return m.fn(ctx, req)
}

type enforcementGeneratorClient struct {
	generateFn func(context.Context, *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error)
	contextFn  func(context.Context, *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error)
}

func (m *enforcementGeneratorClient) GenerateManifest(ctx context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
	return m.generateFn(ctx, req)
}

func (m *enforcementGeneratorClient) GetGenerationContext(ctx context.Context, req *controlplanev1.GetGenerationContextRequest) (*controlplanev1.GetGenerationContextResponse, error) {
	return m.contextFn(ctx, req)
}
