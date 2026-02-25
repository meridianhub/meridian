package session_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
	"github.com/meridianhub/meridian/shared/platform/await"
)

func TestRateLimiter_AllowWithinLimit(t *testing.T) {
	limits := session.DefaultLimits()
	rl := session.NewRateLimiter(limits)

	// CategoryRead allows 60/min; well within limit.
	for i := 0; i < 10; i++ {
		if !rl.Allow(tools.CategoryRead) {
			t.Fatalf("expected Allow to return true on call %d", i+1)
		}
	}
}

func TestRateLimiter_BlockWhenExceeded(t *testing.T) {
	limits := map[tools.ToolCategory]session.CategoryLimit{
		tools.CategoryRead: {MaxRequests: 3, Window: time.Minute},
	}
	rl := session.NewRateLimiter(limits)

	for i := 0; i < 3; i++ {
		if !rl.Allow(tools.CategoryRead) {
			t.Fatalf("expected Allow true on call %d", i+1)
		}
	}

	if rl.Allow(tools.CategoryRead) {
		t.Error("expected Allow to return false when limit exceeded")
	}
}

func TestRateLimiter_ResetAfterWindow(t *testing.T) {
	limits := map[tools.ToolCategory]session.CategoryLimit{
		tools.CategoryRead: {MaxRequests: 2, Window: 50 * time.Millisecond},
	}
	rl := session.NewRateLimiter(limits)

	rl.Allow(tools.CategoryRead)
	rl.Allow(tools.CategoryRead)

	if rl.Allow(tools.CategoryRead) {
		t.Error("expected Allow false when limit reached")
	}

	// Wait for the window to reset.
	err := await.New().
		AtMost(500 * time.Millisecond).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return rl.Allow(tools.CategoryRead) })
	if err != nil {
		t.Error("expected Allow true after window reset")
	}
}

func TestRateLimiter_IndependentCategories(t *testing.T) {
	limits := map[tools.ToolCategory]session.CategoryLimit{
		tools.CategoryRead:     {MaxRequests: 1, Window: time.Minute},
		tools.CategorySimulate: {MaxRequests: 5, Window: time.Minute},
		tools.CategoryWrite:    {MaxRequests: 1, Window: time.Minute},
	}
	rl := session.NewRateLimiter(limits)

	// Exhaust Read.
	rl.Allow(tools.CategoryRead)
	if rl.Allow(tools.CategoryRead) {
		t.Error("CategoryRead should be exhausted")
	}

	// Simulate should still work.
	if !rl.Allow(tools.CategorySimulate) {
		t.Error("CategorySimulate should still be available")
	}

	// Write should still work on first call.
	if !rl.Allow(tools.CategoryWrite) {
		t.Error("CategoryWrite first call should succeed")
	}
}

func TestRateLimiter_DefaultLimits(t *testing.T) {
	limits := session.DefaultLimits()

	if _, ok := limits[tools.CategoryRead]; !ok {
		t.Error("DefaultLimits should include CategoryRead")
	}
	if _, ok := limits[tools.CategorySimulate]; !ok {
		t.Error("DefaultLimits should include CategorySimulate")
	}
	if _, ok := limits[tools.CategoryWrite]; !ok {
		t.Error("DefaultLimits should include CategoryWrite")
	}

	if limits[tools.CategoryRead].MaxRequests != 60 {
		t.Errorf("CategoryRead MaxRequests = %d, want 60", limits[tools.CategoryRead].MaxRequests)
	}
	if limits[tools.CategorySimulate].MaxRequests != 30 {
		t.Errorf("CategorySimulate MaxRequests = %d, want 30", limits[tools.CategorySimulate].MaxRequests)
	}
	if limits[tools.CategoryWrite].MaxRequests != 5 {
		t.Errorf("CategoryWrite MaxRequests = %d, want 5", limits[tools.CategoryWrite].MaxRequests)
	}
}

func TestRateLimiter_UnknownCategoryAlwaysAllowed(t *testing.T) {
	rl := session.NewRateLimiter(map[tools.ToolCategory]session.CategoryLimit{})

	// An unconfigured category should not block.
	for i := 0; i < 100; i++ {
		if !rl.Allow(tools.CategoryWrite) {
			t.Fatalf("unconfigured category should always be allowed (call %d)", i+1)
		}
	}
}
