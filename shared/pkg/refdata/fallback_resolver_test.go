package refdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingSource returns errors for all operations until toggled.
type failingSource struct {
	fail        atomic.Bool
	instruments map[string]InstrumentProperties
}

func newFailingSource(instruments ...InstrumentProperties) *failingSource {
	m := make(map[string]InstrumentProperties)
	for _, inst := range instruments {
		m[inst.Code] = inst
	}
	s := &failingSource{instruments: m}
	s.fail.Store(true)
	return s
}

func (s *failingSource) FetchInstrument(_ context.Context, code string) (InstrumentProperties, error) {
	if s.fail.Load() {
		return InstrumentProperties{}, errors.New("upstream unavailable")
	}
	props, ok := s.instruments[code]
	if !ok {
		return InstrumentProperties{}, ErrUnknownInstrument
	}
	return props, nil
}

func (s *failingSource) FetchAllActive(_ context.Context) ([]InstrumentProperties, error) {
	if s.fail.Load() {
		return nil, errors.New("upstream unavailable")
	}
	result := make([]InstrumentProperties, 0, len(s.instruments))
	for _, props := range s.instruments {
		result = append(result, props)
	}
	return result, nil
}

func TestFileSnapshotStorage_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	storage := NewFileSnapshotStorage(path)

	instruments := []InstrumentProperties{usd, kwh}

	err := storage.Save(instruments)
	require.NoError(t, err)

	loaded, err := storage.Load()
	require.NoError(t, err)
	require.Len(t, loaded, 2)

	// Verify both instruments (order may differ)
	codes := map[string]InstrumentProperties{}
	for _, inst := range loaded {
		codes[inst.Code] = inst
	}
	assert.Equal(t, usd, codes["USD"])
	assert.Equal(t, kwh, codes["KWH"])
}

func TestFileSnapshotStorage_Load_NoFile(t *testing.T) {
	storage := NewFileSnapshotStorage(filepath.Join(t.TempDir(), "nonexistent.json"))

	loaded, err := storage.Load()
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestFileSnapshotStorage_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	storage := NewFileSnapshotStorage(path)

	// Save initial data
	err := storage.Save([]InstrumentProperties{usd})
	require.NoError(t, err)

	// Save updated data
	err = storage.Save([]InstrumentProperties{usd, kwh})
	require.NoError(t, err)

	// Verify no temp files remain
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only snapshot file should remain")

	loaded, err := storage.Load()
	require.NoError(t, err)
	assert.Len(t, loaded, 2)
}

func TestFallbackResolver_UpstreamHealthy(t *testing.T) {
	source := newTestSource(usd, kwh)
	cached := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})
	storage := NewFileSnapshotStorage(filepath.Join(t.TempDir(), "snapshot.json"))

	resolver := NewFallbackResolver(cached, storage, FallbackResolverConfig{
		SnapshotInterval: time.Hour, // won't trigger in test
	})

	err := resolver.Start(context.Background())
	require.NoError(t, err)
	defer resolver.Stop()

	assert.False(t, resolver.IsFallbackActive())

	props, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, "USD", props.Code)
}

func TestFallbackResolver_FallbackFromSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	storage := NewFileSnapshotStorage(path)

	// Pre-populate snapshot
	err := storage.Save([]InstrumentProperties{usd, kwh})
	require.NoError(t, err)

	// Source fails
	source := newFailingSource(usd, kwh)
	cached := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	resolver := NewFallbackResolver(cached, storage, FallbackResolverConfig{
		SnapshotInterval: time.Hour,
	})

	err = resolver.Start(context.Background())
	require.NoError(t, err)
	defer resolver.Stop()

	assert.True(t, resolver.IsFallbackActive())

	props, err := resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, "USD", props.Code)
	assert.Equal(t, 2, props.Precision)
}

func TestFallbackResolver_FailClosed_UnknownInstrument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	storage := NewFileSnapshotStorage(path)

	// Snapshot with only USD
	err := storage.Save([]InstrumentProperties{usd})
	require.NoError(t, err)

	source := newFailingSource(usd)
	cached := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	resolver := NewFallbackResolver(cached, storage, FallbackResolverConfig{
		SnapshotInterval: time.Hour,
	})

	err = resolver.Start(context.Background())
	require.NoError(t, err)
	defer resolver.Stop()

	assert.True(t, resolver.IsFallbackActive())

	// Known instrument works
	_, err = resolver.Resolve(context.Background(), "USD")
	require.NoError(t, err)

	// Unknown instrument returns error even in fallback mode (fail-closed)
	_, err = resolver.Resolve(context.Background(), "UNKNOWN")
	assert.ErrorIs(t, err, ErrUnknownInstrument)
}

func TestFallbackResolver_NoSnapshotNoUpstream(t *testing.T) {
	source := newFailingSource(usd)
	cached := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})
	storage := NewFileSnapshotStorage(filepath.Join(t.TempDir(), "snapshot.json"))

	resolver := NewFallbackResolver(cached, storage, FallbackResolverConfig{
		SnapshotInterval: time.Hour,
	})

	err := resolver.Start(context.Background())
	assert.Error(t, err, "should fail when upstream is down and no snapshot exists")
}

func TestFallbackResolver_Recovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	storage := NewFileSnapshotStorage(path)

	// Pre-populate snapshot
	err := storage.Save([]InstrumentProperties{usd})
	require.NoError(t, err)

	// Source starts failing
	source := newFailingSource(usd, kwh)
	cached := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})

	resolver := NewFallbackResolver(cached, storage, FallbackResolverConfig{
		SnapshotInterval: 10 * time.Millisecond,
	})

	err = resolver.Start(context.Background())
	require.NoError(t, err)
	defer resolver.Stop()

	assert.True(t, resolver.IsFallbackActive())

	// Restore upstream
	source.fail.Store(false)

	// Wait for recovery
	require.Eventually(t, func() bool {
		return !resolver.IsFallbackActive()
	}, 2*time.Second, 10*time.Millisecond, "should recover from fallback mode")

	// Should now resolve from upstream (including KWH which wasn't in snapshot)
	props, err := resolver.Resolve(context.Background(), "KWH")
	require.NoError(t, err)
	assert.Equal(t, "KWH", props.Code)
}

func TestFallbackResolver_Stop(t *testing.T) {
	source := newTestSource(usd)
	cached := NewCachedResolver(source, CachedResolverConfig{TTL: time.Minute})
	storage := NewFileSnapshotStorage(filepath.Join(t.TempDir(), "snapshot.json"))

	resolver := NewFallbackResolver(cached, storage, FallbackResolverConfig{
		SnapshotInterval: 10 * time.Millisecond,
	})

	err := resolver.Start(context.Background())
	require.NoError(t, err)

	// Stop should return quickly
	done := make(chan struct{})
	go func() {
		resolver.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within timeout")
	}
}
