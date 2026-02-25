package session_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

func TestSession_PlanBeforeApply(t *testing.T) {
	s := session.New(session.Config{
		PlanTTL: 5 * time.Minute,
		Limits:  session.DefaultLimits(),
	})

	manifest := []byte(`{"instruments":[{"code":"GBP"}]}`)

	// Apply without a plan should fail.
	if s.ValidatePlan("nonexistent-hash") {
		t.Error("expected ValidatePlan to return false for missing plan")
	}

	// Store plan then apply should succeed.
	hash := s.StorePlan(manifest)
	if !s.ValidatePlan(hash) {
		t.Error("expected ValidatePlan to return true after storing plan")
	}
}

func TestSession_RateLimit(t *testing.T) {
	s := session.New(session.Config{
		PlanTTL: 5 * time.Minute,
		Limits: map[tools.ToolCategory]session.CategoryLimit{
			tools.CategoryWrite: {MaxRequests: 2, Window: time.Minute},
		},
	})

	if !s.Allow(tools.CategoryWrite) {
		t.Error("expected first call to be allowed")
	}
	if !s.Allow(tools.CategoryWrite) {
		t.Error("expected second call to be allowed")
	}
	if s.Allow(tools.CategoryWrite) {
		t.Error("expected third call to be blocked")
	}
}

func TestSession_CleanupExpiredPlans(t *testing.T) {
	s := session.New(session.Config{
		PlanTTL: 50 * time.Millisecond,
		Limits:  session.DefaultLimits(),
	})

	manifest := []byte(`{"test":"cleanup"}`)
	hash := s.StorePlan(manifest)

	if !s.ValidatePlan(hash) {
		t.Fatal("expected plan to exist before expiry")
	}

	time.Sleep(100 * time.Millisecond)
	s.Cleanup()

	if s.ValidatePlan(hash) {
		t.Error("expected expired plan to be gone after cleanup")
	}
}

func TestSession_NewWithDefaultConfig(t *testing.T) {
	s := session.NewDefault()

	if s == nil {
		t.Fatal("expected non-nil session from NewDefault")
	}

	// Verify it operates: store a plan and validate.
	hash := s.StorePlan([]byte(`{"default":"config"}`))
	if !s.ValidatePlan(hash) {
		t.Error("expected ValidatePlan to return true with default config")
	}
}
