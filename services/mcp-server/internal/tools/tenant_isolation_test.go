package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// These tests cover cross-cutting integration scenarios that combine multiple
// tenant isolation features (YAML input, create/amend mode, tenant context
// propagation) to verify they work correctly together.

func TestManifestValidate_YAMLInput_AmendMode_PropagatesTenantContext(t *testing.T) {
	var capturedCtx context.Context
	var capturedReq *controlplanev1.ApplyManifestRequest
	mock := &mockManifestApplier{
		applyFn: func(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			capturedCtx = ctx
			capturedReq = req
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	// Combine YAML string input with amend mode and tenant_id
	yamlStr, _ := json.Marshal(validManifestYAML())
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "mode": "amend", "tenant_id": "acme-corp"}`, yamlStr))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if valid, _ := m["valid"].(bool); !valid {
		t.Errorf("expected valid=true for YAML+amend, got %v", m)
	}

	// Verify tenant context was set from the tenant_id parameter
	tenantID, ok := tenant.FromContext(capturedCtx)
	if !ok {
		t.Fatal("expected tenant context to be set for YAML+amend mode")
	}
	if string(tenantID) != "acme-corp" {
		t.Errorf("expected tenant_id=acme-corp, got %q", tenantID)
	}

	// Verify amend mode does NOT set SkipImmutabilityChecks (immutability checks apply)
	if capturedReq.SkipImmutabilityChecks {
		t.Error("expected SkipImmutabilityChecks=false in amend mode with YAML input")
	}

	// Verify the manifest was parsed from YAML correctly
	if capturedReq.Manifest == nil {
		t.Fatal("expected non-nil manifest from YAML string input")
	}
}

func TestManifestValidate_YAMLInput_CreateMode_SkipsImmutabilityChecks(t *testing.T) {
	var capturedCtx context.Context
	var capturedReq *controlplanev1.ApplyManifestRequest
	mock := &mockManifestApplier{
		applyFn: func(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			capturedCtx = ctx
			capturedReq = req
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	// YAML string + explicit create mode
	yamlStr, _ := json.Marshal(validManifestYAML())
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "mode": "create"}`, yamlStr))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if valid, _ := m["valid"].(bool); !valid {
		t.Errorf("expected valid=true for YAML+create, got %v", m)
	}

	// Create mode should skip immutability checks
	if !capturedReq.SkipImmutabilityChecks {
		t.Error("expected SkipImmutabilityChecks=true in create mode with YAML input")
	}

	// Create mode should NOT inject tenant context
	_, hasTenant := tenant.FromContext(capturedCtx)
	if hasTenant {
		t.Error("expected no tenant context in create mode")
	}
}

func TestManifestValidate_YAMLInput_AmendMode_MissingTenantID_ReturnsError(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			t.Fatal("should not call backend when tenant_id is missing for amend mode")
			return nil, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	// YAML string + amend mode but no tenant_id
	yamlStr, _ := json.Marshal(validManifestYAML())
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "mode": "amend"}`, yamlStr))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if _, hasError := m["error"]; !hasError {
		t.Error("expected error when YAML+amend mode is used without tenant_id")
	}
}

func TestManifestPlanThenApply_YAMLInput_FullLifecycle(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.DryRun {
				return &controlplanev1.ApplyManifestResponse{
					Status:      controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
					DiffSummary: "Added 1 instrument",
				}, nil
			}
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
				JobId:  "job-yaml-lifecycle",
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	yamlStr, _ := json.Marshal(validManifestYAML())

	// Step 1: Plan with YAML string
	planParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, yamlStr))
	planResult, err := r.Call(context.Background(), "meridian_manifest_plan", planParams)
	if err != nil {
		t.Fatalf("plan unexpected error: %v", err)
	}
	planMap := planResult.(map[string]interface{})
	planHash, ok := planMap["plan_hash"].(string)
	if !ok || planHash == "" {
		t.Fatal("expected non-empty plan_hash from YAML plan")
	}
	if diff, _ := planMap["diff_summary"].(string); diff != "Added 1 instrument" {
		t.Errorf("expected diff_summary, got %v", planMap)
	}

	// Step 2: Apply with same YAML string
	applyParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "plan_hash": %q, "applied_by": "integration-test@example.com"}`, yamlStr, planHash))
	applyResult, err := r.Call(context.Background(), "meridian_manifest_apply", applyParams)
	if err != nil {
		t.Fatalf("apply unexpected error: %v", err)
	}
	applyMap := applyResult.(map[string]interface{})
	if status, _ := applyMap["status"].(string); status != controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED.String() {
		t.Errorf("expected APPLIED status, got %v", applyMap)
	}
	if jobID, _ := applyMap["job_id"].(string); jobID != "job-yaml-lifecycle" {
		t.Errorf("expected job_id=job-yaml-lifecycle, got %q", jobID)
	}
}

func TestEconomyGenerate_CreateMode_DoesNotRequireTenantID(t *testing.T) {
	var capturedReq *controlplanev1.GenerateManifestRequest
	mock := &mockEconomyGeneratorClient{
		generateFn: func(_ context.Context, req *controlplanev1.GenerateManifestRequest) (*controlplanev1.GenerateManifestResponse, error) {
			capturedReq = req
			return &controlplanev1.GenerateManifestResponse{Valid: true, ManifestYaml: "version: 1.0\n"}, nil
		},
	}

	reg := newTestServer(t)
	tools.RegisterEconomyGeneratorTools(reg.Server(), mock)

	// Create mode (explicit) without tenant_id should succeed
	params := json.RawMessage(`{"description": "payment system", "mode": "create"}`)
	result, err := reg.Call(context.Background(), "meridian_economy_generate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := result.(map[string]interface{})
	if _, hasError := m["error"]; hasError {
		t.Errorf("create mode should not require tenant_id, got error: %v", m)
	}
	if !m["valid"].(bool) {
		t.Error("expected valid=true")
	}
	if capturedReq.TenantId != "" {
		t.Error("expected empty tenant_id for create mode")
	}
	if capturedReq.Mode != controlplanev1.GenerationMode_GENERATION_MODE_CREATE {
		t.Errorf("expected CREATE mode, got %v", capturedReq.Mode)
	}
}
