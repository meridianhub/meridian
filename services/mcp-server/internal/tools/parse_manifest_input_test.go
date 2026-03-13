package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// validManifestYAML returns a valid manifest in YAML string form.
func validManifestYAML() string {
	return `version: "1.0"
metadata:
  name: Test Economy
  industry: energy
  description: Test`
}

// validManifestJSONString returns the same manifest as a JSON string (not object).
func validManifestJSONString() string {
	return `{"version":"1.0","metadata":{"name":"Test Economy","industry":"energy","description":"Test"}}`
}

// --- parseManifestInput via handleManifestValidate integration tests ---

func TestManifestValidate_YAMLStringInput_Accepted(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.Manifest == nil {
				t.Error("expected non-nil manifest in request")
			}
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	// Pass manifest as a YAML string (not an object)
	yamlStr, _ := json.Marshal(validManifestYAML())
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, yamlStr))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); !valid {
		t.Errorf("expected valid=true for YAML string input, got %v", m)
	}
}

func TestManifestValidate_JSONStringInput_Accepted(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.Manifest == nil {
				t.Error("expected non-nil manifest in request")
			}
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	// Pass manifest as a JSON string (YAML is a superset of JSON)
	jsonStr, _ := json.Marshal(validManifestJSONString())
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, jsonStr))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); !valid {
		t.Errorf("expected valid=true for JSON string input, got %v", m)
	}
}

func TestManifestValidate_InvalidYAMLString_ReturnsError(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			t.Error("should not call backend with invalid YAML")
			return nil, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	// Pass an invalid YAML string
	invalidYAML := `"invalid: yaml: [unclosed bracket"`
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, invalidYAML))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("expected handler error in result, not Go error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); valid {
		t.Error("expected valid=false for invalid YAML string")
	}
	if _, hasErrors := m["errors"]; !hasErrors {
		t.Error("expected errors in response for invalid YAML")
	}
}

func TestManifestPlan_YAMLStringInput_Accepted(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	yamlStr, _ := json.Marshal(validManifestYAML())
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, yamlStr))
	result, err := r.Call(context.Background(), "meridian_manifest_plan", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); !valid {
		t.Errorf("expected valid=true for YAML string input in plan, got %v", m)
	}
	if _, ok := m["plan_hash"]; !ok {
		t.Error("expected plan_hash in response")
	}
}

func TestManifestApply_YAMLStringInput_Accepted(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.DryRun {
				return &controlplanev1.ApplyManifestResponse{
					Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
				}, nil
			}
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
				JobId:  "job-yaml-test",
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	yamlStr, _ := json.Marshal(validManifestYAML())
	planParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, yamlStr))

	// First plan
	planResult, err := r.Call(context.Background(), "meridian_manifest_plan", planParams)
	if err != nil {
		t.Fatalf("plan unexpected error: %v", err)
	}
	planMap := planResult.(map[string]interface{})
	planHash := planMap["plan_hash"].(string)

	// Then apply with same YAML string
	applyParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "plan_hash": %q, "applied_by": "test@example.com"}`, yamlStr, planHash))
	result, err := r.Call(context.Background(), "meridian_manifest_apply", applyParams)
	if err != nil {
		t.Fatalf("apply unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if status, _ := m["status"].(string); status != controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED.String() {
		t.Errorf("expected applied status, got %v", m)
	}
}

func TestManifestValidate_JSONObjectInput_StillWorks(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}

	r := newTestServer(t)
	sess := newTestSession()
	tools.RegisterEconomyTools(r.Server(), sess, tools.EconomyDeps{Applier: mock})

	// Pass manifest as a JSON object (the original behavior)
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, validManifestJSON()))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); !valid {
		t.Errorf("expected valid=true for JSON object input, got %v", m)
	}
}
