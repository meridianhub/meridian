package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	listFn func(ctx context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error)
}

func (m *mockManifestHistorian) ListManifestVersions(ctx context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
	return m.listFn(ctx, req)
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
	if len(listed) != 1 {
		t.Fatalf("expected 1 registered tool, got %d", len(listed))
	}
	if listed[0].Name != "meridian_manifest_history" {
		t.Errorf("expected meridian_manifest_history, got %q", listed[0].Name)
	}
}
