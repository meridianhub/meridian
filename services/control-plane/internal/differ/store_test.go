package differ

import (
	"context"
	"testing"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
)

func TestManifestVersion_Fields(t *testing.T) {
	now := time.Now()
	mv := ManifestVersion{
		ID:        "v-123",
		Version:   "1.0",
		Manifest:  &controlplanev1.Manifest{Version: "1.0"},
		AppliedAt: now,
		AppliedBy: "user@example.com",
	}

	assert.Equal(t, "v-123", mv.ID)
	assert.Equal(t, "1.0", mv.Version)
	assert.Equal(t, "1.0", mv.Manifest.GetVersion())
	assert.Equal(t, now, mv.AppliedAt)
	assert.Equal(t, "user@example.com", mv.AppliedBy)
}

func TestManifestVersion_ZeroValue(t *testing.T) {
	var mv ManifestVersion
	assert.Equal(t, "", mv.ID)
	assert.Equal(t, "", mv.Version)
	assert.Nil(t, mv.Manifest)
	assert.True(t, mv.AppliedAt.IsZero())
	assert.Equal(t, "", mv.AppliedBy)
}

// TestManifestVersionStore_InterfaceCompliance verifies compile-time interface satisfaction.
func TestManifestVersionStore_InterfaceCompliance(_ *testing.T) {
	var _ ManifestVersionStore = (*inMemoryManifestVersionStore)(nil)
}

// inMemoryManifestVersionStore implements ManifestVersionStore for testing.
type inMemoryManifestVersionStore struct {
	latest *ManifestVersion
}

func (s *inMemoryManifestVersionStore) GetLatestApplied(_ context.Context) (*ManifestVersion, error) {
	return s.latest, nil
}

func (s *inMemoryManifestVersionStore) Save(_ context.Context, manifest *controlplanev1.Manifest, appliedBy string) error {
	s.latest = &ManifestVersion{
		Manifest:  manifest,
		AppliedBy: appliedBy,
		AppliedAt: time.Now(),
		Version:   manifest.GetVersion(),
	}
	return nil
}

func TestInMemoryManifestVersionStore_GetLatestApplied_NilInitially(t *testing.T) {
	store := &inMemoryManifestVersionStore{}
	mv, err := store.GetLatestApplied(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, mv)
}

func TestInMemoryManifestVersionStore_SaveAndGet(t *testing.T) {
	store := &inMemoryManifestVersionStore{}
	manifest := &controlplanev1.Manifest{Version: "1.0"}

	err := store.Save(context.Background(), manifest, "user@example.com")
	assert.NoError(t, err)

	mv, err := store.GetLatestApplied(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, mv)
	assert.Equal(t, "user@example.com", mv.AppliedBy)
	assert.Equal(t, "1.0", mv.Manifest.GetVersion())
}
