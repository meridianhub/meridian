package session_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
	"github.com/meridianhub/meridian/shared/platform/await"
)

func TestPlanCache_StoreAndRetrieve(t *testing.T) {
	cache := session.NewPlanCache(5 * time.Minute)

	manifest := []byte(`{"instruments":[{"code":"GBP"}]}`)
	hash := cache.Store(manifest)

	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	ok := cache.Exists(hash)
	if !ok {
		t.Error("expected plan to exist after store")
	}
}

func TestPlanCache_DifferentManifestsDifferentHashes(t *testing.T) {
	cache := session.NewPlanCache(5 * time.Minute)

	manifest1 := []byte(`{"instruments":[{"code":"GBP"}]}`)
	manifest2 := []byte(`{"instruments":[{"code":"USD"}]}`)

	hash1 := cache.Store(manifest1)
	hash2 := cache.Store(manifest2)

	if hash1 == hash2 {
		t.Error("different manifests should produce different hashes")
	}
}

func TestPlanCache_SameManifestSameHash(t *testing.T) {
	cache := session.NewPlanCache(5 * time.Minute)

	manifest := []byte(`{"instruments":[{"code":"GBP"}]}`)

	hash1 := cache.Store(manifest)
	hash2 := cache.Store(manifest)

	if hash1 != hash2 {
		t.Error("same manifest should produce same hash")
	}
}

func TestPlanCache_TTLExpiry(t *testing.T) {
	cache := session.NewPlanCache(50 * time.Millisecond)

	manifest := []byte(`{"instruments":[{"code":"GBP"}]}`)
	hash := cache.Store(manifest)

	if !cache.Exists(hash) {
		t.Fatal("expected plan to exist immediately after store")
	}

	err := await.New().
		AtMost(500 * time.Millisecond).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return !cache.Exists(hash) })
	if err != nil {
		t.Error("expected plan to be expired after TTL")
	}
}

func TestPlanCache_MissingHash(t *testing.T) {
	cache := session.NewPlanCache(5 * time.Minute)

	ok := cache.Exists("nonexistent-hash")
	if ok {
		t.Error("expected Exists to return false for unknown hash")
	}
}

func TestPlanCache_Cleanup(t *testing.T) {
	cache := session.NewPlanCache(50 * time.Millisecond)

	hashes := make([]string, 5)
	for i := 0; i < 5; i++ {
		hashes[i] = cache.Store([]byte{byte(i)})
	}

	// Wait for all entries to expire before cleaning up.
	err := await.New().
		AtMost(500 * time.Millisecond).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			for _, h := range hashes {
				if cache.Exists(h) {
					return false
				}
			}
			return true
		})
	if err != nil {
		t.Fatal("entries did not expire in time")
	}

	cache.Cleanup()

	// All entries should be expired and cleaned up.
	// We can't directly inspect internal state, but we verify that storing a
	// new entry after cleanup still works correctly.
	newManifest := []byte(`{"post":"cleanup"}`)
	hash := cache.Store(newManifest)
	if !cache.Exists(hash) {
		t.Error("expected newly stored entry to exist after cleanup")
	}
}
