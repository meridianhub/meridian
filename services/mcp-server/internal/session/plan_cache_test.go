package session_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
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

	time.Sleep(100 * time.Millisecond)

	if cache.Exists(hash) {
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

	for i := 0; i < 5; i++ {
		manifest := []byte{byte(i)}
		cache.Store(manifest)
	}

	time.Sleep(100 * time.Millisecond)
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
