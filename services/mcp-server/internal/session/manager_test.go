package session_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

func TestManager_KeysAreIndependent(t *testing.T) {
	limits := map[tools.ToolCategory]session.CategoryLimit{
		tools.CategoryWrite: {MaxRequests: 1, Window: time.Minute},
	}
	m := session.NewManager(limits)
	defer m.Close()

	// Exhaust tenant-a's quota.
	if !m.Allow("tenant-a", tools.CategoryWrite) {
		t.Fatal("expected tenant-a's first call to be allowed")
	}
	if m.Allow("tenant-a", tools.CategoryWrite) {
		t.Fatal("expected tenant-a's second call to be blocked")
	}

	// tenant-b must be unaffected by tenant-a exhausting its limit — this is
	// the exact scenario CTL-4 fixes: one tenant's traffic must never
	// throttle a different tenant.
	if !m.Allow("tenant-b", tools.CategoryWrite) {
		t.Error("expected tenant-b to be unaffected by tenant-a's exhausted limit")
	}

	// tenant-a should remain blocked; tenant-b now exhausted on its own quota.
	if m.Allow("tenant-a", tools.CategoryWrite) {
		t.Error("expected tenant-a to remain blocked")
	}
	if m.Allow("tenant-b", tools.CategoryWrite) {
		t.Error("expected tenant-b's own limit to now be exhausted")
	}
}

func TestManager_SameKeySharesState(t *testing.T) {
	limits := map[tools.ToolCategory]session.CategoryLimit{
		tools.CategoryRead: {MaxRequests: 2, Window: time.Minute},
	}
	m := session.NewManager(limits)
	defer m.Close()

	if !m.Allow("tenant-a", tools.CategoryRead) {
		t.Fatal("expected first call to be allowed")
	}
	if !m.Allow("tenant-a", tools.CategoryRead) {
		t.Fatal("expected second call to be allowed")
	}
	if m.Allow("tenant-a", tools.CategoryRead) {
		t.Error("expected third call for the same key to be blocked")
	}
}

func TestManager_UnconfiguredCategoryAlwaysAllowed(t *testing.T) {
	m := session.NewManager(map[tools.ToolCategory]session.CategoryLimit{})
	defer m.Close()

	for i := 0; i < 50; i++ {
		if !m.Allow("any-key", tools.CategoryWrite) {
			t.Fatalf("unconfigured category should always be allowed (call %d)", i+1)
		}
	}
}

func TestManager_CloseIsIdempotent(_ *testing.T) {
	m := session.NewManager(session.DefaultLimits())
	m.Close()
	m.Close() // must not panic
}

func TestManager_ManyKeysDoNotInterfere(t *testing.T) {
	limits := map[tools.ToolCategory]session.CategoryLimit{
		tools.CategoryWrite: {MaxRequests: 1, Window: time.Minute},
	}
	m := session.NewManager(limits)
	defer m.Close()

	keys := []string{"tenant-a", "tenant-b", "tenant-c", "session-1", "session-2"}
	for _, k := range keys {
		if !m.Allow(k, tools.CategoryWrite) {
			t.Fatalf("expected first call for key %q to be allowed", k)
		}
	}
	for _, k := range keys {
		if m.Allow(k, tools.CategoryWrite) {
			t.Fatalf("expected second call for key %q to be blocked", k)
		}
	}
}
