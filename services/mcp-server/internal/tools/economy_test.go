package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// --- Mock implementations ---

type mockManifestApplier struct {
	applyFn func(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error)
}

func (m *mockManifestApplier) ApplyManifest(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
	return m.applyFn(ctx, req)
}

type mockManifestHistorian struct {
	listFn    func(ctx context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error)
	currentFn func(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error)
}

func (m *mockManifestHistorian) ListManifestVersions(ctx context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
	return m.listFn(ctx, req)
}

func (m *mockManifestHistorian) GetCurrentManifest(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
	if m.currentFn != nil {
		return m.currentFn(ctx, req)
	}
	// Default: derive from listFn for backwards compatibility with existing tests
	resp, err := m.listFn(ctx, &controlplanev1.ListManifestVersionsRequest{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(resp.Versions) == 0 {
		return &controlplanev1.GetCurrentManifestResponse{}, nil
	}
	return &controlplanev1.GetCurrentManifestResponse{Version: resp.Versions[0]}, nil
}

// validManifestJSON returns a minimal valid manifest JSON for testing.
func validManifestJSON() json.RawMessage {
	return json.RawMessage(`{
		"version": "1.0",
		"metadata": {"name": "Test Economy", "industry": "energy", "description": "Test"}
	}`)
}

// newTestSession creates a session with a short TTL suitable for tests.
func newTestSession() *session.Session {
	return session.New(session.Config{
		PlanTTL: 5 * time.Minute,
		Limits:  session.DefaultLimits(),
	})
}

// --- meridian_manifest_validate tests ---

func TestManifestValidate_ValidManifest_ReturnsValid(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if !req.DryRun {
				t.Error("expected dry_run to be true for validate")
			}
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

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
		t.Errorf("expected valid=true, got %v", m)
	}
}

func TestManifestValidate_ValidationFailed_ReturnsErrors(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED,
				ValidationErrors: []*controlplanev1.ValidationError{
					{
						Severity: "ERROR",
						Path:     "instruments[0].code",
						Code:     "INVALID_CODE",
						Message:  "instrument code must be uppercase",
					},
				},
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, validManifestJSON()))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); valid {
		t.Error("expected valid=false for validation failure")
	}
	errs, ok := m["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Error("expected non-empty errors slice")
	}
}

func TestManifestValidate_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return nil, status.Errorf(codes.Unavailable, "control plane unavailable")
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, validManifestJSON()))
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("expected handler to return formatted error, not Go error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestManifestValidate_InvalidJSON_ReturnsError(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			t.Fatal("should not call backend with invalid JSON")
			return nil, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	// manifest with invalid field type
	params := json.RawMessage(`{"manifest": {"version": 123}}`)
	result, err := r.Call(context.Background(), "meridian_manifest_validate", params)
	if err != nil {
		t.Fatalf("expected handler error in result, not Go error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); valid {
		t.Error("expected valid=false for invalid manifest JSON")
	}
}

func TestManifestValidate_MissingManifest_SchemaError(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			t.Fatal("should not call backend with missing manifest")
			return nil, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	_, err := r.Call(context.Background(), "meridian_manifest_validate", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected schema validation error for missing manifest")
	}
}

// --- meridian_manifest_plan tests ---

func TestManifestPlan_ValidManifest_StoresInCache(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if !req.DryRun {
				t.Error("expected dry_run to be true for plan")
			}
			return &controlplanev1.ApplyManifestResponse{
				Status:      controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
				DiffSummary: "Added 2 instruments, 1 account type",
				StepResults: []*controlplanev1.StepResult{
					{StepName: "validate", Status: controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS},
					{StepName: "diff", Status: controlplanev1.StepResultStatus_STEP_RESULT_STATUS_SUCCESS, Message: "2 additions"},
				},
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, validManifestJSON()))
	result, err := r.Call(context.Background(), "meridian_manifest_plan", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); !valid {
		t.Errorf("expected valid=true, got %v", m)
	}
	planHash, ok := m["plan_hash"].(string)
	if !ok || planHash == "" {
		t.Error("expected non-empty plan_hash")
	}
	if diff, _ := m["diff_summary"].(string); diff == "" {
		t.Error("expected non-empty diff_summary")
	}

	// Verify the plan was stored in the session cache.
	if !sess.ValidatePlan(planHash) {
		t.Error("expected plan hash to be valid in session cache")
	}
}

func TestManifestPlan_ValidationFailed_NoCache(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_VALIDATION_FAILED,
				ValidationErrors: []*controlplanev1.ValidationError{
					{Message: "bad manifest", Path: "metadata.name"},
				},
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, validManifestJSON()))
	result, err := r.Call(context.Background(), "meridian_manifest_plan", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if valid, _ := m["valid"].(bool); valid {
		t.Error("expected valid=false for validation failure")
	}
	if _, hasPlan := m["plan_hash"]; hasPlan {
		t.Error("expected no plan_hash when validation fails")
	}
}

func TestManifestPlan_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return nil, status.Errorf(codes.Internal, "server error")
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, validManifestJSON()))
	result, err := r.Call(context.Background(), "meridian_manifest_plan", params)
	if err != nil {
		t.Fatalf("expected formatted error result, not Go error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- meridian_manifest_apply tests ---

func TestManifestApply_WithValidPlan_Succeeds(t *testing.T) {
	applyCalled := false
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.DryRun {
				// Plan call (dry_run=true)
				return &controlplanev1.ApplyManifestResponse{
					Status:      controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
					DiffSummary: "changes detected",
				}, nil
			}
			// Apply call (dry_run=false)
			applyCalled = true
			if req.AppliedBy != "test@example.com" {
				t.Errorf("expected applied_by=test@example.com, got %q", req.AppliedBy)
			}
			return &controlplanev1.ApplyManifestResponse{
				Status:      controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
				JobId:       "job-001",
				DiffSummary: "applied changes",
				Snapshot: &controlplanev1.ManifestVersion{
					Id:        "ver-001",
					Version:   "1.0",
					AppliedAt: timestamppb.Now(),
					AppliedBy: "test@example.com",
				},
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	// Step 1: Plan
	manifest := validManifestJSON()
	planParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, manifest))
	planResult, err := r.Call(context.Background(), "meridian_manifest_plan", planParams)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	planMap := planResult.(map[string]interface{})
	planHash := planMap["plan_hash"].(string)

	// Step 2: Apply with the plan hash
	applyParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "plan_hash": %q, "applied_by": "test@example.com"}`, manifest, planHash))
	applyResult, err := r.Call(context.Background(), "meridian_manifest_apply", applyParams)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !applyCalled {
		t.Fatal("expected apply to call the backend")
	}
	m, ok := applyResult.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", applyResult)
	}
	if s, _ := m["status"].(string); s != "APPLY_MANIFEST_STATUS_APPLIED" {
		t.Errorf("expected APPLIED status, got %q", s)
	}
	if jobID, _ := m["job_id"].(string); jobID != "job-001" {
		t.Errorf("expected job_id=job-001, got %q", jobID)
	}
	if snap, ok := m["snapshot"].(map[string]interface{}); !ok || snap["id"] != "ver-001" {
		t.Errorf("expected snapshot with id=ver-001, got %v", m["snapshot"])
	}
}

func TestManifestApply_WithoutPlan_Rejected(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			t.Fatal("should not call backend without valid plan")
			return nil, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "plan_hash": "invalid-hash", "applied_by": "test@example.com"}`, validManifestJSON()))
	result, err := r.Call(context.Background(), "meridian_manifest_apply", params)
	if err != nil {
		t.Fatalf("expected error in result, not Go error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if _, hasError := m["error"]; !hasError {
		t.Error("expected 'error' key in result when no plan exists")
	}
}

func TestManifestApply_ContentMismatch_Rejected(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.DryRun {
				return &controlplanev1.ApplyManifestResponse{
					Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
				}, nil
			}
			t.Fatal("should not call backend with mismatched manifest")
			return nil, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	// Plan with manifest A
	manifestA := validManifestJSON()
	planParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, manifestA))
	planResult, err := r.Call(context.Background(), "meridian_manifest_plan", planParams)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	planHash := planResult.(map[string]interface{})["plan_hash"].(string)

	// Apply with a different manifest B but the plan_hash from manifest A
	manifestB := json.RawMessage(`{"version": "2.0", "metadata": {"name": "Different", "industry": "banking", "description": "Changed"}}`)
	applyParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "plan_hash": %q, "applied_by": "test@example.com"}`, manifestB, planHash))
	result, err := r.Call(context.Background(), "meridian_manifest_apply", applyParams)
	if err != nil {
		t.Fatalf("expected error in result, not Go error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if errMsg, _ := m["error"].(string); errMsg == "" {
		t.Error("expected 'error' key in result for content mismatch")
	}
}

func TestManifestApply_MissingRequiredFields_SchemaError(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			t.Fatal("should not call backend")
			return nil, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	// Missing plan_hash and applied_by
	params := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, validManifestJSON()))
	_, err := r.Call(context.Background(), "meridian_manifest_apply", params)
	if err == nil {
		t.Fatal("expected schema validation error for missing required fields")
	}
}

func TestManifestApply_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.DryRun {
				// Plan step
				return &controlplanev1.ApplyManifestResponse{
					Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
				}, nil
			}
			// Apply step fails
			return nil, status.Errorf(codes.FailedPrecondition, "manifest locked")
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	// Plan first
	manifest := validManifestJSON()
	planParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, manifest))
	planResult, err := r.Call(context.Background(), "meridian_manifest_plan", planParams)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	planHash := planResult.(map[string]interface{})["plan_hash"].(string)

	// Apply with gRPC failure
	applyParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "plan_hash": %q, "applied_by": "test"}`, manifest, planHash))
	result, err := r.Call(context.Background(), "meridian_manifest_apply", applyParams)
	if err != nil {
		t.Fatalf("expected formatted error result, not Go error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- meridian_manifest_history tests ---

func TestManifestHistory_ReturnsVersions(t *testing.T) {
	now := timestamppb.Now()
	mock := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			diffSummary := "Initial manifest"
			return &controlplanev1.ListManifestVersionsResponse{
				Versions: []*controlplanev1.ManifestVersion{
					{
						Id:          "ver-001",
						Version:     "1.0",
						AppliedAt:   now,
						AppliedBy:   "admin@example.com",
						ApplyStatus: controlplanev1.ApplyStatus_APPLY_STATUS_APPLIED,
						DiffSummary: &diffSummary,
						CreatedAt:   now,
					},
				},
				TotalCount: 1,
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: mock})

	result, err := r.Call(context.Background(), "meridian_manifest_history", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if count, _ := m["count"].(int); count != 1 {
		t.Errorf("expected count=1, got %v", m["count"])
	}
	versions, ok := m["versions"].([]map[string]interface{})
	if !ok || len(versions) == 0 {
		t.Fatalf("expected versions slice with entries, got %v", m["versions"])
	}
	if versions[0]["id"] != "ver-001" {
		t.Errorf("expected version id=ver-001, got %v", versions[0]["id"])
	}
}

func TestManifestHistory_Pagination_PassedToClient(t *testing.T) {
	var capturedReq *controlplanev1.ListManifestVersionsRequest

	mock := &mockManifestHistorian{
		listFn: func(_ context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			capturedReq = req
			return &controlplanev1.ListManifestVersionsResponse{}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: mock})

	params := json.RawMessage(`{"limit": 10, "offset": 5}`)
	_, err := r.Call(context.Background(), "meridian_manifest_history", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("expected request to be captured")
	}
	if capturedReq.Limit != 10 {
		t.Errorf("expected limit=10, got %d", capturedReq.Limit)
	}
	if capturedReq.Offset != 5 {
		t.Errorf("expected offset=5, got %d", capturedReq.Offset)
	}
}

func TestManifestHistory_EmptyResults_MeaningfulResponse(t *testing.T) {
	mock := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{
				Versions:   []*controlplanev1.ManifestVersion{},
				TotalCount: 0,
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: mock})

	result, err := r.Call(context.Background(), "meridian_manifest_history", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if msg, _ := m["message"].(string); msg == "" {
		t.Error("expected non-empty message for empty results")
	}
}

func TestManifestHistory_GRPCError_FormattedResponse(t *testing.T) {
	mock := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return nil, status.Errorf(codes.Unavailable, "history service unavailable")
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: mock})

	result, err := r.Call(context.Background(), "meridian_manifest_history", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected formatted error result, not Go error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// --- Registration tests ---

func TestEconomyTools_AllRegistered(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return &controlplanev1.ApplyManifestResponse{}, nil
		},
	}
	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{
		Applier:   mock,
		Historian: historian,
	})

	listed := r.List()
	names := make(map[string]bool)
	for _, tool := range listed {
		names[tool.Name] = true
	}

	required := []string{
		"meridian_manifest_validate",
		"meridian_manifest_plan",
		"meridian_manifest_apply",
		"meridian_manifest_history",
	}
	for _, name := range required {
		if !names[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestEconomyTools_CorrectCategories(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return &controlplanev1.ApplyManifestResponse{}, nil
		},
	}
	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{
		Applier:   mock,
		Historian: historian,
	})

	expected := map[string]tools.ToolCategory{
		"meridian_manifest_validate": tools.CategorySimulate,
		"meridian_manifest_plan":     tools.CategoryWrite,
		"meridian_manifest_apply":    tools.CategoryWrite,
		"meridian_manifest_history":  tools.CategoryRead,
	}

	for _, tool := range r.List() {
		if expectedCat, ok := expected[tool.Name]; ok {
			if tool.Category != expectedCat {
				t.Errorf("tool %q: expected category %v, got %v", tool.Name, expectedCat, tool.Category)
			}
		}
	}
}

func TestEconomyTools_NilClients_SkipsRegistration(t *testing.T) {
	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{})

	listed := r.List()
	if len(listed) != 0 {
		t.Fatalf("expected 0 registered tools with nil clients, got %d", len(listed))
	}
}

func TestEconomyTools_NilSession_SkipsPlanApply(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, _ *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			return &controlplanev1.ApplyManifestResponse{}, nil
		},
	}

	r := tools.NewRegistry()
	// nil session: plan and apply should not be registered
	tools.RegisterEconomyTools(r, nil, tools.EconomyDeps{Applier: mock})

	listed := r.List()
	names := make(map[string]bool)
	for _, tool := range listed {
		names[tool.Name] = true
	}
	if !names["meridian_manifest_validate"] {
		t.Error("expected meridian_manifest_validate to be registered with nil session")
	}
	if names["meridian_manifest_plan"] {
		t.Error("expected meridian_manifest_plan to NOT be registered with nil session")
	}
	if names["meridian_manifest_apply"] {
		t.Error("expected meridian_manifest_apply to NOT be registered with nil session")
	}
}

func TestManifestApply_DifferentJSONWhitespace_StillMatches(t *testing.T) {
	mock := &mockManifestApplier{
		applyFn: func(_ context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
			if req.DryRun {
				return &controlplanev1.ApplyManifestResponse{
					Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_DRY_RUN,
				}, nil
			}
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
				JobId:  "job-whitespace",
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Applier: mock})

	// Plan with compact JSON
	compact := json.RawMessage(`{"version":"1.0","metadata":{"name":"Test","industry":"energy","description":"Test"}}`)
	planParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s}`, compact))
	planResult, err := r.Call(context.Background(), "meridian_manifest_plan", planParams)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	planHash := planResult.(map[string]interface{})["plan_hash"].(string)

	// Apply with same data but different whitespace/key order
	spaced := json.RawMessage(`{
		"version": "1.0",
		"metadata": {
			"name": "Test",
			"industry": "energy",
			"description": "Test"
		}
	}`)
	applyParams := json.RawMessage(fmt.Sprintf(`{"manifest": %s, "plan_hash": %q, "applied_by": "test"}`, spaced, planHash))
	result, err := r.Call(context.Background(), "meridian_manifest_apply", applyParams)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if s, _ := m["status"].(string); s != "APPLY_MANIFEST_STATUS_APPLIED" {
		t.Errorf("expected APPLIED status (whitespace should not matter), got %q; result=%v", s, m)
	}
}

func TestEconomyTools_PartialClients_RegistersAvailable(t *testing.T) {
	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: historian})

	listed := r.List()
	if len(listed) != 2 {
		t.Fatalf("expected 2 registered tools (history + graph), got %d", len(listed))
	}
	names := make(map[string]bool)
	for _, tool := range listed {
		names[tool.Name] = true
	}
	if !names["meridian_manifest_history"] {
		t.Error("expected meridian_manifest_history to be registered")
	}
	if !names["meridian_economy_graph"] {
		t.Error("expected meridian_economy_graph to be registered")
	}
}

// --- meridian_economy_graph tests ---

func testManifest() *controlplanev1.Manifest {
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:        "Test Economy",
			Industry:    "energy",
			Description: "Test",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "GBP",
				Name: "British Pound",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit: "GBP", Precision: 2,
				},
			},
			{
				Code: "KWH",
				Name: "Kilowatt Hour",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit: "kWh", Precision: 3,
				},
			},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:               "SETTLEMENT",
				Name:               "Settlement Account",
				NormalBalance:      controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
				AllowedInstruments: []string{"GBP"},
			},
		},
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
				Source:         "nordpool",
			},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "process_settlement",
				Trigger: "api:/v1/settlements",
				Script:  "x = 1",
			},
		},
	}
}

func TestEconomyGraph_ReturnsNodesAndEdges(t *testing.T) {
	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{
				Versions: []*controlplanev1.ManifestVersion{
					{
						Id:       "v1",
						Version:  "1.0",
						Manifest: testManifest(),
					},
				},
				TotalCount: 1,
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: historian})

	result, err := r.Call(context.Background(), "meridian_economy_graph", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}

	nodeCount, _ := m["node_count"].(int)
	edgeCount, _ := m["edge_count"].(int)

	if nodeCount < 4 {
		t.Errorf("expected at least 4 nodes (2 instruments + 1 account_type + 1 saga), got %d", nodeCount)
	}
	if edgeCount < 3 {
		t.Errorf("expected at least 3 edges (denominated_in + converts + triggers_on), got %d", edgeCount)
	}
}

func TestEconomyGraph_FilterByNodeType(t *testing.T) {
	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{
				Versions: []*controlplanev1.ManifestVersion{
					{Id: "v1", Version: "1.0", Manifest: testManifest()},
				},
				TotalCount: 1,
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: historian})

	result, err := r.Call(context.Background(), "meridian_economy_graph", json.RawMessage(`{"node_type":"instrument"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := result.(map[string]interface{})
	nodeCount := m["node_count"].(int)
	if nodeCount != 2 {
		t.Errorf("expected 2 instrument nodes, got %d", nodeCount)
	}
}

func TestEconomyGraph_ImpactAnalysis(t *testing.T) {
	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{
				Versions: []*controlplanev1.ManifestVersion{
					{Id: "v1", Version: "1.0", Manifest: testManifest()},
				},
				TotalCount: 1,
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: historian})

	result, err := r.Call(context.Background(), "meridian_economy_graph", json.RawMessage(`{"node_id":"instrument:GBP"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := result.(map[string]interface{})
	impact, ok := m["impact"].(map[string]interface{})
	if !ok {
		t.Fatal("expected impact field in response")
	}

	affectedNodes, ok := impact["affected_nodes"].([]string)
	if !ok {
		t.Fatal("expected affected_nodes as []string")
	}

	// GBP is used by SETTLEMENT (denominated_in) and KWH (converts)
	if len(affectedNodes) < 2 {
		t.Errorf("expected at least 2 affected nodes for instrument:GBP, got %d: %v", len(affectedNodes), affectedNodes)
	}
}

func TestEconomyGraph_UsesStoredGraph(t *testing.T) {
	// When a stored relationship graph exists on the manifest version,
	// the tool should use it (including handler nodes/edges) instead of
	// rebuilding a structural-only graph from the manifest.
	storedGraphJSON := `{
		"nodes": [
			{"id": "instrument:GBP", "type": "instrument", "name": "GBP"},
			{"id": "saga:process_settlement", "type": "saga", "name": "process_settlement"},
			{"id": "handler:position_keeping.initiate_log", "type": "handler", "name": "position_keeping.initiate_log"}
		],
		"edges": [
			{"source": "saga:process_settlement", "target": "handler:position_keeping.initiate_log", "relationship": "calls_handler"},
			{"source": "saga:process_settlement", "target": "handler:position_keeping.initiate_log", "relationship": "uses_instrument", "is_dynamic": true}
		]
	}`

	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{
				Versions: []*controlplanev1.ManifestVersion{{
					Id:                "test-id",
					Version:           "1.0",
					Manifest:          testManifest(),
					RelationshipGraph: &storedGraphJSON,
				}},
				TotalCount: 1,
			}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: historian})

	result, err := r.Call(context.Background(), "meridian_economy_graph", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := result.(map[string]interface{})

	// Should have 3 nodes (including handler from stored graph)
	nodeCount, _ := m["node_count"].(int)
	if nodeCount != 3 {
		t.Errorf("expected 3 nodes from stored graph, got %d", nodeCount)
	}

	// Marshal result to JSON to inspect node types (avoids internal type assertions)
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	resultStr := string(resultJSON)

	// Verify handler node exists (only available from stored graph, not structural extraction)
	if !strings.Contains(resultStr, `"handler"`) {
		t.Error("expected handler node from stored graph, not found in result JSON")
	}

	// Verify calls_handler edge exists
	if !strings.Contains(resultStr, `"calls_handler"`) {
		t.Error("expected calls_handler edge from stored graph, not found in result JSON")
	}
}

func TestEconomyGraph_NoManifest(t *testing.T) {
	historian := &mockManifestHistorian{
		listFn: func(_ context.Context, _ *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
			return &controlplanev1.ListManifestVersionsResponse{}, nil
		},
	}

	r := tools.NewRegistry()
	sess := newTestSession()
	tools.RegisterEconomyTools(r, sess, tools.EconomyDeps{Historian: historian})

	result, err := r.Call(context.Background(), "meridian_economy_graph", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := result.(map[string]interface{})
	if m["status"] != "no_manifest" {
		t.Errorf("expected status 'no_manifest', got %v", m["status"])
	}
}
